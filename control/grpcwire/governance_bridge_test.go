// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/features"
	commonv1 "github.com/tao3k/marlin-mygo/gen/marlin/common/v1"
	gatev1 "github.com/tao3k/marlin-mygo/gen/marlin/gate/v1"
	projectionv1 "github.com/tao3k/marlin-mygo/gen/marlin/projection/v1"
	uiv1 "github.com/tao3k/marlin-mygo/gen/marlin/ui/v1"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

type mockGovernanceBackend struct {
	projectionv1.UnimplementedProjectionQueryServiceServer
	uiv1.UnimplementedUiWorkbenchServiceServer
	gatev1.UnimplementedApprovalGateServiceServer

	mu         sync.Mutex
	gen        uint64
	tripped    bool
	tripEpoch  uint64
	revEpoch   uint64
	rearmGate  string
	lastNote   string
	trajNotify chan *projectionv1.GraphTrajectoryEvent
}

func newMockGovernanceBackend() *mockGovernanceBackend {
	return &mockGovernanceBackend{
		tripped:    true,
		tripEpoch:  1,
		revEpoch:   2,
		rearmGate:  "gov-rearm-epoch-1",
		lastNote:   "incident tripped",
		trajNotify: make(chan *projectionv1.GraphTrajectoryEvent, 8),
	}
}

func (m *mockGovernanceBackend) facetsLocked() map[string]string {
	admitted := "true"
	if m.tripped {
		admitted = "false"
	}
	return map[string]string{
		"admitted":                   admitted,
		"revocation_epoch":           fmt.Sprintf("%d", m.revEpoch),
		"policy_revision":            "1",
		"breaker_tripped":            fmt.Sprintf("%t", m.tripped),
		"breaker_trip_epoch":         fmt.Sprintf("%d", m.tripEpoch),
		"breaker_budget_used":        "0",
		"breaker_active_leases":      "0",
		"rearm_gate_id":              m.rearmGate,
		"last_event_note":            m.lastNote,
		"assessment_digest":          "sha256:" + strings.Repeat("a", 64),
		"taint_proof_digest":         "sha256:" + strings.Repeat("b", 64),
		"fixpoint_proof_digest":      "sha256:" + strings.Repeat("c", 64),
		"temporal_proof_digest":      "sha256:" + strings.Repeat("d", 64),
		"invalidation_digest":        "sha256:" + strings.Repeat("e", 64),
		"rejection_reasons_json":     `["ascent-camel-taint-alert","bitemporal-premise-retracted"]`,
		"fenced_actors_json":         `["agent-lead","agent-sub"]`,
		"blocked_tools_json":         `["os-shell"]`,
		"retracted_conclusions_json": `["D-OBO-TICKET","D-TOOL-EXEC"]`,
		"cedar_policies_json":        `[{"policy_id":"marlin.ascent.forbid.fenced.agent-sub","source":"forbid(principal == Marlin::Actor::\"agent-sub\", action, resource);"}]`,
	}
}

func (m *mockGovernanceBackend) emitTrajectoryLocked(nodeID string) {
	status := "armed"
	if m.tripped {
		status = "tripped"
	}
	select {
	case m.trajNotify <- &projectionv1.GraphTrajectoryEvent{
		EventId:  fmt.Sprintf("ev-%d", time.Now().UnixNano()),
		RunId:    "scheme-higher-order-governance",
		NodeId:   nodeID,
		NodeKind: grpcwire.HigherOrderGovernanceNodeKind,
		Status:   status,
		Metadata: m.facetsLocked(),
	}:
	default:
	}
}

func (m *mockGovernanceBackend) QueryProjectGraph(
	_ context.Context,
	req *projectionv1.QueryProjectGraphRequest,
) (*projectionv1.QueryProjectGraphResponse, error) {
	if req.GetSecretVisibility() == projectionv1.SecretVisibility_SECRET_VISIBILITY_ALLOWED {
		return nil, fmt.Errorf("secret visibility denied")
	}
	m.mu.Lock()
	facets := m.facetsLocked()
	m.mu.Unlock()
	return &projectionv1.QueryProjectGraphResponse{
		Records: []*projectionv1.GraphRecordView{{
			RecordId: grpcwire.HigherOrderGovernanceRecordID,
			Surface:  projectionv1.GraphVisibleSurface_GRAPH_VISIBLE_SURFACE_EVIDENCE,
			Kind:     "scheme.higher_order.governance_snapshot",
			Title:    "Mock Scheme Higher-Order Governance",
			Facets:   facets,
		}},
	}, nil
}

