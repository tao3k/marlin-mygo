// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package shell

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

// Direction specifies a 2D spatial direction for Emacs windmove-style navigation.
type Direction uint8

const (
	DirLeft Direction = iota + 1
	DirRight
	DirUp
	DirDown
)

// PaneRect describes a leaf window's normalized [0..1, 0..1] 2D spatial bounds.
type PaneRect struct {
	WindowID WindowID
	X, Y     float32
	W, H     float32
	Buffer   PaneBuffer
}

// WindowID uniquely identifies one leaf window or split container inside a
// composable Emacs-style WindowTree.
type WindowID string

// SplitAxis determines whether a binary split divides horizontally (side-by-side)
// or vertically (stacked).
type SplitAxis string

const (
	SplitRow    SplitAxis = "row"    // side-by-side (`split-window-right` / SPC w v)
	SplitColumn SplitAxis = "column" // stacked (`split-window-below` / SPC w s)

	MaxOpenWindows   = 8
	maxWindowHistory = 32
)

var (
	ErrMaxWindowsReached = errors.New("shell: maximum composable window limit reached")
	ErrLastWindow        = errors.New("shell: cannot delete the only remaining window")
	ErrWindowNotFound    = errors.New("shell: window ID not found")
)

// PaneBuffer identifies the buffer or plugin panel currently displayed inside
// an Emacs-style window pane.
type PaneBuffer struct {
	BufferID string // e.g. "slot:workbench.main", "builtin:transcript", "builtin:projection", "slot:workbench.inspector"
	Title    string
	Kind     string // "webview" | "wasm-gpu" | "native-gpu"
}

// WindowNode is an immutable snapshot node of the binary split tree.
// Leaf nodes carry both Buffer (active tab shortcut) and Tabs/ActiveTab for
// Emacs tab-line-mode multi-tab stacking with 3-state lazy hibernation.
type WindowNode struct {
	ID        WindowID
	IsSplit   bool
	Axis      SplitAxis
	Ratio     float32 // 0.20 .. 0.80 (default 0.50)
	First     *WindowNode
	Second    *WindowNode
	Buffer    PaneBuffer
	Tabs      []PaneTab
	ActiveTab int
}

// WindowTreeSnapshot is a read-only point-in-time layout snapshot consumed at
// 120Hz by MyGo GPU renderers with zero heap allocations on steady frames.
type WindowTreeSnapshot struct {
	Version        uint64
	Perspective    string
	Root           *WindowNode
	FocusedID      WindowID
	Zoomed         bool
	LeafCount      int
	CanUndo        bool
	CanRedo        bool
	TotalTabs      int
	ActiveTabs     int
	PausedTabs     int
	HibernatedTabs int
}

type windowHistoryEntry struct {
	perspective string
	root        *WindowNode
	focusedID   WindowID
	zoomed      bool
	resizeSplit WindowID // non-empty for coalescable continuous divider drags
}

// WindowTree manages dynamic Emacs-style composable window splits, focus
// cycling, 2D spatial navigation, Winner-Mode undo/redo history, per-pane
// buffer assignment, zoom/maximize, balance, and named workspace perspectives.
type WindowTree struct {
	mu             sync.RWMutex
	perspective    string
	root           *WindowNode
	focusedID      WindowID
	zoomed         bool
	nextSeq        uint64
	version        uint64
	cachedSnap     WindowTreeSnapshot
	snapDirty      bool
	undoStack      []windowHistoryEntry
	redoStack      []windowHistoryEntry
	tabPolicy      TabBudgetPolicy
	clock          func() time.Time
	OnTabLifecycle func(ev TabLifecycleEvent)
}

// NewWindowTree initializes a single-window tree displaying initialBuffer.
func NewWindowTree(initialBuffer PaneBuffer) *WindowTree {
	now := time.Now()
	root := newLeafNode("win-1", initialBuffer, now)
	wt := &WindowTree{
		perspective: "zen",
		root:        root,
		focusedID:   root.ID,
		nextSeq:     1,
		version:     1,
		snapDirty:   true,
		tabPolicy:   DefaultTabBudgetPolicy(),
		clock:       time.Now,
	}
	wt.rebuildSnapshotLocked()
	return wt
}

