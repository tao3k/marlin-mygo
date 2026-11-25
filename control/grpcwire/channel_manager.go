// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/status"

	"github.com/tao3k/marlin-mygo/control/transport"
)

// TargetProvider yields the currently active HTTP/2 gRPC *Target.
// Both a static *Target, a *ResilientSidecarTarget, and a *ManagedChannel
// satisfy TargetProvider so long-lived stream pumps seamlessly survive
// sidecar crash-respawns and target endpoint rotations.
type TargetProvider interface {
	CurrentTarget() *Target
}

type swapNotifier interface {
	TargetProvider
	Generation() uint64
	WaitSwap(ctx context.Context, afterGen uint64) (uint64, bool)
}

// ChannelStatus is a lock-free telemetry snapshot for one managed gRPC channel.
type ChannelStatus struct {
	ID               string
	Address          string
	State            connectivity.State
	Phase            transport.ConnPhase
	Generation       uint64
	Reconnects       int
	LastTransitionAt time.Time
	LastError        string
}

// ManagedChannel is a live TargetProvider handle owned by ChannelManager.
// Callers pass it to SessionDriver, ProjectionStreamBridge, TurnStreamController,
// or PanelActionDispatcher so all RPCs and streams follow atomic target swaps.
type ManagedChannel struct {
	mgr *ChannelManager
	id  string
}

// CurrentTarget returns the latest live *Target for this channel, or nil if closed/missing.
func (h *ManagedChannel) CurrentTarget() *Target {
	if h == nil || h.mgr == nil {
		return nil
	}
	return h.mgr.Target(h.id)
}

// Generation returns the monotonic target swap generation for this channel.
func (h *ManagedChannel) Generation() uint64 {
	if h == nil || h.mgr == nil {
		return 0
	}
	return h.mgr.Generation(h.id)
}

// WaitSwap blocks until the channel swaps to a generation strictly greater than
// afterGen, or until ctx is cancelled or the channel is removed.
func (h *ManagedChannel) WaitSwap(ctx context.Context, afterGen uint64) (uint64, bool) {
	if h == nil || h.mgr == nil {
		return afterGen, false
	}
	return h.mgr.WaitSwap(ctx, h.id, afterGen)
}

type managedEntry struct {
	id         string
	target     *Target
	status     ChannelStatus
	generation uint64
	swapCh     chan struct{}
	watchStop  context.CancelFunc
}

// ChannelManager supervises the HTTP/2 lifecycle of N named Go <-> Rust gRPC
// channels (local Authority sidecar, remote agents, and auxiliary gRPC services).
// It tracks live gRPC connectivity.State transitions, supports zero-downtime
// atomic target swaps on sidecar restart, provides laptop-wake backoff reset,
// and drains all watchers cleanly on CloseAll().
type ChannelManager struct {
	runCtx context.Context
	cancel context.CancelFunc

	mu      sync.RWMutex
	entries map[string]*managedEntry
	order   []string
	nextSub uint64
	subs    map[uint64]func(ChannelStatus)
	closed  bool

	wg sync.WaitGroup
}

// NewChannelManager creates a new gRPC lifecycle ChannelManager.
func NewChannelManager() *ChannelManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &ChannelManager{
		runCtx:  ctx,
		cancel:  cancel,
		entries: make(map[string]*managedEntry),
		subs:    make(map[uint64]func(ChannelStatus)),
	}
}

// DialAndRegister dials a new Target using cfg and registers it under cfg.ID.
func (m *ChannelManager) DialAndRegister(cfg DialConfig) (*Target, error) {
	target, err := Dial(cfg)
	if err != nil {
		return nil, err
	}
	if err := m.RegisterTarget(cfg.ID, target); err != nil {
		_ = target.Close()
		return nil, err
	}
	return target, nil
}

// RegisterTarget attaches an existing *Target under id and begins monitoring its
// HTTP/2 connectivity state machine immediately.
func (m *ChannelManager) RegisterTarget(id string, target *Target) error {
	if id == "" || target == nil {
		return fmt.Errorf("grpcwire: ChannelManager requires non-empty id and non-nil target")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return fmt.Errorf("grpcwire: ChannelManager is closed")
	}
	if _, exists := m.entries[id]; exists {
		m.mu.Unlock()
		return fmt.Errorf("grpcwire: channel %q already registered", id)
	}
	now := time.Now()
	st := target.ConnectivityState()
	entry := &managedEntry{
		id:         id,
		target:     target,
		generation: 1,
		swapCh:     make(chan struct{}),
		status: ChannelStatus{
			ID:               id,
			Address:          target.Meta().Address,
			State:            st,
			Phase:            connectivityStateToPhase(st),
			Generation:       1,
			LastTransitionAt: now,
		},
	}
	m.entries[id] = entry
	m.order = append(m.order, id)
	statusCopy := entry.status
	m.startWatchLocked(entry, target, 1)
	m.mu.Unlock()

	m.emit(statusCopy)
	return nil
}