func (m *mockGovernanceBackend) WatchGraphTrajectory(
	_ *projectionv1.WatchGraphTrajectoryRequest,
	stream grpc.ServerStreamingServer[projectionv1.WatchGraphTrajectoryResponse],
) error {
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case ev := <-m.trajNotify:
			if err := stream.Send(&projectionv1.WatchGraphTrajectoryResponse{Event: ev}); err != nil {
				return err
			}
		}
	}
}

func (m *mockGovernanceBackend) OpenWorkbench(
	_ context.Context,
	_ *uiv1.OpenWorkbenchRequest,
) (*uiv1.OpenWorkbenchResponse, error) {
	m.mu.Lock()
	g := m.gen
	m.mu.Unlock()
	return &uiv1.OpenWorkbenchResponse{
		Workbench: &uiv1.UiWorkbenchSnapshot{Generation: g},
	}, nil
}

func (m *mockGovernanceBackend) DispatchUiEvent(
	_ context.Context,
	req *uiv1.DispatchUiEventRequest,
) (*uiv1.DispatchUiEventResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gen++
	cmd := req.GetRecordCommand()
	if strings.HasPrefix(cmd, "scheme:governance-estop") {
		m.tripped = true
		m.rearmGate = fmt.Sprintf("gov-rearm-epoch-%d", m.tripEpoch)
		m.lastNote = "operator estop: " + cmd
		m.emitTrajectoryLocked("governance_operator_estop")
	}
	return &uiv1.DispatchUiEventResponse{Admitted: true}, nil
}

func (m *mockGovernanceBackend) SubmitGraphHumanDecision(
	_ context.Context,
	req *gatev1.SubmitGraphHumanDecisionRequest,
) (*gatev1.SubmitGraphHumanDecisionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if req.GetGateId() != m.rearmGate {
		return &gatev1.SubmitGraphHumanDecisionResponse{
			Receipt: &gatev1.GateDecisionReceipt{
				GateId:  req.GetGateId(),
				Claimed: false,
				Denial:  &commonv1.TypedDenial{Message: "unknown re-arm gate"},
			},
		}, nil
	}
	note := req.GetNote()
	if req.GetDecision() == gatev1.GraphHumanDecision_GRAPH_HUMAN_DECISION_APPROVE &&
		strings.HasPrefix(note, "stepup-verified:actor=") &&
		!strings.Contains(note, "actor=agent") {
		m.tripped = false
		m.tripEpoch++
		m.revEpoch++
		m.rearmGate = ""
		m.lastNote = "rearmed via verified step-up"
		m.emitTrajectoryLocked("governance_rearm_approved")
	} else {
		m.rearmGate = fmt.Sprintf("gov-rearm-epoch-%d-r2", m.tripEpoch)
		m.lastNote = "step-up re-arm denied"
		m.emitTrajectoryLocked("governance_rearm_denied")
	}
	return &gatev1.SubmitGraphHumanDecisionResponse{
		Receipt: &gatev1.GateDecisionReceipt{
			GateId:  req.GetGateId(),
			Claimed: true,
		},
	}, nil
}

