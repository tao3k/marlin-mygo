// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package shell

import (
	"errors"
	"fmt"
	"strings"

	"github.com/tao3k/marlin-mygo/engine/input"
)

// Canonical Emacs / Meow composable window command identifiers projected from
// Scheme keymaps and the Cmd+K Command Palette onto WindowTree.
const (
	CmdWindowSplitRight    = "window.split-right"
	CmdWindowSplitBelow    = "window.split-below"
	CmdWindowDelete        = "window.delete"
	CmdWindowToggleZoom    = "window.toggle-zoom"
	CmdWindowFocusNext     = "window.focus-next"
	CmdWindowBalance       = "window.balance"
	CmdWindowCycleBuffer   = "window.cycle-buffer"
	CmdPerspectiveFocus    = "window.perspective.focus"
	CmdPerspectiveSplit    = "window.perspective.split"
	CmdPerspectiveCockpit  = "window.perspective.cockpit"
	CmdPerspectiveZen      = "window.perspective.zen"
)

// LayoutNodeSpec describes one node in a declarative Scheme layout projection
// (either a leaf buffer binding or a binary split container with ratio).
type LayoutNodeSpec struct {
	Axis     SplitAxis       `json:"axis,omitempty"`     // "row" | "column" (empty for leaf)
	Ratio    float32         `json:"ratio,omitempty"`    // clamped to [0.20, 0.80]
	BufferID string          `json:"bufferId,omitempty"` // leaf buffer identifier
	Title    string          `json:"title,omitempty"`    // optional override title
	Kind     string          `json:"kind,omitempty"`     // optional override kind
	First    *LayoutNodeSpec `json:"first,omitempty"`
	Second   *LayoutNodeSpec `json:"second,omitempty"`
}

// LayoutSpec is a typed, Scheme-authored Emacs workspace layout projection.
type LayoutSpec struct {
	Perspective string          `json:"perspective"`
	Root        *LayoutNodeSpec `json:"root"`
}

var ErrInvalidLayoutSpec = errors.New("shell: invalid declarative layout spec")

// ApplyLayoutSpec atomically projects a declarative Scheme LayoutSpec onto tree,
// resolving BufferIDs against catalog while enforcing MaxOpenWindows and ratio bounds.
func ApplyLayoutSpec(tree *WindowTree, spec LayoutSpec, catalog map[string]PaneBuffer) error {
	if tree == nil || spec.Root == nil {
		return ErrInvalidLayoutSpec
	}
	leafCount := countSpecLeaves(spec.Root)
	if leafCount <= 0 {
		return ErrInvalidLayoutSpec
	}
	if leafCount > MaxOpenWindows {
		return ErrMaxWindowsReached
	}

	tree.mu.Lock()
	defer tree.mu.Unlock()

	var firstLeafID WindowID
	built, err := buildNodeFromSpec(tree, spec.Root, catalog, &firstLeafID)
	if err != nil {
		return err
	}
	persp := strings.TrimSpace(spec.Perspective)
	if persp == "" {
		persp = "custom"
	}
	tree.perspective = persp
	tree.root = built
	tree.focusedID = firstLeafID
	tree.zoomed = false
	return nil
}

func countSpecLeaves(n *LayoutNodeSpec) int {
	if n == nil {
		return 0
	}
	if n.First != nil || n.Second != nil {
		if n.First == nil || n.Second == nil {
			return -1
		}
		a := countSpecLeaves(n.First)
		b := countSpecLeaves(n.Second)
		if a <= 0 || b <= 0 {
			return -1
		}
		return a + b
	}
	if strings.TrimSpace(n.BufferID) == "" {
		return -1
	}
	return 1
}

func buildNodeFromSpec(
	tree *WindowTree,
	spec *LayoutNodeSpec,
	catalog map[string]PaneBuffer,
	firstLeaf *WindowID,
) (*WindowNode, error) {
	if spec.First != nil && spec.Second != nil {
		axis := spec.Axis
		if axis != SplitRow && axis != SplitColumn {
			axis = SplitRow
		}
		ratio := spec.Ratio
		if ratio < 0.20 || ratio > 0.80 {
			ratio = 0.50
		}
		first, err := buildNodeFromSpec(tree, spec.First, catalog, firstLeaf)
		if err != nil {
			return nil, err
		}
		second, err := buildNodeFromSpec(tree, spec.Second, catalog, firstLeaf)
		if err != nil {
			return nil, err
		}
		return &WindowNode{
			ID:      tree.allocIDLocked("split"),
			IsSplit: true,
			Axis:    axis,
			Ratio:   ratio,
			First:   first,
			Second:  second,
		}, nil
	}
	bufID := strings.TrimSpace(spec.BufferID)
	if bufID == "" {
		return nil, fmt.Errorf("%w: empty leaf bufferId", ErrInvalidLayoutSpec)
	}
	buf, ok := catalog[bufID]
	if !ok {
		buf = PaneBuffer{BufferID: bufID, Title: bufID, Kind: "native-gpu"}
	}
	if spec.Title != "" {
		buf.Title = spec.Title
	}
	if spec.Kind != "" {
		buf.Kind = spec.Kind
	}
	id := tree.allocIDLocked("win")
	if *firstLeaf == "" {
		*firstLeaf = id
	}
	return &WindowNode{
		ID:     id,
		Buffer: buf,
	}, nil
}

