// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell_test

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/contextgov"
	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

type testFeatureMod struct {
	meta features.FeatureMeta
}

func (m testFeatureMod) Meta() features.FeatureMeta { return m.meta }
func (m testFeatureMod) Register(host *features.Host) error {
	host.AddBuffer(shell.PaneBuffer{
		BufferID: "feature:" + string(m.meta.ID),
		Title:    m.meta.Name,
		Kind:     "native-gpu",
	}, func(any, any) {})
	return nil
}

func TestTier0GovernanceConsoleFeatureTogglesPolicyDenialAndSPCShortcut(t *testing.T) {
	targets := transport.NewTargetRegistry()
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)
	if err := app.EnableEmacsWindowKeymap(); err != nil {
		t.Fatalf("EnableEmacsWindowKeymap: %v", err)
	}

	reg := features.NewRegistry()
	_ = reg.RegisterCompiled(testFeatureMod{meta: features.FeatureMeta{
		ID:             features.FeatureChatRoom,
		Name:           "Slock-Style Agent Chat Room",
		Description:    "Channels, agent roster, and thread working memory",
		TrustTier:      features.Tier0HostNative,
		DefaultEnabled: true,
	}})
	_ = reg.RegisterCompiled(testFeatureMod{meta: features.FeatureMeta{
		ID:             features.FeatureCompare,
		Name:           "T3 Multi-Model Compare",
		Description:    "Parallel side-by-side model streaming and branching",
		TrustTier:      features.Tier0HostNative,
		DefaultEnabled: false,
	}})

	ledger := features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	host := features.NewHost()
	reg.Activate(features.ParseFeatureSpec("default"), ledger, host)
	ledger.RecordAudit("user", "gate-demo", "approve_once", features.AuditGateClaimed, "idem-1")

	var toggled []string
	govUI := mygoshell.NewGovernanceController(reg, ledger, host)
	govUI.OnFeatureChanged = func(id features.FeatureID, enabled bool) {
		state := "off"
		if enabled {
			state = "on"
		}
		toggled = append(toggled, string(id)+":"+state)
	}
	app.GovernanceUI = govUI
	app.FeatureHost = host

	// Open Governance Console via SPC G!
	app.HandleActiveKeyToken("SPC")
	_, cmd := app.HandleActiveKeyToken("G")
	if cmd != shell.CmdStudioOpenGovernance || !app.IsGovernanceOpen() {
		t.Fatalf("expected SPC G to open Governance Console, cmd=%q open=%v", cmd, app.IsGovernanceOpen())
	}

	tt := ui.NewTester(app.View, 1240, 820)
	tt.Frame()

	for _, want := range []string{
		"TIER-0 GOVERNANCE", "UI Governance & Feature Security",
		"Compiled: 2", "Active: 1", "Slock-Style Agent Chat Room", "T3 Multi-Model Compare",
		"AUDIT RECEIPT TIMELINE", "gate-demo · approve_once",
	} {
		if !tt.HasText(want) {
			t.Fatalf("expected Governance Console to render %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}

	// Enable compare at runtime -> activates and records ADMITTED receipt!
	if err := tt.Click("Enable compare"); err != nil {
		t.Fatalf("click Enable compare: %v", err)
	}
	tt.Frame()
	if !reg.IsEnabled(features.FeatureCompare) || len(toggled) != 1 || toggled[0] != "compare:on" {
		t.Fatalf("expected compare enabled at runtime, enabled=%v toggled=%v", reg.IsEnabled(features.FeatureCompare), toggled)
	}

	// Disable compare, then block it by policy -> attempting to re-enable fails closed with DENIED receipt!
	if err := tt.Click("Disable compare"); err != nil {
		t.Fatalf("click Disable compare: %v", err)
	}
	tt.Frame()
	ledger.BlockFeature(features.FeatureCompare, "policy locked")
	if err := tt.Click("Enable compare"); err != nil {
		t.Fatalf("click Enable compare (blocked): %v", err)
	}
	tt.Frame()
	if reg.IsEnabled(features.FeatureCompare) || !tt.HasText("Policy denial: features: activation denied") {
		t.Fatalf("expected policy denial rendered on blocked feature enable, got %q", strings.Join(tt.Texts(), " | "))
	}

	// ESC closes Governance Console cleanly.
	if !app.DismissTopOverlay() || app.IsGovernanceOpen() {
		t.Fatal("expected ESC DismissTopOverlay to close Governance Console")
	}
}

func TestFeatureHostDesktopPaletteAndMobileSpacesIntegration(t *testing.T) {
	as := mygoshell.NewAdaptiveShell(mygoshell.AdaptiveShellConfig{BrandTitle: "Marlin"})
	host := features.NewHost()
	renderedDesktop := false
	renderedMobile := false
	host.AddBuffer(shell.PaneBuffer{
		BufferID: "feature:chatroom",
		Title:    "Agent Room",
		Kind:     "native-gpu",
	}, func(surface any, _ any) {
		if c, ok := surface.(*ui.Context); ok {
			ui.Text(c, "OPT-IN FEATURE SURFACE ACTIVE")
			renderedDesktop = true
			renderedMobile = true
		}
	})
	as.SetFeatureHost(host, mygoshell.NewGovernanceController(nil, nil, host))

	// 1. Desktop: SPC o r chord opens feature:chatroom directly in the focused Emacs split pane!
	as.Desktop.SetKeyMode(input.ScopeNormal)
	for _, tok := range []string{"SPC", "o", "r"} {
		_, _ = as.Desktop.HandleKeyToken(input.ScopeNormal, tok)
	}
	dtt := ui.NewTester(as.Desktop.View, 1220, 820)
	dtt.Frame()
	if !renderedDesktop || !dtt.HasText("OPT-IN FEATURE SURFACE ACTIVE") {
		t.Fatalf("expected Desktop SPC o r chord to render opt-in FeatureHost buffer, got %q", strings.Join(dtt.Texts(), " | "))
	}

	// 2. Mobile (393x852): Spaces pill appears ONLY when FeatureHost has buffers -> opens overlay -> Back closes cleanly!
	renderedMobile = false
	mtt := ui.NewTester(as.Mobile.View, 393, 852)
	mtt.Frame()
	if !mtt.HasText("Spaces") {
		t.Fatalf("expected Mobile Spaces pill when FeatureHost has active buffers, got %q", strings.Join(mtt.Texts(), " | "))
	}
	if err := mtt.Click("Spaces"); err != nil {
		t.Fatalf("click Mobile Spaces pill: %v", err)
	}
	mtt.Frame()
	if !renderedMobile || !mtt.HasText("OPT-IN FEATURE SURFACE ACTIVE") {
		t.Fatalf("expected Mobile Spaces overlay to render feature buffer, got %q", strings.Join(mtt.Texts(), " | "))
	}
	if err := mtt.Click("Back"); err != nil {
		t.Fatalf("click Mobile feature Back button: %v", err)
	}
	mtt.Frame()
	if as.Mobile.ActiveFeatureID() != "" {
		t.Fatalf("expected Mobile feature overlay closed after Back, got %q", as.Mobile.ActiveFeatureID())
	}
}

func TestTier0GovernanceConsoleEStopBreakerAndSharedContextScopes(t *testing.T) {
	targets := transport.NewTargetRegistry()
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)

	ledger := features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	breaker := pluginhost.NewExecutionBreaker(2, ledger)
	pHost := pluginhost.NewHost(slots, nil)
	ctxGov := contextgov.NewGovernanceStore(ledger)
	ctxGov.RegisterChannelMember("tenant-1", "ch-team", "alice")
	_, _ = ctxGov.CreateEntry(contextgov.ContextEntry{
		ID: "priv-1", TenantID: "tenant-1", Scope: contextgov.ScopePrivate, OwnerID: "alice", Body: "secret",
	})
	_, _ = ctxGov.CreateEntry(contextgov.ContextEntry{
		ID: "ch-1", TenantID: "tenant-1", Scope: contextgov.ScopeChannel, ChannelID: "ch-team", OwnerID: "alice", Body: "shared",
	})
	ctxGov.MarkChannelTainted("tenant-1", "ch-team", "slack", "external msg")

	govUI := mygoshell.NewGovernanceController(features.NewRegistry(), ledger, features.NewHost())
	govUI.AttachSecurityControls(breaker, pHost, ctxGov)
	var eStopEvents []bool
	govUI.OnEmergencyStop = func(tripped bool, _ uint64) {
		eStopEvents = append(eStopEvents, tripped)
	}
	app.GovernanceUI = govUI
	app.OpenGovernance()

	tt := ui.NewTester(app.View, 1240, 820)
	tt.Frame()
	for _, want := range []string{
		"AI & TOOL EXECUTION CIRCUIT BREAKER", "ARMED", "Epoch: 1", "Trip E-Stop",
		"SHARED CONTEXT SCOPES", "Private: 1", "Channel: 1", "Tainted Ch: 1",
	} {
		if !tt.HasText(want) {
			t.Fatalf("expected Governance Console E-Stop/Context UI to render %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}

	// Click Trip E-Stop -> breaker latches tripped, UI switches to TRIPPED · FAIL-CLOSED + Human Re-Arm!
	if err := tt.Click("Trip E-Stop"); err != nil {
		t.Fatalf("click Trip E-Stop: %v", err)
	}
	tt.Frame()
	tripped, ep, _, _ := breaker.State()
	if !tripped || ep != 1 || !tt.HasText("TRIPPED · FAIL-CLOSED") || !tt.HasText("Human Re-Arm") {
		t.Fatalf("expected breaker tripped after Trip E-Stop click, tripped=%v ep=%d texts=%q", tripped, ep, strings.Join(tt.Texts(), " | "))
	}

	// Click Human Re-Arm -> advances epoch to 2 and re-arms!
	if err := tt.Click("Human Re-Arm"); err != nil {
		t.Fatalf("click Human Re-Arm: %v", err)
	}
	tt.Frame()
	tripped, ep, _, _ = breaker.State()
	if tripped || ep != 2 || len(eStopEvents) != 2 || !eStopEvents[0] || eStopEvents[1] {
		t.Fatalf("expected Human Re-Arm to advance epoch=2 and disarm, tripped=%v ep=%d events=%v", tripped, ep, eStopEvents)
	}
}

func TestTier0GovernanceConsoleBackendAuthorityAndStepUpRearm(t *testing.T) {
	targets := transport.NewTargetRegistry()
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)

	authBridge := mygoshell.NewLocalBackendAuthorityBridge(mygoshell.BackendGovernanceSnapshot{
		Admitted:             false,
		RevocationEpoch:      2,
		PolicyRevision:       1,
		BreakerTripped:       true,
		BreakerTripEpoch:     1,
		RejectionReasons:     []string{"ascent-camel-taint-alert", "bitemporal-premise-retracted"},
		FencedActors:         []string{"agent-sub"},
		BlockedTools:         []string{"os-shell"},
		RetractedConclusions: []string{"D-OBO-TICKET", "D-TOOL-EXEC"},
		SynthesizedCedarRules: []mygoshell.SynthesizedCedarRule{
			{PolicyID: "marlin.ascent.forbid.fenced.agent-sub", Source: `forbid (principal == Marlin::Actor::"agent-sub", action, resource);`},
		},
	})

	govUI := mygoshell.NewGovernanceController(features.NewRegistry(), features.NewGovernanceLedger(features.DefaultGovernancePolicy()), features.NewHost())
	govUI.AttachBackendAuthority(authBridge)
	app.GovernanceUI = govUI
	app.OpenGovernance()

	tt := ui.NewTester(app.View, 1280, 860)
	tt.Frame()
	for _, want := range []string{
		"RUST + SCHEME GOVERNANCE AUTHORITY",
		"BACKEND FAIL-CLOSED",
		"Proof Epoch: 2",
		"ASCENT Fenced: agent-sub",
		"TM Retracted: D-OBO-TICKET, D-TOOL-EXEC",
		"Blocked Tools: os-shell",
		"marlin.ascent.forbid.fenced.agent-sub",
	} {
		if !tt.HasText(want) {
			t.Fatalf("expected backend authority console to render %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}

	// Clicking Step-Up Backend Re-Arm BEFORE verifying RFC 9470 Step-Up MUST fail closed!
	if err := tt.Click("Step-Up Backend Re-Arm"); err != nil {
		t.Fatalf("click Step-Up Backend Re-Arm without step-up: %v", err)
	}
	tt.Frame()
	if !authBridge.Snapshot().BreakerTripped || !tt.HasText("RFC 9470 step-up required") {
		t.Fatalf("expected backend re-arm without step-up to fail closed, texts=%q", strings.Join(tt.Texts(), " | "))
	}

	// Verify Step-Up -> Step-Up Backend Re-Arm -> backend advances epoch and admits!
	if err := tt.Click("Verify Step-Up"); err != nil {
		t.Fatalf("click Verify Step-Up: %v", err)
	}
	tt.Frame()
	if !tt.HasText("RFC 9470 STEP-UP READY") {
		t.Fatalf("expected RFC 9470 STEP-UP READY pill, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Step-Up Backend Re-Arm"); err != nil {
		t.Fatalf("click Step-Up Backend Re-Arm after step-up: %v", err)
	}
	tt.Frame()
	snap := authBridge.Snapshot()
	if snap.BreakerTripped || !snap.Admitted || snap.BreakerTripEpoch != 2 || snap.RevocationEpoch != 3 {
		t.Fatalf("expected backend re-arm to advance epoch=2 rev=3 and admit, got %+v", snap)
	}
	if !tt.HasText("BACKEND ADMITTED") {
		t.Fatalf("expected BACKEND ADMITTED pill after step-up re-arm, got %q", strings.Join(tt.Texts(), " | "))
	}
}

func TestTier0GovernanceConsoleInteractiveFactGraphRevokeAndDeclassify(t *testing.T) {
	targets := transport.NewTargetRegistry()
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)

	ledger := features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	authBridge := mygoshell.NewLocalBackendAuthorityBridge(mygoshell.BackendGovernanceSnapshot{
		Admitted:         true,
		RevocationEpoch:  1,
		PolicyRevision:   2,
		BreakerTripped:   false,
		BreakerTripEpoch: 1,
		FactDelegations: []mygoshell.GovernanceDelegationEdge{
			{Parent: "ops-root", Child: "agent-lead"},
			{Parent: "agent-lead", Child: "agent-worker"},
		},
		FactContextFlows: []mygoshell.GovernanceContextFlow{
			{Entry: "vault-secret", SrcScope: "private", Target: "ext-webhook", DstScope: "external"},
		},
		FactDerivations: []mygoshell.GovernanceDerivationSpec{
			{DerivationID: "D-TOKEN", PolicyID: "pol.cert", SubjectPremises: []string{"token-cert-v1"}},
			{DerivationID: "D-DEPLOY", PolicyID: "pol.deploy", SubjectPremises: []string{"approval-sig"}, ParentDerivations: []string{"D-TOKEN"}},
		},
		FactTaintEdges: []mygoshell.GovernanceTaintEdge{
			{Src: "chat-ext", Dst: "shared-ctx"},
			{Src: "shared-ctx", Dst: "os-shell"},
		},
		FactTaintSources: []string{"chat-ext"},
		FactTaintSinks:   []string{"os-shell"},
	})

	govUI := mygoshell.NewGovernanceController(features.NewRegistry(), ledger, features.NewHost())
	govUI.AttachBackendAuthority(authBridge)
	app.GovernanceUI = govUI
	app.OpenGovernance()

	tt := ui.NewTester(app.View, 1280, 860)
	tt.Frame()
	for _, want := range []string{
		"Delegations: 2",
		"Flows: 1",
		"Declassified: 0",
		"Derivations: 2",
		"Taint Edges: 2",
		"Sanitizers: 0",
		"Revoke agent-lead",
		"Declassify vault-secret",
		"Retract token-cert-v1",
		"Sanitize shared-ctx",
		"Probe Cedar",
	} {
		if !tt.HasText(want) {
			t.Fatalf("expected interactive fact-graph control %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}

	// Click Sanitize shared-ctx -> registers CaMeL sanitizer & records audit receipt!
	if err := tt.Click("Sanitize shared-ctx"); err != nil {
		t.Fatalf("click Sanitize shared-ctx: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Sanitizers: 1") {
		t.Fatalf("expected Sanitizers: 1 after clicking Sanitize shared-ctx, got %q", strings.Join(tt.Texts(), " | "))
	}

	// Click Probe Cedar -> renders compiled Cedar decision badge and matched forbid policy ID!
	if err := tt.Click("Probe Cedar"); err != nil {
		t.Fatalf("click Probe Cedar: %v", err)
	}
	tt.Frame()
	if !tt.HasText("CEDAR DENY") || !tt.HasText("marlin.forbid.agent-privileged-governance") {
		t.Fatalf("expected live Cedar probe denial rendered, got %q", strings.Join(tt.Texts(), " | "))
	}

	// Click Revoke agent-lead -> transitively fences agent-lead AND agent-worker & records audit receipt!
	if err := tt.Click("Revoke agent-lead"); err != nil {
		t.Fatalf("click Revoke agent-lead: %v", err)
	}
	tt.Frame()
	snap := authBridge.Snapshot()
	if len(snap.FencedActors) != 2 || !tt.HasText("ASCENT Fenced: agent-lead, agent-worker") {
		t.Fatalf("expected transitive fencing of agent-lead & agent-worker, snap=%+v texts=%q", snap, strings.Join(tt.Texts(), " | "))
	}

	// Click Declassify vault-secret -> updates Declassified badge & records audit receipt!
	if err := tt.Click("Declassify vault-secret"); err != nil {
		t.Fatalf("click Declassify vault-secret: %v", err)
	}
	tt.Frame()
	snap = authBridge.Snapshot()
	if len(snap.FactDeclassifiedEntries) != 1 || snap.FactDeclassifiedEntries[0] != "vault-secret" || !tt.HasText("Declassified: 1") {
		t.Fatalf("expected vault-secret declassified, snap=%+v texts=%q", snap, strings.Join(tt.Texts(), " | "))
	}

	// Click Retract token-cert-v1 -> computes Bi-Temporal reverse-dependency blast radius (D-TOKEN & D-DEPLOY) and auto-trips fail-closed!
	if err := tt.Click("Retract token-cert-v1"); err != nil {
		t.Fatalf("click Retract token-cert-v1: %v", err)
	}
	tt.Frame()
	snap = authBridge.Snapshot()
	if !snap.BreakerTripped || len(snap.RetractedConclusions) != 2 || !tt.HasText("TM Retracted: D-TOKEN, D-DEPLOY") {
		t.Fatalf("expected dynamic Bi-Temporal TM blast radius D-TOKEN & D-DEPLOY, snap=%+v texts=%q", snap, strings.Join(tt.Texts(), " | "))
	}
	trail := ledger.AuditTrail()
	if len(trail) < 3 {
		t.Fatalf("expected immutable audit receipts recorded for interactive revoke, declassify, and retract, got %d", len(trail))
	}
}

func TestTier0GovernanceConsoleCedarInspectorAndDelegationBuilder(t *testing.T) {
	targets := transport.NewTargetRegistry()
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)

	ledger := features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	authBridge := mygoshell.NewLocalBackendAuthorityBridge(mygoshell.BackendGovernanceSnapshot{
		Admitted:         true,
		RevocationEpoch:  1,
		PolicyRevision:   2,
		BreakerTripEpoch: 1,
		SynthesizedCedarRules: []mygoshell.SynthesizedCedarRule{
			{PolicyID: "marlin.forbid.agent-privileged-governance", Source: `forbid (principal in Marlin::Role::"Agent", action in [Marlin::Action::"rearm_breaker"], resource);`},
			{PolicyID: "marlin.permit.verified-human-governance", Source: `permit (principal in Marlin::Role::"VerifiedHuman", action in [Marlin::Action::"rearm_breaker"], resource) when { context.step_up_fresh == true };`},
		},
	})

	govUI := mygoshell.NewGovernanceController(features.NewRegistry(), ledger, features.NewHost())
	govUI.AttachBackendAuthority(authBridge)
	app.GovernanceUI = govUI
	app.OpenGovernance()

	tt := ui.NewTester(app.View, 1280, 960)
	tt.Frame()
	if !tt.HasText("Inspect Cedar Rules (2)") {
		t.Fatalf("expected Inspect Cedar Rules (2) toggle, got %q", strings.Join(tt.Texts(), " | "))
	}

	// Open Cedar Policy Inspector -> displays FORBID & PERMIT badges and full Cedar rule sources!
	if err := tt.Click("Inspect Cedar Rules (2)"); err != nil {
		t.Fatalf("click Inspect Cedar Rules: %v", err)
	}
	tt.Frame()
	for _, want := range []string{
		"SYNTHESIZED CEDAR POLICY SOURCE (FORBID OVERRIDES PERMIT)",
		"FORBID",
		"PERMIT",
		`forbid (principal in Marlin::Role::"Agent", action in [Marlin::Action::"rearm_breaker"], resource);`,
		"Run Custom Probe",
		"Add Delegation",
	} {
		if !tt.HasText(want) {
			t.Fatalf("expected Cedar inspector to render %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}

	// Run custom Cedar probe as verified human (ops-alice, rearm_breaker, workspace, step_up=true) -> CEDAR ALLOW!
	govUI.SetProbeDraft("ops-alice", "rearm_breaker", "workspace", true)
	tt.Frame()
	if err := tt.Click("Run Custom Probe"); err != nil {
		t.Fatalf("click Run Custom Probe: %v", err)
	}
	tt.Frame()
	if !tt.HasText("CEDAR ALLOW") || !tt.HasText("marlin.permit.verified-human-governance") {
		t.Fatalf("expected CEDAR ALLOW for verified human probe, got %q", strings.Join(tt.Texts(), " | "))
	}

	// Add live OBO delegation edge via Delegation Builder -> updates Delegations: 1 & logs audit receipt!
	govUI.SetDelegationDraft("ops-root", "agent-coder")
	tt.Frame()
	if err := tt.Click("Add Delegation"); err != nil {
		t.Fatalf("click Add Delegation: %v", err)
	}
	tt.Frame()
	snap := authBridge.Snapshot()
	if len(snap.FactDelegations) != 1 || snap.FactDelegations[0].Parent != "ops-root" || snap.FactDelegations[0].Child != "agent-coder" {
		t.Fatalf("expected ops-root->agent-coder delegation recorded, got %+v", snap.FactDelegations)
	}
	if !tt.HasText("Delegations: 1") {
		t.Fatalf("expected Delegations: 1 badge after Add Delegation, got %q", strings.Join(tt.Texts(), " | "))
	}
}
