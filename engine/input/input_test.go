// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package input

import (
	"errors"
	"testing"
)

func TestKeyTrieExactPrefixAndConflictDetection(t *testing.T) {
	trie, err := CompileKeyTrie([]Binding{
		{Scope: ScopeNormal, Sequence: []string{"SPC", "w", "v"}, ActionKind: "invoke", CommandName: "split-vertical"},
		{Scope: ScopeNormal, Sequence: []string{"SPC", "b", "b"}, ActionKind: "invoke", CommandName: "switch-buffer"},
		{Scope: ScopeGlobal, Sequence: []string{"C-g"}, ActionKind: "cancel"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if kind, _ := trie.Lookup(ScopeNormal, []string{"SPC", "w"}); kind != MatchPrefix {
		t.Fatalf("expected MatchPrefix for SPC w, got %v", kind)
	}
	if kind, b := trie.Lookup(ScopeNormal, []string{"SPC", "w", "v"}); kind != MatchExact || b.CommandName != "split-vertical" {
		t.Fatalf("expected split-vertical exact match, got kind=%v b=%+v", kind, b)
	}
	if kind, b := trie.Lookup(ScopeInsert, []string{"C-g"}); kind != MatchExact || b.ActionKind != "cancel" {
		t.Fatalf("expected global fallback for C-g, got kind=%v b=%+v", kind, b)
	}

	_, err = CompileKeyTrie([]Binding{
		{Scope: ScopeNormal, Sequence: []string{"SPC", "w"}, CommandName: "window-menu"},
		{Scope: ScopeNormal, Sequence: []string{"SPC", "w", "v"}, CommandName: "split-vertical"},
	})
	if !errors.Is(err, ErrKeyConflict) {
		t.Fatalf("expected ErrKeyConflict for prefix shadow, got %v", err)
	}
}

func TestFuzzyRankPrefersWordBoundaryAndPrefix(t *testing.T) {
	cmds := []CommandEntry{
		{Name: "workspace-open-settings"},
		{Name: "select-buffer"},
		{Name: "switch-buffer-list"},
		{Name: "sb"},
	}
	ranked := RankCommands("sb", cmds, nil)
	if len(ranked) < 3 {
		t.Fatalf("expected at least 3 matches, got %d", len(ranked))
	}
	if cmds[ranked[0].Index].Name != "sb" {
		t.Fatalf("expected exact match 'sb' first, got %q", cmds[ranked[0].Index].Name)
	}
	if cmds[ranked[1].Index].Name != "select-buffer" {
		t.Fatalf("expected boundary match 'select-buffer' second, got %q", cmds[ranked[1].Index].Name)
	}
}
