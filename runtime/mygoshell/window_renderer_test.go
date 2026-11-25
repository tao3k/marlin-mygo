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

func TestWorkbenchAppEmacsWindowSplitsPerspectivesAndThemeCycling(t *testing.T) {
	reg := transport.NewTargetRegistry()
	_ = reg.Register(&projectionTestTarget{meta: transport.TargetMeta{
		ID: "local", DisplayName: "Local", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})
	slots := shell.NewSlotRegistry()
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle, FallbackRenderer: "main.fb",
	}, NewUIElementRenderer("main.fb", true, slots, func() schema.UIElement {
		return schema.UIElement{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Main Workspace Pane"}}}
	}))
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.inspector", Cardinality: schema.SlotSingle, FallbackRenderer: "dock.fb",
	}, NewUIElementRenderer("dock.fb", true, slots, func() schema.UIElement {
		return schema.UIElement{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Focus Dock Pane"}}}
	}))

	wb := shell.NewWorkbenchController(slots)
	curPreset := ThemeByID("midnight")
	gen := uint64(1)
	_ = wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation:  gen,
		ProfileName: "emacs-studio",
		Theme:       curPreset.Base,
		FrameTree:   schema.UIElement{Kind: schema.ElemSlot, SlotID: "workbench.main"},
	})

	bufMain := shell.PaneBuffer{BufferID: "slot:workbench.main", Title: "Agenda", Kind: "webview"}
	bufDock := shell.PaneBuffer{BufferID: "slot:workbench.inspector", Title: "Focus Clock", Kind: "wasm-gpu"}
	bufChat := shell.PaneBuffer{BufferID: "builtin:transcript", Title: "AI Transcript", Kind: "native-gpu"}
	bufProj := shell.PaneBuffer{BufferID: "builtin:projection", Title: "Projection", Kind: "native-gpu"}

	tree := shell.NewWindowTree(bufMain)
	tree.ApplyBuiltinPerspective("focus", bufMain, bufDock, bufChat, bufProj)

	app := NewWorkbenchApp(reg, slots, wb)
	app.Transcript = NewTranscriptController()
	app.Projection = NewProjectionController(ProjectionSnapshot{})
	app.Windows = tree
	app.WindowBuffers = []shell.PaneBuffer{bufMain, bufChat, bufProj, bufDock}
	app.OnSelectPerspective = func(name string) {
		tree.ApplyBuiltinPerspective(name, bufMain, bufDock, bufChat, bufProj)
	}
	app.ActiveThemeNameFn = func() string { return curPreset.Name }
	app.OnCycleTheme = func() {
		curPreset = NextThemePreset(curPreset.ID)
		gen++
		_ = wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
			Generation:  gen,
			ProfileName: "emacs-studio",
			Theme:       curPreset.Base,
			FrameTree:   schema.UIElement{Kind: schema.ElemSlot, SlotID: "workbench.main"},
		})
	}

	tt := ui.NewTester(app.View, 1320, 840)
	if !tt.HasText("Main Workspace Pane") || !tt.HasText("Focus Dock Pane") {
		t.Fatalf("expected Focus perspective to show main + dock panes, got %q", tt.Texts())
	}

	// Switch to 3-pane Cockpit perspective.
	tt.Click("Cockpit")
	tt.Frame()
	if tree.Snapshot().LeafCount != 3 || tree.Snapshot().Perspective != "cockpit" {
		t.Fatalf("expected 3-pane cockpit perspective, got %+v", tree.Snapshot())
	}

	// Split vertically via SF-Symbols vector header control "Split Right" -> 4 panes,
	// then test Winner-Mode Undo Layout -> 3 panes and Redo Layout -> 4 panes.
	if err := tt.Click("Split Right"); err != nil {
		t.Fatalf("click Split Right failed: %v", err)
	}
	tt.Frame()
	if tree.Snapshot().LeafCount != 4 {
		t.Fatalf("expected 4 panes after clicking 'Split Right', got %d", tree.Snapshot().LeafCount)
	}
	if err := tt.Click("Undo Layout"); err != nil {
		t.Fatalf("click Undo Layout failed: %v", err)
	}
	tt.Frame()
	if tree.Snapshot().LeafCount != 3 {
		t.Fatalf("expected 3 panes after Winner-Mode Undo Layout, got %d", tree.Snapshot().LeafCount)
	}
	if err := tt.Click("Redo Layout"); err != nil {
		t.Fatalf("click Redo Layout failed: %v", err)
	}
	tt.Frame()
	if tree.Snapshot().LeafCount != 4 {
		t.Fatalf("expected 4 panes after Winner-Mode Redo Layout, got %d", tree.Snapshot().LeafCount)
	}

	// Cycle curated themes through all 5 presets cleanly.
	tt.Click("Midnight")
	tt.Frame()
	if curPreset.ID != "paper" {
		t.Fatalf("expected theme cycle midnight -> paper, got %q", curPreset.ID)
	}
	tt.Click("Paper Light")
	tt.Frame()
	if curPreset.ID != "doom-one" {
		t.Fatalf("expected theme cycle paper -> doom-one, got %q", curPreset.ID)
	}

	// Verify Emacs chord keymap (SPC w v / C-x 1) + Scheme ApplyLayoutSpec + OnLayoutChanged callback.
	if err := app.EnableEmacsWindowKeymap(); err != nil {
		t.Fatalf("EnableEmacsWindowKeymap: %v", err)
	}
	var lastSnap shell.WindowTreeSnapshot
	app.OnLayoutChanged = func(s shell.WindowTreeSnapshot) { lastSnap = s }

	if err := app.ApplyLayoutSpec(shell.LayoutSpec{
		Perspective: "scheme-duo",
		Root: &shell.LayoutNodeSpec{
			Axis:   shell.SplitRow,
			Ratio:  0.60,
			First:  &shell.LayoutNodeSpec{BufferID: "slot:workbench.main"},
			Second: &shell.LayoutNodeSpec{BufferID: "builtin:transcript"},
		},
	}); err != nil {
		t.Fatalf("ApplyLayoutSpec: %v", err)
	}
	if lastSnap.Perspective != "scheme-duo" || lastSnap.LeafCount != 2 {
		t.Fatalf("expected OnLayoutChanged with scheme-duo (2 panes), got %+v", lastSnap)
	}

	if k, _ := app.HandleKeyToken(input.ScopeNormal, "SPC"); k != input.MatchPrefix {
		t.Fatalf("expected MatchPrefix for SPC, got %v", k)
	}
	if k, _ := app.HandleKeyToken(input.ScopeNormal, "w"); k != input.MatchPrefix {
		t.Fatalf("expected MatchPrefix for SPC w, got %v", k)
	}
	if k, cmd := app.HandleKeyToken(input.ScopeNormal, "v"); k != input.MatchExact || cmd != shell.CmdWindowSplitRight {
		t.Fatalf("expected MatchExact %s, got k=%v cmd=%q", shell.CmdWindowSplitRight, k, cmd)
	}
	if lastSnap.LeafCount != 3 {
		t.Fatalf("expected 3 panes after SPC w v chord, got %d", lastSnap.LeafCount)
	}

	// Verify dual modal keymaps: Normal (SPC) -> Insert (Emacs C-n/C-p) -> Normal (C-g).
	var editCmds []string
	app.OnEditCommand = func(cmd string) { editCmds = append(editCmds, cmd) }
	if app.ActiveKeyMode() != input.ScopeNormal {
		t.Fatalf("expected initial mode ScopeNormal, got %v", app.ActiveKeyMode())
	}
	if k, cmd := app.HandleActiveKeyToken("i"); k != input.MatchExact || cmd != shell.CmdModeInsert {
		t.Fatalf("expected 'i' -> mode.insert, got k=%v cmd=%q", k, cmd)
	}
	if app.ActiveKeyMode() != input.ScopeInsert {
		t.Fatalf("expected mode ScopeInsert after 'i', got %v", app.ActiveKeyMode())
	}
	if k, cmd := app.HandleActiveKeyToken("C-n"); k != input.MatchExact || cmd != shell.CmdEditNextLine {
		t.Fatalf("expected C-n -> edit.next-line in Insert mode, got k=%v cmd=%q", k, cmd)
	}
	if len(editCmds) != 1 || editCmds[0] != shell.CmdEditNextLine {
		t.Fatalf("expected OnEditCommand recorded edit.next-line, got %v", editCmds)
	}
	if k, cmd := app.HandleActiveKeyToken("C-g"); k != input.MatchExact || cmd != shell.CmdModeNormal {
		t.Fatalf("expected C-g -> mode.normal, got k=%v cmd=%q", k, cmd)
	}
	if app.ActiveKeyMode() != input.ScopeNormal {
		t.Fatalf("expected mode ScopeNormal after C-g, got %v", app.ActiveKeyMode())
	}
}
