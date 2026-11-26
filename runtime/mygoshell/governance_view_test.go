// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell_test

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
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
