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

// GovernanceDelegationEdge mirrors one live parent->child AI delegation edge persisted in MGF1.
type GovernanceDelegationEdge struct {
	Parent string `json:"parent"`
	Child  string `json:"child"`
}

// GovernanceContextFlow mirrors one live 5-scope shared-context flow persisted in MGF1.
type GovernanceContextFlow struct {
	Entry    string `json:"entry"`
	SrcScope string `json:"src_scope"`
	Target   string `json:"target"`
	DstScope string `json:"dst_scope"`
}

// GovernanceDerivationSpec mirrors one dynamically registered Bi-Temporal causal derivation node.
type GovernanceDerivationSpec struct {
	DerivationID      string   `json:"derivation_id"`
	PolicyID          string   `json:"policy_id"`
	SubjectPremises   []string `json:"subject_premises"`
	ParentDerivations []string `json:"parent_derivations"`
}

// GovernanceTaintEdge mirrors one live CaMeL taint-flow edge persisted in MGF1 v2.
type GovernanceTaintEdge struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
}

// GovernanceCedarProbeResult mirrors the authoritative compiled-Cedar evaluation result
// returned by `marlin-scheme-host` (`scheme:governance-check-cedar`).
type GovernanceCedarProbeResult struct {
	Seq              uint64   `json:"seq"`
	Actor            string   `json:"actor"`
	Action           string   `json:"action"`
	Resource         string   `json:"resource"`
	StepUpFresh      bool     `json:"step_up_fresh"`
	Decision         string   `json:"decision"`
	MatchedPolicyIDs []string `json:"matched_policy_ids"`
}

// BackendGovernanceSnapshot mirrors the authoritative 14-slot Rust+Scheme
// GovernanceProofEnvelope, ExecutionCircuitBreaker, and MGF1 v2 fact-graph state
// projected from `marlin-agent-core`.
type BackendGovernanceSnapshot struct {
	Admitted                bool
	RevocationEpoch         uint64
	PolicyRevision          uint64
	BreakerTripped          bool
	BreakerTripEpoch        uint64
	BreakerBudgetUsed       uint32
	BreakerActiveLeases     int
	RearmGateID             string
	LastEventNote           string
	AssessmentDigest        string
	TaintProofDigest        string
	FixpointProofDigest     string
	TemporalProofDigest     string
	InvalidationDigest      string
	RejectionReasons        []string
	FencedActors            []string
	BlockedTools            []string
	RetractedConclusions    []string
	SynthesizedCedarRules   []SynthesizedCedarRule
	FactDelegations         []GovernanceDelegationEdge
	FactRevokedActors       []string
	FactContextFlows        []GovernanceContextFlow
	FactDeclassifiedEntries []string
	FactRetractedSubjects   []string
	FactDerivations         []GovernanceDerivationSpec
	FactTaintEdges          []GovernanceTaintEdge
	FactTaintSources        []string
	FactTaintSinks          []string
	FactTaintSanitizers     []string
	LastCedarProbe          *GovernanceCedarProbeResult
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
	RegisterDerivation(derivationID, policyID string, subjectPremises, parentDerivations []string) (BackendGovernanceSnapshot, error)
	RetractPremise(subject string) (BackendGovernanceSnapshot, error)
	AddTaintEdge(src, dst, role string) (BackendGovernanceSnapshot, error)
	AddTaintSanitizer(node string) (BackendGovernanceSnapshot, error)
	CheckCedar(actor, action, resource string, stepUpFresh bool) (BackendGovernanceSnapshot, error)
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
	b.snap.FactDelegations = append(b.snap.FactDelegations, GovernanceDelegationEdge{Parent: parent, Child: child})
	b.snap.LastEventNote = fmt.Sprintf("delegated %s -> %s", parent, child)
	return cloneBackendSnapshot(b.snap), nil
}

