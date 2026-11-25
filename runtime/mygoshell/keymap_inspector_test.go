// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell_test

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

func TestKeymapInspectorSearchExecuteAndGlobalESCAccessibility(t *testing.T) {
	targets := transport.NewTargetRegistry()
	_ = targets.Register(&authUITestTarget{meta: transport.TargetMeta{
		ID: "local-app-server", DisplayName: "Marlin App Server", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Address: "127.0.0.1:50051", Phase: transport.PhaseReady,
	}})
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)
	if err := app.EnableEmacsWindowKeymap(); err != nil {
		t.Fatal(err)
	}

	tt := ui.NewTester(app.View, 1280, 800)

	// 1. Click "Keys" pill in status bar -> opens Tier-0 Keymap Inspector showing compiled bindings!
	if !tt.HasText("Keys") {
		t.Fatalf("expected status bar 'Keys' pill, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Keys"); err != nil {
		t.Fatalf("click Keys failed: %v", err)
	}
	tt.Frame()
	if !app.IsKeymapInspectorOpen() || !tt.HasText("TIER-0 KEYMAP") || !tt.HasText("Keymap Inspector & Shortcut Guide") {
		t.Fatalf("expected Tier-0 Keymap Inspector modal, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 2. Switch scope filter to Insert (Emacs) -> shows C-n / edit.next-line!
	if err := tt.Click("Insert (Emacs)"); err != nil {
		t.Fatalf("click Insert (Emacs) filter failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("edit.next-line") || !tt.HasText("C-n") {
		t.Fatalf("expected Emacs Insert bindings under Insert filter, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 3. Search for "sidebar" under All -> 1-click "Run" toggles the sidebar and closes inspector!
	if err := tt.Click("All"); err != nil {
		t.Fatalf("click All filter failed: %v", err)
	}
	app.KeymapInspector.SetQuery("sidebar")
	tt.Frame()
	if !tt.HasText("SPC w B") || !tt.HasText("studio.toggle-sidebar") {
		t.Fatalf("expected sidebar binding in filtered inspector, got %q", strings.Join(tt.Texts(), " | "))
	}
	if app.IsSidebarCollapsed() {
		t.Fatal("expected sidebar initially expanded")
	}
	if err := tt.Click("Run"); err != nil {
		t.Fatalf("click Run on sidebar binding failed: %v", err)
	}
	tt.Frame()
	if !app.IsSidebarCollapsed() || app.IsKeymapInspectorOpen() {
		t.Fatalf("expected Run to toggle sidebar and close inspector: collapsed=%v open=%v",
			app.IsSidebarCollapsed(), app.IsKeymapInspectorOpen())
	}

	// 4. Global ESC DismissTopOverlay closes non-gate modals, cancels chords, and NEVER bypasses Tier-0 Gates!
	app.OpenKeymapInspector()
	if !app.DismissTopOverlay() || app.IsKeymapInspectorOpen() {
		t.Fatal("expected ESC DismissTopOverlay to close Keymap Inspector")
	}

	app.HandleActiveKeyToken("SPC")
	if !app.DismissTopOverlay() {
		t.Fatal("expected ESC DismissTopOverlay to cancel active SPC chord")
	}

	app.ShowPendingGate(mygoshell.PendingGateView{
		GateID:   "gate-esc-guard",
		ToolName: "shell.exec",
		Prompt:   "Confirm destructive action?",
	})
	if app.DismissTopOverlay() {
		t.Fatal("SECURITY VIOLATION: global ESC MUST NOT silently bypass a Tier-0 Approval Gate")
	}
}
