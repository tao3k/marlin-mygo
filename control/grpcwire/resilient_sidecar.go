// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tao3k/marlin-mygo/control/transport"
)

// ResilientSidecarConfig configures an auto-restarting, crash-healing local
// Rust Authority sidecar target. Every respawn rotates a fresh 64-hex
// crypto/rand ephemeral Bearer token delivered strictly via environment vars.
type ResilientSidecarConfig struct {
	ID             string
	DisplayName    string
	Sidecar        transport.SidecarConfig
	InitialBackoff time.Duration // defaults to 50ms
	MaxBackoff     time.Duration // defaults to 2s
	MaxRestarts    int           // defaults to 5; < 0 allows unlimited restarts
	OnCrash        func(attempt int, exitErr error)
	OnRecovered    func(ctx context.Context, target *Target, restartCount int)
	OnExhausted    func(lastErr error)
}

// ResilientSidecarTarget implements transport.TargetClient while transparently
// supervising a local Rust sidecar process. If the child crashes unexpectedly
// (e.g. SIGKILL / OOM), it applies bounded exponential backoff, spawns a fresh
// process with a rotated ephemeral Bearer token, swaps the live HTTP/2 gRPC
// channel atomically, and invokes OnRecovered so callers can rehydrate durable
// Turso sessions without changing the registered TargetRegistry ID.
type ResilientSidecarTarget struct {
	cfg    ResilientSidecarConfig
	runCtx context.Context
	cancel context.CancelFunc

	mu         sync.RWMutex
	current    *ManagedSidecarTarget
	generation uint64
	swapCh     chan struct{}
	restarts   int
	recovering bool
	closed     bool
	lastErr    error

	wg sync.WaitGroup
}

// LaunchResilientSidecar starts the initial supervised sidecar and arms the
// crash-detection and automatic token-rotating recovery loop.
func LaunchResilientSidecar(ctx context.Context, cfg ResilientSidecarConfig) (*ResilientSidecarTarget, error) {
	if cfg.ID == "" {
		return nil, fmt.Errorf("grpcwire: ResilientSidecarConfig.ID is required")
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 50 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 2 * time.Second
	}
	if cfg.MaxRestarts == 0 {
		cfg.MaxRestarts = 5
	}

	runCtx, cancel := context.WithCancel(context.Background())
	r := &ResilientSidecarTarget{
		cfg:    cfg,
		runCtx: runCtx,
		cancel: cancel,
		swapCh: make(chan struct{}),
	}

	managed, ready, err := r.spawnGeneration(ctx, 1)
	if err != nil {
		cancel()
		return nil, err
	}
	r.mu.Lock()
	r.current = managed
	r.generation = 1
	r.mu.Unlock()
	close(ready)
	return r, nil
}

func (r *ResilientSidecarTarget) spawnGeneration(ctx context.Context, gen uint64) (*ManagedSidecarTarget, chan struct{}, error) {
	sidecarCfg := r.cfg.Sidecar
	// Always rotate a fresh crypto/rand ephemeral Bearer token on every respawn.
	if gen > 1 {
		sidecarCfg.Token = ""
	}
	ready := make(chan struct{})
	origOnExit := r.cfg.Sidecar.OnExit
	sidecarCfg.OnExit = func(exitErr error) {
		<-ready
		if origOnExit != nil {
			origOnExit(exitErr)
		}
		r.onChildCrash(gen, exitErr)
	}
	managed, err := LaunchSidecarTarget(ctx, r.cfg.ID, r.cfg.DisplayName, sidecarCfg)
	if err != nil {
		close(ready)
		return nil, nil, err
	}
	return managed, ready, nil
}

func (r *ResilientSidecarTarget) onChildCrash(gen uint64, exitErr error) {
	r.mu.Lock()
	if r.closed || r.generation != gen || r.recovering {
		r.mu.Unlock()
		return
	}
	r.recovering = true
	r.lastErr = exitErr
	old := r.current
	r.wg.Add(1)
	r.mu.Unlock()

	go r.recoverLoop(old, exitErr)
}

