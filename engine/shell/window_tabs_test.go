// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package shell

import (
	"fmt"
	"testing"
	"time"
)

func TestWindowTabsLazyLifecycleLRUIdleReapPinAndTransparentWake(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	wt := NewWindowTree(PaneBuffer{BufferID: "plugin:tab-0", Title: "Tab 0", Kind: "webview"})
	wt.SetClock(func() time.Time { return now })
	wt.SetTabPolicy(TabBudgetPolicy{
		MaxTabsPerPane:   16,
		MaxWarmTabs:      2,
		IdleHibernateTTL: 5 * time.Minute,
	})

	var events []TabLifecycleEvent
	wt.OnTabLifecycle = func(ev TabLifecycleEvent) {
		events = append(events, ev)
	}

	// Pin Tab 0 so it is immune to automatic LRU hibernation and idle reaping!
	if !wt.TogglePinFocusedTab() {
		t.Fatal("expected Tab 0 to become pinned")
	}

	// Open 4 more tabs spaced 10s apart -> Tab 0 is pinned, so Tab 1..3 become backgrounded.
	// With MaxWarmTabs=2, opening Tab 4 must automatically LRU-hibernate Tab 1 (oldest unpinned paused tab)!
	for i := 1; i <= 4; i++ {
		now = now.Add(10 * time.Second)
		if _, err := wt.OpenTabInFocused(PaneBuffer{
			BufferID: fmt.Sprintf("plugin:tab-%d", i),
			Title:    fmt.Sprintf("Tab %d", i),
			Kind:     "webview",
		}); err != nil {
			t.Fatalf("OpenTabInFocused(tab-%d): %v", i, err)
		}
	}

	snap := wt.Snapshot()
	if snap.TotalTabs != 5 || snap.ActiveTabs != 1 || snap.HibernatedTabs != 1 {
		t.Fatalf("expected 5 total / 1 active / 1 LRU-hibernated tab, got %+v", snap)
	}
	tabs := snap.Root.Tabs
	if !tabs[0].Pinned || tabs[0].State != TabPaused {
		t.Fatalf("pinned Tab 0 must stay TabPaused (immune to LRU), got %+v", tabs[0])
	}
	if tabs[1].State != TabHibernated {
		t.Fatalf("expected oldest unpinned Tab 1 to be LRU-hibernated, got %+v", tabs[1])
	}
	if wt.BufferStillWarm("plugin:tab-1") {
		t.Fatal("hibernated plugin:tab-1 must report BufferStillWarm=false so host can reclaim RAM to 0MB")
	}

	// Advance clock by +6 minutes (> 5m IdleHibernateTTL) -> ReapIdleTabs hibernates unpinned Tab 2 & Tab 3,
	// while keeping pinned Tab 0 warm and Tab 4 active!
	now = now.Add(6 * time.Minute)
	reaped := wt.ReapIdleTabs(now)
	if len(reaped) != 2 {
		t.Fatalf("expected 2 idle tabs reaped (tab-2, tab-3), got %d: %+v", len(reaped), reaped)
	}
	snap = wt.Snapshot()
	if snap.HibernatedTabs != 3 || snap.PausedTabs != 1 || snap.ActiveTabs != 1 {
		t.Fatalf("expected 1 active / 1 pinned-paused / 3 hibernated after idle reap, got %+v", snap)
	}

	// Selecting hibernated Tab 1 wakes it transparently (TabHibernated -> TabActive, WakeCount == 1)!
	if !wt.SelectTab(snap.Root.ID, 1) {
		t.Fatal("SelectTab(1) failed")
	}
	snap = wt.Snapshot()
	if snap.Root.ActiveTab != 1 || snap.Root.Tabs[1].State != TabActive || snap.Root.Tabs[1].WakeCount != 1 {
		t.Fatalf("expected Tab 1 woken with WakeCount=1, got %+v", snap.Root.Tabs[1])
	}
	if !wt.BufferStillWarm("plugin:tab-1") {
		t.Fatal("woken plugin:tab-1 must report BufferStillWarm=true")
	}

	// Verify steady-state 120Hz Snapshot() remains ZERO allocations with 5+ stacked tabs!
	_ = wt.Snapshot()
	allocs := testing.AllocsPerRun(500, func() {
		s := wt.Snapshot()
		if s.TotalTabs != 5 {
			t.Fatalf("expected 5 tabs, got %d", s.TotalTabs)
		}
	})
	if allocs > 0 {
		t.Fatalf("expected 0 allocs/op on 120Hz Snapshot() with stacked tabs, got %.2f", allocs)
	}
}

