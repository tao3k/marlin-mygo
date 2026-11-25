// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package shell

import (
	"errors"
	"sort"
	"time"
)

// TabLifecycleState models the 3-state lazy memory lifecycle of stacked tabs
// inside every composable Emacs pane:
//   - TabActive:     visible in its pane, rendering at 120Hz with live updates.
//   - TabPaused:     backgrounded tab (0 GPU paint, warm in RAM for instant switch).
//   - TabHibernated: dormant background tab (0MB WebView/WASI RAM; wakes transparently on focus).
type TabLifecycleState string

const (
	TabActive     TabLifecycleState = "active"
	TabPaused     TabLifecycleState = "paused"
	TabHibernated TabLifecycleState = "hibernated"

	DefaultMaxTabsPerPane   = 16
	DefaultMaxWarmTabs      = 4
	DefaultIdleHibernateTTL = 5 * time.Minute
)

var (
	ErrMaxTabsReached = errors.New("shell: maximum stacked tabs per pane reached")
	ErrInvalidTabIdx  = errors.New("shell: invalid tab index")
)

// PaneTab represents one stacked buffer/tab inside a leaf Emacs window pane.
type PaneTab struct {
	Buffer       PaneBuffer
	State        TabLifecycleState
	Pinned       bool
	LastActiveAt time.Time
	WakeCount    uint32
}

// TabLifecycleEvent notifies the host (e.g. LazySession) outside WindowTree's
// lock whenever a tab transitions between Active, Paused, and Hibernated.
type TabLifecycleEvent struct {
	WindowID WindowID
	Buffer   PaneBuffer
	From     TabLifecycleState
	To       TabLifecycleState
	Reason   string
}

// TabBudgetPolicy governs O(1) LRU warm-tab memory bounds and idle hibernation.
type TabBudgetPolicy struct {
	MaxTabsPerPane   int
	MaxWarmTabs      int // max simultaneously warm TabPaused background tabs across all panes (0 = hibernate immediately on switch)
	IdleHibernateTTL time.Duration
}

// DefaultTabBudgetPolicy returns balanced 120Hz workstation defaults.
func DefaultTabBudgetPolicy() TabBudgetPolicy {
	return TabBudgetPolicy{
		MaxTabsPerPane:   DefaultMaxTabsPerPane,
		MaxWarmTabs:      DefaultMaxWarmTabs,
		IdleHibernateTTL: DefaultIdleHibernateTTL,
	}
}

// WindowTabEntry projects one open tab across the entire WindowTree for the
// Emacs Minibuffer Buffer/Tab Picker (`SPC b b`).
type WindowTabEntry struct {
	WindowID   WindowID
	TabIndex   int
	Tab        PaneTab
	IsFocusedW bool
	IsActiveT  bool
}

// SetTabPolicy updates the lazy hibernation and LRU warm-tab budget policy.
func (t *WindowTree) SetTabPolicy(policy TabBudgetPolicy) {
	if policy.MaxTabsPerPane <= 0 {
		policy.MaxTabsPerPane = DefaultMaxTabsPerPane
	}
	if policy.MaxWarmTabs < 0 {
		policy.MaxWarmTabs = 0
	}
	t.mu.Lock()
	t.tabPolicy = policy
	evs := t.enforceWarmBudgetLocked("policy")
	if len(evs) > 0 {
		t.markDirtyLocked()
	}
	cb := t.OnTabLifecycle
	t.mu.Unlock()
	emitLifecycleEvents(cb, evs)
}

// SetClock overrides the clock used for LastActiveAt timestamps (for deterministic tests).
func (t *WindowTree) SetClock(fn func() time.Time) {
	t.mu.Lock()
	if fn == nil {
		t.clock = time.Now
	} else {
		t.clock = fn
	}
	t.mu.Unlock()
}

// OpenTabInFocused opens buf as a tab in the currently focused window (or
// activates/wakes it if already stacked in that pane).
func (t *WindowTree) OpenTabInFocused(buf PaneBuffer) (int, error) {
	return t.OpenTab(t.FocusedID(), buf)
}

