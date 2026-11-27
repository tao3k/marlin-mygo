// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package shell

import (
	"math"
	"testing"

	"github.com/tao3k/marlin-mygo/engine/input"
)

func TestWindowTreeZeroAllocSnapshotAt120Hz(t *testing.T) {
	b1 := PaneBuffer{BufferID: "slot:workbench.main", Title: "Main", Kind: "webview"}
	b2 := PaneBuffer{BufferID: "builtin:transcript", Title: "Chat", Kind: "native-gpu"}
	b3 := PaneBuffer{BufferID: "slot:workbench.inspector", Title: "Dock", Kind: "wasm-gpu"}

	wt := NewWindowTree(b1)
	wt.ApplyBuiltinPerspective("cockpit", b1, b3, b2, b3)

	// Warm one snapshot, then assert steady-state 120Hz Snapshot() does ZERO heap allocations.
	_ = wt.Snapshot()
	allocs := testing.AllocsPerRun(500, func() {
		snap := wt.Snapshot()
		if snap.LeafCount != 3 {
			t.Fatalf("expected 3 leaves, got %d", snap.LeafCount)
		}
	})
	if allocs > 0 {
		t.Fatalf("expected 0 allocs/op for steady-state 120Hz WindowTree.Snapshot(), got %.2f", allocs)
	}
}

func TestWindowTreeWinnerModeUndoRedoAndDragCoalescing(t *testing.T) {
	b1 := PaneBuffer{BufferID: "slot:workbench.main", Title: "Main", Kind: "webview"}
	b2 := PaneBuffer{BufferID: "builtin:transcript", Title: "Chat", Kind: "native-gpu"}
	wt := NewWindowTree(b1)

	if wt.Snapshot().CanUndo || wt.Snapshot().CanRedo {
		t.Fatal("fresh WindowTree must have empty undo/redo stacks")
	}
	if _, err := wt.SplitRight(b2); err != nil {
		t.Fatalf("SplitRight: %v", err)
	}
	splitID := wt.Snapshot().Root.ID
	initialRatio := wt.Snapshot().Root.Ratio

	// Simulate 40 continuous 120Hz divider drag frames -> must coalesce into ONE undo step!
	for i := 1; i <= 40; i++ {
		wt.ResizeSplit(splitID, initialRatio+float32(i)*0.004)
	}
	draggedRatio := wt.Snapshot().Root.Ratio
	if math.Abs(float64(draggedRatio-initialRatio)) < 0.05 {
		t.Fatalf("expected ratio to move during drag, initial=%.3f dragged=%.3f", initialRatio, draggedRatio)
	}

	// One Undo restores pre-drag ratio cleanly (proving 40 drag frames coalesced into 1 step).
	if !wt.Undo() {
		t.Fatal("expected Undo to revert coalesced drag")
	}
	if math.Abs(float64(wt.Snapshot().Root.Ratio-initialRatio)) > 0.001 {
		t.Fatalf("expected Undo to restore initial ratio %.3f, got %.3f", initialRatio, wt.Snapshot().Root.Ratio)
	}

	// Second Undo reverts the split back to 1 pane!
	if !wt.Undo() {
		t.Fatal("expected second Undo to revert split")
	}
	if wt.Snapshot().LeafCount != 1 {
		t.Fatalf("expected 1 leaf after undoing split, got %d", wt.Snapshot().LeafCount)
	}

	// Redo twice brings back both the split and the dragged ratio!
	if !wt.Redo() || wt.Snapshot().LeafCount != 2 {
		t.Fatalf("expected Redo #1 to restore 2 leaves, got %d", wt.Snapshot().LeafCount)
	}
	if !wt.Redo() || math.Abs(float64(wt.Snapshot().Root.Ratio-draggedRatio)) > 0.001 {
		t.Fatalf("expected Redo #2 to restore dragged ratio %.3f, got %.3f", draggedRatio, wt.Snapshot().Root.Ratio)
	}
}

