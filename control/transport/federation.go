// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package transport

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// NodeRole groups multi-machine sidebar targets by physical/operational role.
type NodeRole string

const (
	RoleLocalWorkstation NodeRole = "local-workstation"
	RoleRemoteAgent      NodeRole = "remote-agent"
	RoleMobileCompanion  NodeRole = "mobile-companion"
)

// NodeHealth tracks non-blocking health and governance telemetry for one target.
type NodeHealth struct {
	Phase             ConnPhase `json:"phase"`
	LatencyMS         int64     `json:"latencyMs"`
	PendingGates      int       `json:"pendingGates"`
	ActiveSessions    int       `json:"activeSessions"`
	MountedPlugins    int       `json:"mountedPlugins"`
	LastCheckedUnixMS int64     `json:"lastCheckedUnixMs"`
	LastError         string    `json:"lastError,omitempty"`
}

// FederatedNode is one rendered row inside the multi-machine workbench sidebar.
type FederatedNode struct {
	Meta   TargetMeta `json:"meta"`
	Role   NodeRole   `json:"role"`
	Health NodeHealth `json:"health"`
	Active bool       `json:"active"`
}

// FederationSnapshot is an immutable topology view safe for 120Hz UI reads.
type FederationSnapshot struct {
	ActiveTargetID    string          `json:"activeTargetId"`
	TotalPendingGates int             `json:"totalPendingGates"`
	Nodes             []FederatedNode `json:"nodes"`
}

// InferNodeRole derives a target's sidebar group from its Mode and TargetKind.
func InferNodeRole(meta TargetMeta) NodeRole {
	switch meta.TargetKind {
	case schema.TargetMobileIOS, schema.TargetMobileAndroid:
		return RoleMobileCompanion
	}
	switch meta.Mode {
	case ModeRemoteGRPC:
		return RoleRemoteAgent
	case ModeLocalEmbedded:
		return RoleMobileCompanion
	default:
		return RoleLocalWorkstation
	}
}

// FederationSupervisor coordinates health probes, cross-machine pending
// approval gate badges, and role-grouped sidebar topology across all targets.
type FederationSupervisor struct {
	registry *TargetRegistry

	mu      sync.RWMutex
	health  map[string]NodeHealth
	roles   map[string]NodeRole
	nextSub uint64
	subs    map[uint64]func(FederationSnapshot)
}

func NewFederationSupervisor(registry *TargetRegistry) *FederationSupervisor {
	return &FederationSupervisor{
		registry: registry,
		health:   make(map[string]NodeHealth),
		roles:    make(map[string]NodeRole),
		subs:     make(map[uint64]func(FederationSnapshot)),
	}
}

// SetRole overrides the inferred NodeRole for targetID.
func (s *FederationSupervisor) SetRole(targetID string, role NodeRole) {
	s.mu.Lock()
	s.roles[targetID] = role
	s.mu.Unlock()
	s.emit()
}

// UpdateMetrics records live session, mounted plugin, and pending gate counts
// pushed by WatchWorkbench / WatchPendingGates streams for targetID.
func (s *FederationSupervisor) UpdateMetrics(targetID string, pendingGates, activeSessions, mountedPlugins int) {
	s.mu.Lock()
	h := s.health[targetID]
	h.PendingGates = max(0, pendingGates)
	h.ActiveSessions = max(0, activeSessions)
	h.MountedPlugins = max(0, mountedPlugins)
	s.health[targetID] = h
	s.mu.Unlock()
	s.emit()
}

// ProbeAll concurrently pings every registered target with per-node timeout
// and updates Phase + LatencyMS atomically without blocking UI frames.
func (s *FederationSupervisor) ProbeAll(ctx context.Context, perTargetTimeout time.Duration) FederationSnapshot {
	if perTargetTimeout <= 0 {
		perTargetTimeout = 2 * time.Second
	}
	metas := s.registry.List()
	var wg sync.WaitGroup
	for _, meta := range metas {
		client, ok := s.lookupClient(meta.ID)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(id string, c TargetClient) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, perTargetTimeout)
			defer cancel()
			start := time.Now()
			err := c.Ping(pctx)
			elapsed := time.Since(start).Milliseconds()

			s.mu.Lock()
			h := s.health[id]
			h.LastCheckedUnixMS = time.Now().UnixMilli()
			h.LatencyMS = elapsed
			if err != nil {
				h.Phase = PhaseError
				h.LastError = err.Error()
			} else {
				h.Phase = PhaseReady
				h.LastError = ""
			}
			s.health[id] = h
			s.mu.Unlock()
		}(meta.ID, client)
	}
	wg.Wait()
	snap := s.Snapshot()
	s.emitSnapshot(snap)
	return snap
}

func (s *FederationSupervisor) lookupClient(id string) (TargetClient, bool) {
	s.registry.mu.RLock()
	defer s.registry.mu.RUnlock()
	c, ok := s.registry.targets[id]
	return c, ok
}

// Snapshot returns the deterministic role-ordered multi-machine topology.
func (s *FederationSupervisor) Snapshot() FederationSnapshot {
	metas := s.registry.List()
	activeID := ""
	if active, ok := s.registry.Active(); ok {
		activeID = active.Meta().ID
	}

	s.mu.RLock()
	nodes := make([]FederatedNode, 0, len(metas))
	totalGates := 0
	for _, meta := range metas {
		role, ok := s.roles[meta.ID]
		if !ok {
			role = InferNodeRole(meta)
		}
		h, hasHealth := s.health[meta.ID]
		if !hasHealth {
			h.Phase = meta.Phase
		} else if h.Phase == "" {
			h.Phase = meta.Phase
		}
		totalGates += h.PendingGates
		nodes = append(nodes, FederatedNode{
			Meta:   meta,
			Role:   role,
			Health: h,
			Active: meta.ID == activeID,
		})
	}
	s.mu.RUnlock()

	slices.SortStableFunc(nodes, func(a, b FederatedNode) int {
		return roleRank(a.Role) - roleRank(b.Role)
	})
	return FederationSnapshot{
		ActiveTargetID:    activeID,
		TotalPendingGates: totalGates,
		Nodes:             nodes,
	}
}

// NextGateTarget returns the first target ID carrying pending approval gates,
// preferring non-active machines so the user can jump directly to blocked remote agents.
func (s *FederationSupervisor) NextGateTarget() (string, bool) {
	snap := s.Snapshot()
	var fallback string
	for _, n := range snap.Nodes {
		if n.Health.PendingGates > 0 {
			if !n.Active {
				return n.Meta.ID, true
			}
			if fallback == "" {
				fallback = n.Meta.ID
			}
		}
	}
	if fallback != "" {
		return fallback, true
	}
	return "", false
}

func (s *FederationSupervisor) Subscribe(fn func(FederationSnapshot)) func() {
	s.mu.Lock()
	s.nextSub++
	id := s.nextSub
	s.subs[id] = fn
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.subs, id)
		s.mu.Unlock()
	}
}

func (s *FederationSupervisor) emit() {
	s.emitSnapshot(s.Snapshot())
}

func (s *FederationSupervisor) emitSnapshot(snap FederationSnapshot) {
	s.mu.RLock()
	listeners := make([]func(FederationSnapshot), 0, len(s.subs))
	for _, fn := range s.subs {
		listeners = append(listeners, fn)
	}
	s.mu.RUnlock()
	for _, fn := range listeners {
		fn(snap)
	}
}

func roleRank(r NodeRole) int {
	switch r {
	case RoleLocalWorkstation:
		return 0
	case RoleRemoteAgent:
		return 1
	case RoleMobileCompanion:
		return 2
	default:
		return 3
	}
}