// SwapTarget atomically replaces the *Target for id (or registers it if absent),
// cancels the previous connectivity watcher, wakes all waiting stream pumps on
// the new generation, and returns the updated generation number.
// The caller retains ownership of closing the old *Target if not already closed.
func (m *ChannelManager) SwapTarget(id string, next *Target) (uint64, error) {
	if id == "" || next == nil {
		return 0, fmt.Errorf("grpcwire: SwapTarget requires non-empty id and non-nil target")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return 0, fmt.Errorf("grpcwire: ChannelManager is closed")
	}
	entry, exists := m.entries[id]
	if !exists {
		m.mu.Unlock()
		if err := m.RegisterTarget(id, next); err != nil {
			return 0, err
		}
		return 1, nil
	}
	if entry.watchStop != nil {
		entry.watchStop()
		entry.watchStop = nil
	}
	oldSwap := entry.swapCh
	entry.swapCh = make(chan struct{})
	entry.generation++
	gen := entry.generation
	entry.target = next
	st := next.ConnectivityState()
	entry.status.Address = next.Meta().Address
	entry.status.State = st
	entry.status.Phase = connectivityStateToPhase(st)
	entry.status.Generation = gen
	entry.status.Reconnects++
	entry.status.LastTransitionAt = time.Now()
	entry.status.LastError = ""
	statusCopy := entry.status
	m.startWatchLocked(entry, next, gen)
	m.mu.Unlock()

	close(oldSwap)
	m.emit(statusCopy)
	return gen, nil
}

// Provider returns a live ManagedChannel handle for id.
func (m *ChannelManager) Provider(id string) *ManagedChannel {
	return &ManagedChannel{mgr: m, id: id}
}

// Target returns the active *Target registered under id.
func (m *ChannelManager) Target(id string) *Target {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if e, ok := m.entries[id]; ok {
		return e.target
	}
	return nil
}

// Generation returns the current swap generation for id (0 if absent).
func (m *ChannelManager) Generation(id string) uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if e, ok := m.entries[id]; ok {
		return e.generation
	}
	return 0
}

// WaitSwap blocks until id advances beyond afterGen, ctx cancels, or manager closes.
func (m *ChannelManager) WaitSwap(ctx context.Context, id string, afterGen uint64) (uint64, bool) {
	m.mu.RLock()
	e, ok := m.entries[id]
	if !ok {
		m.mu.RUnlock()
		return afterGen, false
	}
	if e.generation > afterGen {
		gen := e.generation
		m.mu.RUnlock()
		return gen, true
	}
	ch := e.swapCh
	m.mu.RUnlock()

	select {
	case <-ch:
		return m.Generation(id), true
	case <-m.runCtx.Done():
		return afterGen, false
	case <-ctx.Done():
		return afterGen, false
	}
}

// Status returns the latest telemetry snapshot for id.
func (m *ChannelManager) Status(id string) (ChannelStatus, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[id]
	if !ok {
		return ChannelStatus{}, false
	}
	return e.status, true
}

// Snapshot returns deterministic registration-ordered statuses for all channels.
func (m *ChannelManager) Snapshot() []ChannelStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ChannelStatus, 0, len(m.order))
	for _, id := range m.order {
		if e, ok := m.entries[id]; ok {
			out = append(out, e.status)
		}
	}
	return out
}

// Subscribe registers a listener notified whenever any channel transitions
// connectivity state or swaps its underlying Target generation.
func (m *ChannelManager) Subscribe(fn func(ChannelStatus)) func() {
	if fn == nil {
		return func() {}
	}
	m.mu.Lock()
	m.nextSub++
	id := m.nextSub
	m.subs[id] = fn
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.subs, id)
		m.mu.Unlock()
	}
}

// WakeAll resets connect backoff and triggers immediate reconnect on every
// managed channel (ideal after laptop sleep-wake or network interface change).
func (m *ChannelManager) WakeAll() {
	m.mu.RLock()
	targets := make([]*Target, 0, len(m.entries))
	for _, e := range m.entries {
		if e.target != nil {
			targets = append(targets, e.target)
		}
	}
	m.mu.RUnlock()
	for _, t := range targets {
		t.Wake()
	}
}