func (t *WindowTree) nowLocked() time.Time {
	if t.clock != nil {
		return t.clock()
	}
	return time.Now()
}

func newLeafNode(id WindowID, buf PaneBuffer, now time.Time) *WindowNode {
	return &WindowNode{
		ID:     id,
		Buffer: buf,
		Tabs: []PaneTab{
			{
				Buffer:       buf,
				State:        TabActive,
				LastActiveAt: now,
			},
		},
		ActiveTab: 0,
	}
}

func (t *WindowTree) allocIDLocked(prefix string) WindowID {
	t.nextSeq++
	return WindowID(fmt.Sprintf("%s-%d", prefix, t.nextSeq))
}

func (t *WindowTree) rebuildSnapshotLocked() {
	normalizeTreeTabs(t.root, t.nowLocked())
	totalTabs, activeTabs, pausedTabs, hibTabs := countTabStates(t.root)
	t.cachedSnap = WindowTreeSnapshot{
		Version:        t.version,
		Perspective:    t.perspective,
		Root:           cloneWindowNode(t.root),
		FocusedID:      t.focusedID,
		Zoomed:         t.zoomed,
		LeafCount:      countLeaves(t.root),
		CanUndo:        len(t.undoStack) > 0,
		CanRedo:        len(t.redoStack) > 0,
		TotalTabs:      totalTabs,
		ActiveTabs:     activeTabs,
		PausedTabs:     pausedTabs,
		HibernatedTabs: hibTabs,
	}
	t.snapDirty = false
}

func (t *WindowTree) markDirtyLocked() {
	t.version++
	t.rebuildSnapshotLocked()
}

func (t *WindowTree) pushUndoLocked(coalesceSplitID WindowID) {
	if coalesceSplitID != "" && len(t.undoStack) > 0 && t.undoStack[len(t.undoStack)-1].resizeSplit == coalesceSplitID {
		// Coalesce consecutive 120Hz drag frames of the same split divider into one Winner-Mode undo step.
		t.redoStack = t.redoStack[:0]
		return
	}
	entry := windowHistoryEntry{
		perspective: t.perspective,
		root:        cloneWindowNode(t.root),
		focusedID:   t.focusedID,
		zoomed:      t.zoomed,
		resizeSplit: coalesceSplitID,
	}
	if len(t.undoStack) >= maxWindowHistory {
		copy(t.undoStack, t.undoStack[1:])
		t.undoStack[len(t.undoStack)-1] = entry
	} else {
		t.undoStack = append(t.undoStack, entry)
	}
	t.redoStack = t.redoStack[:0]
}

// Snapshot returns an immutable layout snapshot with ZERO heap allocations on
// steady 120Hz frames (rebuilt only when the window tree mutates).
func (t *WindowTree) Snapshot() WindowTreeSnapshot {
	t.mu.RLock()
	if !t.snapDirty {
		snap := t.cachedSnap
		t.mu.RUnlock()
		return snap
	}
	t.mu.RUnlock()

	t.mu.Lock()
	if t.snapDirty {
		t.rebuildSnapshotLocked()
	}
	snap := t.cachedSnap
	t.mu.Unlock()
	return snap
}

// Version returns the monotonic layout mutation counter for O(1) dirty checks.
func (t *WindowTree) Version() uint64 {
	t.mu.RLock()
	v := t.version
	t.mu.RUnlock()
	return v
}

// FocusedBuffer returns the focused window ID and its mounted PaneBuffer.
func (t *WindowTree) FocusedBuffer() (WindowID, PaneBuffer) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if n := findWindow(t.root, t.focusedID); n != nil && !n.IsSplit {
		return n.ID, n.Buffer
	}
	return t.focusedID, PaneBuffer{}
}

