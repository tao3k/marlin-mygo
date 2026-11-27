// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package shell

import (
	"errors"
	"testing"
)

func TestWindowTreeEmacsSplitFocusZoomBalanceAndDelete(t *testing.T) {
	bufMain := PaneBuffer{BufferID: "slot:workbench.main", Title: "Agenda", Kind: "webview"}
	bufChat := PaneBuffer{BufferID: "builtin:transcript", Title: "AI Transcript", Kind: "native-gpu"}
	bufProj := PaneBuffer{BufferID: "builtin:projection", Title: "Graph & Diff", Kind: "native-gpu"}
	bufDock := PaneBuffer{BufferID: "slot:workbench.inspector", Title: "Focus Clock", Kind: "wasm-gpu"}

	tree := NewWindowTree(bufMain)
	if err := tree.DeleteFocused(); !errors.Is(err, ErrLastWindow) {
		t.Fatalf("expected ErrLastWindow on single window delete, got %v", err)
	}

	w2, err := tree.SplitRight(bufChat)
	if err != nil {
		t.Fatalf("SplitRight failed: %v", err)
	}
	w3, err := tree.SplitBelow(bufProj)
	if err != nil {
		t.Fatalf("SplitBelow failed: %v", err)
	}

	snap := tree.Snapshot()
	if snap.LeafCount != 3 || snap.FocusedID != w3 {
		t.Fatalf("expected 3 leaves focused on %q, got count=%d focused=%q", w3, snap.LeafCount, snap.FocusedID)
	}

	// other-window cycles across all 3 leaves deterministically.
	next1 := tree.FocusNext()
	next2 := tree.FocusNext()
	next3 := tree.FocusNext()
	if next1 == w3 || next2 == next1 || next3 != w3 {
		t.Fatalf("unexpected FocusNext cycle: %q -> %q -> %q (start %q)", next1, next2, next3, w3)
	}

	// Zoom toggles cleanly without destroying split tree.
	if !tree.ToggleZoom() || !tree.Snapshot().Zoomed {
		t.Fatalf("expected ToggleZoom to enable zoom")
	}
	if tree.ToggleZoom() || tree.Snapshot().Zoomed {
		t.Fatalf("expected second ToggleZoom to restore split layout")
	}

	// Balance resets all ratios to 0.50.
	tree.Balance()
	balSnap := tree.Snapshot()
	if balSnap.Root.Ratio != 0.50 || balSnap.Root.Second.Ratio != 0.50 {
		t.Fatalf("expected balanced 0.50 ratios, got root=%.2f child=%.2f", balSnap.Root.Ratio, balSnap.Root.Second.Ratio)
	}

	// Delete middle pane promotes remaining sibling cleanly.
	if err := tree.DeleteWindow(w2); err != nil {
		t.Fatalf("DeleteWindow(%s) failed: %v", w2, err)
	}
	if tree.Snapshot().LeafCount != 2 {
		t.Fatalf("expected 2 leaves after delete, got %d", tree.Snapshot().LeafCount)
	}

	// Built-in perspectives construct deterministic multi-pane layouts.
	tree.ApplyBuiltinPerspective("cockpit", bufMain, bufDock, bufChat, bufProj)
	cockpit := tree.Snapshot()
	if cockpit.Perspective != "cockpit" || cockpit.LeafCount != 3 {
		t.Fatalf("expected cockpit 3-pane perspective, got %+v", cockpit)
	}
}
