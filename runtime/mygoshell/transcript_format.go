// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

func (e *TranscriptEntry) refreshLabels() {
	if e.Title != "" {
		e.headerLabel = string(e.Role) + " · " + e.Title
	} else {
		e.headerLabel = string(e.Role)
	}
	if e.OutcomeLabel != "" {
		e.outcomeBadge = "[" + e.OutcomeLabel + "]"
	} else {
		e.outcomeBadge = ""
	}
	if e.Collapsible {
		e.foldKey = "fold-" + e.ID
		e.collapseLabel = "Collapse " + e.ID
		e.expandLabel = "Expand " + e.ID
		if e.Role == RoleThinking {
			e.foldedSummary = fmt.Sprintf("Thought · %d chars (click to expand)", utf8.RuneCountInString(e.Text))
		} else {
			e.foldedSummary = fmt.Sprintf("(folded · %d chars)", utf8.RuneCountInString(e.Text))
		}
	} else {
		e.foldKey = ""
		e.collapseLabel = ""
		e.expandLabel = ""
		e.foldedSummary = ""
	}
	if e.ToolCard != nil && e.ToolCard.ReceiptID != "" {
		e.receiptBadge = "RECEIPT: " + e.ToolCard.ReceiptID
	} else {
		e.receiptBadge = ""
	}
	if e.ToolCard != nil && e.ToolCard.DenialMessage != "" {
		e.denialBadge = "DENIED: " + e.ToolCard.DenialMessage
	} else {
		e.denialBadge = ""
	}
	if e.Cancellation != nil {
		rc := e.Cancellation
		e.cancelBadge = fmt.Sprintf("CANCELLATION RECEIPT · status=%s completed=%v tracker_closed=%v tasks=%d->%d",
			rc.Status, rc.Completed, rc.TrackerClosed, rc.TrackedTasks, rc.RemainingTasks)
	} else {
		e.cancelBadge = ""
	}
	if strings.Contains(e.Text, "```") {
		e.segments = parseFencedCodeSegments(e.ID, e.Text)
	} else {
		e.segments = nil
	}
}

func parseFencedCodeSegments(entryID, text string) []TranscriptSegment {
	var out []TranscriptSegment
	lines := strings.Split(text, "\n")
	var buf strings.Builder
	inCode := false
	lang := ""
	codeIdx := 0

	flushProse := func() {
		s := strings.TrimSpace(buf.String())
		buf.Reset()
		if s != "" {
			out = append(out, TranscriptSegment{IsCode: false, Content: s})
		}
	}
	flushCode := func() {
		s := strings.TrimRight(buf.String(), "\n")
		buf.Reset()
		l := strings.ToUpper(strings.TrimSpace(lang))
		if l == "" {
			l = "CODE"
		}
		codeIdx++
		out = append(out, TranscriptSegment{
			IsCode:  true,
			Lang:    l,
			Content: s,
			CopyKey: fmt.Sprintf("copy-%s-%d", entryID, codeIdx),
		})
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if !inCode {
				flushProse()
				inCode = true
				lang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			} else {
				flushCode()
				inCode = false
				lang = ""
			}
			continue
		}
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(line)
	}
	if inCode {
		flushCode()
	} else {
		flushProse()
	}
	return out
}

func formatToolCardBody(card ToolCallCardView) string {
	var parts []string
	if card.Kind != "" {
		parts = append(parts, fmt.Sprintf("[%s] %s", card.Kind, card.ToolName))
	} else if card.ToolName != "" {
		parts = append(parts, card.ToolName)
	}
	if card.Summary != "" {
		parts = append(parts, card.Summary)
	}
	if card.Detail != "" {
		parts = append(parts, card.Detail)
	}
	return strings.Join(parts, "\n")
}

// estimateWrappedVisualLines counts wrapped lines in O(len(text)) with zero heap
// allocations using Unicode display column width (2 columns for CJK/East Asian
// wide runes, 0 columns for combining marks/format runes, 1 column otherwise)
// so long CJK responses do not clip.
func estimateWrappedVisualLines(text string, cols int) int {
	if cols <= 0 {
		cols = defaultTranscriptWrapCols
	}
	lines := 0
	colWidth := 0
	for _, r := range text {
		if r == '\n' {
			if colWidth == 0 {
				lines++
			} else {
				lines += (colWidth + cols - 1) / cols
				colWidth = 0
			}
			continue
		}
		colWidth += displayRuneWidth(r)
	}
	if colWidth == 0 {
		lines++
	} else {
		lines += (colWidth + cols - 1) / cols
	}
	if lines < 1 {
		return 1
	}
	return lines
}

// displayRuneWidth estimates terminal/monospace display columns (0 for combining
// marks & format runes; 2 for CJK, Hangul, Kana, and Fullwidth forms/punctuation;
// 1 otherwise) with zero allocations.
func displayRuneWidth(r rune) int {
	if r < 0x0300 {
		return 1
	}
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
		return 0
	}
	if r < 0x1100 {
		return 1
	}
	if (r >= 0x1100 && r <= 0x115F) || // Hangul Jamo
		(r >= 0x2E80 && r <= 0xA4CF) || // CJK Radicals, Punctuation (0x3000..0x303F), Kana, Bopomofo, Han
		(r >= 0xAC00 && r <= 0xD7A3) || // Hangul Syllables
		(r >= 0xF900 && r <= 0xFAFF) || // CJK Compatibility Ideographs
		(r >= 0xFE10 && r <= 0xFE6F) || // CJK Compatibility Forms
		(r >= 0xFF00 && r <= 0xFF60) || // Fullwidth Forms
		(r >= 0xFFE0 && r <= 0xFFE6) ||
		(r >= 0x20000 && r <= 0x3FFFD) { // CJK Extension B..I
		return 2
	}
	return 1
}
