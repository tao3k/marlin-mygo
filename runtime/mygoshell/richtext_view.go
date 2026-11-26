// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/richtext"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// RichDocCallbacks wires optional interactive callbacks for pre-parsed
// Org-mode and Markdown Document IR without introducing any Go-side parser.
type RichDocCallbacks struct {
	DocKey         string
	FoldedHeadings map[string]bool
	LastCopiedKey  string
	OnCopyCode     func(copyKey, code string)
	OnToggleFold   func(headingKey string)
	OnToggleCheck  func(blockIdx, itemIdx int, nextCheck string)
}

// RenderRichBlocks renders pre-parsed Markdown and Emacs Org-mode blocks on
// MyGo's native GPU pipeline with zero per-frame text re-parsing.
func RenderRichBlocks(
	c *ui.Context,
	dt DerivedTheme,
	blocks []richtext.Block,
	lastCopiedKey string,
	onCopyCode func(copyKey, code string),
) {
	RenderRichBlocksInteractive(c, dt, blocks, RichDocCallbacks{
		LastCopiedKey: lastCopiedKey,
		OnCopyCode:    onCopyCode,
	})
}

// RenderRichBlocksInteractive renders pre-parsed Org-mode and Markdown Document
// IR with interactive heading outline folding, clickable TODO/checklist cycling,
// and adaptive wide-table layout.
func RenderRichBlocksInteractive(
	c *ui.Context,
	dt DerivedTheme,
	blocks []richtext.Block,
	cb RichDocCallbacks,
) {
	ui.Column(c).MinWidth(0).Gap(6).AlignItems(ui.Stretch).Children(func() {
		for idx := 0; idx < len(blocks); idx++ {
			b := blocks[idx]
			switch b.Kind {
			case richtext.BlockHeading:
				subEnd := richHeadingSubtreeEnd(blocks, idx)
				hasSub := subEnd > idx+1
				hKey := fmt.Sprintf("%s:h:%d", cb.DocKey, idx)
				folded := hasSub && cb.FoldedHeadings != nil && cb.FoldedHeadings[hKey]
				renderRichHeading(c, dt, b, hKey, hasSub, folded, cb.OnToggleFold)
				if folded {
					hidden := subEnd - (idx + 1)
					ui.Row(c).Padding(0, 0, 0, 18).Gap(6).AlignItems(ui.Center).Children(func() {
						AppleBadgePill(c, fmt.Sprintf("... %d hidden block(s)", hidden), dt.InkMuted, dt)
					})
					idx = subEnd - 1
				}
			case richtext.BlockOrgPlanning:
				renderOrgPlanning(c, dt, b)
			case richtext.BlockDrawer:
				renderOrgDrawer(c, dt, b)
			case richtext.BlockTable:
				renderRichTable(c, dt, b)
			case richtext.BlockQuote:
				renderRichQuote(c, dt, b)
			case richtext.BlockList:
				renderRichList(c, dt, cb.DocKey, idx, b, cb.OnToggleCheck)
			case richtext.BlockCode:
				renderRichCodeBlock(c, dt, b, cb.LastCopiedKey, cb.OnCopyCode)
			case richtext.BlockRule:
				ui.Box(c).Height(1).Background(toColor(dt.Border))
			default:
				renderInlineSpans(c, dt, b.Spans, b.RawText, 13, 400, toColor(dt.Ink))
			}
		}
	})
}

func richHeadingSubtreeEnd(blocks []richtext.Block, headIdx int) int {
	lvl := blocks[headIdx].Level
	if lvl <= 0 {
		lvl = 1
	}
	end := headIdx + 1
	for end < len(blocks) {
		if blocks[end].Kind == richtext.BlockHeading {
			nextLvl := blocks[end].Level
			if nextLvl <= 0 {
				nextLvl = 1
			}
			if nextLvl <= lvl {
				break
			}
		}
		end++
	}
	return end
}

