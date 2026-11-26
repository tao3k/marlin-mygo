// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gatev1 "github.com/tao3k/marlin-mygo/gen/marlin/gate/v1"
	projectionv1 "github.com/tao3k/marlin-mygo/gen/marlin/projection/v1"
	uiv1 "github.com/tao3k/marlin-mygo/gen/marlin/ui/v1"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

const (
	// GovernanceAuthorityRecordID matches `marlin-scheme-host`'s authoritative
	// ProjectionQueryService evidence record for Rust+Scheme Governance Authority.
	GovernanceAuthorityRecordID = "scheme:governance:authority-snapshot"
	// GovernanceAuthorityNodeKind matches the live WatchGraphTrajectory event kind.
	GovernanceAuthorityNodeKind = "scheme_governance_authority"
	// GovernanceRearmGatePrefix identifies one-shot ApprovalGateService re-arm gates.
	GovernanceRearmGatePrefix = "gov-rearm-epoch-"
)

// RustGovernanceAuthorityConfig wires a live HTTP/2 gRPC Target (or hot-swappable
// TargetProvider) into `mygoshell.BackendGovernanceAuthority`.
type RustGovernanceAuthorityConfig struct {
	Target                  *Target
	Provider                TargetProvider
	ProjectID               string
	SessionID               string
	Timeout                 time.Duration
	ReconnectInitialBackoff time.Duration
	ReconnectMaxBackoff     time.Duration
	OnSnapshot              func(mygoshell.BackendGovernanceSnapshot)
	OnStreamState           func(connected bool, err error)
}

// RustGovernanceAuthority implements `mygoshell.BackendGovernanceAuthority` over real
// HTTP/2 gRPC against `marlin-scheme-host` (`ProjectionQueryService`,
// `UiWorkbenchService`, and one-shot `ApprovalGateService`).
//
// Security invariant: E-Stop may be tripped immediately via `DispatchUiEvent`, whereas
// Re-Arm can NEVER bypass one-shot `ApprovalGateService::SubmitGraphHumanDecision`
// carrying an RFC 9470 Step-Up attestation.
type RustGovernanceAuthority struct {
	cfg RustGovernanceAuthorityConfig

	mu          sync.RWMutex
	snap        mygoshell.BackendGovernanceSnapshot
	hasSnapshot bool
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	idempSeq    atomic.Uint64
}

var (
	_ mygoshell.BackendGovernanceAuthority = (*RustGovernanceAuthority)(nil)
	_ mygoshell.BackendGovernanceMutator   = (*RustGovernanceAuthority)(nil)
)

// NewRustGovernanceAuthority constructs a live gRPC-backed governance authority client.
func NewRustGovernanceAuthority(cfg RustGovernanceAuthorityConfig) *RustGovernanceAuthority {
	if strings.TrimSpace(cfg.ProjectID) == "" {
		cfg.ProjectID = "default"
	}
	if strings.TrimSpace(cfg.SessionID) == "" {
		cfg.SessionID = "marlin-main"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.ReconnectInitialBackoff <= 0 {
		cfg.ReconnectInitialBackoff = 50 * time.Millisecond
	}
	if cfg.ReconnectMaxBackoff <= 0 {
		cfg.ReconnectMaxBackoff = 2 * time.Second
	}
	return &RustGovernanceAuthority{
		cfg: cfg,
		snap: mygoshell.BackendGovernanceSnapshot{
			Admitted:         true,
			RevocationEpoch:  1,
			BreakerTripEpoch: 1,
		},
	}
}

func (a *RustGovernanceAuthority) activeTarget() (*Target, uint64) {
	return resolveProviderTarget(a.cfg.Provider, a.cfg.Target)
}

// Hydrate fetches the latest authoritative Rust+Scheme Governance Authority snapshot
// via secret-denied `ProjectionQueryService.QueryProjectGraph`.
func (a *RustGovernanceAuthority) Hydrate(ctx context.Context) (mygoshell.BackendGovernanceSnapshot, error) {
	target, _ := a.activeTarget()
	if target == nil {
		return a.Snapshot(), errors.New("grpcwire: RustGovernanceAuthority has no active Target")
	}
	sess := a.cfg.SessionID
	resp, err := target.Projection().QueryProjectGraph(ctx, &projectionv1.QueryProjectGraphRequest{
		ProjectId: a.cfg.ProjectID,
		SessionId: &sess,
		Surfaces: []projectionv1.GraphVisibleSurface{
			projectionv1.GraphVisibleSurface_GRAPH_VISIBLE_SURFACE_EVIDENCE,
			projectionv1.GraphVisibleSurface_GRAPH_VISIBLE_SURFACE_WORKSPACE,
		},
		SecretVisibility: projectionv1.SecretVisibility_SECRET_VISIBILITY_DENIED,
	})
	if err != nil {
		return a.Snapshot(), fmt.Errorf("grpcwire: QueryProjectGraph governance hydrate: %w", err)
	}
	for _, rec := range resp.GetRecords() {
		if rec.GetRecordId() == GovernanceAuthorityRecordID || rec.GetKind() == "scheme.governance.authority_snapshot" {
			snap := DecodeGovernanceSnapshotFromFacets(rec.GetFacets())
			a.storeSnapshot(snap)
			return snap, nil
		}
	}
	return a.Snapshot(), fmt.Errorf("grpcwire: governance record %q not published by backend", GovernanceAuthorityRecordID)
}

// Start performs an initial best-effort hydrate and spawns an auto-reconnecting
// `WatchGraphTrajectory` stream pumping real-time Rust+Scheme governance updates.
func (a *RustGovernanceAuthority) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	a.cancel = cancel
	a.mu.Unlock()

	hydrateCtx, hCancel := context.WithTimeout(runCtx, a.cfg.Timeout)
	_, _ = a.Hydrate(hydrateCtx)
	hCancel()

	a.wg.Add(1)
	go a.pumpGovernanceTrajectory(runCtx)
	return nil
}

