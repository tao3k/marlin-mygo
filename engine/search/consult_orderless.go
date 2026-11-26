// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package search

import (
	"strings"
	"unicode"
)

// ConsultQuery splits a modern Consult/Orderless prompt on '#' into an async
// Rust backend query (supporting Rust's full multi-axis playbook qualifiers
// `@lang:`, `@doc:`, `@prefix:`, `@ext:`, `@sym:`, `@graph:`, and `(...)`
// `:gerbil-ascent` / Dusa DSL S-expressions) plus instant local Orderless
// refinement tokens (including '!negation'), matching marlin-agent-core MAR-HELM-R02.
type ConsultQuery struct {
	Raw          string
	BackendQuery string
	Pattern      string
	IsDSL        bool
	Languages    []string
	Documents    []string
	PathPrefix   string
	Extension    string
	Symbol       string
	GraphTarget  string
	Positive     []string
	Negative     []string
	HasStageSep  bool
}

// ParseConsultQuery parses raw input into two-stage Consult + Rust multi-axis
// playbook qualifiers + Orderless tokens.
// Examples:
//   - "auth # !test session"            -> BackendQuery="auth", Positive=["session"], Negative=["test"]
//   - "@lang:rust @prefix:crates/ helm" -> Languages=["rust"], PathPrefix="crates/", Pattern="helm"
//   - "(query (symbol ?s))"             -> IsDSL=true, BackendQuery="(query (symbol ?s))"
func ParseConsultQuery(raw string) ConsultQuery {
	trimmed := strings.TrimSpace(raw)
	cq := ConsultQuery{Raw: trimmed}
	if trimmed == "" {
		return cq
	}
	if strings.HasPrefix(trimmed, "(") {
		cq.IsDSL = true
		cq.BackendQuery = trimmed
		cq.Pattern = trimmed
		return cq
	}

	left, right, hasSep := strings.Cut(trimmed, "#")
	cq.HasStageSep = hasSep
	if hasSep {
		cq.BackendQuery = strings.TrimSpace(left)
		cq.parseRustAxes(cq.BackendQuery)
		cq.Positive, cq.Negative = splitOrderlessTokens(right)
		return cq
	}

	pos, neg := splitOrderlessTokens(trimmed)
	cq.Negative = neg
	cq.BackendQuery = strings.Join(pos, " ")
	cq.parseRustAxes(cq.BackendQuery)
	if cq.Pattern != "" {
		cq.Positive = strings.Fields(cq.Pattern)
	}
	return cq
}

func (q *ConsultQuery) parseRustAxes(asyncRaw string) {
	var pat []string
	for _, tok := range strings.Fields(asyncRaw) {
		switch {
		case strings.HasPrefix(tok, "@lang:"):
			if v := strings.TrimPrefix(tok, "@lang:"); v != "" {
				q.Languages = append(q.Languages, v)
			}
		case strings.HasPrefix(tok, "@doc:"):
			if v := strings.TrimPrefix(tok, "@doc:"); v != "" {
				q.Documents = append(q.Documents, v)
			}
		case strings.HasPrefix(tok, "@prefix:"):
			if v := strings.TrimPrefix(tok, "@prefix:"); v != "" {
				q.PathPrefix = v
			}
		case strings.HasPrefix(tok, "@ext:"):
			if v := strings.TrimPrefix(tok, "@ext:"); v != "" {
				q.Extension = v
			}
		case strings.HasPrefix(tok, "@sym:"):
			if v := strings.TrimPrefix(tok, "@sym:"); v != "" {
				q.Symbol = v
			}
		case strings.HasPrefix(tok, "@graph:"):
			if v := strings.TrimPrefix(tok, "@graph:"); v != "" {
				q.GraphTarget = v
			}
		default:
			pat = append(pat, tok)
		}
	}
	q.Pattern = strings.Join(pat, " ")
}

// LocalFilterText returns the normalized positive Orderless tokens used for
// local match highlighting.
func (q ConsultQuery) LocalFilterText() string {
	if len(q.Positive) == 0 {
		return q.BackendQuery
	}
	return strings.Join(q.Positive, " ")
}

func splitOrderlessTokens(s string) (positive, negative []string) {
	for _, tok := range strings.Fields(s) {
		if strings.HasPrefix(tok, "!") {
			neg := strings.TrimSpace(strings.TrimPrefix(tok, "!"))
			if neg != "" {
				negative = append(negative, neg)
			}
			continue
		}
		positive = append(positive, tok)
	}
	return positive, negative
}

// ScoreOrderlessAndMask evaluates Consult/Orderless multi-token matching with
// '!negation' exclusions and returns a combined score and bitmask (bits 0..63
// over text runes) so 120Hz Vertico/Helm renderers can highlight every hit.
func ScoreOrderlessAndMask(rawQuery, text string) (score int32, mask uint64, matched bool) {
	cq := ParseConsultQuery(rawQuery)
	if len(cq.Positive) == 0 && len(cq.Negative) == 0 {
		if cq.BackendQuery == "" {
			return 1, 0, true
		}
		return ScoreAndMask(cq.BackendQuery, text)
	}

	lowerText := strings.ToLower(text)
	for _, neg := range cq.Negative {
		if strings.Contains(lowerText, strings.ToLower(neg)) {
			return 0, 0, false
		}
	}

	tokens := cq.Positive
	if len(tokens) == 0 && cq.BackendQuery != "" {
		tokens = strings.Fields(cq.BackendQuery)
	}
	if len(tokens) == 0 {
		return 1, 0, true
	}

	var totalScore int32
	var totalMask uint64
	for _, tok := range tokens {
		// Prefer contiguous substring match for sharper scoring & exact highlight mask.
		if subScore, subMask, ok := scoreSubstringAndMask(tok, text); ok {
			totalScore += subScore
			totalMask |= subMask
			continue
		}
		s, m, ok := ScoreAndMask(tok, text)
		if !ok {
			return 0, 0, false
		}
		totalScore += s
		totalMask |= m
	}
	if totalScore < 1 {
		totalScore = 1
	}
	return totalScore, totalMask, true
}

func scoreSubstringAndMask(token, text string) (score int32, mask uint64, ok bool) {
	tRunes := []rune(strings.ToLower(token))
	if len(tRunes) == 0 {
		return 1, 0, true
	}
	sRunes := []rune(text)
	if len(tRunes) > len(sRunes) {
		return 0, 0, false
	}
	for i := 0; i <= len(sRunes)-len(tRunes); i++ {
		match := true
		for j := 0; j < len(tRunes); j++ {
			if unicode.ToLower(sRunes[i+j]) != tRunes[j] {
				match = false
				break
			}
		}
		if match {
			for j := 0; j < len(tRunes); j++ {
				if idx := i + j; idx < 64 {
					mask |= uint64(1) << uint(idx)
				}
			}
			sc := int32(90 + len(tRunes)*8)
			if i == 0 {
				sc += 30
			} else if !unicode.IsLetter(sRunes[i-1]) && !unicode.IsDigit(sRunes[i-1]) {
				sc += 18
			}
			return sc, mask, true
		}
	}
	return 0, 0, false
}