// SplitRight splits the focused window side-by-side (`SPC w v` / split-window-right).
func (t *WindowTree) SplitRight(newBuffer PaneBuffer) (WindowID, error) {
	return t.splitWindow(t.FocusedID(), SplitRow, 0.52, newBuffer)
}

// SplitBelow splits the focused window top-to-bottom (`SPC w s` / split-window-below).
func (t *WindowTree) SplitBelow(newBuffer PaneBuffer) (WindowID, error) {
	return t.splitWindow(t.FocusedID(), SplitColumn, 0.62, newBuffer)
}

// SplitWindow splits a specific leaf window along axis with the given buffer.
func (t *WindowTree) SplitWindow(targetID WindowID, axis SplitAxis, ratio float32, newBuffer PaneBuffer) (WindowID, error) {
	return t.splitWindow(targetID, axis, ratio, newBuffer)
}

func (t *WindowTree) FocusedID() WindowID {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.focusedID
}

func (t *WindowTree) splitWindow(targetID WindowID, axis SplitAxis, ratio float32, newBuffer PaneBuffer) (WindowID, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if countLeaves(t.root) >= MaxOpenWindows {
		return "", ErrMaxWindowsReached
	}
	if ratio < 0.20 || ratio > 0.80 {
		ratio = 0.50
	}
	leaf := findWindow(t.root, targetID)
	if leaf == nil || leaf.IsSplit {
		return "", ErrWindowNotFound
	}
	t.pushUndoLocked("")
	now := t.nowLocked()
	normalizeLeafTabs(leaf, now)
	existingCopy := &WindowNode{
		ID:        leaf.ID,
		Buffer:    leaf.Buffer,
		Tabs:      clonePaneTabs(leaf.Tabs),
		ActiveTab: leaf.ActiveTab,
	}
	newID := t.allocIDLocked("win")
	newLeaf := newLeafNode(newID, newBuffer, now)
	leaf.ID = t.allocIDLocked("split")
	leaf.IsSplit = true
	leaf.Axis = axis
	leaf.Ratio = ratio
	leaf.First = existingCopy
	leaf.Second = newLeaf
	leaf.Buffer = PaneBuffer{}
	leaf.Tabs = nil
	leaf.ActiveTab = 0
	t.focusedID = newID
	t.zoomed = false
	t.perspective = "custom"
	t.markDirtyLocked()
	return newID, nil
}

// DeleteFocused closes the currently focused window (`SPC w d` / delete-window).
func (t *WindowTree) DeleteFocused() error {
	return t.DeleteWindow(t.FocusedID())
}

// DeleteWindow closes targetID and promotes its sibling in place.
func (t *WindowTree) DeleteWindow(targetID WindowID) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if countLeaves(t.root) <= 1 {
		return ErrLastWindow
	}
	if n := findWindow(t.root, targetID); n == nil || n.IsSplit {
		return ErrWindowNotFound
	}
	t.pushUndoLocked("")
	nextRoot, removed := removeLeaf(t.root, targetID)
	if !removed || nextRoot == nil {
		return ErrWindowNotFound
	}
	t.root = nextRoot
	if findWindow(t.root, t.focusedID) == nil {
		leaves := collectLeaves(t.root, nil)
		if len(leaves) > 0 {
			t.focusedID = leaves[0].ID
		}
	}
	if countLeaves(t.root) <= 1 {
		t.zoomed = false
	}
	t.perspective = "custom"
	t.markDirtyLocked()
	return nil
}

// FocusWindow sets active focus to targetID if it is a live leaf window.
func (t *WindowTree) FocusWindow(targetID WindowID) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := findWindow(t.root, targetID)
	if n == nil || n.IsSplit {
		return false
	}
	if t.focusedID != targetID {
		t.focusedID = targetID
		t.markDirtyLocked()
	}
	return true
}