func (r *ResilientSidecarTarget) recoverLoop(old *ManagedSidecarTarget, exitErr error) {
	defer r.wg.Done()

	// Close the dead gRPC client connection to avoid leaking HTTP/2 sockets.
	if old != nil && old.Target != nil {
		_ = old.Target.Close()
	}

	backoff := r.cfg.InitialBackoff
	for attempt := 1; ; attempt++ {
		if r.cfg.MaxRestarts > 0 && attempt > r.cfg.MaxRestarts {
			r.mu.Lock()
			r.recovering = false
			r.lastErr = fmt.Errorf("grpcwire: sidecar %s exhausted max restarts (%d): %w", r.cfg.ID, r.cfg.MaxRestarts, exitErr)
			err := r.lastErr
			r.mu.Unlock()
			if r.cfg.OnExhausted != nil {
				r.cfg.OnExhausted(err)
			}
			return
		}

		if r.cfg.OnCrash != nil {
			r.cfg.OnCrash(attempt, exitErr)
		}

		timer := time.NewTimer(backoff)
		select {
		case <-r.runCtx.Done():
			timer.Stop()
			r.mu.Lock()
			r.recovering = false
			r.mu.Unlock()
			return
		case <-timer.C:
		}

		r.mu.RLock()
		if r.closed {
			r.mu.RUnlock()
			return
		}
		nextGen := r.generation + 1
		r.mu.RUnlock()

		next, ready, err := r.spawnGeneration(r.runCtx, nextGen)
		if err != nil {
			exitErr = err
			backoff *= 2
			if backoff > r.cfg.MaxBackoff {
				backoff = r.cfg.MaxBackoff
			}
			continue
		}

		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			close(ready)
			_ = next.Close()
			return
		}
		r.current = next
		r.generation = nextGen
		r.restarts++
		r.recovering = false
		r.lastErr = nil
		oldSwap := r.swapCh
		r.swapCh = make(chan struct{})
		restarts := r.restarts
		r.mu.Unlock()
		close(oldSwap)
		close(ready)

		if r.cfg.OnRecovered != nil {
			r.cfg.OnRecovered(r.runCtx, next.Target, restarts)
		}
		return
	}
}

// CurrentTarget returns the currently live HTTP/2 gRPC Target.
func (r *ResilientSidecarTarget) CurrentTarget() *Target {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.current == nil {
		return nil
	}
	return r.current.Target
}

// Generation returns the monotonic sidecar spawn generation (starts at 1).
func (r *ResilientSidecarTarget) Generation() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.generation
}

// WaitSwap blocks until the sidecar respawns into a generation strictly greater
// than afterGen, or until ctx cancels or the supervisor closes.
func (r *ResilientSidecarTarget) WaitSwap(ctx context.Context, afterGen uint64) (uint64, bool) {
	r.mu.RLock()
	if r.closed {
		gen := r.generation
		r.mu.RUnlock()
		return gen, false
	}
	if r.generation > afterGen {
		gen := r.generation
		r.mu.RUnlock()
		return gen, true
	}
	ch := r.swapCh
	r.mu.RUnlock()

	select {
	case <-ch:
		return r.Generation(), true
	case <-r.runCtx.Done():
		return afterGen, false
	case <-ctx.Done():
		return afterGen, false
	}
}

// Sidecar returns the currently live SidecarSupervisor.
func (r *ResilientSidecarTarget) Sidecar() *transport.SidecarSupervisor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.current == nil {
		return nil
	}
	return r.current.Sidecar
}

// PID returns the OS process ID of the active sidecar child.
func (r *ResilientSidecarTarget) PID() int {
	if s := r.Sidecar(); s != nil {
		return s.PID()
	}
	return 0
}

// Restarts returns the total number of successful automatic crash recoveries.
func (r *ResilientSidecarTarget) Restarts() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.restarts
}

// Meta implements transport.TargetClient, reflecting PhaseConnecting while recovering.
func (r *ResilientSidecarTarget) Meta() transport.TargetMeta {
	r.mu.RLock()
	cur := r.current
	recovering := r.recovering
	lastErr := r.lastErr
	r.mu.RUnlock()

	if cur == nil || cur.Target == nil {
		meta := transport.TargetMeta{
			ID:          r.cfg.ID,
			DisplayName: r.cfg.DisplayName,
			Mode:        transport.ModeLocalSidecar,
			Phase:       transport.PhaseDisconnected,
		}
		if lastErr != nil {
			meta.Phase = transport.PhaseError
			meta.LastErrorBrief = lastErr.Error()
		}
		return meta
	}
	meta := cur.Target.Meta()
	if recovering {
		meta.Phase = transport.PhaseConnecting
		if lastErr != nil {
			meta.LastErrorBrief = lastErr.Error()
		}
	}
	return meta
}

// Ping implements transport.TargetClient by probing the currently live Target.
func (r *ResilientSidecarTarget) Ping(ctx context.Context) error {
	target := r.CurrentTarget()
	if target == nil {
		return errors.New("grpcwire: resilient sidecar has no active target")
	}
	return target.Ping(ctx)
}

// Close permanently stops supervision, cancels any pending restart backoff,
// and gracefully shuts down the active sidecar child process.
func (r *ResilientSidecarTarget) Close() error {
	r.mu.Lock()
	r.closed = true
	cur := r.current
	r.mu.Unlock()

	r.cancel()
	r.wg.Wait()

	// Re-read current in case recovery swapped right before cancel settled.
	r.mu.RLock()
	finalCur := r.current
	r.mu.RUnlock()
	if finalCur != nil && finalCur != cur {
		_ = finalCur.Close()
	}
	if cur != nil {
		return cur.Close()
	}
	return nil
}