// Stop cancels the live trajectory watch and waits for clean shutdown.
func (a *RustGovernanceAuthority) Stop() {
	a.mu.Lock()
	cancel := a.cancel
	a.cancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.wg.Wait()
}

// Snapshot returns an immutable copy of the latest authoritative backend governance state.
func (a *RustGovernanceAuthority) Snapshot() mygoshell.BackendGovernanceSnapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cloneGovernanceSnapshot(a.snap)
}

// TripBackendBreaker dispatches an immediate operator E-Stop (`scheme:governance-estop`)
// over `UiWorkbenchService.DispatchUiEvent` and awaits backend confirmation.
func (a *RustGovernanceAuthority) TripBackendBreaker(actor, reason string) (mygoshell.BackendGovernanceSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)
	defer cancel()

	target, _ := a.activeTarget()
	if target == nil {
		return a.Snapshot(), errors.New("grpcwire: no active Rust backend target for E-Stop")
	}
	actorClean := strings.TrimSpace(actor)
	if actorClean == "" {
		actorClean = "operator"
	}
	reasonClean := strings.TrimSpace(reason)
	if reasonClean == "" {
		reasonClean = "ui-emergency-stop"
	}
	cmd := fmt.Sprintf("scheme:governance-estop %s:%s", actorClean, reasonClean)
	if err := a.dispatchUiCommand(ctx, target, cmd); err != nil {
		return a.Snapshot(), err
	}
	return a.awaitSnapshotCondition(ctx, func(s mygoshell.BackendGovernanceSnapshot) bool {
		return s.BreakerTripped && strings.TrimSpace(s.RearmGateID) != ""
	})
}