func TestWindowTree2DSpatialNavigationAndResizeRotate(t *testing.T) {
	// Build a 2x2 grid:
	//   [ TL (win-1) | TR (win-2) ]
	//   [ BL (win-3) | BR (win-4) ]
	wt := NewWindowTree(PaneBuffer{BufferID: "tl", Title: "TL"})
	spec := LayoutSpec{
		Perspective: "grid-2x2",
		Root: &LayoutNodeSpec{
			Axis:  SplitRow,
			Ratio: 0.50,
			First: &LayoutNodeSpec{
				Axis:   SplitColumn,
				Ratio:  0.50,
				First:  &LayoutNodeSpec{BufferID: "tl", Title: "TL"},
				Second: &LayoutNodeSpec{BufferID: "bl", Title: "BL"},
			},
			Second: &LayoutNodeSpec{
				Axis:   SplitColumn,
				Ratio:  0.50,
				First:  &LayoutNodeSpec{BufferID: "tr", Title: "TR"},
				Second: &LayoutNodeSpec{BufferID: "br", Title: "BR"},
			},
		},
	}
	if err := ApplyLayoutSpec(wt, spec, nil); err != nil {
		t.Fatalf("ApplyLayoutSpec: %v", err)
	}

	assertFocusedBuf := func(want string) {
		t.Helper()
		_, buf := wt.FocusedBuffer()
		if buf.BufferID != want {
			t.Fatalf("focused buffer = %q, want %q", buf.BufferID, want)
		}
	}

	assertFocusedBuf("tl")
	wt.FocusDirection(DirRight)
	assertFocusedBuf("tr")
	wt.FocusDirection(DirDown)
	assertFocusedBuf("br")
	wt.FocusDirection(DirLeft)
	assertFocusedBuf("bl")
	wt.FocusDirection(DirUp)
	assertFocusedBuf("tl")

	// Keyboard widen/shrink + rotate axis via ExecuteWindowCommand.
	kt, err := CompileDualModalKeymap()
	if err != nil {
		t.Fatalf("CompileDualModalKeymap: %v", err)
	}
	for _, tc := range []struct {
		seq  []string
		want string
	}{
		{[]string{"SPC", "w", "h"}, CmdWindowFocusLeft},
		{[]string{"SPC", "w", "j"}, CmdWindowFocusDown},
		{[]string{"SPC", "w", "k"}, CmdWindowFocusUp},
		{[]string{"SPC", "w", "l"}, CmdWindowFocusRight},
		{[]string{"SPC", "w", ">"}, CmdWindowWiden},
		{[]string{"SPC", "w", "<"}, CmdWindowShrink},
		{[]string{"SPC", "w", "R"}, CmdWindowRotate},
		{[]string{"SPC", "w", "u"}, CmdWindowUndo},
		{[]string{"SPC", "w", "r"}, CmdWindowRedo},
		{[]string{"C-x", "u"}, CmdWindowUndo},
	} {
		kind, b := kt.Lookup(input.ScopeNormal, tc.seq)
		if kind != input.MatchExact || b.CommandName != tc.want {
			t.Fatalf("Lookup(%v) = (%v, %q), want (MatchExact, %q)", tc.seq, kind, b.CommandName, tc.want)
		}
	}

	if handled, err := ExecuteWindowCommand(wt, CmdWindowWiden, nil); !handled || err != nil {
		t.Fatalf("ExecuteWindowCommand(widen) failed: handled=%v err=%v", handled, err)
	}
	if handled, err := ExecuteWindowCommand(wt, CmdWindowRotate, nil); !handled || err != nil {
		t.Fatalf("ExecuteWindowCommand(rotate) failed: handled=%v err=%v", handled, err)
	}
	if wt.Snapshot().Root.First.Axis != SplitRow {
		t.Fatalf("expected left column to rotate to SplitRow, got %q", wt.Snapshot().Root.First.Axis)
	}
}
