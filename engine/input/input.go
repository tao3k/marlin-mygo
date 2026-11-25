// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package input provides O(chord_len) compiled multi-key prefix tries for
// Scheme Meow/Hydra keymaps and allocation-free fuzzy ranking for Cmd+K/M-x.
package input

import (
	"errors"
	"fmt"
	"slices"
	"unicode"
	"unicode/utf8"
)

var (
	ErrKeyConflict   = errors.New("input: conflicting key binding in trie")
	ErrEmptySequence = errors.New("input: key binding sequence must be non-empty")
)

type KeyScope uint8

const (
	ScopeGlobal KeyScope = iota + 1
	ScopeInsert
	ScopeNormal
)

type Binding struct {
	Scope       KeyScope
	Sequence    []string
	ActionKind  string
	CommandName string
}

type MatchKind uint8

const (
	MatchNone   MatchKind = iota
	MatchPrefix           // Valid prefix of a longer chord (trigger Hydra hint)
	MatchExact            // Complete chord matched
)

type trieNode struct {
	children map[string]*trieNode
	terminal *Binding
}

// KeyTrie compiles Scheme keymaps once at WorkbenchSnapshot admission so every
// keypress resolves in O(len(seq)) without scanning the binding list.
type KeyTrie struct {
	roots [4]*trieNode
}

func CompileKeyTrie(bindings []Binding) (*KeyTrie, error) {
	kt := &KeyTrie{}
	for i := range bindings {
		b := bindings[i]
		if len(b.Sequence) == 0 {
			return nil, ErrEmptySequence
		}
		if int(b.Scope) >= len(kt.roots) {
			return nil, fmt.Errorf("input: invalid key scope %d", b.Scope)
		}
		if kt.roots[b.Scope] == nil {
			kt.roots[b.Scope] = &trieNode{}
		}
		cur := kt.roots[b.Scope]
		for depth, tok := range b.Sequence {
			if cur.terminal != nil {
				return nil, fmt.Errorf("%w: %v shadowed by shorter prefix %v", ErrKeyConflict, b.Sequence, cur.terminal.Sequence)
			}
			if cur.children == nil {
				cur.children = make(map[string]*trieNode, 4)
			}
			next, ok := cur.children[tok]
			if !ok {
				next = &trieNode{}
				cur.children[tok] = next
			}
			cur = next
			if depth == len(b.Sequence)-1 {
				if cur.terminal != nil || len(cur.children) > 0 {
					return nil, fmt.Errorf("%w: duplicate or prefix conflict at %v", ErrKeyConflict, b.Sequence)
				}
				cp := b
				cur.terminal = &cp
			}
		}
	}
	return kt, nil
}

// Lookup resolves seq in activeMode first, then falls back to ScopeGlobal.
func (kt *KeyTrie) Lookup(activeMode KeyScope, seq []string) (MatchKind, Binding) {
	if activeMode != ScopeGlobal && activeMode != 0 {
		if kind, b := kt.lookupScope(activeMode, seq); kind != MatchNone {
			return kind, b
		}
	}
	return kt.lookupScope(ScopeGlobal, seq)
}

func (kt *KeyTrie) lookupScope(scope KeyScope, seq []string) (MatchKind, Binding) {
	if int(scope) >= len(kt.roots) || kt.roots[scope] == nil || len(seq) == 0 {
		return MatchNone, Binding{}
	}
	cur := kt.roots[scope]
	for _, tok := range seq {
		next, ok := cur.children[tok]
		if !ok {
			return MatchNone, Binding{}
		}
		cur = next
	}
	if cur.terminal != nil {
		return MatchExact, *cur.terminal
	}
	if len(cur.children) > 0 {
		return MatchPrefix, Binding{}
	}
	return MatchNone, Binding{}
}

// CommandEntry is one admitted Scheme or Marketplace command in the Cmd+K catalog.
type CommandEntry struct {
	Name        string
	Description string
}

type RankedCommand struct {
	Index int
	Score int32
}