// FocusNext cycles active focus forward across leaf windows (`SPC w o` / other-window).
func (t *WindowTree) FocusNext() WindowID {
	t.mu.Lock()
	defer t.mu.Unlock()
	leaves := collectLeaves(t.root, nil)
	if len(leaves) == 0 {
		return ""
	}
	for i, l := range leaves {
		if l.ID == t.focusedID {
			t.focusedID = leaves[(i+1)%len(leaves)].ID
			t.markDirtyLocked()
			return t.focusedID
		}
	}
	t.focusedID = leaves[0].ID
	t.markDirtyLocked()
	return t.focusedID
}

// SetWindowBuffer mounts buf into targetID's active tab (`switch-to-buffer`).
func (t *WindowTree) SetWindowBuffer(targetID WindowID, buf PaneBuffer) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := findWindow(t.root, targetID)
	if n == nil || n.IsSplit {
		return false
	}
	t.pushUndoLocked("")
	now := t.nowLocked()
	normalizeLeafTabs(n, now)
	n.Buffer = buf
	n.Tabs[n.ActiveTab].Buffer = buf
	n.Tabs[n.ActiveTab].State = TabActive
	n.Tabs[n.ActiveTab].LastActiveAt = now
	t.markDirtyLocked()
	return true
}

// SetFocusedBuffer mounts buf into whichever window currently has focus.
func (t *WindowTree) SetFocusedBuffer(buf PaneBuffer) bool {
	return t.SetWindowBuffer(t.FocusedID(), buf)
}

// ToggleZoom maximizes/restores the focused window (`SPC w m` / maximize-window).
func (t *WindowTree) ToggleZoom() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if countLeaves(t.root) <= 1 {
		if t.zoomed {
			t.zoomed = false
			t.markDirtyLocked()
		}
		return false
	}
	t.zoomed = !t.zoomed
	t.markDirtyLocked()
	return t.zoomed
}

// Balance resets every split ratio to 0.50 (`SPC w =` / balance-windows).
func (t *WindowTree) Balance() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pushUndoLocked("")
	balanceNode(t.root)
	t.markDirtyLocked()
}

// ResizeSplit updates one split container's ratio (clamped to [0.20, 0.80]).
// Consecutive drag updates on the same splitID coalesce into one Undo step.
func (t *WindowTree) ResizeSplit(splitID WindowID, ratio float32) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := findWindow(t.root, splitID)
	if n == nil || !n.IsSplit {
		return false
	}
	if ratio < 0.20 {
		ratio = 0.20
	} else if ratio > 0.80 {
		ratio = 0.80
	}
	if math.Abs(float64(n.Ratio-ratio)) < 0.0005 {
		return true
	}
	t.pushUndoLocked(splitID)
	n.Ratio = ratio
	t.markDirtyLocked()
	return true
}

// ApplyBuiltinPerspective switches the entire workspace to one of four named
// Emacs-style layouts ("focus", "split", "cockpit", "zen").
func (t *WindowTree) ApplyBuiltinPerspective(name string, primary, dock, chat, inspector PaneBuffer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pushUndoLocked("")
	t.zoomed = false
	t.perspective = name
	switch name {
	case "zen":
		t.root = &WindowNode{ID: "win-1", Buffer: primary}
		t.focusedID = "win-1"
		t.nextSeq = 1

	case "split":
		t.root = &WindowNode{
			ID:      "split-1",
			IsSplit: true,
			Axis:    SplitRow,
			Ratio:   0.60,
			First:   &WindowNode{ID: "win-1", Buffer: primary},
			Second:  &WindowNode{ID: "win-2", Buffer: chat},
		}
		t.focusedID = "win-1"
		t.nextSeq = 2

	case "cockpit":
		t.root = &WindowNode{
			ID:      "split-1",
			IsSplit: true,
			Axis:    SplitRow,
			Ratio:   0.58,
			First:   &WindowNode{ID: "win-1", Buffer: primary},
			Second: &WindowNode{
				ID:      "split-2",
				IsSplit: true,
				Axis:    SplitColumn,
				Ratio:   0.52,
				First:   &WindowNode{ID: "win-2", Buffer: chat},
				Second:  &WindowNode{ID: "win-3", Buffer: inspector},
			},
		}
		t.focusedID = "win-1"
		t.nextSeq = 3

	default: // "focus": primary workspace + compact bottom WASI GPU dock
		t.perspective = "focus"
		t.root = &WindowNode{
			ID:      "split-1",
			IsSplit: true,
			Axis:    SplitColumn,
			Ratio:   0.78,
			First:   &WindowNode{ID: "win-1", Buffer: primary},
			Second:  &WindowNode{ID: "win-2", Buffer: dock},
		}
		t.focusedID = "win-1"
		t.nextSeq = 2
	}
	t.markDirtyLocked()
}