func renderRichHeading(
	c *ui.Context,
	dt DerivedTheme,
	b richtext.Block,
	headingKey string,
	hasSubtree bool,
	folded bool,
	onToggleFold func(string),
) {
	size := float32(15.5)
	if b.Level == 2 {
		size = 14.5
	} else if b.Level >= 3 {
		size = 13.5
	}
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		if hasSubtree && onToggleFold != nil {
			glyph := "v"
			actionLbl := "Fold " + strings.TrimSpace(b.RawText)
			if folded {
				glyph = ">"
				actionLbl = "Expand " + strings.TrimSpace(b.RawText)
			}
			btn := ui.ButtonBase(c.Key("rich-fold-"+headingKey)).Label(actionLbl).
				Padding(2, 6).Radius(6).
				Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt))
			btn.Children(func() {
				ui.Text(c, glyph).FontSize(10.5).FontWeight(700).TextColor(toColor(dt.Accent))
			})
			if btn.Clicked() {
				onToggleFold(headingKey)
			}
		}
		if b.TodoWord != "" {
			AppleBadgePill(c, b.TodoWord, orgTodoTone(b.TodoWord, dt), dt)
		}
		if b.Priority != "" {
			AppleBadgePill(c, "[#"+b.Priority+"]", orgPriorityTone(b.Priority, dt), dt)
		}
		renderInlineSpans(c, dt, b.Spans, b.RawText, size, 700, toColor(dt.Ink))
		if len(b.Tags) > 0 {
			ui.Spacer(c)
			for _, tg := range b.Tags {
				AppleBadgePill(c, ":"+tg+":", dt.Info, dt)
			}
		}
	})
}

func renderOrgPlanning(c *ui.Context, dt DerivedTheme, b richtext.Block) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		for _, p := range b.Planning {
			tone := dt.Accent
			switch p.Keyword {
			case "DEADLINE":
				tone = dt.Warning
			case "CLOSED":
				tone = dt.Success
			}
			AppleBadgePill(c, p.Keyword+" "+p.Timestamp, tone, dt)
		}
	})
}

func renderOrgDrawer(c *ui.Context, dt DerivedTheme, b richtext.Block) {
	ui.Column(c).MinWidth(0).Padding(8, 10).Gap(4).Radius(10).
		Background(toColor(dt.SurfaceRecessed)).
		BorderWidth(0, 0, 0, 2).BorderColor(toColor(dt.Info)).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			AppleBadgePill(c, "ORG DRAWER", dt.Info, dt)
			ui.Text(c, ":"+b.DrawerName+":").FontSize(11).FontWeight(700).TextColor(toColor(dt.InkSecondary))
		})
		for _, row := range b.DrawerRows {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, ":"+row.Key+":").FontSize(11).FontWeight(700).TextColor(toColor(dt.Accent))
				ui.Text(c, row.Value).FontSize(11.5).TextColor(toColor(dt.Ink))
			})
		}
	})
}

func renderRichTable(c *ui.Context, dt DerivedTheme, b richtext.Block) {
	ui.Column(c).MinWidth(0).Radius(10).Clip().
		Background(toColor(dt.SurfaceRecessed)).
		Border(1, glassBorderColor(dt)).Children(func() {
		if len(b.Headers) >= 4 {
			ui.Row(c).Padding(4, 10).Gap(6).AlignItems(ui.Center).
				Background(rgbaFromRGB(dt.Info, 0.08)).Children(func() {
				AppleBadgePill(c, fmt.Sprintf("TABLE · %d cols × %d rows", len(b.Headers), len(b.Rows)), dt.Info, dt)
			})
		}
		// Header row
		ui.Row(c).MinWidth(0).Padding(6, 10).Gap(8).AlignItems(ui.Center).
			Background(rgbaFromRGB(dt.Accent, 0.12)).
			BorderWidth(0, 0, 1, 0).BorderColor(toColor(dt.Border)).Children(func() {
			for _, h := range b.Headers {
				ui.Text(c, h).FontSize(11.5).FontWeight(700).TextColor(toColor(dt.Ink)).Grow(1).MinWidth(0)
			}
		})
		// Data rows
		for rIdx, row := range b.Rows {
			rowElem := ui.Row(c).MinWidth(0).Padding(5, 10).Gap(8).AlignItems(ui.Center)
			if rIdx%2 == 1 {
				rowElem.Background(rgbaFromRGB(dt.Ink, 0.03))
			}
			if rIdx < len(b.Rows)-1 {
				rowElem.BorderWidth(0, 0, 1, 0).BorderColor(rgbaFromRGB(dt.Border, 0.6))
			}
			rowElem.Children(func() {
				colCount := max(len(b.Headers), len(row))
				for ci := 0; ci < colCount; ci++ {
					val := ""
					if ci < len(row) {
						val = row[ci]
					}
					ui.Text(c, val).FontSize(11.5).TextColor(toColor(dt.InkSecondary)).Grow(1).MinWidth(0)
				}
			})
		}
	})
}

