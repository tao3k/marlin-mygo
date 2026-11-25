// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestDualModalWhichKeyHUDAndEmacsEditStrip(t *testing.T) {
	reg := transport.NewTargetRegistry()
	_ = reg.Register(&projectionTestTarget{meta: transport.TargetMeta{
		ID: "local", DisplayName: "Local", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})
	slots := shell.NewSlotRegistry()
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle, FallbackRenderer: "main.fb",
	}, NewUIElementRenderer("main.fb", true, slots, func() schema.UIElement {
		return schema.UIElement{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Host Fallback"}}}
	}))
	wb := shell.NewWorkbenchController(slots)
	_ = wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation: 1, ProfileName: "whichkey-test", Theme: StudioDarkPalette(),
		FrameTree: schema.UIElement{Kind: schema.ElemSlot, SlotID: "workbench.main"},
	})

	bufMain := shell.PaneBuffer{BufferID: "slot:workbench.main", Title: "Workspace Panel", Kind: "webview"}
	tree := shell.NewWindowTree(bufMain)

	app := NewWorkbenchApp(reg, slots, wb)
	app.Windows = tree
	app.WindowBuffers = []shell.PaneBuffer{bufMain}
	if err := app.EnableEmacsWindowKeymap(); err != nil {
		t.Fatalf("EnableEmacsWindowKeymap: %v", err)
	}

	tt := ui.NewTester(app.View, 1280, 860)

	// 1. Pressing SPC opens live Which-Key HUD with prefix groups (+window, +perspective, +theme).
	kind, prefix := app.HandleActiveKeyToken("SPC")
	if kind != input.MatchPrefix || prefix != "SPC" {
		t.Fatalf("expected MatchPrefix SPC, got %v %q", kind, prefix)
	}
	tt.Frame()
	if !tt.HasText("WHICH-KEY · SPC …") || !tt.HasText("[w] +window (17)") {
		t.Fatalf("expected SPC Which-Key HUD with +window hints, got texts=%q", tt.Texts())
	}

	// 2. Clicking [w] +window (17) drills down into window split chords, then [v] splits right!
	if err := tt.Click("[w] +window (17)"); err != nil {
		t.Fatalf("click [w] +window (17) failed: %v", err)
	}
	if !tt.HasText("WHICH-KEY · SPC w …") {
		t.Fatalf("expected SPC w Which-Key HUD, got texts=%q", tt.Texts())
	}
	app.HandleActiveKeyToken("v")
	if tree.Snapshot().LeafCount != 2 {
		t.Fatalf("expected 2 leaves after SPC w v, got %d", tree.Snapshot().LeafCount)
	}

	// 3. Pressing 'i' switches to Emacs Insert/Edit mode and displays the Emacs C-n/C-p/C-k/C-y HUD bar.
	app.HandleActiveKeyToken("i")
	if app.ActiveKeyMode() != input.ScopeInsert {
		t.Fatalf("expected ScopeInsert after 'i', got %v", app.ActiveKeyMode())
	}
	tt.Frame()
	if !tt.HasText("EMACS EDIT · INSERT") || !tt.HasText("C-n next") {
		t.Fatalf("expected Emacs Edit HUD bar in Insert mode, got texts=%q", tt.Texts())
	}

	// 4. ESC returns to Normal mode; SPC ? opens full dual-layout cheatsheet.
	app.HandleActiveKeyToken("ESC")
	if app.ActiveKeyMode() != input.ScopeNormal {
		t.Fatalf("expected ScopeNormal after ESC, got %v", app.ActiveKeyMode())
	}
	app.HandleActiveKeyToken("SPC")
	app.HandleActiveKeyToken("?")
	tt.Frame()
	if !tt.HasText("DUAL KEYMAP CHEATSHEET") {
		t.Fatalf("expected DUAL KEYMAP CHEATSHEET after SPC ?, got texts=%q", tt.Texts())
	}
}