func cloneWindowNode(n *WindowNode) *WindowNode {
	if n == nil {
		return nil
	}
	cp := *n
	if n.IsSplit {
		cp.First = cloneWindowNode(n.First)
		cp.Second = cloneWindowNode(n.Second)
	} else if len(n.Tabs) > 0 {
		cp.Tabs = clonePaneTabs(n.Tabs)
	}
	return &cp
}

func findWindow(n *WindowNode, id WindowID) *WindowNode {
	if n == nil {
		return nil
	}
	if n.ID == id {
		return n
	}
	if n.IsSplit {
		if found := findWindow(n.First, id); found != nil {
			return found
		}
		return findWindow(n.Second, id)
	}
	return nil
}

func countLeaves(n *WindowNode) int {
	if n == nil {
		return 0
	}
	if !n.IsSplit {
		return 1
	}
	return countLeaves(n.First) + countLeaves(n.Second)
}

func collectLeaves(n *WindowNode, out []*WindowNode) []*WindowNode {
	if n == nil {
		return out
	}
	if !n.IsSplit {
		return append(out, n)
	}
	out = collectLeaves(n.First, out)
	return collectLeaves(n.Second, out)
}

func removeLeaf(n *WindowNode, target WindowID) (*WindowNode, bool) {
	if n == nil || !n.IsSplit {
		return n, false
	}
	if n.First != nil && !n.First.IsSplit && n.First.ID == target {
		return n.Second, true
	}
	if n.Second != nil && !n.Second.IsSplit && n.Second.ID == target {
		return n.First, true
	}
	if nextFirst, ok := removeLeaf(n.First, target); ok {
		n.First = nextFirst
		return n, true
	}
	if nextSecond, ok := removeLeaf(n.Second, target); ok {
		n.Second = nextSecond
		return n, true
	}
	return n, false
}

func balanceNode(n *WindowNode) {
	if n == nil || !n.IsSplit {
		return
	}
	n.Ratio = 0.50
	balanceNode(n.First)
	balanceNode(n.Second)
}

// ReplaceBuffer replaces any leaf window currently displaying oldBufferID with
// fallback (e.g. when a Marketplace plugin is hot-unplugged), guaranteeing zero
// dead or empty panes. Returns the number of migrated panes.
func (t *WindowTree) ReplaceBuffer(oldBufferID string, fallback PaneBuffer) int {
	if oldBufferID == "" {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	leaves := collectLeaves(t.root, nil)
	replaced := 0
	for _, leaf := range leaves {
		normalizeLeafTabs(leaf, t.nowLocked())
		for i := range leaf.Tabs {
			if leaf.Tabs[i].Buffer.BufferID == oldBufferID {
				leaf.Tabs[i].Buffer = fallback
				replaced++
			}
		}
		if leaf.Buffer.BufferID == oldBufferID {
			leaf.Buffer = fallback
		}
	}
	if replaced > 0 {
		t.markDirtyLocked()
	}
	return replaced
}

// SwapWithNext swaps the mounted PaneBuffer of the focused window with the
// next leaf window in traversal order (`SPC w x` / window.swap-next).
func (t *WindowTree) SwapWithNext() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	leaves := collectLeaves(t.root, nil)
	if len(leaves) < 2 {
		return false
	}
	now := t.nowLocked()
	for i, leaf := range leaves {
		if leaf.ID == t.focusedID {
			t.pushUndoLocked("")
			next := leaves[(i+1)%len(leaves)]
			normalizeLeafTabs(leaf, now)
			normalizeLeafTabs(next, now)
			leaf.Buffer, next.Buffer = next.Buffer, leaf.Buffer
			leaf.Tabs[leaf.ActiveTab].Buffer, next.Tabs[next.ActiveTab].Buffer = next.Tabs[next.ActiveTab].Buffer, leaf.Tabs[leaf.ActiveTab].Buffer
			t.focusedID = next.ID
			t.markDirtyLocked()
			return true
		}
	}
	return false
}