func renderRichQuote(c *ui.Context, dt DerivedTheme, b richtext.Block) {
	tone := dt.Accent
	switch b.AlertKind {
	case "NOTE":
		tone = dt.Info
	case "TIP":
		tone = dt.Success
	case "IMPORTANT":
		tone = dt.Accent
	case "WARNING":
		tone = dt.Warning
	case "CAUTION":
		tone = dt.Danger
	}
	ui.Column(c).MinWidth(0).Padding(8, 12).Gap(4).Radius(10).
		Background(rgbaFromRGB(tone, 0.08)).
		BorderWidth(0, 0, 0, 3).BorderColor(toColor(tone)).Children(func() {
		if b.AlertKind != "" {
			AppleBadgePill(c, b.AlertKind, tone, dt)
		} else if b.IsOrg {
			AppleBadgePill(c, "ORG QUOTE", tone, dt)
		}
		if b.RawText != "" {
			renderInlineSpans(c, dt, b.Spans, b.RawText, 12.5, 400, toColor(dt.InkSecondary))
		}
	})
}

func nextChecklistState(cur string) string {
	switch cur {
	case "X":
		return " "
	default:
		return "X"
	}
}

func renderRichList(
	c *ui.Context,
	dt DerivedTheme,
	docKey string,
	blockIdx int,
	b richtext.Block,
	onToggleCheck func(blockIdx, itemIdx int, nextCheck string),
) {
	ui.Column(c).MinWidth(0).Gap(4).AlignItems(ui.Stretch).Children(func() {
		for itemIdx, item := range b.Items {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				isCheck := item.Check == "X" || item.Check == "-" || item.Check == " "
				if isCheck {
					badgeTxt := "[" + item.Check + "]"
					tone := dt.InkMuted
					if item.Check == "X" {
						tone = dt.Success
					} else if item.Check == "-" {
						tone = dt.Warning
					}
					if onToggleCheck != nil {
						btnKey := fmt.Sprintf("rich-chk-%s-%d-%d", docKey, blockIdx, itemIdx)
						lbl := fmt.Sprintf("Toggle %s %s", badgeTxt, strings.TrimSpace(item.RawText))
						btn := ui.ButtonBase(c.Key(btnKey)).Label(lbl).
							Padding(2, 6).Radius(6).
							Background(rgbaFromRGB(tone, 0.14)).Border(1, rgbaFromRGB(tone, 0.34))
						btn.Children(func() {
							ui.Text(c, badgeTxt).FontSize(11).FontWeight(700).TextColor(toColor(tone))
						})
						if btn.Clicked() {
							onToggleCheck(blockIdx, itemIdx, nextChecklistState(item.Check))
						}
					} else {
						AppleBadgePill(c, badgeTxt, tone, dt)
					}
				} else {
					marker := item.Marker
					if marker == "-" || marker == "+" {
						marker = "•"
					}
					ui.Text(c, marker).FontSize(12.5).FontWeight(700).TextColor(toColor(dt.Accent))
				}
				renderInlineSpans(c, dt, item.Spans, item.RawText, 12.5, 400, toColor(dt.Ink))
			})
		}
	})
}

