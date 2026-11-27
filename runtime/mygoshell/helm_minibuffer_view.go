// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/search"
)

// HelmMinibufferController drives Marlin's unified 120Hz Helm / Consult-style
// live search & minibuffer surface. It pairs a zero-allocation 100k-hit
// search.HelmEngine with reusable render scratch slices, non-destructive
// Overlay / BottomDock switching, Follow-Mode split preview, and multi-mark
// action dispatch into the user's WindowTree or Composer.
type HelmMinibufferController struct {
	Engine *search.HelmEngine

	mu          sync.Mutex
	queryDraft  string
	lastQuery   string
	queryRev    uint64
	lastPrevID  string
	rowScratch  []search.VisibleRow
	srcScratch  []search.SourceSummary
	actScratch  []search.Candidate
	spanScratch []search.HighlightSpan

	// Thin backend/host hooks (Rust search engine streams batches into Engine.IngestBatch).
	OnQueryChanged   func(gen uint64, query string)
	OnRequestPreview func(gen uint64, cand search.Candidate)
	OnExecuteAction  func(action search.TargetAction, targets []search.Candidate)
}

// NewHelmMinibufferController constructs a 120Hz Helm/Minibuffer controller.
func NewHelmMinibufferController(maxCandidates int) *HelmMinibufferController {
	eng := search.NewHelmEngine(maxCandidates)
	eng.SetViewportRows(12)
	return &HelmMinibufferController{
		Engine:     eng,
		rowScratch: make([]search.VisibleRow, 0, 16),
		srcScratch: make([]search.SourceSummary, 0, 8),
		actScratch: make([]search.Candidate, 0, 16),
	}
}

// SetQuery programmatically sets the minibuffer query, starts a fresh
// SearchGeneration (preempting stale backend streams in O(1)), and notifies
// OnQueryChanged.
func (h *HelmMinibufferController) SetQuery(q string) uint64 {
	h.mu.Lock()
	h.queryDraft = q
	h.lastQuery = q
	h.queryRev++
	cb := h.OnQueryChanged
	h.mu.Unlock()
	gen := h.Engine.BeginQuery(q)
	if cb != nil {
		cb(gen, q)
	}
	return gen
}

