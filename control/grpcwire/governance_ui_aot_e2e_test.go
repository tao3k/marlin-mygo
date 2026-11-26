// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

// TestLiveGPUConsoleDrivesRealSchemeHostAOTProcessE2E proves the complete visual
// GPU-to-Kernel control loop against the REAL zero-`.ss` Linked-AOT
// `marlin-scheme-host-aot` OS process:
//
//   GPU UI Clicks (mygoshell.GovernanceController)
//     -> gRPC (DispatchUiEvent / ResolveHumanGate / QueryProjectGraph)
//     -> Embedded Linked-AOT Gerbil/Gambit VM (POO x ASCENT x Bi-Temporal TM x Cedar)
//     -> Rust GovernanceAdmissionGate + Compiled Cedar Engine + Turso WAL (MGV1 + MGF1 v2)
//     -> Back to GPU UI rendered badges, rules, probe decisions, and audit receipts!
func TestLiveGPUConsoleDrivesRealSchemeHostAOTProcessE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	tursoDB := filepath.Join(t.TempDir(), "gpu-aot-gov.db")
	handle, err := grpcwire.LaunchGovernanceSidecar(ctx, grpcwire.GovernanceSidecarConfig{
		Mode:      "clean",
		TursoPath: tursoDB,
		SessionID: "gpu-ui-live-aot",
	})
	if errors.Is(err, grpcwire.ErrSchemeHostBinaryNotFound) {
		t.Skip("no local marlin-scheme-host-aot or marlin-scheme-host binary discovered")
	}
	if err != nil {
		t.Fatalf("LaunchGovernanceSidecar failed: %v", err)
	}
	defer func() {
		_ = handle.Close()
	}()

	targets := transport.NewTargetRegistry()
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)
	if err := app.EnableEmacsWindowKeymap(); err != nil {
		t.Fatalf("EnableEmacsWindowKeymap: %v", err)
	}

	ledger := features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	govUI := mygoshell.NewGovernanceController(features.NewRegistry(), ledger, features.NewHost())
	govUI.AttachBackendAuthority(handle.Authority)
	app.GovernanceUI = govUI

	// 1. Open Tier-0 Governance Console via SPC G chord -> live SHA-256 digests & Cedar rules rendered!
	app.HandleActiveKeyToken("SPC")
	_, cmd := app.HandleActiveKeyToken("G")
	if cmd != shell.CmdStudioOpenGovernance || !app.IsGovernanceOpen() {
		t.Fatalf("expected SPC G to open Governance Console, cmd=%q open=%v", cmd, app.IsGovernanceOpen())
	}

	tt := ui.NewTester(app.View, 1280, 1280)
	tt.Frame()
	for _, want := range []string{
		"RUST + SCHEME GOVERNANCE AUTHORITY",
		"BACKEND ADMITTED",
		"Proof Digests: assess=sha256:",
		"Inspect Cedar Rules (2)",
	} {
		if !tt.HasText(want) {
			t.Fatalf("expected live AOT governance UI to render %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}

	// 2. Open Cedar Inspector drawer -> verify real Scheme-synthesized FORBID & PERMIT rules!
	if err := tt.Click("Inspect Cedar Rules (2)"); err != nil {
		t.Fatalf("click Inspect Cedar Rules (2): %v", err)
	}
	tt.Frame()
	for _, want := range []string{
		"SYNTHESIZED CEDAR POLICY SOURCE (FORBID OVERRIDES PERMIT)",
		"marlin.forbid.agent-privileged-governance",
		"marlin.permit.verified-human-governance",
		"Run Custom Probe",
		"Add Delegation",
		"Record Flow",
		"Register Derivation",
		"Add Taint Edge",
	} {
		if !tt.HasText(want) {
			t.Fatalf("expected expanded inspector to render %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}

	// 3. Live Compiled-Cedar Probe via GPU UI -> real Rust Cedar evaluator returns ALLOW for VerifiedHuman!
	govUI.SetProbeDraft("ops-alice", "rearm_breaker", "workspace", true)
	tt.Frame()
	if err := tt.Click("Run Custom Probe"); err != nil {
		t.Fatalf("click Run Custom Probe (human allow): %v", err)
	}
	tt.Frame()
	if !tt.HasText("CEDAR ALLOW") || !tt.HasText("marlin.permit.verified-human-governance") {
		t.Fatalf("expected real Rust Cedar probe ALLOW rendered in GPU UI, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 4. Live OBO Delegation Builder + 3-Hop Transitive Revoke via GPU UI:
	//    Clean mode seeds `ops-admin -> ops-bob`; extending with `ops-bob -> agent-gpu`
	//    and clicking `Revoke ops-admin` proves real 3-hop Scheme ASCENT fencing!
	govUI.SetDelegationDraft("ops-bob", "agent-gpu")
	tt.Frame()
	if err := tt.Click("Add Delegation"); err != nil {
		t.Fatalf("click Add Delegation: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Delegations: 2") || !tt.HasText("Revoke ops-admin") {
		t.Fatalf("expected live AOT 2-edge delegation chain with Revoke ops-admin pill, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Revoke ops-admin"); err != nil {
		t.Fatalf("click Revoke ops-admin: %v", err)
	}
	tt.Frame()
	snap := handle.Authority.Snapshot()
	if !containsString(snap.FencedActors, "agent-gpu") || !tt.HasText("marlin.ascent.forbid.fenced.agent-gpu") {
		t.Fatalf("expected real Scheme ASCENT 3-hop transitive fence on agent-gpu, snap=%+v texts=%q", snap, strings.Join(tt.Texts(), " | "))
	}

	// 5. Live 5-Scope Context Flow Builder (private -> external) -> Exfil Cedar Forbid -> Custom Probe DENY -> Declassify!
	govUI.SetContextFlowDraft("gpu-secret", "private", "ext-gpu-hook", "external")
	tt.Frame()
	if err := tt.Click("Record Flow"); err != nil {
		t.Fatalf("click Record Flow: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Flows: 1") || !tt.HasText("BACKEND FAIL-CLOSED") ||
		!tt.HasText("marlin.ascent.forbid.exfil.gpu-secret.ext-gpu-hook") ||
		!tt.HasText("marlin.ascent.forbid.scope.external-unless-declassified") {
		t.Fatalf("expected real Scheme ASCENT exfil + external-scope unless Cedar forbids + fail-closed state, got %q", strings.Join(tt.Texts(), " | "))
	}
	govUI.SetProbeDraft("ops-alice", "rearm_breaker", "ext-gpu-hook", true)
	tt.Frame()
	if err := tt.Click("Run Custom Probe"); err != nil {
		t.Fatalf("click Run Custom Probe (exfil forbid): %v", err)
	}
	tt.Frame()
	if !tt.HasText("CEDAR DENY") || !tt.HasText("marlin.ascent.forbid.exfil.gpu-secret.ext-gpu-hook") {
		t.Fatalf("expected real Rust compiled Cedar exfil forbid to override permit, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Declassify gpu-secret"); err != nil {
		t.Fatalf("click Declassify gpu-secret: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Declassified: 1") {
		t.Fatalf("expected Declassified: 1 rendered after real AOT declassification, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 6. Live Bi-Temporal Derivation DAG Builder -> Retract root premise -> Real Scheme TM cascading blast radius!
	govUI.SetDerivationDraft("D-A-ROOT", "pol.root", "gpu-cert-v1", "")
	tt.Frame()
	if err := tt.Click("Register Derivation"); err != nil {
		t.Fatalf("click Register Derivation (D-A-ROOT): %v", err)
	}
	tt.Frame()
	govUI.SetDerivationDraft("D-B-LEAF", "pol.leaf", "gpu-ok", "D-A-ROOT")
	tt.Frame()
	if err := tt.Click("Register Derivation"); err != nil {
		t.Fatalf("click Register Derivation (D-B-LEAF): %v", err)
	}
	tt.Frame()
	if !tt.HasText("Derivations: 2") || !tt.HasText("Retract gpu-cert-v1") {
		t.Fatalf("expected Derivations: 2 and Retract gpu-cert-v1 pill, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Retract gpu-cert-v1"); err != nil {
		t.Fatalf("click Retract gpu-cert-v1: %v", err)
	}
	tt.Frame()
	snap = handle.Authority.Snapshot()
	if !containsString(snap.RetractedConclusions, "D-A-ROOT") || !containsString(snap.RetractedConclusions, "D-B-LEAF") {
		t.Fatalf("expected real Scheme Bi-Temporal TM blast radius [D-A-ROOT, D-B-LEAF], got %v", snap.RetractedConclusions)
	}

	// 7. Live CaMeL Taint Edge Builder -> Sanitize via GPU UI!
	//    Clean mode seeds 2 taint edges + 1 sanitizer; adding `gpu-ext -> gpu-mid` yields 3 edges & 2 sanitizers.
	govUI.SetTaintEdgeDraft("gpu-ext", "gpu-mid", "source")
	tt.Frame()
	if err := tt.Click("Add Taint Edge"); err != nil {
		t.Fatalf("click Add Taint Edge: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Taint Edges: 3") || !tt.HasText("Sanitize gpu-mid") {
		t.Fatalf("expected Taint Edges: 3 and Sanitize gpu-mid pill, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Sanitize gpu-mid"); err != nil {
		t.Fatalf("click Sanitize gpu-mid: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Sanitizers: 2") {
		t.Fatalf("expected Sanitizers: 2 rendered after real AOT sanitizer registration, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 8. Eval Clean + RFC 9470 Step-Up Re-Arm via GPU UI against real one-shot ApprovalGateService!
	if err := tt.Click("Eval Clean"); err != nil {
		t.Fatalf("click Eval Clean: %v", err)
	}
	tt.Frame()
	// Attempt re-arm BEFORE clicking Verify Step-Up -> MUST fail closed!
	if err := tt.Click("Step-Up Backend Re-Arm"); err != nil {
		t.Fatalf("click Step-Up Backend Re-Arm (without step-up): %v", err)
	}
	tt.Frame()
	if !handle.Authority.Snapshot().BreakerTripped || !tt.HasText("RFC 9470") {
		t.Fatalf("expected real backend re-arm without step-up to fail closed, texts=%q", strings.Join(tt.Texts(), " | "))
	}
	// Verify Step-Up -> Step-Up Backend Re-Arm -> resolves one-shot gate, advances epoch, restores BACKEND ADMITTED!
	if err := tt.Click("Verify Step-Up"); err != nil {
		t.Fatalf("click Verify Step-Up: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Step-Up Backend Re-Arm"); err != nil {
		t.Fatalf("click Step-Up Backend Re-Arm (verified): %v", err)
	}
	tt.Frame()
	finalSnap := handle.Authority.Snapshot()
	if finalSnap.BreakerTripped || !finalSnap.Admitted || finalSnap.BreakerTripEpoch < 2 {
		t.Fatalf("expected real AOT breaker disarmed and epoch >= 2 after verified step-up, got %+v", finalSnap)
	}
	if !tt.HasText("BACKEND ADMITTED") {
		t.Fatalf("expected BACKEND ADMITTED restored in GPU UI after step-up re-arm, got %q", strings.Join(tt.Texts(), " | "))
	}
	if len(ledger.AuditTrail()) < 9 {
		t.Fatalf("expected >= 9 immutable GPU governance audit receipts, got %d", len(ledger.AuditTrail()))
	}

	sidecar := handle.Sidecar.Sidecar()
	if err := handle.Close(); err != nil {
		t.Fatalf("handle.Close failed: %v", err)
	}
	if exitErr := sidecar.ExitErr(); exitErr != nil {
		t.Fatalf("expected clean exit code 0 from real marlin-scheme-host-aot daemon, got %v", exitErr)
	}
}

// TestLiveGPUConsoleSurvivesSchemeHostAOTRestartE2E verifies visual Anti-Restart-Reset
// and MGF1 v2 durability across a real OS-level SIGTERM death and reboot of the
// zero-`.ss` Linked-AOT `marlin-scheme-host-aot` daemon:
//
//   Process 1: GPU UI records OBO delegation, private->external exfil flow (synthesizing
//              external-scope unless-declassified Cedar forbid + tripping E-Stop), and a
//              dynamic Bi-Temporal derivation -> clean SIGTERM shutdown.
//   Process 2: Brand-new OS daemon booted with `Mode: "clean"` on the same Turso SQLite WAL
//              -> fresh GPU GovernanceController opens via SPC G and IMMEDIATELY displays
//              `BACKEND FAIL-CLOSED`, restored `Delegations: 2`, `Flows: 1`, `Derivations: 1`,
//              `marlin.ascent.forbid.scope.external-unless-declassified`, then executes live
//              post-restart `Revoke ops-admin` (fencing `agent-persist`) and `Retract persist-cert-v1`!
func TestLiveGPUConsoleSurvivesSchemeHostAOTRestartE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	tursoDB := filepath.Join(t.TempDir(), "gpu-aot-restart.db")

	newGPUHarness := func(auth mygoshell.BackendGovernanceAuthority) (*mygoshell.WorkbenchApp, *mygoshell.GovernanceController, *ui.Tester) {
		targets := transport.NewTargetRegistry()
		slots := shell.NewSlotRegistry()
		wb := shell.NewWorkbenchController(slots)
		app := mygoshell.NewWorkbenchApp(targets, slots, wb)
		if err := app.EnableEmacsWindowKeymap(); err != nil {
			t.Fatalf("EnableEmacsWindowKeymap: %v", err)
		}
		govUI := mygoshell.NewGovernanceController(features.NewRegistry(), features.NewGovernanceLedger(features.DefaultGovernancePolicy()), features.NewHost())
		govUI.AttachBackendAuthority(auth)
		govUI.SetShowCedarInspector(true)
		app.GovernanceUI = govUI
		app.HandleActiveKeyToken("SPC")
		_, _ = app.HandleActiveKeyToken("G")
		tt := ui.NewTester(app.View, 1280, 1280)
		tt.Frame()
		return app, govUI, tt
	}

	// --- OS Process 1: Mutate live MGF1 v2 facts & trigger fail-closed E-Stop via GPU UI ---
	h1, err := grpcwire.LaunchGovernanceSidecar(ctx, grpcwire.GovernanceSidecarConfig{
		Mode:      "clean",
		TursoPath: tursoDB,
		SessionID: "gpu-restart-proc-1",
	})
	if errors.Is(err, grpcwire.ErrSchemeHostBinaryNotFound) {
		t.Skip("no local marlin-scheme-host-aot or marlin-scheme-host binary discovered")
	}
	if err != nil {
		t.Fatalf("LaunchGovernanceSidecar (proc 1) failed: %v", err)
	}
	_, gov1, tt1 := newGPUHarness(h1.Authority)

	gov1.SetDelegationDraft("ops-bob", "agent-persist")
	tt1.Frame()
	if err := tt1.Click("Add Delegation"); err != nil {
		_ = h1.Close()
		t.Fatalf("proc 1 Add Delegation: %v", err)
	}
	tt1.Frame()

	gov1.SetContextFlowDraft("vault-key", "private", "ext-leak", "external")
	tt1.Frame()
	if err := tt1.Click("Record Flow"); err != nil {
		_ = h1.Close()
		t.Fatalf("proc 1 Record Flow: %v", err)
	}
	tt1.Frame()

	gov1.SetDerivationDraft("D-A-PERSIST", "pol.persist", "persist-cert-v1", "")
	tt1.Frame()
	if err := tt1.Click("Register Derivation"); err != nil {
		_ = h1.Close()
		t.Fatalf("proc 1 Register Derivation: %v", err)
	}
	tt1.Frame()

	for _, want := range []string{
		"BACKEND FAIL-CLOSED",
		"Delegations: 2",
		"Flows: 1",
		"Derivations: 1",
		"marlin.ascent.forbid.exfil.vault-key.ext-leak",
		"marlin.ascent.forbid.scope.external-unless-declassified",
	} {
		if !tt1.HasText(want) {
			_ = h1.Close()
			t.Fatalf("proc 1 expected GPU UI to render %q, got %q", want, strings.Join(tt1.Texts(), " | "))
		}
	}

	sidecar1 := h1.Sidecar.Sidecar()
	if err := h1.Close(); err != nil {
		t.Fatalf("proc 1 Close: %v", err)
	}
	if exitErr := sidecar1.ExitErr(); exitErr != nil {
		t.Fatalf("proc 1 expected clean exit 0, got %v", exitErr)
	}

	// --- OS Process 2: Cold-boot fresh daemon with `Mode: "clean"` on same Turso WAL ---
	h2, err := grpcwire.LaunchGovernanceSidecar(ctx, grpcwire.GovernanceSidecarConfig{
		Mode:      "clean",
		TursoPath: tursoDB,
		SessionID: "gpu-restart-proc-2",
	})
	if err != nil {
		t.Fatalf("LaunchGovernanceSidecar (proc 2) failed: %v", err)
	}
	defer func() {
		_ = h2.Close()
	}()

	_, _, tt2 := newGPUHarness(h2.Authority)
	for _, want := range []string{
		"BACKEND FAIL-CLOSED",
		"Delegations: 2",
		"Flows: 1",
		"Derivations: 1",
		"marlin.ascent.forbid.scope.external-unless-declassified",
		"Revoke ops-admin",
		"Retract persist-cert-v1",
	} {
		if !tt2.HasText(want) {
			t.Fatalf("proc 2 Anti-Restart-Reset expected GPU UI to restore %q, got %q", want, strings.Join(tt2.Texts(), " | "))
		}
	}

	// Incremental post-restart ASCENT closure via GPU UI click: Revoke ops-admin -> fences persisted `agent-persist`!
	if err := tt2.Click("Revoke ops-admin"); err != nil {
		t.Fatalf("proc 2 click Revoke ops-admin: %v", err)
	}
	tt2.Frame()
	snap2 := h2.Authority.Snapshot()
	if !containsString(snap2.FencedActors, "agent-persist") {
		t.Fatalf("proc 2 expected post-restart ASCENT transitive fencing of agent-persist, got %v", snap2.FencedActors)
	}

	// Incremental post-restart Bi-Temporal TM retraction via GPU UI click: Retract persist-cert-v1 -> retracts `D-A-PERSIST`!
	if err := tt2.Click("Retract persist-cert-v1"); err != nil {
		t.Fatalf("proc 2 click Retract persist-cert-v1: %v", err)
	}
	tt2.Frame()
	snap2 = h2.Authority.Snapshot()
	if !containsString(snap2.RetractedConclusions, "D-A-PERSIST") || !tt2.HasText("TM Retracted: D-A-PERSIST") {
		t.Fatalf("proc 2 expected post-restart Bi-Temporal TM retraction of D-A-PERSIST, snap=%+v texts=%q", snap2, strings.Join(tt2.Texts(), " | "))
	}

	sidecar2 := h2.Sidecar.Sidecar()
	if err := h2.Close(); err != nil {
		t.Fatalf("proc 2 Close: %v", err)
	}
	if exitErr := sidecar2.ExitErr(); exitErr != nil {
		t.Fatalf("proc 2 expected clean exit 0, got %v", exitErr)
	}
}