// ExecuteWindowCommand dispatches a canonical Emacs/Meow window command onto tree.
// Returns handled=true if cmdName was a recognized window command.
func ExecuteWindowCommand(tree *WindowTree, cmdName string, buffers []PaneBuffer) (handled bool, err error) {
	if tree == nil {
		return false, nil
	}
	primary := PaneBuffer{BufferID: "slot:workbench.main", Title: "Workspace", Kind: "webview"}
	secondary := PaneBuffer{BufferID: "builtin:transcript", Title: "AI Transcript", Kind: "native-gpu"}
	projection := PaneBuffer{BufferID: "builtin:projection", Title: "Projection Inspector", Kind: "native-gpu"}
	dock := PaneBuffer{BufferID: "slot:workbench.inspector", Title: "Focus Clock", Kind: "wasm-gpu"}
	if len(buffers) > 0 {
		primary = buffers[0]
	}
	if len(buffers) > 1 {
		secondary = buffers[1]
	}
	if len(buffers) > 2 {
		projection = buffers[2]
	}
	if len(buffers) > 3 {
		dock = buffers[3]
	}

	switch strings.TrimSpace(cmdName) {
	case CmdWindowSplitRight:
		_, err = tree.SplitRight(secondary)
		return true, err
	case CmdWindowSplitBelow:
		_, err = tree.SplitBelow(secondary)
		return true, err
	case CmdWindowDelete:
		return true, tree.DeleteFocused()
	case CmdWindowToggleZoom:
		tree.ToggleZoom()
		return true, nil
	case CmdWindowFocusNext:
		tree.FocusNext()
		return true, nil
	case CmdWindowBalance:
		tree.Balance()
		return true, nil
	case CmdWindowCycleBuffer:
		if len(buffers) == 0 {
			return true, nil
		}
		winID, cur := tree.FocusedBuffer()
		next := buffers[0]
		for i, b := range buffers {
			if b.BufferID == cur.BufferID {
				next = buffers[(i+1)%len(buffers)]
				break
			}
		}
		tree.SetWindowBuffer(winID, next)
		return true, nil
	case CmdPerspectiveFocus:
		tree.ApplyBuiltinPerspective("focus", primary, dock, secondary, projection)
		return true, nil
	case CmdPerspectiveSplit:
		tree.ApplyBuiltinPerspective("split", primary, dock, secondary, projection)
		return true, nil
	case CmdPerspectiveCockpit:
		tree.ApplyBuiltinPerspective("cockpit", primary, dock, secondary, projection)
		return true, nil
	case CmdPerspectiveZen:
		tree.ApplyBuiltinPerspective("zen", primary, dock, secondary, projection)
		return true, nil
	default:
		return false, nil
	}
}

// DefaultEmacsWindowBindings returns conflict-free Emacs (`C-x ...`) and
// Meow/Spacemacs (`SPC w ...`) chord bindings for composable WindowTree control.
func DefaultEmacsWindowBindings() []input.Binding {
	return []input.Binding{
		// Spacemacs / Meow leader chords (SPC w ...)
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "w", "v"}, ActionKind: "window", CommandName: CmdWindowSplitRight},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "w", "s"}, ActionKind: "window", CommandName: CmdWindowSplitBelow},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "w", "d"}, ActionKind: "window", CommandName: CmdWindowDelete},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "w", "z"}, ActionKind: "window", CommandName: CmdWindowToggleZoom},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "w", "o"}, ActionKind: "window", CommandName: CmdWindowFocusNext},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "w", "="}, ActionKind: "window", CommandName: CmdWindowBalance},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "w", "b"}, ActionKind: "window", CommandName: CmdWindowCycleBuffer},
		// Classic Emacs chords (C-x ...)
		{Scope: input.ScopeGlobal, Sequence: []string{"C-x", "3"}, ActionKind: "window", CommandName: CmdWindowSplitRight},
		{Scope: input.ScopeGlobal, Sequence: []string{"C-x", "2"}, ActionKind: "window", CommandName: CmdWindowSplitBelow},
		{Scope: input.ScopeGlobal, Sequence: []string{"C-x", "0"}, ActionKind: "window", CommandName: CmdWindowDelete},
		{Scope: input.ScopeGlobal, Sequence: []string{"C-x", "1"}, ActionKind: "window", CommandName: CmdWindowToggleZoom},
		{Scope: input.ScopeGlobal, Sequence: []string{"C-x", "o"}, ActionKind: "window", CommandName: CmdWindowFocusNext},
		{Scope: input.ScopeGlobal, Sequence: []string{"C-x", "+"}, ActionKind: "window", CommandName: CmdWindowBalance},
	}
}

// DefaultEmacsWindowCommands returns Command Palette entries for composable window management.
func DefaultEmacsWindowCommands() []input.CommandEntry {
	return []input.CommandEntry{
		{Name: CmdWindowSplitRight, Description: "Split focused window right (SPC w v / C-x 3)"},
		{Name: CmdWindowSplitBelow, Description: "Split focused window below (SPC w s / C-x 2)"},
		{Name: CmdWindowDelete, Description: "Delete focused window (SPC w d / C-x 0)"},
		{Name: CmdWindowToggleZoom, Description: "Toggle zoom on focused window (SPC w z / C-x 1)"},
		{Name: CmdWindowFocusNext, Description: "Focus next window pane (SPC w o / C-x o)"},
		{Name: CmdWindowBalance, Description: "Balance split ratios (SPC w = / C-x +)"},
		{Name: CmdWindowCycleBuffer, Description: "Cycle buffer in focused window (SPC w b)"},
		{Name: CmdPerspectiveFocus, Description: "Apply Focus 2-pane perspective"},
		{Name: CmdPerspectiveSplit, Description: "Apply Split 2-pane perspective"},
		{Name: CmdPerspectiveCockpit, Description: "Apply Cockpit 3-pane perspective"},
		{Name: CmdPerspectiveZen, Description: "Apply Zen single-pane perspective"},
	}
}
