// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package input

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
)

func linearOracleLookup(bindings []Binding, mode KeyScope, seq []string) (MatchKind, Binding) {
	checkScope := func(sc KeyScope) (MatchKind, Binding) {
		if len(seq) == 0 {
			return MatchNone, Binding{}
		}
		isPrefix := false
		for _, b := range bindings {
			if b.Scope != sc {
				continue
			}
			if slices.Equal(b.Sequence, seq) {
				return MatchExact, b
			}
			if len(b.Sequence) > len(seq) && slices.Equal(b.Sequence[:len(seq)], seq) {
				isPrefix = true
			}
		}
		if isPrefix {
			return MatchPrefix, Binding{}
		}
		return MatchNone, Binding{}
	}
	if mode != ScopeGlobal && mode != 0 {
		if k, b := checkScope(mode); k != MatchNone {
			return k, b
		}
	}
	return checkScope(ScopeGlobal)
}

func isPrefixConflict(a, b []string) bool {
	n := min(len(a), len(b))
	return slices.Equal(a[:n], b[:n])
}

func TestKeyTriePropertyMatchesLinearOracle(t *testing.T) {
	rng := rand.New(rand.NewPCG(1234, 5678))
	keys := []string{"C-x", "SPC", "g", "k", "j", "M-x", "ESC", "RET"}
	scopes := []KeyScope{ScopeGlobal, ScopeInsert, ScopeNormal}

	for trial := 0; trial < 150; trial++ {
		var bindings []Binding
		targetCount := 4 + rng.IntN(16)
		for attempt := 0; attempt < targetCount*4 && len(bindings) < targetCount; attempt++ {
			sc := scopes[rng.IntN(len(scopes))]
			seqLen := 1 + rng.IntN(3)
			seq := make([]string, seqLen)
			for i := range seq {
				seq[i] = keys[rng.IntN(len(keys))]
			}
			conflict := false
			for _, existing := range bindings {
				if existing.Scope == sc && isPrefixConflict(existing.Sequence, seq) {
					conflict = true
					break
				}
			}
			if conflict {
				continue
			}
			bindings = append(bindings, Binding{
				Scope:       sc,
				Sequence:    seq,
				ActionKind:  "command",
				CommandName: fmt.Sprintf("cmd-%d-%d", trial, len(bindings)),
			})
		}

		kt, err := CompileKeyTrie(bindings)
		if err != nil {
			t.Fatalf("trial %d: unexpected compile error on conflict-free set: %v", trial, err)
		}

		// Verify every binding and prefix plus random queries match the linear oracle.
		for _, b := range bindings {
			for prefLen := 1; prefLen <= len(b.Sequence); prefLen++ {
				q := b.Sequence[:prefLen]
				for _, mode := range scopes {
					gotK, gotB := kt.Lookup(mode, q)
					wantK, wantB := linearOracleLookup(bindings, mode, q)
					if gotK != wantK || gotB.CommandName != wantB.CommandName {
						t.Fatalf("trial %d mode %d seq %v: got (%d,%q) want (%d,%q)",
							trial, mode, q, gotK, gotB.CommandName, wantK, wantB.CommandName)
					}
				}
			}
		}

		// Inject a prefix shadow conflict and verify CompileKeyTrie ALWAYS catches it.
		if len(bindings) > 0 {
			dup := bindings[0]
			withConflict := append(slices.Clone(bindings), Binding{
				Scope:       dup.Scope,
				Sequence:    append(slices.Clone(dup.Sequence), "z"),
				CommandName: "shadowed",
			})
			if _, err := CompileKeyTrie(withConflict); !errors.Is(err, ErrKeyConflict) {
				t.Fatalf("trial %d: expected ErrKeyConflict, got %v", trial, err)
			}
		}
	}
}

func isASCIISubsequence(query, target string) bool {
	qi := 0
	for ti := 0; ti < len(target) && qi < len(query); ti++ {
		if toLowerASCII(target[ti]) == toLowerASCII(query[qi]) {
			qi++
		}
	}
	return qi == len(query)
}

func hasASCIIPrefix(target, prefix string) bool {
	if len(target) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if toLowerASCII(target[i]) != toLowerASCII(prefix[i]) {
			return false
		}
	}
	return true
}

func FuzzFuzzyScore(f *testing.F) {
	f.Add("ok", "org-kanban")
	f.Add("Marlin", "marlin.plugin.reload")
	f.Add("", "anything")
	f.Add("zzz", "abc")
	f.Fuzz(func(t *testing.T, query, target string) {
		score, ok := FuzzyScore(query, target)
		want := isASCIISubsequence(query, target)
		if ok != want {
			t.Fatalf("FuzzyScore(%q, %q) ok=%v want %v", query, target, ok, want)
		}
		if ok && len(query) > 0 && len(query) < 64 {
			exactScore, exactOK := FuzzyScore(query, query)
			if !exactOK {
				t.Fatalf("exact self-match failed for %q", query)
			}
			if len(target) > len(query) && hasASCIIPrefix(target, query) {
				// Same prefix + trailing chars must not outscore exact self-match.
				if score > exactScore {
					t.Fatalf("prefix+suffix %q (%d) outscored exact %q (%d)", target, score, query, exactScore)
				}
			}
		}
	})
}