// Unregister closes and removes one named channel from lifecycle management.
func (m *ChannelManager) Unregister(id string) error {
	m.mu.Lock()
	e, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	delete(m.entries, id)
	m.order = slices.DeleteFunc(m.order, func(s string) bool { return s == id })
	watchStop := e.watchStop
	swapCh := e.swapCh
	target := e.target
	m.mu.Unlock()

	if watchStop != nil {
		watchStop()
	}
	close(swapCh)
	if target != nil {
		return target.Close()
	}
	return nil
}

// CloseAll stops all connectivity watchers, waits for goroutines to exit cleanly,
// and closes every managed HTTP/2 gRPC channel.
func (m *ChannelManager) CloseAll() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	targets := make([]*Target, 0, len(m.entries))
	swapChs := make([]chan struct{}, 0, len(m.entries))
	for _, e := range m.entries {
		if e.watchStop != nil {
			e.watchStop()
		}
		if e.target != nil {
			targets = append(targets, e.target)
		}
		if e.swapCh != nil {
			swapChs = append(swapChs, e.swapCh)
		}
	}
	m.entries = make(map[string]*managedEntry)
	m.order = nil
	m.mu.Unlock()

	m.cancel()
	for _, ch := range swapChs {
		close(ch)
	}
	var errs []error
	for _, t := range targets {
		if err := t.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	m.wg.Wait()
	return errors.Join(errs...)
}

func (m *ChannelManager) startWatchLocked(entry *managedEntry, target *Target, gen uint64) {
	wCtx, wCancel := context.WithCancel(m.runCtx)
	entry.watchStop = wCancel
	id := entry.id
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		target.WatchConnectivity(wCtx, func(prev, next connectivity.State) {
			m.mu.Lock()
			cur, ok := m.entries[id]
			if !ok || cur.generation != gen {
				m.mu.Unlock()
				return
			}
			cur.status.State = next
			cur.status.Phase = connectivityStateToPhase(next)
			cur.status.LastTransitionAt = time.Now()
			if next == connectivity.Ready && prev == connectivity.TransientFailure {
				cur.status.Reconnects++
				cur.status.LastError = ""
			} else if next == connectivity.TransientFailure {
				cur.status.LastError = "transient HTTP/2 channel failure"
			}
			snap := cur.status
			m.mu.Unlock()
			m.emit(snap)
		})
	}()
}

func (m *ChannelManager) emit(st ChannelStatus) {
	m.mu.RLock()
	listeners := make([]func(ChannelStatus), 0, len(m.subs))
	for _, fn := range m.subs {
		listeners = append(listeners, fn)
	}
	m.mu.RUnlock()
	for _, fn := range listeners {
		fn(st)
	}
}

func connectivityStateToPhase(state connectivity.State) transport.ConnPhase {
	switch state {
	case connectivity.Ready:
		return transport.PhaseReady
	case connectivity.Connecting:
		return transport.PhaseConnecting
	case connectivity.TransientFailure:
		return transport.PhaseReconnecting
	case connectivity.Shutdown:
		return transport.PhaseDisconnected
	default:
		return transport.PhaseConnecting
	}
}

// isRetryableStreamError classifies whether a gRPC stream error is transient
// (network drop, server restart, EOF on long-lived watch) versus terminal
// (auth denial, bad arguments, unsupported RPC, or caller cancellation).
func isRetryableStreamError(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if err == nil {
		return true
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	switch status.Code(err) {
	case codes.Unauthenticated,
		codes.PermissionDenied,
		codes.InvalidArgument,
		codes.NotFound,
		codes.Unimplemented,
		codes.FailedPrecondition,
		codes.OutOfRange:
		return false
	default:
		return true
	}
}

// resolveProviderTarget returns the currently active *Target from provider if
// present, otherwise falling back to staticTarget.
func resolveProviderTarget(provider TargetProvider, staticTarget *Target) (*Target, uint64) {
	if provider != nil {
		var gen uint64
		if sn, ok := provider.(swapNotifier); ok {
			gen = sn.Generation()
		}
		if t := provider.CurrentTarget(); t != nil {
			return t, gen
		}
	}
	return staticTarget, 0
}

// waitReconnectOrSwap sleeps up to delay, returning immediately if ctx is
// cancelled OR if provider swaps to a newer Target generation (e.g. sidecar respawn).
func waitReconnectOrSwap(ctx context.Context, provider TargetProvider, seenGen uint64, delay time.Duration) bool {
	if ctx.Err() != nil {
		return false
	}
	if delay <= 0 {
		delay = 50 * time.Millisecond
	}
	if sn, ok := provider.(swapNotifier); ok && seenGen > 0 {
		waitCtx, cancel := context.WithTimeout(ctx, delay)
		defer cancel()
		_, _ = sn.WaitSwap(waitCtx, seenGen)
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