// Render paints the non-destructive Helm / Minibuffer surface as either a
// floating glass deck (DockOverlay) or a docked bottom drawer (DockBottom),
// rendering ONLY the ~12 visible rows out of 100,000+ streamed hits.
func (h *HelmMinibufferController) Render(
	c *ui.Context,
	dt DerivedTheme,
	viewportW float32,
	onClose func(),
) {
	if h == nil || h.Engine == nil {
		return
	}

	h.mu.Lock()
	query := h.queryDraft
	prevQuery := h.lastQuery
	queryRev := h.queryRev
	rows := h.rowScratch
	srcs := h.srcScratch
	h.mu.Unlock()

	rows, totalRows, totalHits, scrollRow, selRow, gen := h.Engine.VisibleWindow(rows)
	srcs = h.Engine.SourceSummaries(srcs)
	activeFilter := h.Engine.ActiveSourceFilter()
	dockMode := h.Engine.DockMode()
	previewOpen := h.Engine.PreviewOpen()
	markedCount := h.Engine.MarkedCount()
	_, staleDrops, _, _ := h.Engine.Stats()
	selCand, hasSel := h.Engine.Selected()
	_, prevTitle, prevBody := h.Engine.Preview()
	if prevTitle == "" && hasSel {
		prevTitle = selCand.Title
	}
	if prevBody == "" && hasSel {
		prevBody = selCand.Snippet
		if prevBody == "" {
			prevBody = selCand.Location
		}
	}

	var (
		clickedFlatRow = -1
		filterSource   *string
		moveDelta      int
		jumpSrcDelta   int
		scrollDelta    int
		toggleMarkNow  bool
		toggleDockNow  bool
		togglePrevNow  bool
		runAction      search.TargetAction
		closeNow       bool
	)

	// Direct Emacs / Helm keyboard shortcuts (C-n/C-p/Arrows, C-o next source, TAB mark, Enter/Cmd+Enter).
	if c.Shortcut(ui.Ctrl, ui.KeyN) || c.Shortcut(0, ui.KeyDown) {
		moveDelta = +1
	} else if c.Shortcut(ui.Ctrl, ui.KeyP) || c.Shortcut(0, ui.KeyUp) {
		moveDelta = -1
	} else if c.Shortcut(ui.Ctrl, ui.KeyO) {
		jumpSrcDelta = +1
	} else if c.Shortcut(0, ui.KeyTab) {
		toggleMarkNow = true
	} else if c.Shortcut(ui.Cmd, ui.KeyEnter) {
		runAction = search.ActionOpenSplitRight
	} else if c.Shortcut(0, ui.KeyEnter) {
		runAction = search.ActionOpenInFocused
	}

	showSplitPreview := previewOpen && (viewportW == 0 || viewportW >= 820)
	deckW := float32(780)
	if viewportW > 0 && viewportW < 840 {
		deckW = viewportW - 36
		if deckW < 340 {
			deckW = 340
		}
	}

	paintContent := func() {
		// 1. Header bar: title, live hit telemetry, marked counter, preview & dock toggles.
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			AppleBadgePill(c, "HELM SEARCH", dt.Accent, dt)
			AppleBadgePill(c, fmt.Sprintf("%d hits · gen #%d", totalHits, gen), dt.Info, dt)
			if staleDrops > 0 {
				AppleBadgePill(c, fmt.Sprintf("%d stale pruned", staleDrops), dt.InkMuted, dt)
			}
			if markedCount > 0 {
				AppleBadgePill(c, fmt.Sprintf("Marked: %d", markedCount), dt.Warning, dt)
			}
			ui.Spacer(c)
			dockLbl := "Dock Bottom"
			if dockMode == search.DockBottom {
				dockLbl = "Float Deck"
			}
			if ApplePillButton(c, "helm-dock-toggle", dockLbl, BtnGlass, dt, nil, true).Clicked() {
				toggleDockNow = true
			}
			prevVar := BtnGlass
			if previewOpen {
				prevVar = BtnTinted
			}
			if ApplePillButton(c, "helm-prev-toggle", "Preview", prevVar, dt, nil, true).Clicked() {
				togglePrevNow = true
			}
			if ApplePillButton(c, "helm-close", "Close", BtnGlass, dt, svgIconXMark, true).Clicked() {
				closeNow = true
			}
		})

		// 2. Unified search input + source filter pills.
		ui.Row(c).Padding(8, 12).Radius(14).Gap(8).AlignItems(ui.Center).
			Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
			ui.TextInputBase(c.Key(fmt.Sprintf("helm-input-%d", queryRev)), &query).
				Placeholder("Search buffers, files, Rust symbols, Org headings, commands... (C-o next source · TAB mark)").
				FontSize(13.5).TextColor(toColor(dt.Ink)).Grow(1).AutoFocus()
		})

		if len(srcs) > 0 {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				allVar := BtnGlass
				if activeFilter == "" {
					allVar = BtnTinted
				}
				if ApplePillButton(c, "helm-src-all", fmt.Sprintf("All (%d)", totalHits), allVar, dt, nil, true).Clicked() {
					empty := ""
					filterSource = &empty
				}
				limit := min(len(srcs), 6)
				for i := 0; i < limit; i++ {
					s := srcs[i]
					v := BtnGlass
					if activeFilter == s.ID {
						v = BtnTinted
					}
					if ApplePillButton(c, "helm-src-"+s.ID, fmt.Sprintf("%s (%d)", s.Label, s.Count), v, dt, nil, true).Clicked() {
						id := s.ID
						filterSource = &id
					}
				}
			})
		}

		// 3. Split body: virtualized candidate viewport (left) + Follow-Mode live preview (right).
		ui.Row(c).Gap(10).MinWidth(0).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c.Key("helm-list")).Grow(1).MinWidth(0).Gap(3).Padding(6).Radius(12).
				Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).AlignItems(ui.Stretch).Children(func() {
				if len(rows) == 0 {
					ui.Text(c, "No matching candidates yet — Rust search engine streams live as you type.").
						FontSize(12).TextColor(toColor(dt.InkMuted)).Padding(12, 8)
				} else {
					for i := range rows {
						r := rows[i]
						if r.IsHeader {
							ui.Row(c).Padding(4, 6, 2, 6).Gap(6).AlignItems(ui.Center).Children(func() {
								ui.Text(c, strings.ToUpper(r.SourceLabel)).FontSize(10).FontWeight(700).LetterSpacing(0.5).TextColor(toColor(dt.Accent))
								AppleBadgePill(c, fmt.Sprintf("%d", r.SourceCount), dt.InkMuted, dt)
							})
							continue
						}
						flat := r.FlatIndex
						bg := toColor(dt.Surface)
						border := glassBorderColor(dt)
						if r.Selected {
							bg = rgbaFromRGB(dt.Accent, 0.16)
							border = rgbaFromRGB(dt.Accent, 0.55)
						}
						rowBox := ui.Row(c.Key(fmt.Sprintf("helm-row-%d", i))).
							Padding(5, 8).Gap(6).Radius(8).AlignItems(ui.Center).
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
							ui.Spacer(c)
							if r.Candidate.Location != "" {
								ui.Text(c, r.Candidate.Location).FontSize(10.5).TextColor(toColor(dt.InkMuted)).MaxLines(1)
							}
						})
					}
					if totalRows > len(rows) {
						ui.Row(c).Padding(4, 6, 0, 6).Gap(6).AlignItems(ui.Center).Children(func() {
							ui.Textf(c, "Window %d-%d of %d rows (sel #%d)", scrollRow+1, scrollRow+len(rows), totalRows, selRow+1).
								FontSize(10.5).TextColor(toColor(dt.InkMuted))
							ui.Spacer(c)
							if ApplePillButton(c, "helm-pgup", "PgUp", BtnGlass, dt, nil, true).Clicked() {
								scrollDelta = -10
							}
							if ApplePillButton(c, "helm-pgdn", "PgDn", BtnGlass, dt, nil, true).Clicked() {
								scrollDelta = 10
							}
						})
					}
				}
			})

			if showSplitPreview {
				ui.Column(c.Key("helm-preview")).Width(250).MinWidth(200).Padding(10).Gap(6).Radius(12).
					Background(toColor(dt.SurfaceElevated)).Border(1, glassBorderColor(dt)).AlignItems(ui.Stretch).Children(func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						AppleBadgePill(c, "LIVE PREVIEW", dt.Info, dt)
						if hasSel && selCand.PayloadKind != "" {
							AppleBadgePill(c, selCand.PayloadKind, dt.InkMuted, dt)
						}
					})
					if prevTitle != "" {
						ui.Text(c, prevTitle).FontSize(12.5).FontWeight(700).TextColor(toColor(dt.Ink))
					}
					if hasSel && selCand.Location != "" {
						ui.Text(c, selCand.Location).FontSize(10.5).TextColor(toColor(dt.InkMuted))
					}
					if prevBody != "" {
						ui.Text(c, prevBody).FontSize(11.5).LineHeight(1.42).TextColor(toColor(dt.InkSecondary))
					} else {
						ui.Text(c, "Select any candidate to inspect live preview without disturbing your WindowTree.").
							FontSize(11.5).TextColor(toColor(dt.InkMuted))
					}
				})
			}
		})

		// 4. Non-destructive multi-window & composer action footer.
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			if ApplePillButton(c, "helm-act-open", "Open", BtnPrimary, dt, nil, true).Clicked() {
				runAction = search.ActionOpenInFocused
			}
			if ApplePillButton(c, "helm-act-split", "Split Right", BtnTinted, dt, nil, true).Clicked() {
				runAction = search.ActionOpenSplitRight
			}
			if ApplePillButton(c, "helm-act-tab", "New Tab", BtnGlass, dt, nil, true).Clicked() {
				runAction = search.ActionOpenInNewTab
			}
			if ApplePillButton(c, "helm-act-mention", "Attach @", BtnGlass, dt, nil, true).Clicked() {
				runAction = search.ActionMentionInComposer
			}
			ui.Spacer(c)
			if ApplePillButton(c, "helm-nav-prev", "Prev", BtnGlass, dt, nil, true).Clicked() {
				moveDelta = -1
			}
			if ApplePillButton(c, "helm-nav-next", "Next", BtnGlass, dt, nil, true).Clicked() {
				moveDelta = +1
			}
			if ApplePillButton(c, "helm-nav-src", "Next Source", BtnGlass, dt, nil, true).Clicked() {
				jumpSrcDelta = +1
			}
			if ApplePillButton(c, "helm-act-mark", "Mark", BtnGlass, dt, nil, true).Clicked() {
				toggleMarkNow = true
			}
		})
	}

	if dockMode == search.DockBottom {
		ui.Column(c.Key("helm-dock-backdrop")).Absolute().Right(12).Bottom(12).Left(12).ZIndex(940).
			Padding(12).Gap(8).Radius(16).
			Background(toColor(dt.Surface)).Border(1, rgbaFromRGB(dt.Accent, 0.45)).
			AlignItems(ui.Stretch).Children(paintContent)
	} else {
		ui.Column(c.Key("helm-overlay-backdrop")).Absolute().Top(0).Right(0).Bottom(0).Left(0).ZIndex(940).
			Background(ui.RGBA(0, 0, 0, 0.48)).AlignItems(ui.Center).Justify(ui.Center).Padding(18).Children(func() {
			ui.Column(c.Key("helm-overlay-card")).Width(deckW).Padding(14).Gap(10).Radius(18).
				Background(toColor(dt.Surface)).Border(1, glassBorderColor(dt)).
				AlignItems(ui.Stretch).Children(paintContent)
		})
	}

	// Persist reused scratch slices and apply state transitions outside layout closures.
	h.mu.Lock()
	h.rowScratch = rows
	h.srcScratch = srcs
	h.queryDraft = query
	queryChanged := query != prevQuery
	if queryChanged {
		h.lastQuery = query
	}
	onQuery := h.OnQueryChanged
	onPrev := h.OnRequestPreview
	onExec := h.OnExecuteAction
	lastPrevID := h.lastPrevID
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
	if moveDelta != 0 {
		_, _ = h.Engine.MoveSelection(moveDelta)
	}
	if jumpSrcDelta != 0 {
		_, _ = h.Engine.JumpSource(jumpSrcDelta)
	}
	if scrollDelta != 0 {
		h.Engine.ScrollBy(scrollDelta)
	}
	if toggleMarkNow {
		_, _ = h.Engine.ToggleMarkCurrent()
	}
	if toggleDockNow {
		h.Engine.ToggleDockMode()
	}
	if togglePrevNow {
		h.Engine.TogglePreview()
	}

	// Follow-mode preview trigger (only when highlighted candidate ID changes).
	if onPrev != nil && h.Engine.PreviewOpen() {
		if cur, ok := h.Engine.Selected(); ok && cur.ID != "" && cur.ID != lastPrevID {
			h.mu.Lock()
			h.lastPrevID = cur.ID
			h.mu.Unlock()
			onPrev(h.Engine.Generation(), cur)
		}
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
		if onClose != nil {
			onClose()
		}
	} else if closeNow && onClose != nil {
		onClose()
	}
}
