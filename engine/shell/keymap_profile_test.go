// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package shell

import (
	"testing"

	"github.com/tao3k/marlin-mygo/engine/input"
)

func TestDualModalKeymapNormalSPCAndEmacsInsertBindings(t *testing.T) {
	kt, err := CompileDualModalKeymap()
	if err != nil {
		t.Fatalf("CompileDualModalKeymap failed: %v", err)
	}

	// Layout 1: Normal mode SPC-guided chords.
	for _, tc := range []struct {
		seq  []string
		want string
	}{
		{[]string{"SPC", "w", "v"}, CmdWindowSplitRight},
		{[]string{"SPC", "w", "x"}, CmdWindowSwapNext},
		{[]string{"SPC", "p", "3"}, CmdPerspectiveCockpit},
		{[]string{"SPC", "m"}, CmdStudioOpenMarketplace},
		{[]string{"SPC", "t", "t"}, CmdStudioCycleTheme},
		{[]string{"i"}, CmdModeInsert},
	} {
		kind, b := kt.Lookup(input.ScopeNormal, tc.seq)
		if kind != input.MatchExact || b.CommandName != tc.want {
			t.Fatalf("Normal %v: got kind=%v cmd=%q, want %q", tc.seq, kind, b.CommandName, tc.want)
		}
	}

	// Layout 2: Insert/Edit mode classic Emacs bindings (C-n, C-p, C-a, C-e, C-k, C-y, ESC, C-g).
	for _, tc := range []struct {
		seq  []string
		want string
	}{
		{[]string{"C-n"}, CmdEditNextLine},
		{[]string{"C-p"}, CmdEditPrevLine},
		{[]string{"C-f"}, CmdEditForwardChar},
		{[]string{"C-b"}, CmdEditBackwardChar},
		{[]string{"C-a"}, CmdEditBeginningOfLine},
		{[]string{"C-e"}, CmdEditEndOfLine},
		{[]string{"C-k"}, CmdEditKillLine},
		{[]string{"C-y"}, CmdEditYank},
		{[]string{"ESC"}, CmdModeNormal},
		{[]string{"C-g"}, CmdModeNormal},
	} {
		kind, b := kt.Lookup(input.ScopeInsert, tc.seq)
		if kind != input.MatchExact || b.CommandName != tc.want {
			t.Fatalf("Insert %v: got kind=%v cmd=%q, want %q", tc.seq, kind, b.CommandName, tc.want)
		}
	}

	// Isolation: Emacs Insert C-n must NOT fire in Normal mode, and Normal SPC w v must NOT hijack Insert typing.
	if kind, _ := kt.Lookup(input.ScopeNormal, []string{"C-n"}); kind != input.MatchNone {
		t.Fatalf("expected C-n unbound in ScopeNormal, got %v", kind)
	}
	if kind, _ := kt.Lookup(input.ScopeInsert, []string{"SPC"}); kind != input.MatchNone {
		t.Fatalf("expected SPC unbound in ScopeInsert (must type literal space), got %v", kind)
	}

	// Global Emacs C-x window chords work seamlessly in BOTH modes.
	for _, scope := range []input.KeyScope{input.ScopeNormal, input.ScopeInsert} {
		kind, b := kt.Lookup(scope, []string{"C-x", "2"})
		if kind != input.MatchExact || b.CommandName != CmdWindowSplitBelow {
			t.Fatalf("Scope %v C-x 2: got kind=%v cmd=%q", scope, kind, b.CommandName)
		}
	}
}