// Undo restores the previous window layout from Winner-Mode history (`SPC w u`).
func (t *WindowTree) Undo() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.undoStack) == 0 {
		return false
	}
	cur := windowHistoryEntry{
		perspective: t.perspective,
		root:        cloneWindowNode(t.root),
		focusedID:   t.focusedID,
		zoomed:      t.zoomed,
	}
	t.redoStack = append(t.redoStack, cur)
	prev := t.undoStack[len(t.undoStack)-1]
	t.undoStack = t.undoStack[:len(t.undoStack)-1]
	t.perspective = prev.perspective
	t.root = prev.root
	t.focusedID = prev.focusedID
	t.zoomed = prev.zoomed
	t.markDirtyLocked()
	return true
}

// Redo re-applies an undone window layout from Winner-Mode history (`SPC w r`).
func (t *WindowTree) Redo() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.redoStack) == 0 {
		return false
	}
	cur := windowHistoryEntry{
		perspective: t.perspective,
		root:        cloneWindowNode(t.root),
		focusedID:   t.focusedID,
		zoomed:      t.zoomed,
	}
	t.undoStack = append(t.undoStack, cur)
	next := t.redoStack[len(t.redoStack)-1]
	t.redoStack = t.redoStack[:len(t.redoStack)-1]
	t.perspective = next.perspective
	t.root = next.root
	t.focusedID = next.focusedID
	t.zoomed = next.zoomed
	t.markDirtyLocked()
	return true
}

// ComputePaneRects projects every leaf window onto normalized [0..1, 0..1] 2D space.
func (t *WindowTree) ComputePaneRects() []PaneRect {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return computeRectsNode(t.root, 0, 0, 1, 1, nil)
}

func computeRectsNode(n *WindowNode, x, y, w, h float32, out []PaneRect) []PaneRect {
	if n == nil {
		return out
	}
	if !n.IsSplit {
		return append(out, PaneRect{WindowID: n.ID, X: x, Y: y, W: w, H: h, Buffer: n.Buffer})
	}
	r := n.Ratio
	if r < 0.20 || r > 0.80 {
		r = 0.50
	}
	if n.Axis == SplitRow {
		w1 := w * r
		out = computeRectsNode(n.First, x, y, w1, h, out)
		return computeRectsNode(n.Second, x+w1, y, w-w1, h, out)
	}
	h1 := h * r
	out = computeRectsNode(n.First, x, y, w, h1, out)
	return computeRectsNode(n.Second, x, y+h1, w, h-h1, out)
}