func (b *LocalBackendAuthorityBridge) RevokeActor(actor string) (BackendGovernanceSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snap.RevocationEpoch++
	b.snap.PolicyRevision++
	b.snap.FactRevokedActors = appendUniqueString(b.snap.FactRevokedActors, actor)
	// Propagate transitive closure across local preview delegations.
	queue := []string{actor}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		b.snap.FencedActors = appendUniqueString(b.snap.FencedActors, cur)
		for _, edge := range b.snap.FactDelegations {
			if edge.Parent == cur && !sliceContainsString(b.snap.FencedActors, edge.Child) {
				queue = append(queue, edge.Child)
			}
		}
	}
	b.snap.LastEventNote = "revoked " + actor
	return cloneBackendSnapshot(b.snap), nil
}

func (b *LocalBackendAuthorityBridge) RecordContextFlow(entry, srcScope, target, dstScope string) (BackendGovernanceSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snap.PolicyRevision++
	b.snap.FactContextFlows = append(b.snap.FactContextFlows, GovernanceContextFlow{
		Entry: entry, SrcScope: srcScope, Target: target, DstScope: dstScope,
	})
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
	b.snap.FactDeclassifiedEntries = appendUniqueString(b.snap.FactDeclassifiedEntries, entry)
	b.snap.LastEventNote = "declassified " + entry
	return cloneBackendSnapshot(b.snap), nil
}

func (b *LocalBackendAuthorityBridge) RegisterDerivation(derivationID, policyID string, subjectPremises, parentDerivations []string) (BackendGovernanceSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snap.PolicyRevision++
	spec := GovernanceDerivationSpec{
		DerivationID:      derivationID,
		PolicyID:          policyID,
		SubjectPremises:   append([]string(nil), subjectPremises...),
		ParentDerivations: append([]string(nil), parentDerivations...),
	}
	replaced := false
	for i := range b.snap.FactDerivations {
		if b.snap.FactDerivations[i].DerivationID == derivationID {
			b.snap.FactDerivations[i] = spec
			replaced = true
			break
		}
	}
	if !replaced {
		b.snap.FactDerivations = append(b.snap.FactDerivations, spec)
	}
	b.snap.LastEventNote = fmt.Sprintf("registered derivation %s (%s)", derivationID, policyID)
	return cloneBackendSnapshot(b.snap), nil
}

func (b *LocalBackendAuthorityBridge) RetractPremise(subject string) (BackendGovernanceSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snap.RevocationEpoch++
	b.snap.PolicyRevision++
	b.snap.Admitted = false
	b.snap.BreakerTripped = true
	b.snap.FactRetractedSubjects = appendUniqueString(b.snap.FactRetractedSubjects, subject)
	if len(b.snap.FactDerivations) > 0 {
		// Propagate reverse-dependency blast radius over local preview derivation DAG.
		var affected []string
		for _, d := range b.snap.FactDerivations {
			if sliceContainsString(d.SubjectPremises, subject) {
				affected = appendUniqueString(affected, d.DerivationID)
			}
		}
		changed := true
		for changed {
			changed = false
			for _, d := range b.snap.FactDerivations {
				if sliceContainsString(affected, d.DerivationID) {
					continue
				}
				for _, p := range d.ParentDerivations {
					if sliceContainsString(affected, p) {
						affected = append(affected, d.DerivationID)
						changed = true
						break
					}
				}
			}
		}
		for _, id := range affected {
			b.snap.RetractedConclusions = appendUniqueString(b.snap.RetractedConclusions, id)
		}
	} else {
		b.snap.RetractedConclusions = appendUniqueString(b.snap.RetractedConclusions, "D-TOOL-EXEC")
	}
	b.snap.LastEventNote = "retracted " + subject
	return cloneBackendSnapshot(b.snap), nil
}

func (b *LocalBackendAuthorityBridge) AddTaintEdge(src, dst, role string) (BackendGovernanceSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snap.PolicyRevision++
	b.snap.FactTaintEdges = append(b.snap.FactTaintEdges, GovernanceTaintEdge{Src: src, Dst: dst})
	if role == "source" || role == "both" {
		b.snap.FactTaintSources = appendUniqueString(b.snap.FactTaintSources, src)
	}
	if role == "sink" || role == "both" {
		b.snap.FactTaintSinks = appendUniqueString(b.snap.FactTaintSinks, dst)
	}
	b.snap.LastEventNote = fmt.Sprintf("recorded taint edge %s -> %s", src, dst)
	return cloneBackendSnapshot(b.snap), nil
}

