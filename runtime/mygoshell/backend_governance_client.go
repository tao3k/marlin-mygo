// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"errors"
	"fmt"
	"sync"
)

// SynthesizedCedarRule mirrors one dynamically synthesized Cedar policy emitted by
// Marlin's Gerbil Scheme Higher-Order Governance Engine (ASCENT fixpoint -> Cedar).
type SynthesizedCedarRule struct {
	PolicyID string
	Source   string
}

// BackendHigherOrderSnapshot mirrors the authoritative 14-slot Rust+Scheme
// HigherOrderGovernanceProofEnvelope & ExecutionCircuitBreaker state projected
// from `marlin-agent-core`.
type BackendHigherOrderSnapshot struct {
	Admitted              bool
	RevocationEpoch       uint64
	PolicyRevision        uint64
	BreakerTripped        bool
	BreakerTripEpoch      uint64
	BreakerBudgetUsed     uint32
	BreakerActiveLeases   int
	RearmGateID           string
	LastEventNote         string
	AssessmentDigest      string
	TaintProofDigest      string
	FixpointProofDigest   string
	TemporalProofDigest   string
	InvalidationDigest    string
	RejectionReasons      []string
	FencedActors          []string
	BlockedTools          []string
	RetractedConclusions  []string
	SynthesizedCedarRules []SynthesizedCedarRule
}

// BackendGovernanceAuthority is the thin-client boundary between `marlin-mygo`'s
// Tier-0 Governance Console UI and the authoritative Rust+Scheme governance plane
// in `marlin-agent-core`.
type BackendGovernanceAuthority interface {
	Snapshot() BackendHigherOrderSnapshot
	TripBackendBreaker(actor, reason string) (BackendHigherOrderSnapshot, error)
	RearmBackendBreakerWithStepUp(actor string, stepUpFresh bool) (BackendHigherOrderSnapshot, error)
}

// LocalBackendAuthorityBridge provides a thread-safe in-memory implementation of
// BackendGovernanceAuthority for tests, local preview, and gRPC transport shims.
type LocalBackendAuthorityBridge struct {
	mu   sync.RWMutex
	snap BackendHigherOrderSnapshot
}

// NewLocalBackendAuthorityBridge creates a bridge seeded with an authoritative snapshot.
func NewLocalBackendAuthorityBridge(initial BackendHigherOrderSnapshot) *LocalBackendAuthorityBridge {
	if initial.BreakerTripEpoch == 0 {
		initial.BreakerTripEpoch = 1
	}
	if initial.RevocationEpoch == 0 {
		initial.RevocationEpoch = 1
	}
	if initial.PolicyRevision == 0 {
		initial.PolicyRevision = 1
	}
	return &LocalBackendAuthorityBridge{snap: cloneBackendSnapshot(initial)}
}

// UpdateSnapshot installs a newly synchronized Rust+Scheme proof envelope.
// Stale revocation epochs are rejected fail-closed.
func (b *LocalBackendAuthorityBridge) UpdateSnapshot(next BackendHigherOrderSnapshot) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if next.RevocationEpoch < b.snap.RevocationEpoch {
		return fmt.Errorf("backend governance: stale proof epoch %d < %d", next.RevocationEpoch, b.snap.RevocationEpoch)
	}
	b.snap = cloneBackendSnapshot(next)
	if !b.snap.Admitted || len(b.snap.RetractedConclusions) > 0 {
		b.snap.BreakerTripped = true
	}
	return nil
}

// Snapshot returns an immutable copy of the current authoritative backend governance state.
func (b *LocalBackendAuthorityBridge) Snapshot() BackendHigherOrderSnapshot {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return cloneBackendSnapshot(b.snap)
}

// TripBackendBreaker latches the backend ExecutionCircuitBreaker fail-closed.
func (b *LocalBackendAuthorityBridge) TripBackendBreaker(actor, reason string) (BackendHigherOrderSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snap.BreakerTripped = true
	b.snap.Admitted = false
	if reason != "" {
		b.snap.RejectionReasons = append([]string{"operator-estop:" + actor + ":" + reason}, b.snap.RejectionReasons...)
	}
	return cloneBackendSnapshot(b.snap), nil
}

// RearmBackendBreakerWithStepUp enforces RFC 9470 human step-up verification before
// advancing BreakerTripEpoch and clearing the fail-closed E-Stop latch.
func (b *LocalBackendAuthorityBridge) RearmBackendBreakerWithStepUp(actor string, stepUpFresh bool) (BackendHigherOrderSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.snap.BreakerTripped {
		return cloneBackendSnapshot(b.snap), errors.New("backend governance: breaker is not tripped")
	}
	if actor == "" || !stepUpFresh {
		return cloneBackendSnapshot(b.snap), errors.New("backend governance: verified human RFC 9470 step-up required to re-arm")
	}
	b.snap.BreakerTripped = false
	b.snap.BreakerTripEpoch++
	b.snap.BreakerBudgetUsed = 0
	b.snap.RevocationEpoch++
	b.snap.RejectionReasons = nil
	b.snap.RetractedConclusions = nil
	b.snap.Admitted = true
	return cloneBackendSnapshot(b.snap), nil
}

func cloneBackendSnapshot(in BackendHigherOrderSnapshot) BackendHigherOrderSnapshot {
	out := in
	out.RejectionReasons = append([]string(nil), in.RejectionReasons...)
	out.FencedActors = append([]string(nil), in.FencedActors...)
	out.BlockedTools = append([]string(nil), in.BlockedTools...)
	out.RetractedConclusions = append([]string(nil), in.RetractedConclusions...)
	out.SynthesizedCedarRules = append([]SynthesizedCedarRule(nil), in.SynthesizedCedarRules...)
	return out
}