func TestRustGovernanceAuthorityMockGRPCFlow(t *testing.T) {
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	backend := newMockGovernanceBackend()
	projectionv1.RegisterProjectionQueryServiceServer(srv, backend)
	uiv1.RegisterUiWorkbenchServiceServer(srv, backend)
	gatev1.RegisterApprovalGateServiceServer(srv, backend)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	target, err := grpcwire.Dial(grpcwire.DialConfig{
		ID:          "mock-scheme-host",
		DisplayName: "Mock Scheme Host",
		Mode:        transport.ModeLocalSidecar,
		Address:     "passthrough:///bufnet",
		DialOptions: []grpc.DialOption{
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	if err != nil {
		t.Fatalf("Dial mock server: %v", err)
	}
	defer target.Close()

	streamUpdates := make(chan mygoshell.BackendHigherOrderSnapshot, 8)
	auth := grpcwire.NewRustGovernanceAuthority(grpcwire.RustGovernanceAuthorityConfig{
		Target:    target,
		ProjectID: "default",
		SessionID: "test-gov",
		OnSnapshot: func(s mygoshell.BackendHigherOrderSnapshot) {
			select {
			case streamUpdates <- s:
			default:
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := auth.Start(ctx); err != nil {
		t.Fatalf("Start RustGovernanceAuthority: %v", err)
	}
	defer auth.Stop()

	snap := auth.Snapshot()
	if !snap.BreakerTripped || snap.RearmGateID != "gov-rearm-epoch-1" {
		t.Fatalf("expected hydrated tripped snapshot with gov-rearm-epoch-1, got %+v", snap)
	}
	if len(snap.FencedActors) != 2 || len(snap.SynthesizedCedarRules) != 1 {
		t.Fatalf("expected ASCENT fenced actors & Cedar rules, got %+v", snap)
	}

	// Step-up false MUST fail closed without touching backend.
	if _, err := auth.RearmBackendBreakerWithStepUp("ops-alice", false); err == nil {
		t.Fatal("expected fail-closed denial when stepUpFresh=false")
	}

	// Verified human RFC 9470 step-up claims one-shot gate and advances epoch.
	rearmed, err := auth.RearmBackendBreakerWithStepUp("ops-alice", true)
	if err != nil {
		t.Fatalf("RearmBackendBreakerWithStepUp failed: %v", err)
	}
	if rearmed.BreakerTripped || rearmed.BreakerTripEpoch != 2 {
		t.Fatalf("expected armed breaker at epoch 2, got %+v", rearmed)
	}

	// Operator E-Stop trips backend fail-closed and issues epoch-2 re-arm gate.
	tripped, err := auth.TripBackendBreaker("ops-alice", "manual-red-button")
	if err != nil {
		t.Fatalf("TripBackendBreaker failed: %v", err)
	}
	if !tripped.BreakerTripped || tripped.RearmGateID != "gov-rearm-epoch-2" {
		t.Fatalf("expected re-tripped breaker with gov-rearm-epoch-2, got %+v", tripped)
	}
}

// TestLiveCrossProcessSchemeHigherOrderGovernanceE2E connects Go `marlin-mygo`
// directly over real HTTP/2 gRPC to a live running Rust `marlin-scheme-host`
// embedding the live Gambit/Gerbil Scheme VM (`MARLIN_SCHEME_GOVERNANCE_MODE=incident`),
// proving end-to-end:
//  1. Live Gerbil Scheme VM (Atomic POO × ASCENT × Bi-Temporal TM × Cedar) -> zero-JSON
//     14-slot rooted vector -> Rust HigherOrderGovernanceAdmissionGate -> HTTP/2 gRPC -> Go UI;
//  2. Anti-forgery: `DispatchUiEvent(RecordCommand("scheme:governance-rearm ..."))` CANNOT re-arm;
//  3. Human-only RFC 9470 Step-Up: agent impersonation on `ApprovalGateService` is REJECTED fail-closed;
//  4. Verified human RFC 9470 Step-Up re-arms the Rust breaker & advances `BreakerTripEpoch`;
//  5. Live operator E-Stop trips Rust breaker fail-closed and rotates one-shot re-arm gate;
//  6. Live on-demand Gambit VM clean evaluation clears ASCENT fences at advancing Proof Epoch.
//
// Skipped unless MARLIN_SCHEME_HOST_ADDR is set.
func TestLiveCrossProcessSchemeHigherOrderGovernanceE2E(t *testing.T) {
	addr := strings.TrimSpace(os.Getenv("MARLIN_SCHEME_HOST_ADDR"))
	if addr == "" {
		t.Skip("set MARLIN_SCHEME_HOST_ADDR to run live Go <-> Rust+Scheme Higher-Order Governance E2E")
	}
	token := strings.TrimSpace(os.Getenv("MARLIN_SCHEME_HOST_TOKEN"))
	authHeader := ""
	if token != "" {
		authHeader = "Bearer " + token
	}

	target, err := grpcwire.Dial(grpcwire.DialConfig{
		ID:          "live-scheme-host",
		DisplayName: "Live Rust+Scheme Authority Host",
		Mode:        transport.ModeLocalSidecar,
		Address:     addr,
		AuthToken:   authHeader,
	})
	if err != nil {
		t.Fatalf("Dial live marlin-scheme-host %s: %v", addr, err)
	}
	defer target.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		mu       sync.Mutex
		streamed []mygoshell.BackendHigherOrderSnapshot
	)
	govAuth := grpcwire.NewRustGovernanceAuthority(grpcwire.RustGovernanceAuthorityConfig{
		Target:    target,
		ProjectID: "default",
		SessionID: "e2e-higher-order-gov",
		Timeout:   10 * time.Second,
		OnSnapshot: func(s mygoshell.BackendHigherOrderSnapshot) {
			mu.Lock()
			streamed = append(streamed, s)
			mu.Unlock()
		},
	})
	if err := govAuth.Start(ctx); err != nil {
		t.Fatalf("Start RustGovernanceAuthority: %v", err)
	}
	defer govAuth.Stop()

	// Bind directly onto the real Tier-0 Governance Console controller.
	govUI := mygoshell.NewGovernanceController(
		features.NewRegistry(),
		features.NewGovernanceLedger(features.DefaultGovernancePolicy()),
		features.NewHost(),
	)
	govUI.AttachBackendAuthority(govAuth)

	// 1. Verify hydrated live Scheme VM incident proof envelope.
	initial, err := govAuth.Hydrate(ctx)
	if err != nil {
		t.Fatalf("Hydrate live Scheme governance snapshot failed: %v", err)
	}
	if initial.Admitted || !initial.BreakerTripped {
		t.Fatalf("expected live Scheme incident to auto-trip Rust breaker, got %+v", initial)
	}
	for label, digest := range map[string]string{
		"assessment":   initial.AssessmentDigest,
		"taint":        initial.TaintProofDigest,
		"fixpoint":     initial.FixpointProofDigest,
		"temporal":     initial.TemporalProofDigest,
		"invalidation": initial.InvalidationDigest,
	} {
		if !strings.HasPrefix(digest, "sha256:") || len(digest) != 71 {
			t.Fatalf("expected real sha256 %s proof digest from live Gambit VM, got %q", label, digest)
		}
	}
	if !containsString(initial.FencedActors, "agent-sub") || !containsString(initial.FencedActors, "agent-lead") {
		t.Fatalf("expected ASCENT transitive fencing of agent-lead & agent-sub, got %v", initial.FencedActors)
	}
	if !containsString(initial.BlockedTools, "os-shell") {
		t.Fatalf("expected ASCENT blocked tool os-shell, got %v", initial.BlockedTools)
	}
	if !containsString(initial.RetractedConclusions, "D-TOOL-EXEC") || !containsString(initial.RetractedConclusions, "D-OBO-TICKET") {
		t.Fatalf("expected Bi-Temporal TM retracted conclusions, got %v", initial.RetractedConclusions)
	}
	if len(initial.SynthesizedCedarRules) == 0 {
		t.Fatalf("expected dynamically synthesized Cedar forbid policies from live Scheme VM")
	}
	if !strings.HasPrefix(initial.RearmGateID, grpcwire.HigherOrderRearmGatePrefix) {
		t.Fatalf("expected one-shot ApprovalGateService re-arm gate ID, got %q", initial.RearmGateID)
	}

	// 2. Anti-forgery check: DispatchUiEvent("scheme:governance-rearm ...") MUST NOT re-arm!
	wb, err := target.UI().OpenWorkbench(ctx, &uiv1.OpenWorkbenchRequest{SessionId: "e2e-higher-order-gov"})
	if err != nil {
		t.Fatalf("OpenWorkbench: %v", err)
	}
	_, err = target.UI().DispatchUiEvent(ctx, &uiv1.DispatchUiEventRequest{
		SessionId:          "e2e-higher-order-gov",
		ExpectedGeneration: wb.GetWorkbench().GetGeneration(),
		Event:              &uiv1.DispatchUiEventRequest_RecordCommand{RecordCommand: "scheme:governance-rearm ops-alice"},
	})
	if err != nil {
		t.Fatalf("DispatchUiEvent forgery probe: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	afterForgery, err := govAuth.Hydrate(ctx)
	if err != nil {
		t.Fatalf("Hydrate after forgery probe: %v", err)
	}
	if !afterForgery.BreakerTripped || afterForgery.BreakerTripEpoch != 1 {
		t.Fatalf("SECURITY VIOLATION: UI command forged breaker re-arm: %+v", afterForgery)
	}

	// 3. Human-only Step-Up check: agent impersonation on ApprovalGateService MUST fail closed!
	agentNote := "stepup-verified:actor=agent-rogue:acr=urn:ietf:params:oauth:acr:mfa"
	_, err = target.Gate().SubmitGraphHumanDecision(ctx, &gatev1.SubmitGraphHumanDecisionRequest{
		GateId:         afterForgery.RearmGateID,
		Decision:       gatev1.GraphHumanDecision_GRAPH_HUMAN_DECISION_APPROVE,
		IdempotencyKey: "e2e-agent-impersonation-1",
		Note:           &agentNote,
	})
	if err != nil {
		t.Fatalf("SubmitGraphHumanDecision agent impersonation probe: %v", err)
	}
	// Wait for Rust host to reject agent impersonation and rotate the one-shot gate ID.
	deadline := time.Now().Add(3 * time.Second)
	var afterAgent mygoshell.BackendHigherOrderSnapshot
	for time.Now().Before(deadline) {
		afterAgent, err = govAuth.Hydrate(ctx)
		if err == nil && afterAgent.RearmGateID != "" && afterAgent.RearmGateID != afterForgery.RearmGateID {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !afterAgent.BreakerTripped || afterAgent.BreakerTripEpoch != 1 || afterAgent.RearmGateID == afterForgery.RearmGateID {
		t.Fatalf("expected Rust breaker to stay tripped & rotate re-arm gate after agent impersonation, got %+v", afterAgent)
	}

	// 4. Verified human RFC 9470 Step-Up Re-Arm succeeds & advances trip epoch 1 -> 2!
	rearmed, err := govAuth.RearmBackendBreakerWithStepUp("ops-alice", true)
	if err != nil {
		t.Fatalf("verified human step-up re-arm failed: %v", err)
	}
	if rearmed.BreakerTripped || rearmed.BreakerTripEpoch != 2 {
		t.Fatalf("expected armed breaker at epoch 2 after verified step-up, got %+v", rearmed)
	}

	// 5. Live operator E-Stop from Go UI trips Rust breaker fail-closed and issues epoch-2 gate!
	tripped, err := govAuth.TripBackendBreaker("ops-alice", "live-red-button")
	if err != nil {
		t.Fatalf("TripBackendBreaker failed: %v", err)
	}
	if !tripped.BreakerTripped || !strings.HasPrefix(tripped.RearmGateID, "gov-rearm-epoch-2") {
		t.Fatalf("expected tripped breaker with epoch-2 re-arm gate, got %+v", tripped)
	}

	// 6. Trigger live on-demand Gambit Scheme VM clean evaluation over gRPC!
	cleanSnap, err := govAuth.TriggerSchemeCleanEval(ctx)
	if err != nil {
		t.Fatalf("TriggerSchemeCleanEval failed: %v", err)
	}
	if len(cleanSnap.FencedActors) != 0 || len(cleanSnap.BlockedTools) != 0 {
		t.Fatalf("expected live Gambit VM clean evaluation to clear ASCENT fences/blocked tools, got %+v", cleanSnap)
	}
	if !cleanSnap.BreakerTripped {
		t.Fatalf("expected manual E-Stop latch to remain tripped until human step-up re-arm, got %+v", cleanSnap)
	}

	// 7. Verify GovernanceController UI sees the live authoritative backend state.
	uiSnap := govUI.BackendAuthority.Snapshot()
	if uiSnap.PolicyRevision != cleanSnap.PolicyRevision || uiSnap.RevocationEpoch != cleanSnap.RevocationEpoch {
		t.Fatalf("GovernanceController snapshot mismatch: ui=%+v backend=%+v", uiSnap, cleanSnap)
	}
	mu.Lock()
	streamCount := len(streamed)
	mu.Unlock()
	if streamCount == 0 {
		t.Fatalf("expected live WatchGraphTrajectory push notifications, got 0")
	}
}

// TestLiveCrossProcessGovernanceSurvivesOSRestartE2E verifies the Anti-Restart-Reset
// invariant across two sequential OS processes of `marlin-scheme-host` sharing one
// Turso SQLite WAL database:
//   - Phase 1 (MARLIN_SCHEME_HOST_RESTART_PHASE=1): live Gambit VM evaluates incident
//     -> human RFC 9470 step-up re-arms (trip_epoch 1->2, rev_epoch 2->3) -> operator
//     trips E-Stop ("pre-restart-halt") -> durably persisted to Turso CAS/WAL.
//   - Phase 2 (MARLIN_SCHEME_HOST_RESTART_PHASE=2, after SIGTERM + fresh Process #2
//     launched with MARLIN_SCHEME_GOVERNANCE_MODE=clean on the SAME Turso WAL):
//     verifies Process #2 refuses to wipe E-Stop/epochs/ASCENT fences/Cedar rules,
//     restores tripped=true at trip_epoch=2 & rev_epoch=3 with gov-rearm-epoch-2,
//     and advances to trip_epoch=3 after verified human RFC 9470 step-up re-arm.
func TestLiveCrossProcessGovernanceSurvivesOSRestartE2E(t *testing.T) {
	phase := strings.TrimSpace(os.Getenv("MARLIN_SCHEME_HOST_RESTART_PHASE"))
	addr := strings.TrimSpace(os.Getenv("MARLIN_SCHEME_HOST_ADDR"))
	if phase == "" || addr == "" {
		t.Skip("set MARLIN_SCHEME_HOST_ADDR and MARLIN_SCHEME_HOST_RESTART_PHASE=1|2 to run cross-OS-restart E2E")
	}
	token := strings.TrimSpace(os.Getenv("MARLIN_SCHEME_HOST_TOKEN"))
	authHeader := ""
	if token != "" {
		authHeader = "Bearer " + token
	}
	target, err := grpcwire.Dial(grpcwire.DialConfig{
		ID:          "live-scheme-host-restart-" + phase,
		DisplayName: "Live Rust+Scheme Restart Host Phase " + phase,
		Mode:        transport.ModeLocalSidecar,
		Address:     addr,
		AuthToken:   authHeader,
	})
	if err != nil {
		t.Fatalf("Dial phase %s host %s: %v", phase, addr, err)
	}
	defer target.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	govAuth := grpcwire.NewRustGovernanceAuthority(grpcwire.RustGovernanceAuthorityConfig{
		Target:    target,
		ProjectID: "default",
		SessionID: "e2e-restart-" + phase,
		Timeout:   10 * time.Second,
	})

	switch phase {
	case "1":
		s0, err := govAuth.Hydrate(ctx)
		if err != nil {
			t.Fatalf("Phase 1 Hydrate: %v", err)
		}
		if !s0.BreakerTripped || s0.BreakerTripEpoch != 1 || s0.RevocationEpoch != 2 {
			t.Fatalf("Phase 1 expected incident boot tripped=true trip_epoch=1 rev_epoch=2, got %+v", s0)
		}
		rearmed, err := govAuth.RearmBackendBreakerWithStepUp("ops-alice", true)
		if err != nil {
			t.Fatalf("Phase 1 Step-Up Re-Arm: %v", err)
		}
		if rearmed.BreakerTripped || rearmed.BreakerTripEpoch != 2 || rearmed.RevocationEpoch != 3 {
			t.Fatalf("Phase 1 expected armed trip_epoch=2 rev_epoch=3, got %+v", rearmed)
		}
		tripped, err := govAuth.TripBackendBreaker("ops-alice", "pre-restart-halt")
		if err != nil {
			t.Fatalf("Phase 1 TripBackendBreaker: %v", err)
		}
		if !tripped.BreakerTripped || tripped.BreakerTripEpoch != 2 {
			t.Fatalf("Phase 1 expected tripped at epoch 2 before crash/restart, got %+v", tripped)
		}
	case "2":
		restored, err := govAuth.Hydrate(ctx)
		if err != nil {
			t.Fatalf("Phase 2 Hydrate after OS restart: %v", err)
		}
		if !restored.BreakerTripped {
			t.Fatalf("RESTART-RESET VIOLATION: breaker failed open after OS restart: %+v", restored)
		}
		if restored.BreakerTripEpoch != 2 || restored.RevocationEpoch != 3 {
			t.Fatalf("EPOCH AMNESIA VIOLATION: expected trip_epoch=2 rev_epoch=3 after OS restart, got %+v", restored)
		}
		if !containsString(restored.FencedActors, "agent-sub") || !containsString(restored.FencedActors, "agent-lead") {
			t.Fatalf("ASCENT FENCE LOST ON RESTART: got %v", restored.FencedActors)
		}
		if len(restored.SynthesizedCedarRules) == 0 {
			t.Fatalf("CEDAR POLICIES LOST ON RESTART")
		}
		if !strings.HasPrefix(restored.RearmGateID, "gov-rearm-epoch-2") {
			t.Fatalf("expected restored one-shot gate gov-rearm-epoch-2*, got %q", restored.RearmGateID)
		}
		rearmed2, err := govAuth.RearmBackendBreakerWithStepUp("ops-alice", true)
		if err != nil {
			t.Fatalf("Phase 2 post-restart Step-Up Re-Arm failed: %v", err)
		}
		if rearmed2.BreakerTripped || rearmed2.BreakerTripEpoch != 3 || rearmed2.RevocationEpoch != 4 {
			t.Fatalf("Phase 2 expected armed trip_epoch=3 rev_epoch=4, got %+v", rearmed2)
		}
	default:
		t.Fatalf("unknown MARLIN_SCHEME_HOST_RESTART_PHASE=%q", phase)
	}
}

func containsString(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}