// RearmBackendBreakerWithStepUp submits a one-shot `ApprovalGateService.SubmitGraphHumanDecision`
// carrying an RFC 9470 Step-Up attestation to re-arm the Rust `ExecutionCircuitBreaker`.
func (a *RustGovernanceAuthority) RearmBackendBreakerWithStepUp(
	actor string,
	stepUpFresh bool,
) (mygoshell.BackendGovernanceSnapshot, error) {
	actorClean := strings.TrimSpace(actor)
	if actorClean == "" || !stepUpFresh {
		return a.Snapshot(), errors.New("backend governance: verified human RFC 9470 step-up required to re-arm")
	}

	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)
	defer cancel()

	target, _ := a.activeTarget()
	if target == nil {
		return a.Snapshot(), errors.New("grpcwire: no active Rust backend target for step-up re-arm")
	}

	// Ensure we have the freshest authoritative snapshot & active one-shot gate ID.
	cur, err := a.Hydrate(ctx)
	if err != nil {
		cur = a.Snapshot()
	}
	if !cur.BreakerTripped {
		return cur, errors.New("backend governance: breaker is not tripped")
	}
	gateID := strings.TrimSpace(cur.RearmGateID)
	if gateID == "" {
		gateID, err = a.discoverPendingRearmGate(ctx, target)
		if err != nil {
			return cur, err
		}
	}

	prevEpoch := cur.BreakerTripEpoch
	note := fmt.Sprintf("stepup-verified:actor=%s:acr=urn:ietf:params:oauth:acr:mfa", actorClean)
	seq := a.idempSeq.Add(1)
	idempKey := fmt.Sprintf("gov-rearm-%s-%d", gateID, seq)

	resp, err := target.Gate().SubmitGraphHumanDecision(ctx, &gatev1.SubmitGraphHumanDecisionRequest{
		GateId:         gateID,
		Decision:       gatev1.GraphHumanDecision_GRAPH_HUMAN_DECISION_APPROVE,
		IdempotencyKey: idempKey,
		Note:           &note,
	})
	if err != nil {
		return a.Snapshot(), fmt.Errorf("grpcwire: SubmitGraphHumanDecision re-arm failed: %w", err)
	}
	if rcpt := resp.GetReceipt(); rcpt == nil || !rcpt.GetClaimed() {
		msg := "re-arm gate decision was not claimed"
		if rcpt != nil && rcpt.GetDenial() != nil {
			msg = rcpt.GetDenial().GetMessage()
		}
		return a.Snapshot(), fmt.Errorf("backend governance: %s", msg)
	}

	// Await authoritative backend transition: either armed with advanced trip epoch, or
	// fail-closed denial with a rotated gate ID (e.g. agent impersonation rejected by Rust).
	snap, waitErr := a.awaitSnapshotCondition(ctx, func(s mygoshell.BackendGovernanceSnapshot) bool {
		if !s.BreakerTripped && s.BreakerTripEpoch > prevEpoch {
			return true
		}
		if s.BreakerTripped && s.RearmGateID != "" && s.RearmGateID != gateID {
			return true
		}
		return false
	})
	if waitErr != nil {
		return snap, waitErr
	}
	if snap.BreakerTripped {
		reason := strings.TrimSpace(snap.LastEventNote)
		if reason == "" {
			reason = "backend denied step-up re-arm"
		}
		return snap, errors.New(reason)
	}
	return snap, nil
}

// TriggerSchemeIncidentEval asks `marlin-scheme-host` to run the live embedded Gambit
// Scheme Governance Authority incident evaluation (ASCENT + Bi-Temporal TM + Cedar)
// and awaits the resulting authoritative proof snapshot.
func (a *RustGovernanceAuthority) TriggerSchemeIncidentEval(ctx context.Context) (mygoshell.BackendGovernanceSnapshot, error) {
	return a.triggerSchemeEval(ctx, "scheme:governance-eval-incident")
}

// TriggerSchemeCleanEval asks `marlin-scheme-host` to run a live embedded Gambit Scheme
// clean sanitized baseline evaluation and awaits the resulting authoritative proof snapshot.
func (a *RustGovernanceAuthority) TriggerSchemeCleanEval(ctx context.Context) (mygoshell.BackendGovernanceSnapshot, error) {
	return a.triggerSchemeEval(ctx, "scheme:governance-eval-clean")
}

// RunIncidentEval implements mygoshell.BackendGovernanceMutator using the configured RPC timeout.
func (a *RustGovernanceAuthority) RunIncidentEval() (mygoshell.BackendGovernanceSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)
	defer cancel()
	return a.TriggerSchemeIncidentEval(ctx)
}

// RunCleanEval implements mygoshell.BackendGovernanceMutator using the configured RPC timeout.
func (a *RustGovernanceAuthority) RunCleanEval() (mygoshell.BackendGovernanceSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)
	defer cancel()
	return a.TriggerSchemeCleanEval(ctx)
}

// DelegateActor records a live parent->child AI delegation edge and triggers immediate
// Linked-AOT/VM Scheme ASCENT governance fixpoint re-evaluation.
func (a *RustGovernanceAuthority) DelegateActor(parent, child string) (mygoshell.BackendGovernanceSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)
	defer cancel()
	return a.triggerSchemeEval(ctx, fmt.Sprintf("scheme:governance-delegate %s %s", strings.TrimSpace(parent), strings.TrimSpace(child)))
}