// OpenTab appends buf to winID's tab stack (or focuses it if already present),
// pausing the previous active tab and hibernating LRU background tabs over budget.
func (t *WindowTree) OpenTab(winID WindowID, buf PaneBuffer) (int, error) {
	t.mu.Lock()
	leaf := findWindow(t.root, winID)
	if leaf == nil || leaf.IsSplit {
		t.mu.Unlock()
		return -1, ErrWindowNotFound
	}
	now := t.nowLocked()
	normalizeLeafTabs(leaf, now)

	// Reuse existing tab with identical BufferID inside this pane.
	for i := range leaf.Tabs {
		if leaf.Tabs[i].Buffer.BufferID == buf.BufferID {
			evs := t.activateTabLocked(leaf, i, now, "open-existing")
			t.focusedID = winID
			t.markDirtyLocked()
			cb := t.OnTabLifecycle
			t.mu.Unlock()
			emitLifecycleEvents(cb, evs)
			return i, nil
		}
	}

	maxTabs := t.tabPolicy.MaxTabsPerPane
	if maxTabs <= 0 {
		maxTabs = DefaultMaxTabsPerPane
	}
	if len(leaf.Tabs) >= maxTabs {
		t.mu.Unlock()
		return -1, ErrMaxTabsReached
	}

	t.pushUndoLocked("")
	var evs []TabLifecycleEvent
	prevIdx := leaf.ActiveTab
	if prevIdx >= 0 && prevIdx < len(leaf.Tabs) && leaf.Tabs[prevIdx].State == TabActive {
		leaf.Tabs[prevIdx].State = TabPaused
		leaf.Tabs[prevIdx].LastActiveAt = now
		evs = append(evs, TabLifecycleEvent{
			WindowID: leaf.ID,
			Buffer:   leaf.Tabs[prevIdx].Buffer,
			From:     TabActive,
			To:       TabPaused,
			Reason:   "background",
		})
	}

	leaf.Tabs = append(leaf.Tabs, PaneTab{
		Buffer:       buf,
		State:        TabActive,
		LastActiveAt: now,
	})
	newIdx := len(leaf.Tabs) - 1
	leaf.ActiveTab = newIdx
	leaf.Buffer = buf
	t.focusedID = winID
	evs = append(evs, TabLifecycleEvent{
		WindowID: leaf.ID,
		Buffer:   buf,
		From:     "",
		To:       TabActive,
		Reason:   "open",
	})
	evs = append(evs, t.enforceWarmBudgetLocked("lru-evict")...)
	t.markDirtyLocked()
	cb := t.OnTabLifecycle
	t.mu.Unlock()

	emitLifecycleEvents(cb, evs)
	return newIdx, nil
}

// SelectTab switches winID to tab index, transparently waking a TabHibernated
// tab (0MB -> Active) and pausing the outgoing tab.
func (t *WindowTree) SelectTab(winID WindowID, index int) bool {
	t.mu.Lock()
	leaf := findWindow(t.root, winID)
	if leaf == nil || leaf.IsSplit {
		t.mu.Unlock()
		return false
	}
	now := t.nowLocked()
	normalizeLeafTabs(leaf, now)
	if index < 0 || index >= len(leaf.Tabs) {
		t.mu.Unlock()
		return false
	}
	if leaf.ActiveTab == index && leaf.Tabs[index].State == TabActive && t.focusedID == winID {
		t.mu.Unlock()
		return true
	}
	evs := t.activateTabLocked(leaf, index, now, "select")
	t.focusedID = winID
	t.markDirtyLocked()
	cb := t.OnTabLifecycle
	t.mu.Unlock()

	emitLifecycleEvents(cb, evs)
	return true
}

// NextTab cycles forward through stacked tabs in the focused pane (`SPC b n`).
func (t *WindowTree) NextTab() bool {
	return t.stepTab(1)
}

// PrevTab cycles backward through stacked tabs in the focused pane (`SPC b p`).
func (t *WindowTree) PrevTab() bool {
	return t.stepTab(-1)
}

func (t *WindowTree) stepTab(delta int) bool {
	t.mu.Lock()
	leaf := findWindow(t.root, t.focusedID)
	if leaf == nil || leaf.IsSplit {
		t.mu.Unlock()
		return false
	}
	now := t.nowLocked()
	normalizeLeafTabs(leaf, now)
	if len(leaf.Tabs) <= 1 {
		t.mu.Unlock()
		return false
	}
	next := (leaf.ActiveTab + delta) % len(leaf.Tabs)
	if next < 0 {
		next += len(leaf.Tabs)
	}
	evs := t.activateTabLocked(leaf, next, now, "cycle")
	t.markDirtyLocked()
	cb := t.OnTabLifecycle
	t.mu.Unlock()

	emitLifecycleEvents(cb, evs)
	return true
}

