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

func TestFuzzyScoreUnicodeAndCJKZeroAlloc(t *testing.T) {
	// CJK subsequence match (using Unicode escapes to preserve pure-ASCII source files).
	cjkTarget := "Org \u4efb\u52a1\u770b\u677f"
	cjkQuery := "\u4efb\u770b"
	score, ok := FuzzyScore(cjkQuery, cjkTarget)
	if !ok || score <= 0 {
		t.Fatalf("expected CJK subsequence match, got score=%d ok=%t", score, ok)
	}

	// Non-ASCII case folding (German umlaut).
	uScore, uOK := FuzzyScore("\u00fcber", "\u00dcbersicht")
	if !uOK || uScore <= 0 {
		t.Fatalf("expected Unicode case-fold match, got score=%d ok=%t", uScore, uOK)
	}

	if _, bad := FuzzyScore("\u770b\u4efb", cjkTarget); bad {
		t.Fatal("out-of-order CJK runes must not match")
	}

	allocs := testing.AllocsPerRun(100, func() {
		_, _ = FuzzyScore(cjkQuery, cjkTarget)
		_, _ = FuzzyScore("\u00fcber", "\u00dcbersicht")
	})
	if allocs != 0 {
		t.Fatalf("expected 0 heap allocs for Unicode FuzzyScore, got %.2f", allocs)
	}
}