// RevokeActor revokes a principal in the live runtime fact graph and triggers immediate
// ASCENT transitive closure fencing & dynamic Cedar `forbid` synthesis.
func (a *RustGovernanceAuthority) RevokeActor(actor string) (mygoshell.BackendGovernanceSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)
	defer cancel()
	return a.triggerSchemeEval(ctx, fmt.Sprintf("scheme:governance-revoke %s", strings.TrimSpace(actor)))
}

// RecordContextFlow records a live 5-scope shared-context flow (`entry` from `srcScope`
// toward `target` at `dstScope`) and triggers immediate ASCENT exfiltration analysis.
func (a *RustGovernanceAuthority) RecordContextFlow(entry, srcScope, target, dstScope string) (mygoshell.BackendGovernanceSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)
	defer cancel()
	cmd := fmt.Sprintf(
		"scheme:governance-context-flow %s %s %s %s",
		strings.TrimSpace(entry),
		strings.TrimSpace(srcScope),
		strings.TrimSpace(target),
		strings.TrimSpace(dstScope),
	)
	return a.triggerSchemeEval(ctx, cmd)
}

// DeclassifyContext marks a shared-context entry as explicitly declassified and re-evaluates ASCENT.
func (a *RustGovernanceAuthority) DeclassifyContext(entry string) (mygoshell.BackendGovernanceSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)
	defer cancel()
	return a.triggerSchemeEval(ctx, fmt.Sprintf("scheme:governance-declassify %s", strings.TrimSpace(entry)))
}

// RegisterDerivation registers a dynamic Bi-Temporal Truth Maintenance derivation node
// into the live MGF1 v2 fact store and triggers immediate Linked-AOT/VM Scheme re-evaluation.
func (a *RustGovernanceAuthority) RegisterDerivation(
	derivationID, policyID string,
	subjectPremises, parentDerivations []string,
) (mygoshell.BackendGovernanceSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)
	defer cancel()
	premisesCSV := strings.Join(subjectPremises, ",")
	parentsCSV := "none"
	if len(parentDerivations) > 0 {
		parentsCSV = strings.Join(parentDerivations, ",")
	}
	cmd := fmt.Sprintf(
		"scheme:governance-register-derivation %s %s %s %s",
		strings.TrimSpace(derivationID),
		strings.TrimSpace(policyID),
		strings.TrimSpace(premisesCSV),
		strings.TrimSpace(parentsCSV),
	)
	return a.triggerSchemeEval(ctx, cmd)
}

// RetractPremise retracts a Bi-Temporal Truth Maintenance premise in the live Scheme VM
// and computes reverse-dependency blast radius invalidation.
func (a *RustGovernanceAuthority) RetractPremise(subject string) (mygoshell.BackendGovernanceSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)
	defer cancel()
	return a.triggerSchemeEval(ctx, fmt.Sprintf("scheme:governance-retract %s", strings.TrimSpace(subject)))
}

// AddTaintEdge records a live CaMeL taint-flow edge (`role` may be "", "source", "sink", or "both")
// and triggers immediate stratified Datalog ASCENT re-evaluation.
func (a *RustGovernanceAuthority) AddTaintEdge(src, dst, role string) (mygoshell.BackendGovernanceSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)
	defer cancel()
	roleClean := strings.TrimSpace(role)
	cmd := fmt.Sprintf("scheme:governance-taint-edge %s %s", strings.TrimSpace(src), strings.TrimSpace(dst))
	if roleClean != "" {
		cmd += " " + roleClean
	}
	return a.triggerSchemeEval(ctx, cmd)
}

// AddTaintSanitizer registers a verified human/sanitizer node cutting CaMeL taint paths in ASCENT.
func (a *RustGovernanceAuthority) AddTaintSanitizer(node string) (mygoshell.BackendGovernanceSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)
	defer cancel()
	return a.triggerSchemeEval(ctx, fmt.Sprintf("scheme:governance-sanitize %s", strings.TrimSpace(node)))
}