// CloseFocusedTab closes the active tab in the focused pane (`SPC b d`).
func (t *WindowTree) CloseFocusedTab() error {
	t.mu.RLock()
	winID := t.focusedID
	leaf := findWindow(t.root, winID)
	activeIdx := 0
	if leaf != nil {
		activeIdx = leaf.ActiveTab
	}
	t.mu.RUnlock()
	return t.CloseTab(winID, activeIdx)
}

// CloseTab removes tab index from winID. If it was the pane's only tab, deletes
// the pane (returning ErrLastWindow if it is the sole window).
func (t *WindowTree) CloseTab(winID WindowID, index int) error {
	t.mu.Lock()
	leaf := findWindow(t.root, winID)
	if leaf == nil || leaf.IsSplit {
		t.mu.Unlock()
		return ErrWindowNotFound
	}
	now := t.nowLocked()
	normalizeLeafTabs(leaf, now)
	if index < 0 || index >= len(leaf.Tabs) {
		t.mu.Unlock()
		return ErrInvalidTabIdx
	}
	if len(leaf.Tabs) == 1 {
		t.mu.Unlock()
		return t.DeleteWindow(winID)
	}

	t.pushUndoLocked("")
	closed := leaf.Tabs[index]
	evs := []TabLifecycleEvent{{
		WindowID: leaf.ID,
		Buffer:   closed.Buffer,
		From:     closed.State,
		To:       TabHibernated,
		Reason:   "close",
	}}
	leaf.Tabs = append(leaf.Tabs[:index], leaf.Tabs[index+1:]...)
	if index < leaf.ActiveTab {
		leaf.ActiveTab--
	} else if index == leaf.ActiveTab {
		if leaf.ActiveTab >= len(leaf.Tabs) {
			leaf.ActiveTab = len(leaf.Tabs) - 1
		}
		prevState := leaf.Tabs[leaf.ActiveTab].State
		if prevState == TabHibernated {
			leaf.Tabs[leaf.ActiveTab].WakeCount++
		}
		leaf.Tabs[leaf.ActiveTab].State = TabActive
		leaf.Tabs[leaf.ActiveTab].LastActiveAt = now
		leaf.Buffer = leaf.Tabs[leaf.ActiveTab].Buffer
		if prevState != TabActive {
			evs = append(evs, TabLifecycleEvent{
				WindowID: leaf.ID,
				Buffer:   leaf.Buffer,
				From:     prevState,
				To:       TabActive,
				Reason:   "close-promote",
			})
		}
	}
	t.markDirtyLocked()
	cb := t.OnTabLifecycle
	t.mu.Unlock()

	emitLifecycleEvents(cb, evs)
	return nil
}

// HibernateFocusedTab puts the focused tab to sleep (0MB RAM) if another tab
// exists in that pane to take focus, or hibernates the first paused background tab.
func (t *WindowTree) HibernateFocusedTab() bool {
	t.mu.Lock()
	leaf := findWindow(t.root, t.focusedID)
	if leaf == nil || leaf.IsSplit {
		t.mu.Unlock()
		return false
	}
	now := t.nowLocked()
	normalizeLeafTabs(leaf, now)
	if len(leaf.Tabs) <= 1 {
		t.mu.Unlock()
		return false
	}
	activeIdx := leaf.ActiveTab
	t.mu.Unlock()
	return t.HibernateTab(leaf.ID, activeIdx)
}

// HibernateTab explicitly transitions tab index in winID to TabHibernated (0MB RAM).
// If index is currently active and another tab exists in winID, focus moves to the
// adjacent tab first so no pane ever goes blank.
func (t *WindowTree) HibernateTab(winID WindowID, index int) bool {
	t.mu.Lock()
	leaf := findWindow(t.root, winID)
	if leaf == nil || leaf.IsSplit {
		t.mu.Unlock()
		return false
	}
	now := t.nowLocked()
	normalizeLeafTabs(leaf, now)
	if index < 0 || index >= len(leaf.Tabs) {
		t.mu.Unlock()
		return false
	}
	if leaf.Tabs[index].State == TabHibernated {
		t.mu.Unlock()
		return true
	}

	var evs []TabLifecycleEvent
	if index == leaf.ActiveTab {
		if len(leaf.Tabs) <= 1 {
			t.mu.Unlock()
			return false
		}
		next := (index + 1) % len(leaf.Tabs)
		evs = append(evs, t.activateTabLocked(leaf, next, now, "manual-sleep-handoff")...)
	}
	prev := leaf.Tabs[index].State
	leaf.Tabs[index].Pinned = false
	leaf.Tabs[index].State = TabHibernated
	evs = append(evs, TabLifecycleEvent{
		WindowID: leaf.ID,
		Buffer:   leaf.Tabs[index].Buffer,
		From:     prev,
		To:       TabHibernated,
		Reason:   "manual-sleep",
	})
	t.markDirtyLocked()
	cb := t.OnTabLifecycle
	t.mu.Unlock()

	emitLifecycleEvents(cb, evs)
	return true
}