// FocusDirection moves active focus to the nearest geometric neighbor in dir
// (Emacs windmove-left/right/up/down -> SPC w h/l/k/j).
func (t *WindowTree) FocusDirection(dir Direction) WindowID {
	t.mu.Lock()
	defer t.mu.Unlock()
	rects := computeRectsNode(t.root, 0, 0, 1, 1, nil)
	if len(rects) <= 1 {
		return t.focusedID
	}
	var cur PaneRect
	found := false
	for _, r := range rects {
		if r.WindowID == t.focusedID {
			cur = r
			found = true
			break
		}
	}
	if !found {
		return t.focusedID
	}
	curCX := cur.X + cur.W*0.5
	curCY := cur.Y + cur.H*0.5

	const eps = 1e-4
	var bestID WindowID
	bestScore := float64(math.MaxFloat64)

	for _, cand := range rects {
		if cand.WindowID == cur.WindowID {
			continue
		}
		candCX := cand.X + cand.W*0.5
		candCY := cand.Y + cand.H*0.5

		var primaryGap float64
		var perpOverlap float32
		switch dir {
		case DirLeft:
			if cand.X+cand.W > cur.X+eps {
				continue
			}
			primaryGap = float64(cur.X - (cand.X + cand.W))
			perpOverlap = min(cur.Y+cur.H, cand.Y+cand.H) - max(cur.Y, cand.Y)
		case DirRight:
			if cand.X < cur.X+cur.W-eps {
				continue
			}
			primaryGap = float64(cand.X - (cur.X + cur.W))
			perpOverlap = min(cur.Y+cur.H, cand.Y+cand.H) - max(cur.Y, cand.Y)
		case DirUp:
			if cand.Y+cand.H > cur.Y+eps {
				continue
			}
			primaryGap = float64(cur.Y - (cand.Y + cand.H))
			perpOverlap = min(cur.X+cur.W, cand.X+cand.W) - max(cur.X, cand.X)
		case DirDown:
			if cand.Y < cur.Y+cur.H-eps {
				continue
			}
			primaryGap = float64(cand.Y - (cur.Y + cur.H))
			perpOverlap = min(cur.X+cur.W, cand.X+cand.W) - max(cur.X, cand.X)
		default:
			continue
		}

		// Strongly prefer directly adjacent panes sharing an edge along the perpendicular axis.
		perpPenalty := 0.0
		if perpOverlap <= eps {
			perpPenalty = 10.0
		}
		dist := math.Hypot(float64(candCX-curCX), float64(candCY-curCY))
		score := primaryGap*5.0 + perpPenalty + dist
		if score < bestScore {
			bestScore = score
			bestID = cand.WindowID
		}
	}
	if bestID != "" && bestID != t.focusedID {
		t.focusedID = bestID
		t.markDirtyLocked()
	}
	return t.focusedID
}

// ResizeFocusedSplit adjusts the immediate parent split container of the focused
// window by delta (e.g. +0.05 to widen, -0.05 to shrink; clamped to [0.20, 0.80]).
func (t *WindowTree) ResizeFocusedSplit(delta float32) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	parent, isFirst := findParentSplit(t.root, t.focusedID)
	if parent == nil {
		return false
	}
	nextRatio := parent.Ratio
	if isFirst {
		nextRatio += delta
	} else {
		nextRatio -= delta
	}
	if nextRatio < 0.20 {
		nextRatio = 0.20
	} else if nextRatio > 0.80 {
		nextRatio = 0.80
	}
	if math.Abs(float64(parent.Ratio-nextRatio)) < 0.0005 {
		return false
	}
	t.pushUndoLocked(parent.ID)
	parent.Ratio = nextRatio
	t.markDirtyLocked()
	return true
}

// RotateFocusedSplit flips the immediate parent split container of the focused
// window between SplitRow (side-by-side) and SplitColumn (stacked) (`SPC w R`).
func (t *WindowTree) RotateFocusedSplit() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	parent, _ := findParentSplit(t.root, t.focusedID)
	if parent == nil {
		return false
	}
	t.pushUndoLocked("")
	if parent.Axis == SplitRow {
		parent.Axis = SplitColumn
	} else {
		parent.Axis = SplitRow
	}
	t.perspective = "custom"
	t.markDirtyLocked()
	return true
}

func findParentSplit(n *WindowNode, target WindowID) (*WindowNode, bool) {
	if n == nil || !n.IsSplit {
		return nil, false
	}
	if n.First != nil && n.First.ID == target {
		return n, true
	}
	if n.Second != nil && n.Second.ID == target {
		return n, false
	}
	if p, first := findParentSplit(n.First, target); p != nil {
		return p, first
	}
	return findParentSplit(n.Second, target)
}
