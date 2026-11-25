// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestMobileCompanionTabsGateApprovalCaptureAndAppStorePolicy(t *testing.T) {
	mob := NewMobileCompanionApp("iPhone Personal -> Desktop Studio")
	mob.SetPendingGates([]PendingGateView{
		{
			GateID:      "gate-mob-9",
			ToolName:    "device.ios.notify.send",
			Prompt:      "Approve evening reminder?",
			Description: "Tier-0 gate over mTLS.",
		},
	})
	mob.SetPacks([]MobileDeclarativePack{
		{PluginID: "tao3k.daily-planner-pack", Version: "0.1.0", Runtime: schema.RuntimeDeclarative},
		{PluginID: "tao3k.org-agenda-planner", Version: "0.2.0", Runtime: schema.RuntimeWebViewUntrusted},
	})

	var approvedGate string
	mob.OnGateDecision = func(gateID string, approve bool) {
		if approve {
			approvedGate = gateID
		}
	}
	var capturedText string
	mob.OnQuickCapture = func(_, text string) {
		capturedText = text
	}

	tt := ui.NewTester(mob.View, 390, 844)
	if !tt.HasText("Marlin Mobile") || !tt.HasText("1 GATE") {
		t.Fatalf("expected mobile header and gate badge, got %q", tt.Texts())
	}

	// Switch to Gates tab and approve once.
	if err := tt.Click("Gates"); err != nil {
		t.Fatalf("click Gates tab: %v", err)
	}
	if !tt.HasText("Approve evening reminder?") {
		t.Fatalf("expected pending gate on Gates tab, got %q", tt.Texts())
	}
	if err := tt.Click("Approve Once"); err != nil {
		t.Fatalf("click Approve Once: %v", err)
	}
	if approvedGate != "gate-mob-9" {
		t.Fatalf("expected gate-mob-9 approved, got %q", approvedGate)
	}

	// Switch to Capture tab, type note, and submit.
	if err := tt.Click("Capture"); err != nil {
		t.Fatalf("click Capture tab: %v", err)
	}
	mob.mu.Lock()
	mob.captureDraft = "Buy coffee beans tomorrow"
	mob.mu.Unlock()
	tt.Frame()
	if err := tt.Click("Capture to Inbox"); err != nil {
		t.Fatalf("click Capture to Inbox: %v", err)
	}
	if capturedText != "Buy coffee beans tomorrow" {
		t.Fatalf("expected captured text, got %q", capturedText)
	}

	// Switch to Packs tab: declarative succeeds, webview-untrusted is blocked (App Store 2.5.2).
	if err := tt.Click("Packs"); err != nil {
		t.Fatalf("click Packs tab: %v", err)
	}
	if err := tt.Click("Apply tao3k.daily-planner-pack"); err != nil {
		t.Fatalf("click declarative pack: %v", err)
	}
	if err := mob.TryActivatePack("tao3k.org-agenda-planner"); err == nil || !strings.Contains(err.Error(), "2.5.2") {
		t.Fatalf("expected App Store 2.5.2 denial for webview-untrusted pack, got %v", err)
	}
	tt.Frame()
	if !tt.HasText("App Store 2.5.2") {
		t.Fatalf("expected App Store 2.5.2 policy banner visible, got %q", tt.Texts())
	}
}

func TestStudioShortcutsThemeToggleAndSettingsDrawer(t *testing.T) {
	app, _, sw := buildStudioBenchHarness()
	isLight := false
	app.IsLightThemeFn = func() bool { return isLight }
	app.OnToggleTheme = func() { isLight = !isLight }
	spec := schema.BoundedSchema{
		ID:    "studio.prefs",
		Title: "Marlin Studio Preferences",
		Fields: []schema.FieldSpec{
			{Key: "theme", Label: "Theme", Kind: schema.FieldEnum, EnumValues: []string{"obsidian", "paper"}, Default: "obsidian"},
			{Key: "focus_min", Label: "Focus Minutes", Kind: schema.FieldInt, MinInt: 5, MaxInt: 90, Default: 25},
		},
	}
	app.SettingsSchema = &spec
	app.SettingsState = NewSchemaFormState(spec, nil)
	var saved map[string]any
	app.OnSaveSettings = func(v map[string]any) { saved = v }

	tt := ui.NewTester(app.View, 1340, 860)

	// Cmd+2 switches to AI Memory Inbox, Cmd+3 switches to AI Workflow Cockpit.
	tt.Key(ui.Cmd, ui.Key2)
	if got := sw.ActiveID(); got != "tao3k.ai-memory-inbox" {
		t.Fatalf("expected Cmd+2 -> tao3k.ai-memory-inbox, got %q", got)
	}
	tt.Key(ui.Cmd, ui.Key3)
	if got := sw.ActiveID(); got != "tao3k.ai-workflow-cockpit" {
		t.Fatalf("expected Cmd+3 -> tao3k.ai-workflow-cockpit, got %q", got)
	}
	tt.Key(ui.Cmd, ui.Key1)
	if got := sw.ActiveID(); got != "tao3k.org-agenda-planner" {
		t.Fatalf("expected Cmd+1 -> tao3k.org-agenda-planner, got %q", got)
	}

	// Click Light theme button.
	if err := tt.Click("Light"); err != nil {
		t.Fatalf("click Light theme toggle: %v", err)
	}
	if !isLight {
		t.Fatal("expected theme toggled to light")
	}

	// Cmd+, opens Settings modal and Save Settings submits validated form.
	tt.Key(ui.Cmd, ui.KeyComma)
	if !tt.HasText("Marlin Studio Preferences") {
		t.Fatalf("expected Settings modal open on Cmd+,, got %q", tt.Texts())
	}
	if err := tt.Click("Save Settings"); err != nil {
		t.Fatalf("click Save Settings: %v", err)
	}
	if saved == nil || saved["focus_min"] != int64(25) {
		t.Fatalf("expected validated settings saved, got %+v", saved)
	}
}
