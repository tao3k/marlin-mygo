// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package transport unifies local desktop sidecars, mobile embedded hosts,
// and remote gRPC machines behind a single multi-target registry.
package transport

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

var (
	ErrTargetNotFound = errors.New("transport: target not found")
	ErrTargetExists   = errors.New("transport: target already registered")
)

// Mode identifies how the MyGo shell reaches a Marlin AppServer host.
type Mode string

const (
	ModeLocalSidecar  Mode = "local-sidecar"  // Desktop: spawn marlin-host over loopback HTTP/2
	ModeLocalEmbedded Mode = "local-embedded" // iOS .xcframework / Android .so in-process host
	ModeRemoteGRPC    Mode = "remote-grpc"    // Remote macOS/Linux/cloud host over mTLS/Bearer HTTP/2
)

// ConnPhase drives the client shell status indicator.
type ConnPhase string

const (
	PhaseDisconnected ConnPhase = "disconnected"
	PhaseConnecting   ConnPhase = "connecting"
	PhaseReady        ConnPhase = "ready"
	PhaseReconnecting ConnPhase = "reconnecting"
	PhaseError        ConnPhase = "error"
)

// TargetMeta describes one sidebar machine/host entry.
type TargetMeta struct {
	ID             string            `json:"id"`
	DisplayName    string            `json:"displayName"`
	Mode           Mode              `json:"mode"`
	TargetKind     schema.TargetKind `json:"targetKind"`
	Address        string            `json:"address"`
	Phase          ConnPhase         `json:"phase"`
	ActiveProfile  string            `json:"activeProfile,omitempty"`
	SupportedSvcs  []string          `json:"supportedServices,omitempty"`
	LastErrorBrief string            `json:"lastErrorBrief,omitempty"`
}

// TargetClient is the uniform handle returned by TargetRegistry for Local
// Sidecar, Mobile Embedded, and Remote gRPC hosts. Concrete gRPC stubs from
// gen/marlin/*/v1 attach through ServiceSet.
type TargetClient interface {
	Meta() TargetMeta
	Ping(ctx context.Context) error
	Close() error
}

// TargetRegistry maintains all local and remote targets mapped in the sidebar.
type TargetRegistry struct {
	mu        sync.RWMutex
	order     []string
	targets   map[string]TargetClient
	activeID  string
	nextSubID uint64
	subs      map[uint64]func([]TargetMeta, string)
}

func NewTargetRegistry() *TargetRegistry {
	return &TargetRegistry{
		targets: make(map[string]TargetClient),
		subs:    make(map[uint64]func([]TargetMeta, string)),
	}
}

func (r *TargetRegistry) Register(client TargetClient) error {
	meta := client.Meta()
	if meta.ID == "" {
		return fmt.Errorf("transport: target ID is required")
	}
	r.mu.Lock()
	if _, exists := r.targets[meta.ID]; exists {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrTargetExists, meta.ID)
	}
	r.targets[meta.ID] = client
	r.order = append(r.order, meta.ID)
	if r.activeID == "" {
		r.activeID = meta.ID
	}
	snapshot, active, listeners := r.snapshotLocked()
	r.mu.Unlock()
	notifyListeners(listeners, snapshot, active)
	return nil
}

func (r *TargetRegistry) Unregister(id string) error {
	r.mu.Lock()
	c, ok := r.targets[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrTargetNotFound, id)
	}
	delete(r.targets, id)
	r.order = slices.DeleteFunc(r.order, func(s string) bool { return s == id })
	if r.activeID == id {
		if len(r.order) > 0 {
			r.activeID = r.order[0]
		} else {
			r.activeID = ""
		}
	}
	snapshot, active, listeners := r.snapshotLocked()
	r.mu.Unlock()
	_ = c.Close()
	notifyListeners(listeners, snapshot, active)
	return nil
}

func (r *TargetRegistry) Switch(id string) (TargetClient, error) {
	r.mu.Lock()
	c, ok := r.targets[id]
	if !ok {
		r.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrTargetNotFound, id)
	}
	r.activeID = id
	snapshot, active, listeners := r.snapshotLocked()
	r.mu.Unlock()
	notifyListeners(listeners, snapshot, active)
	return c, nil
}

func (r *TargetRegistry) Active() (TargetClient, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.targets[r.activeID]
	return c, ok
}

func (r *TargetRegistry) List() []TargetMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]TargetMeta, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.targets[id].Meta())
	}
	return out
}

func (r *TargetRegistry) Subscribe(fn func(targets []TargetMeta, activeID string)) func() {
	r.mu.Lock()
	r.nextSubID++
	id := r.nextSubID
	r.subs[id] = fn
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.subs, id)
		r.mu.Unlock()
	}
}

func (r *TargetRegistry) snapshotLocked() ([]TargetMeta, string, []func([]TargetMeta, string)) {
	metas := make([]TargetMeta, 0, len(r.order))
	for _, id := range r.order {
		metas = append(metas, r.targets[id].Meta())
	}
	listeners := make([]func([]TargetMeta, string), 0, len(r.subs))
	for _, fn := range r.subs {
		listeners = append(listeners, fn)
	}
	return metas, r.activeID, listeners
}

func notifyListeners(listeners []func([]TargetMeta, string), metas []TargetMeta, active string) {
	for _, fn := range listeners {
		fn(slices.Clone(metas), active)
	}
}