// CheckCedar executes a live compiled-Cedar authorization probe against the Rust kernel's
// synchronized Cedar policy set (`forbid`-overrides-`permit`) and awaits the authoritative decision.
func (a *RustGovernanceAuthority) CheckCedar(
	actor, action, resource string,
	stepUpFresh bool,
) (mygoshell.BackendGovernanceSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)
	defer cancel()
	target, _ := a.activeTarget()
	if target == nil {
		return a.Snapshot(), errors.New("grpcwire: no active Rust backend target")
	}
	before := a.Snapshot()
	beforeSeq := uint64(0)
	if before.LastCedarProbe != nil {
		beforeSeq = before.LastCedarProbe.Seq
	}
	cmd := fmt.Sprintf(
		"scheme:governance-check-cedar %s %s %s %t",
		strings.TrimSpace(actor),
		strings.TrimSpace(action),
		strings.TrimSpace(resource),
		stepUpFresh,
	)
	if err := a.dispatchUiCommand(ctx, target, cmd); err != nil {
		return before, err
	}
	return a.awaitSnapshotCondition(ctx, func(s mygoshell.BackendGovernanceSnapshot) bool {
		return s.LastCedarProbe != nil && s.LastCedarProbe.Seq > beforeSeq
	})
}

func (a *RustGovernanceAuthority) triggerSchemeEval(
	ctx context.Context,
	cmd string,
) (mygoshell.BackendGovernanceSnapshot, error) {
	target, _ := a.activeTarget()
	if target == nil {
		return a.Snapshot(), errors.New("grpcwire: no active Rust backend target")
	}
	before := a.Snapshot()
	if err := a.dispatchUiCommand(ctx, target, cmd); err != nil {
		return before, err
	}
	return a.awaitSnapshotCondition(ctx, func(s mygoshell.BackendGovernanceSnapshot) bool {
		return s.PolicyRevision > before.PolicyRevision || s.RevocationEpoch > before.RevocationEpoch
	})
}

func (a *RustGovernanceAuthority) dispatchUiCommand(ctx context.Context, target *Target, cmd string) error {
	wb, err := target.UI().OpenWorkbench(ctx, &uiv1.OpenWorkbenchRequest{
		SessionId: a.cfg.SessionID,
	})
	if err != nil {
		return fmt.Errorf("grpcwire: OpenWorkbench before %q: %w", cmd, err)
	}
	gen := uint64(0)
	if wb.GetWorkbench() != nil {
		gen = wb.GetWorkbench().GetGeneration()
	}
	resp, err := target.UI().DispatchUiEvent(ctx, &uiv1.DispatchUiEventRequest{
		SessionId:          a.cfg.SessionID,
		ExpectedGeneration: gen,
		Event:              &uiv1.DispatchUiEventRequest_RecordCommand{RecordCommand: cmd},
	})
	if err != nil {
		return fmt.Errorf("grpcwire: DispatchUiEvent(%q): %w", cmd, err)
	}
	if !resp.GetAdmitted() {
		msg := "UI command rejected"
		if resp.GetDenial() != nil {
			msg = resp.GetDenial().GetMessage()
		}
		return fmt.Errorf("grpcwire: %s", msg)
	}
	return nil
}

func (a *RustGovernanceAuthority) discoverPendingRearmGate(ctx context.Context, target *Target) (string, error) {
	list, err := target.Gate().ListPendingGates(ctx, &gatev1.ListPendingGatesRequest{})
	if err != nil {
		return "", fmt.Errorf("grpcwire: ListPendingGates for re-arm: %w", err)
	}
	for _, g := range list.GetGates() {
		if strings.HasPrefix(g.GetGateId(), GovernanceRearmGatePrefix) {
			return g.GetGateId(), nil
		}
	}
	return "", errors.New("backend governance: no pending step-up re-arm gate found")
}

