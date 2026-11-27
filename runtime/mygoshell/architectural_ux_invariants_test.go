// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestTranscriptThinkingCollapseAndStreamingZeroDriftInvariants(t *testing.T) {
	tc := NewTranscriptController()

	// Build 120 rows including a large expanded DeepSeek Thinking block at row 5.
	for i := 0; i < 5; i++ {
		tc.AppendUser(fmt.Sprintf("Prompt %d", i))
	}
	hugeThought := strings.Repeat("Inspecting graph receipts and proving layout invariants.\n", 40)
	thinkRow := tc.AppendThinking("Deep Reasoning", hugeThought, false)
	for i := 0; i < 114; i++ {
		tc.AppendUser(fmt.Sprintf("History message #%d", i))
	}

	// Reader scrolls up to read row 80 (strictly below thinkRow=5).
	tc.JumpToRow(80)
	if tc.AutoFollow() {
		t.Fatal("expected AutoFollow=false after scrolling into history")
	}
	firstBefore, _, offBefore := tc.VisibleRange()
	if firstBefore != 80 || offBefore != 0 {
		t.Fatalf("expected viewport anchored cleanly at row 80 (+0px), got row=%d off=%d", firstBefore, offBefore)
	}

	// 1. Collapse the huge Thinking block at row 5 (above viewport) -> reader at row 80 MUST experience 0px drift!
	tc.ToggleFold(thinkRow)
	firstAfterFold, _, offAfterFold := tc.VisibleRange()
	if firstAfterFold != 80 || offAfterFold != 0 {
		t.Fatalf("Thinking collapse caused scroll jump: expected row=80 off=0, got row=%d off=%d", firstAfterFold, offAfterFold)
	}

	// 2. Stream 100 assistant deltas at the tail while user is reading history -> MUST NOT snap away from row 80!
	for i := 0; i < 100; i++ {
		tc.ApplyCheckpoint(99, fmt.Sprintf("ev-%d", i), "streaming token line\n")
	}
	firstAfterStream, _, offAfterStream := tc.VisibleRange()
	if firstAfterStream != 80 || offAfterStream != 0 || tc.AutoFollow() {
		t.Fatalf("tail streaming broke reader anchor: row=%d off=%d autoFollow=%v",
			firstAfterStream, offAfterStream, tc.AutoFollow())
	}

	// 3. Dynamic pane width resize (960px -> 360px -> 960px) adapts wrapCols with 0px anchor drift!
	if !tc.SyncWrapWidth(360) {
		t.Fatal("expected SyncWrapWidth(360) to update narrow wrapCols")
	}
	if tc.WrapCols() >= 72 {
		t.Fatalf("expected narrow pane wrapCols < 72, got %d", tc.WrapCols())
	}
	firstAfterResize, _, _ := tc.VisibleRange()
	if firstAfterResize != 80 {
		t.Fatalf("pane width resize drifted reader from row 80 to row %d", firstAfterResize)
	}

	// 4. Backend search JumpToRow(thinkRow) unfolds the collapsed Thinking block and lands directly on it!
	if !tc.JumpToRow(thinkRow) {
		t.Fatal("JumpToRow(thinkRow) failed")
	}
	entry, ok := tc.Entry(thinkRow)
	if !ok || entry.Folded {
		t.Fatalf("expected JumpToRow to unfold collapsed Thinking row, got folded=%v", entry.Folded)
	}
	firstJumped, _, _ := tc.VisibleRange()
	if firstJumped != thinkRow {
		t.Fatalf("expected viewport landed on thinkRow=%d, got %d", thinkRow, firstJumped)
	}

	// 5. Re-engaging ScrollToBottom locks AutoFollow=true and survives continuous streaming deltas!
	tc.ScrollToBottom()
	for i := 100; i < 140; i++ {
		tc.ApplyCheckpoint(99, fmt.Sprintf("ev-%d", i), "more live tokens\n")
	}
	if !tc.AutoFollow() {
		t.Fatal("streaming deltas must never falsely disengage AutoFollow")
	}
}

func TestWorkbenchCollapsibleSidebarViaButtonAndChord(t *testing.T) {
	reg := transport.NewTargetRegistry()
	_ = reg.Register(&projectionTestTarget{meta: transport.TargetMeta{
		ID: "local", DisplayName: "Local Mac", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})
	slots := shell.NewSlotRegistry()
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle, FallbackRenderer: "main.fb",
	}, NewUIElementRenderer("main.fb", true, slots, func() schema.UIElement {
		return schema.UIElement{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Workspace Canvas"}}}
	}))
	wb := shell.NewWorkbenchController(slots)
	_ = wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation: 1, ProfileName: "sidebar-test", Theme: StudioDarkPalette(),
		FrameTree: schema.UIElement{Kind: schema.ElemSlot, SlotID: "workbench.main"},
	})

	app := NewWorkbenchApp(reg, slots, wb)
	app.BrandTitle = "Marlin Sidebar Brand"
	if err := app.EnableEmacsWindowKeymap(); err != nil {
		t.Fatalf("EnableEmacsWindowKeymap: %v", err)
	}

	tt := ui.NewTester(app.View, 1280, 860)
	if !tt.HasText("Marlin Sidebar Brand") {
		t.Fatalf("expected expanded sidebar brand initially, texts=%q", tt.Texts())
	}

	// 1. Click "Sidebar" pill in top bar -> collapses sidebar cleanly.
	if err := tt.Click("Sidebar"); err != nil {
		t.Fatalf("click Sidebar failed: %v", err)
	}
	tt.Frame()
	if !app.IsSidebarCollapsed() || tt.HasText("Marlin Sidebar Brand") {
		t.Fatalf("expected sidebar collapsed after click, collapsed=%v texts=%q", app.IsSidebarCollapsed(), tt.Texts())
	}

	// 2. Press SPC w B chord -> restores sidebar cleanly!
	app.HandleActiveKeyToken("SPC")
	app.HandleActiveKeyToken("w")
	app.HandleActiveKeyToken("B")
	tt.Frame()
	if app.IsSidebarCollapsed() || !tt.HasText("Marlin Sidebar Brand") {
		t.Fatalf("expected sidebar expanded after SPC w B, collapsed=%v texts=%q", app.IsSidebarCollapsed(), tt.Texts())
	}
}
