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
// Marlin's Gerbil Scheme Governance Authority Engine (ASCENT fixpoint -> Cedar).
type SynthesizedCedarRule struct {
	PolicyID string
	Source   string
}

// BackendGovernanceSnapshot mirrors the authoritative 14-slot Rust+Scheme
// GovernanceProofEnvelope & ExecutionCircuitBreaker state projected
// from `marlin-agent-core`.
type BackendGovernanceSnapshot struct {
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
	Snapshot() BackendGovernanceSnapshot
	TripBackendBreaker(actor, reason string) (BackendGovernanceSnapshot, error)
	RearmBackendBreakerWithStepUp(actor string, stepUpFresh bool) (BackendGovernanceSnapshot, error)
}

// BackendGovernanceMutator optionally extends BackendGovernanceAuthority with live
// runtime governance fact mutations and on-demand Scheme VM re-evaluations.
type BackendGovernanceMutator interface {
	RunIncidentEval() (BackendGovernanceSnapshot, error)
	RunCleanEval() (BackendGovernanceSnapshot, error)
	DelegateActor(parent, child string) (BackendGovernanceSnapshot, error)
	RevokeActor(actor string) (BackendGovernanceSnapshot, error)
	RecordContextFlow(entry, srcScope, target, dstScope string) (BackendGovernanceSnapshot, error)
	DeclassifyContext(entry string) (BackendGovernanceSnapshot, error)
	RetractPremise(subject string) (BackendGovernanceSnapshot, error)
}

// LocalBackendAuthorityBridge provides a thread-safe in-memory implementation of
// BackendGovernanceAuthority for tests, local preview, and gRPC transport shims.
type LocalBackendAuthorityBridge struct {
	mu   sync.RWMutex
	snap BackendGovernanceSnapshot
}

// NewLocalBackendAuthorityBridge creates a bridge seeded with an authoritative snapshot.
func NewLocalBackendAuthorityBridge(initial BackendGovernanceSnapshot) *LocalBackendAuthorityBridge {
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
func (b *LocalBackendAuthorityBridge) UpdateSnapshot(next BackendGovernanceSnapshot) error {
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
func (b *LocalBackendAuthorityBridge) Snapshot() BackendGovernanceSnapshot {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return cloneBackendSnapshot(b.snap)
}

// TripBackendBreaker latches the backend ExecutionCircuitBreaker fail-closed.
func (b *LocalBackendAuthorityBridge) TripBackendBreaker(actor, reason string) (BackendGovernanceSnapshot, error) {
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
func (b *LocalBackendAuthorityBridge) RearmBackendBreakerWithStepUp(actor string, stepUpFresh bool) (BackendGovernanceSnapshot, error) {
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

// RunIncidentEval simulates a Scheme governance incident evaluation on the local preview bridge.
func (b *LocalBackendAuthorityBridge) RunIncidentEval() (BackendGovernanceSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snap.RevocationEpoch++
	b.snap.PolicyRevision++
	b.snap.Admitted = false
	b.snap.BreakerTripped = true
	b.snap.RearmGateID = fmt.Sprintf("gov-rearm-epoch-%d", b.snap.BreakerTripEpoch)
	b.snap.FencedActors = []string{"agent-lead", "agent-sub"}
	b.snap.BlockedTools = []string{"os-shell"}
	b.snap.RetractedConclusions = []string{"D-OBO-TICKET", "D-TOOL-EXEC"}
	b.snap.RejectionReasons = []string{"ascent-camel-taint-alert", "bitemporal-premise-retracted"}
	b.snap.SynthesizedCedarRules = []SynthesizedCedarRule{
		{PolicyID: "marlin.ascent.forbid.fenced.agent-sub", Source: `forbid(principal == Marlin::Actor::"agent-sub", action, resource);`},
	}
	b.snap.LastEventNote = "preview incident evaluated"
	return cloneBackendSnapshot(b.snap), nil
}

// RunCleanEval resets the local preview bridge to a clean sanitized policy state.
func (b *LocalBackendAuthorityBridge) RunCleanEval() (BackendGovernanceSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snap.RevocationEpoch++
	b.snap.PolicyRevision++
	b.snap.FencedActors = nil
	b.snap.BlockedTools = nil
	b.snap.RetractedConclusions = nil
	b.snap.SynthesizedCedarRules = nil
	if !b.snap.BreakerTripped {
		b.snap.Admitted = true
		b.snap.RejectionReasons = nil
	}
	b.snap.LastEventNote = "preview clean baseline evaluated"
	return cloneBackendSnapshot(b.snap), nil
}

func (b *LocalBackendAuthorityBridge) DelegateActor(parent, child string) (BackendGovernanceSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snap.PolicyRevision++
	b.snap.LastEventNote = fmt.Sprintf("delegated %s -> %s", parent, child)
	return cloneBackendSnapshot(b.snap), nil
}

func (b *LocalBackendAuthorityBridge) RevokeActor(actor string) (BackendGovernanceSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snap.RevocationEpoch++
	b.snap.PolicyRevision++
	b.snap.FencedActors = append(b.snap.FencedActors, actor)
	b.snap.LastEventNote = "revoked " + actor
	return cloneBackendSnapshot(b.snap), nil
}

func (b *LocalBackendAuthorityBridge) RecordContextFlow(entry, srcScope, target, dstScope string) (BackendGovernanceSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snap.PolicyRevision++
	if srcScope == "private" && dstScope == "external" {
		b.snap.Admitted = false
		b.snap.BreakerTripped = true
		b.snap.RejectionReasons = append(b.snap.RejectionReasons, "ascent-scope-escalation")
	}
	b.snap.LastEventNote = fmt.Sprintf("context flow %s(%s) -> %s(%s)", entry, srcScope, target, dstScope)
	return cloneBackendSnapshot(b.snap), nil
}

func (b *LocalBackendAuthorityBridge) DeclassifyContext(entry string) (BackendGovernanceSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snap.PolicyRevision++
	b.snap.LastEventNote = "declassified " + entry
	return cloneBackendSnapshot(b.snap), nil
}

func (b *LocalBackendAuthorityBridge) RetractPremise(subject string) (BackendGovernanceSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snap.RevocationEpoch++
	b.snap.PolicyRevision++
	b.snap.Admitted = false
	b.snap.BreakerTripped = true
	b.snap.RetractedConclusions = append(b.snap.RetractedConclusions, "D-TOOL-EXEC")
	b.snap.LastEventNote = "retracted " + subject
	return cloneBackendSnapshot(b.snap), nil
}

func cloneBackendSnapshot(in BackendGovernanceSnapshot) BackendGovernanceSnapshot {
	out := in
	out.RejectionReasons = append([]string(nil), in.RejectionReasons...)
	out.FencedActors = append([]string(nil), in.FencedActors...)
	out.BlockedTools = append([]string(nil), in.BlockedTools...)
	out.RetractedConclusions = append([]string(nil), in.RetractedConclusions...)
	out.SynthesizedCedarRules = append([]SynthesizedCedarRule(nil), in.SynthesizedCedarRules...)
	return out
}
