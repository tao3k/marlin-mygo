// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package search

import "unicode"

// ScoreAndMask performs zero-allocation subsequence fuzzy scoring of query
// against text and returns (score, matchMask, matched). Bits 0..63 of
// matchMask mark matched byte/rune positions in text (up to first 64 runes)
// so 120Hz Helm renderers can highlight hits without re-scanning strings.
func ScoreAndMask(query, text string) (score int32, mask uint64, matched bool) {
	if query == "" {
		return 1, 0, true
	}
	qRunes := []rune(query)
	if len(qRunes) == 0 {
		return 1, 0, true
	}
	qi := 0
	qCur := unicode.ToLower(qRunes[0])
	consecutive := int32(0)
	prevIdx := -2
	runeIdx := 0

	for _, r := range text {
		lr := unicode.ToLower(r)
		if lr == qCur {
			if runeIdx < 64 {
				mask |= uint64(1) << uint(runeIdx)
			}
			// Bonus for word boundary or prefix start.
			if runeIdx == 0 {
				score += 24
			} else if prevIdx == runeIdx-1 {
				consecutive++
				score += 12 + consecutive*4
			} else {
				consecutive = 0
				score += 6
			}
			prevIdx = runeIdx
			qi++
			if qi == len(qRunes) {
				// Mild preference for tighter/shorter targets.
				penalty := int32(runeIdx)
				if penalty > 40 {
					penalty = 40
				}
				score = score + 50 - penalty
				if score < 1 {
					score = 1
				}
				return score, mask, true
			}
			qCur = unicode.ToLower(qRunes[qi])
		}
		runeIdx++
	}
	return 0, 0, false
}

// HighlightSpan represents one contiguous matched or unmatched slice of a title.
type HighlightSpan struct {
	Text    string
	Matched bool
}

// SplitHighlightSpans splits title into at most 12 contiguous matched/unmatched
// spans using mask (appending into caller-owned dst for minimal allocations).
func SplitHighlightSpans(title string, mask uint64, dst []HighlightSpan) []HighlightSpan {
	dst = dst[:0]
	if title == "" {
		return dst
	}
	if mask == 0 {
		return append(dst, HighlightSpan{Text: title, Matched: false})
	}
	startByte := 0
	curMatched := (mask & 1) != 0
	runeIdx := 0
	for bytePos := range title {
		m := runeIdx < 64 && ((mask>>uint(runeIdx))&1) != 0
		if runeIdx > 0 && m != curMatched {
			dst = append(dst, HighlightSpan{Text: title[startByte:bytePos], Matched: curMatched})
			startByte = bytePos
			curMatched = m
			if len(dst) >= 11 {
				dst = append(dst, HighlightSpan{Text: title[startByte:], Matched: curMatched})
				return dst
			}
		}
		runeIdx++
	}
	if startByte < len(title) {
		dst = append(dst, HighlightSpan{Text: title[startByte:], Matched: curMatched})
	}
	return dst
}