func renderRichCodeBlock(
	c *ui.Context,
	dt DerivedTheme,
	b richtext.Block,
	lastCopiedKey string,
	onCopyCode func(copyKey, code string),
) {
	ui.Column(c).MinWidth(0).Padding(8, 10).Gap(5).Radius(10).
		Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			AppleBadgePill(c, b.Lang, dt.Info, dt)
			if b.IsOrg {
				AppleBadgePill(c, "ORG SRC", dt.Accent, dt)
			}
			ui.Spacer(c)
			if onCopyCode != nil && b.CopyKey != "" {
				copyLbl := "Copy Code"
				copyVar := BtnGlass
				if lastCopiedKey == b.CopyKey {
					copyLbl = "Copied"
					copyVar = BtnSuccess
				}
				if ApplePillButton(c, b.CopyKey, copyLbl, copyVar, dt, nil, true).Clicked() {
					onCopyCode(b.CopyKey, b.Code)
				}
			}
		})
		ui.Text(c, b.Code).FontSize(12).LineHeight(1.4).TextColor(toColor(dt.Ink))
	})
}

func renderInlineSpans(
	c *ui.Context,
	dt DerivedTheme,
	spans []richtext.InlineSpan,
	rawFallback string,
	fontSize float32,
	baseWeight int,
	baseColor ui.Color,
) {
	if len(spans) == 0 {
		ui.Text(c, rawFallback).FontSize(fontSize).FontWeight(baseWeight).LineHeight(1.45).TextColor(baseColor)
		return
	}
	if len(spans) == 1 && spans[0].Kind == richtext.SpanPlain {
		ui.Text(c, spans[0].Text).FontSize(fontSize).FontWeight(baseWeight).LineHeight(1.45).TextColor(baseColor)
		return
	}
	ui.Row(c).Gap(3).AlignItems(ui.Center).Children(func() {
		for _, sp := range spans {
			switch sp.Kind {
			case richtext.SpanBold:
				ui.Text(c, sp.Text).FontSize(fontSize).FontWeight(700).TextColor(baseColor)
			case richtext.SpanCode:
				ui.Row(c).Padding(1, 5).Radius(5).Background(toColor(dt.SurfaceRecessed)).
					Border(1, glassBorderColor(dt)).Children(func() {
					ui.Text(c, sp.Text).FontSize(fontSize - 1).FontWeight(600).TextColor(toColor(dt.Accent))
				})
			case richtext.SpanLink:
				ui.Text(c, sp.Text).FontSize(fontSize).FontWeight(600).TextColor(toColor(dt.Accent))
			default:
				txt := strings.TrimSpace(sp.Text)
				if txt != "" {
					ui.Text(c, txt).FontSize(fontSize).FontWeight(baseWeight).TextColor(baseColor)
				}
			}
		}
	})
}

func orgTodoTone(word string, dt DerivedTheme) schema.RGB {
	switch word {
	case "DONE":
		return dt.Success
	case "CANCELLED", "CANCELED":
		return dt.InkMuted
	case "NEXT", "IN-PROGRESS", "STARTED":
		return dt.Accent
	default:
		return dt.Warning
	}
}

func orgPriorityTone(priority string, dt DerivedTheme) schema.RGB {
	switch priority {
	case "A":
		return dt.Danger
	case "B":
		return dt.Warning
	default:
		return dt.Info
	}
}

func estimateRichBlockExtraPx(blocks []richtext.Block) uint32 {
	var extra uint32
	for _, b := range blocks {
		switch b.Kind {
		case richtext.BlockTable:
			extra += 28 + uint32(len(b.Rows))*10
		case richtext.BlockDrawer:
			extra += 26 + uint32(len(b.DrawerRows))*8
		case richtext.BlockCode:
			extra += 30
		case richtext.BlockQuote, richtext.BlockOrgPlanning, richtext.BlockHeading:
			extra += 14
		}
	}
	return extra
}

func isRichMarkupFilename(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".org") || strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown")
}

func formatRichPreviewHeader(name string, lineCount int) string {
	if strings.HasSuffix(strings.ToLower(name), ".org") {
		return fmt.Sprintf("ORG-MODE PREVIEW · %d lines", lineCount)
	}
	return fmt.Sprintf("MARKDOWN PREVIEW · %d lines", lineCount)
}