// TogglePinFocusedTab pins/unpins the active tab in the focused pane (`SPC b P`).
// Pinned tabs are immune to automatic LRU hibernation and idle TTL reaping.
func (t *WindowTree) TogglePinFocusedTab() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	leaf := findWindow(t.root, t.focusedID)
	if leaf == nil || leaf.IsSplit {
		return false
	}
	normalizeLeafTabs(leaf, t.nowLocked())
	leaf.Tabs[leaf.ActiveTab].Pinned = !leaf.Tabs[leaf.ActiveTab].Pinned
	t.markDirtyLocked()
	return leaf.Tabs[leaf.ActiveTab].Pinned
}

// ReapIdleTabs hibernates every unpinned TabPaused background tab across all
// panes whose LastActiveAt is older than IdleHibernateTTL relative to now.
func (t *WindowTree) ReapIdleTabs(now time.Time) []TabLifecycleEvent {
	t.mu.Lock()
	ttl := t.tabPolicy.IdleHibernateTTL
	if ttl <= 0 {
		t.mu.Unlock()
		return nil
	}
	var evs []TabLifecycleEvent
	for _, leaf := range collectLeaves(t.root, nil) {
		normalizeLeafTabs(leaf, now)
		for i := range leaf.Tabs {
			tab := &leaf.Tabs[i]
			if i == leaf.ActiveTab || tab.Pinned || tab.State != TabPaused {
				continue
			}
			if now.Sub(tab.LastActiveAt) >= ttl {
				tab.State = TabHibernated
				evs = append(evs, TabLifecycleEvent{
					WindowID: leaf.ID,
					Buffer:   tab.Buffer,
					From:     TabPaused,
					To:       TabHibernated,
					Reason:   "idle-reap",
				})
			}
		}
	}
	if len(evs) > 0 {
		t.markDirtyLocked()
	}
	cb := t.OnTabLifecycle
	t.mu.Unlock()

	emitLifecycleEvents(cb, evs)
	return evs
}

// AllTabs returns every open tab across every pane for Minibuffer Picker rendering.
func (t *WindowTree) AllTabs() []WindowTabEntry {
	t.mu.RLock()
	defer t.mu.RUnlock()
	leaves := collectLeaves(t.root, nil)
	out := make([]WindowTabEntry, 0, len(leaves)*2)
	for _, leaf := range leaves {
		for i, tab := range leaf.Tabs {
			out = append(out, WindowTabEntry{
				WindowID:   leaf.ID,
				TabIndex:   i,
				Tab:        tab,
				IsFocusedW: leaf.ID == t.focusedID,
				IsActiveT:  i == leaf.ActiveTab,
			})
		}
	}
	return out
}

// BufferStillWarm reports whether bufferID is currently Active or Paused in any
// pane tab (when false, the host may safely sleep its backing plugin to 0MB RAM).
func (t *WindowTree) BufferStillWarm(bufferID string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, leaf := range collectLeaves(t.root, nil) {
		for _, tab := range leaf.Tabs {
			if tab.Buffer.BufferID == bufferID && (tab.State == TabActive || tab.State == TabPaused) {
				return true
			}
		}
	}
	return false
}

