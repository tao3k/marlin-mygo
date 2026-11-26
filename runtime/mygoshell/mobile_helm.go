// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/search"
)

// OpenHelmSearch opens the zero-overflow compact Mobile Helm Search drawer.
func (m *MobileCompanionApp) OpenHelmSearch() {
	if m == nil || m.HelmSearch == nil {
		return
	}
	m.mu.Lock()
	m.helmOpen = true
	m.mu.Unlock()
}

// CloseHelmSearch dismisses the compact Mobile Helm Search drawer.
func (m *MobileCompanionApp) CloseHelmSearch() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.helmOpen = false
	m.mu.Unlock()
}

// IsHelmSearchOpen reports whether the compact Mobile Helm Search drawer is open.
func (m *MobileCompanionApp) IsHelmSearchOpen() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.helmOpen
}

// renderMobileHelmSheet paints a phone-native (375-412px zero-overflow) Helm
// live search drawer backed by the exact same 120Hz zero-alloc HelmEngine.
func (m *MobileCompanionApp) renderMobileHelmSheet(c *ui.Context, dt DerivedTheme) {
	if m == nil || m.HelmSearch == nil || !m.IsHelmSearchOpen() {
		return
	}
	h := m.HelmSearch
	h.mu.Lock()
	query := h.queryDraft
	prevQuery := h.lastQuery
	queryRev := h.queryRev
	rows := h.rowScratch
	srcs := h.srcScratch
	h.mu.Unlock()

	h.Engine.SetViewportRows(9)
	rows, totalRows, totalHits, scrollRow, _, gen := h.Engine.VisibleWindow(rows)
	srcs = h.Engine.SourceSummaries(srcs)
	activeFilter := h.Engine.ActiveSourceFilter()
	markedCount := h.Engine.MarkedCount()

	var (
		clickedFlatRow = -1
		filterSource   *string
		jumpSrcDelta   int
		toggleMarkNow  bool
		runAction      search.TargetAction
		closeNow       bool
	)

	ui.Column(c.Key("mob-helm-overlay")).Absolute().Top(0).Right(0).Bottom(0).Left(0).ZIndex(935).
		Background(toColor(dt.Canvas)).Padding(12, 12, 16, 12).Gap(8).AlignItems(ui.Stretch).Clip().Children(func() {
		// Row 1: Compact title + hit badge + Done button.
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			AppleBadgePill(c, "HELM", dt.Accent, dt)
			AppleBadgePill(c, fmt.Sprintf("%d hits", totalHits), dt.Info, dt)
			if markedCount > 0 {
				AppleBadgePill(c, fmt.Sprintf("Mark %d", markedCount), dt.Warning, dt)
			}
			ui.Spacer(c)
			if ApplePillButton(c, "mhelm-close", "Done", BtnGlass, dt, nil, true).Clicked() {
				closeNow = true
			}
		})

		// Row 2: Live query input.
		ui.Row(c).Padding(7, 10).Radius(14).AlignItems(ui.Center).
			Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
			ui.TextInputBase(c.Key(fmt.Sprintf("mhelm-in-%d", queryRev)), &query).
				Placeholder("Search files, buffers, turns, commands...").
				FontSize(13).TextColor(toColor(dt.Ink)).Grow(1).AutoFocus()
		})

		// Row 3: Compact source filter pills (capped to fit 393px cleanly).
		if len(srcs) > 0 {
			ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
				allVar := BtnGlass
				if activeFilter == "" {
					allVar = BtnTinted
				}
				if ApplePillButton(c, "mhelm-src-all", "All", allVar, dt, nil, true).Clicked() {
					empty := ""
					filterSource = &empty
				}
				limit := min(len(srcs), 3)
				for i := 0; i < limit; i++ {
					s := srcs[i]
					v := BtnGlass
					if activeFilter == s.ID {
						v = BtnTinted
					}
					if ApplePillButton(c, "mhelm-src-"+s.ID, s.Label, v, dt, nil, true).Clicked() {
						id := s.ID
						filterSource = &id
					}
				}
			})
		}

		// Row 4: Virtualized single-column candidate list with MatchMask highlights.
		ui.Column(c.Key("mhelm-list")).Grow(1).MinWidth(0).Gap(4).Padding(6).Radius(12).
			Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).AlignItems(ui.Stretch).Clip().Children(func() {
			if len(rows) == 0 {
				ui.Text(c, "No matching hits yet.").FontSize(12).TextColor(toColor(dt.InkMuted)).Padding(10, 6)
			} else {
				for i := range rows {
					r := rows[i]
					if r.IsHeader {
						ui.Textf(c, "%s (%d)", strings.ToUpper(r.SourceLabel), r.SourceCount).
							FontSize(10).FontWeight(700).LetterSpacing(0.4).TextColor(toColor(dt.Accent)).Padding(4, 4, 1, 4)
						continue
					}
					flat := r.FlatIndex
					bg := toColor(dt.Surface)
					border := glassBorderColor(dt)
					if r.Selected {
						bg = rgbaFromRGB(dt.Accent, 0.16)
						border = rgbaFromRGB(dt.Accent, 0.55)
					}
					rowBox := ui.Row(c.Key(fmt.Sprintf("mhelm-row-%d", i))).
						Padding(6, 8).Gap(5).Radius(9).AlignItems(ui.Center).
						Background(bg).Border(1, border)
					if rowBox.Clicked() {
						clickedFlatRow = flat
					}
					rowBox.Children(func() {
						if r.Marked {
							AppleBadgePill(c, "[x]", dt.Warning, dt)
						}
						if r.Candidate.MatchMask != 0 {
							h.spanScratch = search.SplitHighlightSpans(r.Candidate.Title, r.Candidate.MatchMask, h.spanScratch)
							ui.Row(c).AlignItems(ui.Center).Children(func() {
								for _, sp := range h.spanScratch {
									if sp.Matched {
										ui.Text(c, sp.Text).FontSize(12.5).FontWeight(800).TextColor(toColor(dt.Accent))
									} else {
										ui.Text(c, sp.Text).FontSize(12.5).FontWeight(600).TextColor(toColor(dt.Ink))
									}
								}
							})
						} else {
							ui.Text(c, r.Candidate.Title).FontSize(12.5).FontWeight(600).TextColor(toColor(dt.Ink)).MaxLines(1)
						}
					})
				}
				if totalRows > len(rows) {
					ui.Textf(c, "Rows %d-%d of %d · gen #%d", scrollRow+1, scrollRow+len(rows), totalRows, gen).
						FontSize(10).TextColor(toColor(dt.InkMuted)).Padding(3, 4, 0, 4)
				}
			}
		})

		// Row 5: Compact thumb-friendly action bar (fits 393px with zero overflow).
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			if ApplePillButton(c, "mhelm-open", "Open", BtnPrimary, dt, nil, true).Clicked() {
				runAction = search.ActionOpenInFocused
			}
			if ApplePillButton(c, "mhelm-mention", "Attach @", BtnTinted, dt, nil, true).Clicked() {
				runAction = search.ActionMentionInComposer
			}
			ui.Spacer(c)
			if ApplePillButton(c, "mhelm-src", "Next Src", BtnGlass, dt, nil, true).Clicked() {
				jumpSrcDelta = +1
			}
			if ApplePillButton(c, "mhelm-mark", "Mark", BtnGlass, dt, nil, true).Clicked() {
				toggleMarkNow = true
			}
		})
	})

	h.mu.Lock()
	h.rowScratch = rows
	h.srcScratch = srcs
	h.queryDraft = query
	queryChanged := query != prevQuery
	if queryChanged {
		h.lastQuery = query
	}
	onQuery := h.OnQueryChanged
	onExec := h.OnExecuteAction
	h.mu.Unlock()

	if queryChanged {
		newGen := h.Engine.BeginQuery(query)
		if onQuery != nil {
			onQuery(newGen, query)
		}
	}
	if filterSource != nil {
		h.Engine.SetSourceFilter(*filterSource)
	}
	if clickedFlatRow >= 0 {
		_, _ = h.Engine.SelectFlatRow(clickedFlatRow)
	}
	if jumpSrcDelta != 0 {
		_, _ = h.Engine.JumpSource(jumpSrcDelta)
	}
	if toggleMarkNow {
		_, _ = h.Engine.ToggleMarkCurrent()
	}
	if runAction != "" {
		h.mu.Lock()
		targets := h.Engine.ResolveActionTargets(h.actScratch)
		h.actScratch = targets
		copied := append([]search.Candidate(nil), targets...)
		h.mu.Unlock()
		if len(copied) > 0 && onExec != nil {
			onExec(runAction, copied)
		}
		m.CloseHelmSearch()
	} else if closeNow {
		m.CloseHelmSearch()
	}
}
