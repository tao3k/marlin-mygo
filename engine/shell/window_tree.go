// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package shell

import (
	"errors"
	"fmt"
	"sync"
)

// WindowID uniquely identifies one leaf window or split container inside a
// composable Emacs-style WindowTree.
type WindowID string

// SplitAxis determines whether a binary split divides horizontally (side-by-side)
// or vertically (stacked).
type SplitAxis string

const (
	SplitRow    SplitAxis = "row"    // side-by-side (`split-window-right` / SPC w v)
	SplitColumn SplitAxis = "column" // stacked (`split-window-below` / SPC w s)

	MaxOpenWindows = 8
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
type WindowNode struct {
	ID      WindowID
	IsSplit bool
	Axis    SplitAxis
	Ratio   float32 // 0.20 .. 0.80 (default 0.50)
	First   *WindowNode
	Second  *WindowNode
	Buffer  PaneBuffer
}

// WindowTreeSnapshot is a read-only point-in-time layout snapshot consumed at
// 120Hz by MyGo GPU renderers without holding locks during paint.
type WindowTreeSnapshot struct {
	Perspective string
	Root        *WindowNode
	FocusedID   WindowID
	Zoomed      bool
	LeafCount   int
}

// WindowTree manages dynamic Emacs-style composable window splits, focus
// cycling, per-pane buffer assignment, zoom/maximize, balance, and named
// workspace perspectives.
type WindowTree struct {
	mu          sync.RWMutex
	perspective string
	root        *WindowNode
	focusedID   WindowID
	zoomed      bool
	nextSeq     uint64
}

// NewWindowTree initializes a single-window tree displaying initialBuffer.
func NewWindowTree(initialBuffer PaneBuffer) *WindowTree {
	root := &WindowNode{
		ID:     "win-1",
		Buffer: initialBuffer,
	}
	return &WindowTree{
		perspective: "zen",
		root:        root,
		focusedID:   root.ID,
		nextSeq:     1,
	}
}

func (t *WindowTree) allocIDLocked(prefix string) WindowID {
	t.nextSeq++
	return WindowID(fmt.Sprintf("%s-%d", prefix, t.nextSeq))
}

// Snapshot returns a deep-cloned, lock-free layout tree ready for rendering.
func (t *WindowTree) Snapshot() WindowTreeSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return WindowTreeSnapshot{
		Perspective: t.perspective,
		Root:        cloneWindowNode(t.root),
		FocusedID:   t.focusedID,
		Zoomed:      t.zoomed,
		LeafCount:   countLeaves(t.root),
	}
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
	existingCopy := &WindowNode{
		ID:     leaf.ID,
		Buffer: leaf.Buffer,
	}
	newID := t.allocIDLocked("win")
	newLeaf := &WindowNode{
		ID:     newID,
		Buffer: newBuffer,
	}
	leaf.ID = t.allocIDLocked("split")
	leaf.IsSplit = true
	leaf.Axis = axis
	leaf.Ratio = ratio
	leaf.First = existingCopy
	leaf.Second = newLeaf
	leaf.Buffer = PaneBuffer{}
	t.focusedID = newID
	t.zoomed = false
	t.perspective = "custom"
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
	t.focusedID = targetID
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
			return t.focusedID
		}
	}
	t.focusedID = leaves[0].ID
	return t.focusedID
}

// SetWindowBuffer mounts buf into targetID (`switch-to-buffer` / `SPC b b`).
func (t *WindowTree) SetWindowBuffer(targetID WindowID, buf PaneBuffer) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := findWindow(t.root, targetID)
	if n == nil || n.IsSplit {
		return false
	}
	n.Buffer = buf
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
		t.zoomed = false
		return false
	}
	t.zoomed = !t.zoomed
	return t.zoomed
}

// Balance resets every split ratio to 0.50 (`SPC w =` / balance-windows).
func (t *WindowTree) Balance() {
	t.mu.Lock()
	defer t.mu.Unlock()
	balanceNode(t.root)
}

// ResizeSplit updates one split container's ratio (clamped to [0.20, 0.80]).
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
	n.Ratio = ratio
	return true
}

// ApplyBuiltinPerspective switches the entire workspace to one of four named
// Emacs-style layouts ("focus", "split", "cockpit", "zen").
func (t *WindowTree) ApplyBuiltinPerspective(name string, primary, dock, chat, inspector PaneBuffer) {
	t.mu.Lock()
	defer t.mu.Unlock()
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
}

func cloneWindowNode(n *WindowNode) *WindowNode {
	if n == nil {
		return nil
	}
	cp := *n
	if n.IsSplit {
		cp.First = cloneWindowNode(n.First)
		cp.Second = cloneWindowNode(n.Second)
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
