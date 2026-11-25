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

func TestPaneMultiTabStripHibernationAndMinibufferPickerUI(t *testing.T) {
	reg := transport.NewTargetRegistry()
	_ = reg.Register(&projectionTestTarget{meta: transport.TargetMeta{
		ID: "local", DisplayName: "Local", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})
	slots := shell.NewSlotRegistry()
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle, FallbackRenderer: "main.fb",
	}, NewUIElementRenderer("main.fb", true, slots, func() schema.UIElement {
		return schema.UIElement{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Main Tab Body"}}}
	}))
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.inspector", Cardinality: schema.SlotSingle, FallbackRenderer: "dock.fb",
	}, NewUIElementRenderer("dock.fb", true, slots, func() schema.UIElement {
		return schema.UIElement{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Inspector Tab Body"}}}
	}))
	wb := shell.NewWorkbenchController(slots)
	_ = wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation: 1, ProfileName: "tabs-test", Theme: StudioDarkPalette(),
		FrameTree: schema.UIElement{Kind: schema.ElemSlot, SlotID: "workbench.main"},
	})

	bufMain := shell.PaneBuffer{BufferID: "slot:workbench.main", Title: "Workspace", Kind: "webview"}
	bufDock := shell.PaneBuffer{BufferID: "slot:workbench.inspector", Title: "Focus Clock", Kind: "wasm-gpu"}
	bufChat := shell.PaneBuffer{BufferID: "builtin:transcript", Title: "AI Transcript", Kind: "native-gpu"}

	tree := shell.NewWindowTree(bufMain)
	tree.SetTabPolicy(shell.TabBudgetPolicy{MaxTabsPerPane: 16, MaxWarmTabs: 1})

	app := NewWorkbenchApp(reg, slots, wb)
	app.Transcript = NewTranscriptController()
	app.Windows = tree
	app.WindowBuffers = []shell.PaneBuffer{bufMain, bufDock, bufChat}
	if err := app.EnableEmacsWindowKeymap(); err != nil {
		t.Fatalf("EnableEmacsWindowKeymap: %v", err)
	}

	tt := ui.NewTester(app.View, 1280, 860)

	// 1. Stack two more tabs in the focused pane via "+ Tab" -> triggers MaxWarmTabs=1 LRU hibernation of Tab 0 ("Workspace")!
	if err := tt.Click("+ Tab"); err != nil {
		t.Fatalf("click + Tab #1 failed: %v", err)
	}
	if err := tt.Click("+ Tab"); err != nil {
		t.Fatalf("click + Tab #2 failed: %v", err)
	}
	tt.Frame()

	snap := tree.Snapshot()
	if snap.TotalTabs != 3 || snap.HibernatedTabs != 1 {
		t.Fatalf("expected 3 stacked tabs with 1 LRU-hibernated, got %+v", snap)
	}
	// Background tabs must NOT paint their bodies (only active AI Transcript renders, not Main/Inspector bodies)!
	if tt.HasText("Main Tab Body") || tt.HasText("Inspector Tab Body") {
		t.Fatalf("hibernated/paused background tabs must do 0 body GPU paint, texts=%q", tt.Texts())
	}
	if !tt.HasText("☾ Workspace") {
		t.Fatalf("expected hibernated moon glyph '☾ Workspace' in pane tab strip, texts=%q", tt.Texts())
	}

	// 2. Open Emacs Minibuffer Buffer/Tab Picker via SPC b b -> verify HIBERNATED · 0MB badge & click to wake!
	app.HandleActiveKeyToken("SPC")
	app.HandleActiveKeyToken("b")
	app.HandleActiveKeyToken("b")
	tt.Frame()
	if !tt.HasText("Buffer & Tab Minibuffer (SPC b b)") || !tt.HasText("HIBERNATED · 0MB") {
		t.Fatalf("expected Minibuffer Picker with HIBERNATED · 0MB badge, texts=%q", tt.Texts())
	}

	if err := tt.Click("Workspace [win-1]"); err != nil {
		t.Fatalf("click hibernated Workspace tab in Minibuffer failed: %v", err)
	}
	tt.Frame()

	// Workspace tab woke transparently and now paints its body!
	if !tt.HasText("Main Tab Body") {
		t.Fatalf("expected woken Workspace tab to render Main Tab Body, texts=%q", tt.Texts())
	}
	if tree.Snapshot().Root.Tabs[0].WakeCount != 1 {
		t.Fatalf("expected Workspace tab WakeCount=1 after waking from Minibuffer, got %d", tree.Snapshot().Root.Tabs[0].WakeCount)
	}
}