func (a *RustGovernanceAuthority) awaitSnapshotCondition(
	ctx context.Context,
	pred func(mygoshell.BackendGovernanceSnapshot) bool,
) (mygoshell.BackendGovernanceSnapshot, error) {
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	for {
		if snap, err := a.Hydrate(ctx); err == nil && pred(snap) {
			return snap, nil
		}
		select {
		case <-ctx.Done():
			return a.Snapshot(), fmt.Errorf("grpcwire: timed out waiting for backend governance state: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (a *RustGovernanceAuthority) pumpGovernanceTrajectory(ctx context.Context) {
	defer a.wg.Done()
	backoff := a.cfg.ReconnectInitialBackoff
	firstAttempt := true
	for {
		if ctx.Err() != nil {
			return
		}
		target, seenGen := a.activeTarget()
		if target == nil {
			if !waitReconnectOrSwap(ctx, a.cfg.Provider, seenGen, backoff) {
				return
			}
			backoff = min(backoff*2, a.cfg.ReconnectMaxBackoff)
			continue
		}
		if !firstAttempt {
			_, _ = a.Hydrate(ctx)
		}
		firstAttempt = false

		stream, err := target.Projection().WatchGraphTrajectory(ctx, &projectionv1.WatchGraphTrajectoryRequest{
			SessionId: a.cfg.SessionID,
		})
		if err != nil {
			a.notifyStream(false, err)
			if !isRetryableStreamError(ctx, err) {
				return
			}
			if !waitReconnectOrSwap(ctx, a.cfg.Provider, seenGen, backoff) {
				return
			}
			backoff = min(backoff*2, a.cfg.ReconnectMaxBackoff)
			continue
		}
		a.notifyStream(true, nil)
		backoff = a.cfg.ReconnectInitialBackoff
		for {
			resp, recvErr := stream.Recv()
			if recvErr != nil {
				a.notifyStream(false, recvErr)
				if !isRetryableStreamError(ctx, recvErr) {
					return
				}
				break
			}
			ev := resp.GetEvent()
			if ev == nil || ev.GetNodeKind() != GovernanceAuthorityNodeKind {
				continue
			}
			snap := DecodeGovernanceSnapshotFromFacets(ev.GetMetadata())
			a.storeSnapshot(snap)
		}
		if !waitReconnectOrSwap(ctx, a.cfg.Provider, seenGen, backoff) {
			return
		}
		backoff = min(backoff*2, a.cfg.ReconnectMaxBackoff)
	}
}

func (a *RustGovernanceAuthority) notifyStream(connected bool, err error) {
	if a.cfg.OnStreamState != nil {
		a.cfg.OnStreamState(connected, err)
	}
}

func (a *RustGovernanceAuthority) storeSnapshot(next mygoshell.BackendGovernanceSnapshot) {
	a.mu.Lock()
	a.snap = cloneGovernanceSnapshot(next)
	a.hasSnapshot = true
	cb := a.cfg.OnSnapshot
	a.mu.Unlock()
	if cb != nil {
		cb(cloneGovernanceSnapshot(next))
	}
}

// DecodeGovernanceSnapshotFromFacets translates the authoritative Rust+Scheme
// `GraphRecordView.facets` / `GraphTrajectoryEvent.metadata` map into a typed
// `mygoshell.BackendGovernanceSnapshot`.
func DecodeGovernanceSnapshotFromFacets(facets map[string]string) mygoshell.BackendGovernanceSnapshot {
	type wireCedarRule struct {
		PolicyID string `json:"policy_id"`
		Source   string `json:"source"`
	}
	parseBool := func(k string) bool {
		return strings.EqualFold(strings.TrimSpace(facets[k]), "true")
	}
	parseU64 := func(k string) uint64 {
		v, _ := strconv.ParseUint(strings.TrimSpace(facets[k]), 10, 64)
		return v
	}
	parseStringSlice := func(k string) []string {
		raw := strings.TrimSpace(facets[k])
		if raw == "" {
			return nil
		}
		var out []string
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			return nil
		}
		return out
	}

	var cedarRules []mygoshell.SynthesizedCedarRule
	if raw := strings.TrimSpace(facets["cedar_policies_json"]); raw != "" {
		var decoded []wireCedarRule
		if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
			cedarRules = make([]mygoshell.SynthesizedCedarRule, 0, len(decoded))
			for _, c := range decoded {
				cedarRules = append(cedarRules, mygoshell.SynthesizedCedarRule{
					PolicyID: c.PolicyID,
					Source:   c.Source,
				})
			}
		}
	}

	var factDelegations []mygoshell.GovernanceDelegationEdge
	if raw := strings.TrimSpace(facets["fact_delegations_json"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &factDelegations)
	}
	var factFlows []mygoshell.GovernanceContextFlow
	if raw := strings.TrimSpace(facets["fact_context_flows_json"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &factFlows)
	}
	var factDerivations []mygoshell.GovernanceDerivationSpec
	if raw := strings.TrimSpace(facets["fact_derivations_json"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &factDerivations)
	}
	var factTaintEdges []mygoshell.GovernanceTaintEdge
	if raw := strings.TrimSpace(facets["fact_taint_edges_json"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &factTaintEdges)
	}
	var lastCedarProbe *mygoshell.GovernanceCedarProbeResult
	if raw := strings.TrimSpace(facets["last_cedar_probe_json"]); raw != "" {
		var decoded mygoshell.GovernanceCedarProbeResult
		if err := json.Unmarshal([]byte(raw), &decoded); err == nil && decoded.Seq > 0 {
			lastCedarProbe = &decoded
		}
	}

	return mygoshell.BackendGovernanceSnapshot{
		Admitted:                parseBool("admitted"),
		RevocationEpoch:         parseU64("revocation_epoch"),
		PolicyRevision:          parseU64("policy_revision"),
		BreakerTripped:          parseBool("breaker_tripped"),
		BreakerTripEpoch:        parseU64("breaker_trip_epoch"),
		BreakerBudgetUsed:       uint32(parseU64("breaker_budget_used")),
		BreakerActiveLeases:     int(parseU64("breaker_active_leases")),
		RearmGateID:             strings.TrimSpace(facets["rearm_gate_id"]),
		LastEventNote:           strings.TrimSpace(facets["last_event_note"]),
		AssessmentDigest:        strings.TrimSpace(facets["assessment_digest"]),
		TaintProofDigest:        strings.TrimSpace(facets["taint_proof_digest"]),
		FixpointProofDigest:     strings.TrimSpace(facets["fixpoint_proof_digest"]),
		TemporalProofDigest:     strings.TrimSpace(facets["temporal_proof_digest"]),
		InvalidationDigest:      strings.TrimSpace(facets["invalidation_digest"]),
		RejectionReasons:        parseStringSlice("rejection_reasons_json"),
		FencedActors:            parseStringSlice("fenced_actors_json"),
		BlockedTools:            parseStringSlice("blocked_tools_json"),
		RetractedConclusions:    parseStringSlice("retracted_conclusions_json"),
		SynthesizedCedarRules:   cedarRules,
		FactDelegations:         factDelegations,
		FactRevokedActors:       parseStringSlice("fact_revoked_actors_json"),
		FactContextFlows:        factFlows,
		FactDeclassifiedEntries: parseStringSlice("fact_declassified_entries_json"),
		FactRetractedSubjects:   parseStringSlice("fact_retracted_subjects_json"),
		FactDerivations:         factDerivations,
		FactTaintEdges:          factTaintEdges,
		FactTaintSources:        parseStringSlice("fact_taint_sources_json"),
		FactTaintSinks:          parseStringSlice("fact_taint_sinks_json"),
		FactTaintSanitizers:     parseStringSlice("fact_taint_sanitizers_json"),
		LastCedarProbe:          lastCedarProbe,
	}
}

func cloneGovernanceSnapshot(in mygoshell.BackendGovernanceSnapshot) mygoshell.BackendGovernanceSnapshot {
	out := in
	out.RejectionReasons = append([]string(nil), in.RejectionReasons...)
	out.FencedActors = append([]string(nil), in.FencedActors...)
	out.BlockedTools = append([]string(nil), in.BlockedTools...)
	out.RetractedConclusions = append([]string(nil), in.RetractedConclusions...)
	out.SynthesizedCedarRules = append([]mygoshell.SynthesizedCedarRule(nil), in.SynthesizedCedarRules...)
	out.FactDelegations = append([]mygoshell.GovernanceDelegationEdge(nil), in.FactDelegations...)
	out.FactRevokedActors = append([]string(nil), in.FactRevokedActors...)
	out.FactContextFlows = append([]mygoshell.GovernanceContextFlow(nil), in.FactContextFlows...)
	out.FactDeclassifiedEntries = append([]string(nil), in.FactDeclassifiedEntries...)
	out.FactRetractedSubjects = append([]string(nil), in.FactRetractedSubjects...)
	if len(in.FactDerivations) > 0 {
		out.FactDerivations = make([]mygoshell.GovernanceDerivationSpec, len(in.FactDerivations))
		for i, d := range in.FactDerivations {
			out.FactDerivations[i] = mygoshell.GovernanceDerivationSpec{
				DerivationID:      d.DerivationID,
				PolicyID:          d.PolicyID,
				SubjectPremises:   append([]string(nil), d.SubjectPremises...),
				ParentDerivations: append([]string(nil), d.ParentDerivations...),
			}
		}
	}
	out.FactTaintEdges = append([]mygoshell.GovernanceTaintEdge(nil), in.FactTaintEdges...)
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
