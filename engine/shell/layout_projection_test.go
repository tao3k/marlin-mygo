// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package shell

import (
	"errors"
	"testing"

	"github.com/tao3k/marlin-mygo/engine/input"
)

func TestApplyLayoutSpecProjectsSchemeBinaryTreeAndGuardsBounds(t *testing.T) {
	catalog := map[string]PaneBuffer{
		"slot:workbench.main":      {BufferID: "slot:workbench.main", Title: "Org Agenda", Kind: "webview"},
		"builtin:transcript":       {BufferID: "builtin:transcript", Title: "AI Transcript", Kind: "native-gpu"},
		"slot:workbench.inspector": {BufferID: "slot:workbench.inspector", Title: "Focus Clock", Kind: "wasm-gpu"},
	}
	tree := NewWindowTree(catalog["slot:workbench.main"])

	spec := LayoutSpec{
		Perspective: "scheme-ai-review",
		Root: &LayoutNodeSpec{
			Axis:  SplitRow,
			Ratio: 0.65,
			First: &LayoutNodeSpec{BufferID: "slot:workbench.main"},
			Second: &LayoutNodeSpec{
				Axis:   SplitColumn,
				Ratio:  0.95, // out of bounds -> clamps to 0.50
				First:  &LayoutNodeSpec{BufferID: "builtin:transcript"},
				Second: &LayoutNodeSpec{BufferID: "slot:workbench.inspector"},
			},
		},
	}

	if err := ApplyLayoutSpec(tree, spec, catalog); err != nil {
		t.Fatalf("ApplyLayoutSpec failed: %v", err)
	}
	snap := tree.Snapshot()
	if snap.Perspective != "scheme-ai-review" || snap.LeafCount != 3 {
		t.Fatalf("unexpected projected snapshot: perspective=%q leaves=%d", snap.Perspective, snap.LeafCount)
	}
	if snap.Root.Ratio != 0.65 || snap.Root.Second.Ratio != 0.50 {
		t.Fatalf("expected ratio 0.65 and clamped 0.50, got %v / %v", snap.Root.Ratio, snap.Root.Second.Ratio)
	}
	if _, buf := tree.FocusedBuffer(); buf.Title != "Org Agenda" {
		t.Fatalf("expected first leaf focused, got %+v", buf)
	}

	// Reject malformed empty leaf spec.
	if err := ApplyLayoutSpec(tree, LayoutSpec{Root: &LayoutNodeSpec{}}, catalog); !errors.Is(err, ErrInvalidLayoutSpec) {
		t.Fatalf("expected ErrInvalidLayoutSpec, got %v", err)
	}
}

func TestDefaultEmacsWindowKeyTrieAndCommandExecution(t *testing.T) {
	kt, err := input.CompileKeyTrie(DefaultEmacsWindowBindings())
	if err != nil {
		t.Fatalf("CompileKeyTrie failed: %v", err)
	}

	// Verify SPC w prefix -> Hydra hint, then SPC w v -> split-right.
	if kind, _ := kt.Lookup(input.ScopeNormal, []string{"SPC", "w"}); kind != input.MatchPrefix {
		t.Fatalf("expected MatchPrefix for SPC w, got %v", kind)
	}
	kind, binding := kt.Lookup(input.ScopeNormal, []string{"SPC", "w", "v"})
	if kind != input.MatchExact || binding.CommandName != CmdWindowSplitRight {
		t.Fatalf("expected SPC w v -> %s, got kind=%v cmd=%q", CmdWindowSplitRight, kind, binding.CommandName)
	}

	// Verify C-x 2 -> split-below.
	kind, binding = kt.Lookup(input.ScopeNormal, []string{"C-x", "2"})
	if kind != input.MatchExact || binding.CommandName != CmdWindowSplitBelow {
		t.Fatalf("expected C-x 2 -> %s, got kind=%v cmd=%q", CmdWindowSplitBelow, kind, binding.CommandName)
	}

	buffers := []PaneBuffer{
		{BufferID: "slot:workbench.main", Title: "Agenda", Kind: "webview"},
		{BufferID: "builtin:transcript", Title: "Transcript", Kind: "native-gpu"},
		{BufferID: "slot:workbench.inspector", Title: "Clock", Kind: "wasm-gpu"},
	}
	tree := NewWindowTree(buffers[0])

	for _, cmd := range []string{
		CmdWindowSplitRight,
		CmdWindowSplitBelow,
		CmdWindowFocusNext,
		CmdWindowCycleBuffer,
		CmdWindowBalance,
		CmdWindowToggleZoom,
		CmdWindowToggleZoom,
		CmdWindowDelete,
		CmdPerspectiveCockpit,
		CmdPerspectiveZen,
	} {
		handled, err := ExecuteWindowCommand(tree, cmd, buffers)
		if !handled || err != nil {
			t.Fatalf("ExecuteWindowCommand(%s): handled=%v err=%v", cmd, handled, err)
		}
	}
	if snap := tree.Snapshot(); snap.Perspective != "zen" || snap.LeafCount != 1 {
		t.Fatalf("expected zen perspective with 1 leaf, got %+v", snap)
	}
}