func (t *WindowTree) activateTabLocked(leaf *WindowNode, nextIdx int, now time.Time, reason string) []TabLifecycleEvent {
	var evs []TabLifecycleEvent
	prevIdx := leaf.ActiveTab
	if prevIdx != nextIdx && prevIdx >= 0 && prevIdx < len(leaf.Tabs) && leaf.Tabs[prevIdx].State == TabActive {
		leaf.Tabs[prevIdx].State = TabPaused
		leaf.Tabs[prevIdx].LastActiveAt = now
		evs = append(evs, TabLifecycleEvent{
			WindowID: leaf.ID,
			Buffer:   leaf.Tabs[prevIdx].Buffer,
			From:     TabActive,
			To:       TabPaused,
			Reason:   "background",
		})
	}
	prevState := leaf.Tabs[nextIdx].State
	if prevState == TabHibernated {
		leaf.Tabs[nextIdx].WakeCount++
	}
	leaf.Tabs[nextIdx].State = TabActive
	leaf.Tabs[nextIdx].LastActiveAt = now
	leaf.ActiveTab = nextIdx
	leaf.Buffer = leaf.Tabs[nextIdx].Buffer
	if prevState != TabActive {
		evs = append(evs, TabLifecycleEvent{
			WindowID: leaf.ID,
			Buffer:   leaf.Buffer,
			From:     prevState,
			To:       TabActive,
			Reason:   reason,
		})
	}
	evs = append(evs, t.enforceWarmBudgetLocked("lru-evict")...)
	return evs
}

func (t *WindowTree) enforceWarmBudgetLocked(reason string) []TabLifecycleEvent {
	type warmRef struct {
		leaf  *WindowNode
		index int
		ts    time.Time
	}
	var warm []warmRef
	for _, leaf := range collectLeaves(t.root, nil) {
		for i := range leaf.Tabs {
			tab := &leaf.Tabs[i]
			if i == leaf.ActiveTab || tab.Pinned || tab.State != TabPaused {
				continue
			}
			warm = append(warm, warmRef{leaf: leaf, index: i, ts: tab.LastActiveAt})
		}
	}
	excess := len(warm) - t.tabPolicy.MaxWarmTabs
	if excess <= 0 {
		return nil
	}
	sort.SliceStable(warm, func(i, j int) bool {
		return warm[i].ts.Before(warm[j].ts)
	})
	evs := make([]TabLifecycleEvent, 0, excess)
	for i := 0; i < excess; i++ {
		ref := warm[i]
		ref.leaf.Tabs[ref.index].State = TabHibernated
		evs = append(evs, TabLifecycleEvent{
			WindowID: ref.leaf.ID,
			Buffer:   ref.leaf.Tabs[ref.index].Buffer,
			From:     TabPaused,
			To:       TabHibernated,
			Reason:   reason,
		})
	}
	return evs
}

func emitLifecycleEvents(cb func(TabLifecycleEvent), evs []TabLifecycleEvent) {
	if cb == nil {
		return
	}
	for _, ev := range evs {
		cb(ev)
	}
}

func normalizeTreeTabs(n *WindowNode, now time.Time) {
	if n == nil {
		return
	}
	if n.IsSplit {
		normalizeTreeTabs(n.First, now)
		normalizeTreeTabs(n.Second, now)
		return
	}
	normalizeLeafTabs(n, now)
}

func normalizeLeafTabs(leaf *WindowNode, now time.Time) {
	if leaf == nil || leaf.IsSplit {
		return
	}
	if len(leaf.Tabs) == 0 {
		leaf.Tabs = []PaneTab{{
			Buffer:       leaf.Buffer,
			State:        TabActive,
			LastActiveAt: now,
		}}
		leaf.ActiveTab = 0
		return
	}
	if leaf.ActiveTab < 0 || leaf.ActiveTab >= len(leaf.Tabs) {
		leaf.ActiveTab = 0
	}
	leaf.Tabs[leaf.ActiveTab].State = TabActive
	leaf.Buffer = leaf.Tabs[leaf.ActiveTab].Buffer
}

func clonePaneTabs(in []PaneTab) []PaneTab {
	if len(in) == 0 {
		return nil
	}
	out := make([]PaneTab, len(in))
	copy(out, in)
	return out
}

func countTabStates(n *WindowNode) (total, active, paused, hibernated int) {
	if n == nil {
		return 0, 0, 0, 0
	}
	if n.IsSplit {
		t1, a1, p1, h1 := countTabStates(n.First)
		t2, a2, p2, h2 := countTabStates(n.Second)
		return t1 + t2, a1 + a2, p1 + p2, h1 + h2
	}
	for _, tab := range n.Tabs {
		total++
		switch tab.State {
		case TabActive:
			active++
		case TabPaused:
			paused++
		case TabHibernated:
			hibernated++
		}
	}
	return total, active, paused, hibernated
}