// FuzzyScore computes a deterministic, zero-allocation subsequence match score
// rewarding exact matches, prefixes, word boundaries ('-', '_', '/', '.', CamelCase),
// and consecutive runs while lightly penalizing gaps and excess length.
// Pure-ASCII inputs take a byte-level fast path (zero allocations, as measured
// by the engine benchmarks); inputs containing non-ASCII runes fall back to a
// case-folded rune path so CJK command names rank like any other subsequence.
func FuzzyScore(query, target string) (int32, bool) {
	if len(query) == 0 {
		return 1, true
	}
	if isASCII(query) && isASCII(target) {
		return fuzzyScoreASCII(query, target)
	}
	return fuzzyScoreUnicode(query, target)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// fuzzyScoreASCII is the zero-allocation byte-level fast path over ASCII input.
func fuzzyScoreASCII(query, target string) (int32, bool) {
	qLen := len(query)
	tLen := len(target)
	if qLen > tLen {
		return 0, false
	}

	var score int32
	qi := 0
	prevMatch := -2
	consecutive := int32(0)

	for ti := 0; ti < tLen && qi < qLen; ti++ {
		tc := toLowerASCII(target[ti])
		qc := toLowerASCII(query[qi])
		if tc != qc {
			continue
		}
		score += 10
		if ti == 0 {
			score += 32 // prefix start bonus
		} else if isBoundary(target[ti-1], target[ti]) {
			score += 18 // segment / CamelCase boundary bonus
		}
		if ti == prevMatch+1 {
			consecutive++
			score += 12 * consecutive
		} else {
			if prevMatch >= 0 {
				gap := int32(ti - prevMatch - 1)
				score -= min(gap, 8)
			}
			consecutive = 0
		}
		prevMatch = ti
		qi++
	}
	if qi != qLen {
		return 0, false
	}
	if qLen == tLen {
		score += 64 // exact length match
	}
	score -= int32(tLen - qLen)
	return score, true
}

// fuzzyScoreUnicode is the rune-level case-folded path for non-ASCII input.
// Matching operates on folded runes, so CJK command names rank like any other
// subsequence; boundary bonuses still apply between a delimiter rune and an
// uppercase rune (CamelCase segments inside localized names).
func fuzzyScoreUnicode(query, target string) (int32, bool) {
	qRunes := []rune(query)
	tRunes := []rune(target)
	qLen := len(qRunes)
	tLen := len(tRunes)
	if qLen > tLen {
		return 0, false
	}

	var score int32
	qi := 0
	prevMatch := -2
	consecutive := int32(0)

	for ti := 0; ti < tLen && qi < qLen; ti++ {
		tc := unicode.ToLower(tRunes[ti])
		qc := unicode.ToLower(qRunes[qi])
		if tc != qc {
			continue
		}
		score += 10
		if ti == 0 {
			score += 32 // prefix start bonus
		} else if isBoundaryRune(tRunes[ti-1], tRunes[ti]) {
			score += 18 // segment / CamelCase boundary bonus
		}
		if ti == prevMatch+1 {
			consecutive++
			score += 12 * consecutive
		} else {
			if prevMatch >= 0 {
				gap := int32(ti - prevMatch - 1)
				score -= min(gap, 8)
			}
			consecutive = 0
		}
		prevMatch = ti
		qi++
	}
	if qi != qLen {
		return 0, false
	}
	if qLen == tLen {
		score += 64 // exact length match
	}
	score -= int32(tLen - qLen)
	return score, true
}

// RankCommands scores commands against query into caller-provided dst without
// per-candidate heap allocations.
func RankCommands(query string, commands []CommandEntry, dst []RankedCommand) []RankedCommand {
	out := dst[:0]
	for i := range commands {
		if s, ok := FuzzyScore(query, commands[i].Name); ok {
			out = append(out, RankedCommand{Index: i, Score: s})
		}
	}
	slices.SortFunc(out, func(a, b RankedCommand) int {
		if a.Score != b.Score {
			if a.Score > b.Score {
				return -1
			}
			return 1
		}
		la, lb := len(commands[a.Index].Name), len(commands[b.Index].Name)
		if la != lb {
			return la - lb
		}
		if commands[a.Index].Name < commands[b.Index].Name {
			return -1
		}
		if commands[a.Index].Name > commands[b.Index].Name {
			return 1
		}
		return 0
	})
	return out
}

func toLowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

func isBoundary(prev, cur byte) bool {
	if prev == '-' || prev == '_' || prev == '/' || prev == '.' || prev == ' ' {
		return true
	}
	return prev >= 'a' && prev <= 'z' && cur >= 'A' && cur <= 'Z'
}

func isBoundaryRune(prev, cur rune) bool {
	switch prev {
	case '-', '_', '/', '.', ' ':
		return true
	}
	return unicode.IsLower(prev) && unicode.IsUpper(cur)
}
