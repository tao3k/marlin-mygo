// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
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

	// Split vertically via Emacs window header control "|" -> 4 panes.
	tt.Click("|")
	tt.Frame()
	if tree.Snapshot().LeafCount != 4 {
		t.Fatalf("expected 4 panes after clicking '|', got %d", tree.Snapshot().LeafCount)
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
}
