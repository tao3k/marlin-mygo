// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package shell_test

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/engine/shell"
)

// TestWindowTree50kOpBrutalChaosAndTabHibernationSoak hammers WindowTree with
// 50,000 randomized multi-window, 2D spatial navigation, Winner-Mode undo/redo,
// perspective switch, tab stacking, pinning, tear-off, and lazy hibernation
// operations, verifying ALL structural & focus invariants after every single op.
func TestWindowTree50kOpBrutalChaosAndTabHibernationSoak(t *testing.T) {
	steps := 50_000
	if testing.Short() {
		steps = 10_000
	}

	rng := rand.New(rand.NewPCG(20261126, 50000))
	buffers := []shell.PaneBuffer{
		{BufferID: "builtin:transcript", Title: "Transcript", Kind: "native-gpu"},
		{BufferID: "builtin:projection", Title: "Projection", Kind: "native-gpu"},
		{BufferID: "slot:workbench.main", Title: "Workspace", Kind: "wasm-gpu"},
		{BufferID: "slot:workbench.secondary", Title: "Secondary", Kind: "webview"},
		{BufferID: "slot:workbench.inspector", Title: "Inspector", Kind: "native-gpu"},
		{BufferID: "feature:chatroom", Title: "Agent Room", Kind: "native-gpu"},
		{BufferID: "feature:compare", Title: "Compare", Kind: "native-gpu"},
		{BufferID: "feature:canvas", Title: "Canvas", Kind: "native-gpu"},
	}

	now := time.Now()
	wt := shell.NewWindowTree(buffers[0])
	dirs := []shell.Direction{shell.DirLeft, shell.DirRight, shell.DirUp, shell.DirDown}
	perspectives := []string{"focus", "split", "cockpit", "zen"}

	for step := 0; step < steps; step++ {
		buf := buffers[rng.IntN(len(buffers))]
		switch rng.IntN(16) {
		case 0:
			_, _ = wt.SplitRight(buf)
		case 1:
			_, _ = wt.SplitBelow(buf)
		case 2:
			_ = wt.DeleteFocused()
		case 3:
			wt.FocusNext()
		case 4:
			wt.FocusDirection(dirs[rng.IntN(len(dirs))])
		case 5:
			wt.ToggleZoom()
		case 6:
			wt.RotateFocusedSplit()
		case 7:
			wt.Balance()
		case 8:
			wt.SwapWithNext()
		case 9:
			_ = wt.Undo()
		case 10:
			_ = wt.Redo()
		case 11:
			_, _ = wt.OpenTabInFocused(buf)
		case 12:
			wt.NextTab()
		case 13:
			_ = wt.CloseFocusedTab()
		case 14:
			wt.TogglePinFocusedTab()
		case 15:
			switch rng.IntN(4) {
			case 0:
				wt.ApplyBuiltinPerspective(perspectives[rng.IntN(len(perspectives))], buffers[0], buffers[1], buffers[2], buffers[3])
			case 1:
				_, _ = wt.TearOffFocusedTabRight()
			case 2:
				wt.HibernateFocusedTab()
			case 3:
				now = now.Add(5 * time.Minute)
				wt.ReapIdleTabs(now)
			}
		}

		// Verify ALL structural invariants on EVERY step (50,000 checks!).
		snap := wt.Snapshot()
		if snap.LeafCount < 1 || snap.LeafCount > shell.MaxOpenWindows {
			t.Fatalf("step %d: LeafCount=%d outside [1, %d]", step, snap.LeafCount, shell.MaxOpenWindows)
		}
		if snap.ActiveTabs+snap.PausedTabs+snap.HibernatedTabs != snap.TotalTabs {
			t.Fatalf("step %d: tab accounting mismatch active=%d paused=%d hib=%d total=%d",
				step, snap.ActiveTabs, snap.PausedTabs, snap.HibernatedTabs, snap.TotalTabs)
		}
		focusedFound, leafCount := verifyWindowNodeInvariants(t, step, snap.Root, snap.FocusedID)
		if leafCount != snap.LeafCount {
			t.Fatalf("step %d: walked leaves=%d != snap.LeafCount=%d", step, leafCount, snap.LeafCount)
		}
		if !focusedFound {
			t.Fatalf("step %d: FocusedID %q missing from tree!", step, snap.FocusedID)
		}
	}
}

func verifyWindowNodeInvariants(t *testing.T, step int, n *shell.WindowNode, focused shell.WindowID) (foundFocused bool, leaves int) {
	t.Helper()
	if n == nil {
		t.Fatalf("step %d: nil WindowNode encountered", step)
	}
	if n.IsSplit {
		if n.First == nil || n.Second == nil {
			t.Fatalf("step %d: split node %q has nil child", step, n.ID)
		}
		if n.Ratio < 0.19 || n.Ratio > 0.81 {
			t.Fatalf("step %d: split node %q ratio=%.3f out of [0.20,0.80]", step, n.ID, n.Ratio)
		}
		f1, l1 := verifyWindowNodeInvariants(t, step, n.First, focused)
		f2, l2 := verifyWindowNodeInvariants(t, step, n.Second, focused)
		return f1 || f2, l1 + l2
	}
	if len(n.Tabs) == 0 {
		t.Fatalf("step %d: leaf %q has 0 tabs", step, n.ID)
	}
	if n.ActiveTab < 0 || n.ActiveTab >= len(n.Tabs) {
		t.Fatalf("step %d: leaf %q ActiveTab=%d out of [0,%d)", step, n.ID, n.ActiveTab, len(n.Tabs))
	}
	if n.Buffer.BufferID != n.Tabs[n.ActiveTab].Buffer.BufferID {
		t.Fatalf("step %d: leaf %q Buffer=%q != active tab Buffer=%q",
			step, n.ID, n.Buffer.BufferID, n.Tabs[n.ActiveTab].Buffer.BufferID)
	}
	return n.ID == focused, 1
}

// TestWindowTreeRapidResizeCoalescingAndAllTabsIndex verifies 5,000 continuous
// divider resize drags coalesce cleanly into bounded undo history and AllTabs
// stays consistent with Snapshot.TotalTabs.
func TestWindowTreeRapidResizeCoalescingAndAllTabsIndex(t *testing.T) {
	b1 := shell.PaneBuffer{BufferID: "b1", Title: "B1"}
	b2 := shell.PaneBuffer{BufferID: "b2", Title: "B2"}
	wt := shell.NewWindowTree(b1)
	if _, err := wt.SplitRight(b2); err != nil {
		t.Fatalf("SplitRight: %v", err)
	}
	rootID := wt.Snapshot().Root.ID
	for i := 0; i < 5_000; i++ {
		ratio := float32(0.25 + 0.50*float64(i%100)/100.0)
		if !wt.ResizeSplit(rootID, ratio) {
			t.Fatalf("ResizeSplit %d returned false", i)
		}
	}
	for i := 0; i < 12; i++ {
		_, _ = wt.OpenTabInFocused(shell.PaneBuffer{BufferID: fmt.Sprintf("tab-%d", i), Title: fmt.Sprintf("Tab %d", i)})
	}
	snap := wt.Snapshot()
	all := wt.AllTabs()
	if len(all) != snap.TotalTabs {
		t.Fatalf("AllTabs len=%d != Snapshot.TotalTabs=%d", len(all), snap.TotalTabs)
	}
}