func (b *LocalBackendAuthorityBridge) AddTaintSanitizer(node string) (BackendGovernanceSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snap.PolicyRevision++
	b.snap.FactTaintSanitizers = appendUniqueString(b.snap.FactTaintSanitizers, node)
	b.snap.LastEventNote = "sanitized taint node " + node
	return cloneBackendSnapshot(b.snap), nil
}

func (b *LocalBackendAuthorityBridge) CheckCedar(actor, action, resource string, stepUpFresh bool) (BackendGovernanceSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	seq := uint64(1)
	if b.snap.LastCedarProbe != nil {
		seq = b.snap.LastCedarProbe.Seq + 1
	}
	decision := "implicit_deny"
	var matched []string
	if sliceContainsString(b.snap.FencedActors, actor) {
		decision = "deny"
		matched = []string{"marlin.ascent.forbid.fenced." + actor}
	} else if (action == "rearm_breaker" || action == "promote_context_scope") && len(actor) >= 5 && actor[:5] == "agent" {
		decision = "deny"
		matched = []string{"marlin.forbid.agent-privileged-governance"}
	} else if action == "rearm_breaker" && stepUpFresh {
		decision = "allow"
		matched = []string{"marlin.permit.verified-human-governance"}
	}
	b.snap.LastCedarProbe = &GovernanceCedarProbeResult{
		Seq:              seq,
		Actor:            actor,
		Action:           action,
		Resource:         resource,
		StepUpFresh:      stepUpFresh,
		Decision:         decision,
		MatchedPolicyIDs: matched,
	}
	b.snap.LastEventNote = fmt.Sprintf("cedar probe #%d -> %s", seq, decision)
	return cloneBackendSnapshot(b.snap), nil
}

func appendUniqueString(items []string, value string) []string {
	if sliceContainsString(items, value) {
		return items
	}
	return append(items, value)
}

func sliceContainsString(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

func cloneBackendSnapshot(in BackendGovernanceSnapshot) BackendGovernanceSnapshot {
	out := in
	out.RejectionReasons = append([]string(nil), in.RejectionReasons...)
	out.FencedActors = append([]string(nil), in.FencedActors...)
	out.BlockedTools = append([]string(nil), in.BlockedTools...)
	out.RetractedConclusions = append([]string(nil), in.RetractedConclusions...)
	out.SynthesizedCedarRules = append([]SynthesizedCedarRule(nil), in.SynthesizedCedarRules...)
	out.FactDelegations = append([]GovernanceDelegationEdge(nil), in.FactDelegations...)
	out.FactRevokedActors = append([]string(nil), in.FactRevokedActors...)
	out.FactContextFlows = append([]GovernanceContextFlow(nil), in.FactContextFlows...)
	out.FactDeclassifiedEntries = append([]string(nil), in.FactDeclassifiedEntries...)
	out.FactRetractedSubjects = append([]string(nil), in.FactRetractedSubjects...)
	if len(in.FactDerivations) > 0 {
		out.FactDerivations = make([]GovernanceDerivationSpec, len(in.FactDerivations))
		for i, d := range in.FactDerivations {
			out.FactDerivations[i] = GovernanceDerivationSpec{
				DerivationID:      d.DerivationID,
				PolicyID:          d.PolicyID,
				SubjectPremises:   append([]string(nil), d.SubjectPremises...),
				ParentDerivations: append([]string(nil), d.ParentDerivations...),
			}
		}
	}
	out.FactTaintEdges = append([]GovernanceTaintEdge(nil), in.FactTaintEdges...)
	out.FactTaintSources = append([]string(nil), in.FactTaintSources...)
	out.FactTaintSinks = append([]string(nil), in.FactTaintSinks...)
	out.FactTaintSanitizers = append([]string(nil), in.FactTaintSanitizers...)
	if in.LastCedarProbe != nil {
		cp := *in.LastCedarProbe
		cp.MatchedPolicyIDs = append([]string(nil), in.LastCedarProbe.MatchedPolicyIDs...)
		out.LastCedarProbe = &cp
	}
	return out
}