func TestLayoutSpecLazyZeroMBRestoreAndCrossPaneTabMoveDetach(t *testing.T) {
	wt := NewWindowTree(PaneBuffer{BufferID: "slot:workbench.main", Title: "Main"})

	// Simulate Rust durable backend restoring a pane with 6 stacked tabs over gRPC.
	spec := LayoutSpec{
		Perspective: "restored-session",
		Root: &LayoutNodeSpec{
			ActiveTab: 2,
			Tabs: []LayoutTabSpec{
				{BufferID: "plugin:tab-0", Title: "Tab 0", Kind: "webview", Pinned: true},
				{BufferID: "plugin:tab-1", Title: "Tab 1", Kind: "webview"},
				{BufferID: "plugin:tab-2", Title: "Tab 2", Kind: "webview"},
				{BufferID: "plugin:tab-3", Title: "Tab 3", Kind: "webview"},
				{BufferID: "plugin:tab-4", Title: "Tab 4", Kind: "webview"},
				{BufferID: "plugin:tab-5", Title: "Tab 5", Kind: "wasm-gpu"},
			},
		},
	}
	if err := ApplyLayoutSpec(wt, spec, nil); err != nil {
		t.Fatalf("ApplyLayoutSpec(multi-tab): %v", err)
	}

	// Lazy 0MB cold start: ONLY ActiveTab (index 2) is TabActive; all 5 others start TabHibernated!
	snap := wt.Snapshot()
	if snap.TotalTabs != 6 || snap.ActiveTabs != 1 || snap.PausedTabs != 0 || snap.HibernatedTabs != 5 {
		t.Fatalf("expected lazy 0MB restore (1 active, 0 paused, 5 hibernated), got %+v", snap)
	}
	if snap.Root.ActiveTab != 2 || snap.Root.Buffer.BufferID != "plugin:tab-2" || !snap.Root.Tabs[0].Pinned {
		t.Fatalf("unexpected restored active tab / pinned metadata: %+v", snap.Root)
	}

	// Pure DTO round-trip preserves multi-tab stack & pinned flags for backend gRPC serialization.
	exported := ExportLayoutSpec(wt)
	raw, err := MarshalLayoutSpecJSON(exported)
	if err != nil {
		t.Fatalf("MarshalLayoutSpecJSON: %v", err)
	}
	parsed, err := UnmarshalLayoutSpecJSON(raw)
	if err != nil {
		t.Fatalf("UnmarshalLayoutSpecJSON: %v", err)
	}
	if len(parsed.Root.Tabs) != 6 || parsed.Root.ActiveTab != 2 || !parsed.Root.Tabs[0].Pinned {
		t.Fatalf("LayoutSpec multi-tab round-trip mismatch: %+v", parsed.Root)
	}

	// Tear off active tab (Tab 2) into a new right-hand split pane (`SPC b t`).
	newWin, err := wt.TearOffFocusedTabRight()
	if err != nil {
		t.Fatalf("TearOffFocusedTabRight: %v", err)
	}
	snap = wt.Snapshot()
	if snap.LeafCount != 2 || snap.FocusedID != newWin {
		t.Fatalf("expected 2 leaves focused on detached pane %q, got %+v", newWin, snap)
	}
	if len(snap.Root.First.Tabs) != 5 || len(snap.Root.Second.Tabs) != 1 || snap.Root.Second.Buffer.BufferID != "plugin:tab-2" {
		t.Fatalf("unexpected tabs after tear-off: first=%d second=%d buf=%q",
			len(snap.Root.First.Tabs), len(snap.Root.Second.Tabs), snap.Root.Second.Buffer.BufferID)
	}

	// Move Tab 2 back into the left pane (`SPC b m`) -> right pane collapses cleanly back to 1 leaf!
	if err := wt.MoveFocusedTabToNextPane(); err != nil {
		t.Fatalf("MoveFocusedTabToNextPane: %v", err)
	}
	snap = wt.Snapshot()
	if snap.LeafCount != 1 || snap.TotalTabs != 6 || snap.Root.Buffer.BufferID != "plugin:tab-2" {
		t.Fatalf("expected moved tab merged back into 1 leaf with 6 tabs, got %+v", snap)
	}

	// Winner-Mode Undo restores the 2-pane detached layout!
	if !wt.Undo() || wt.Snapshot().LeafCount != 2 {
		t.Fatalf("expected Winner-Mode Undo to restore 2-pane split, got %+v", wt.Snapshot())
	}
}
