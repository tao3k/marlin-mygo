// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func formatDurationCompact(d time.Duration) string {
	if d <= 0 {
		return "0ms"
	}
	if d < time.Millisecond {
		return fmt.Sprintf("%.1fus", float64(d.Nanoseconds())/1000.0)
	}
	return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000.0)
}

// Render paints the built-in Tier-0 Marketplace drawer onto MyGo's GPU scene graph.
func (m *MarketplaceController) Render(c *ui.Context, t *ui.Theme, onClose func()) {
	m.renderMu.Lock()
	defer m.renderMu.Unlock()

	m.mu.RLock()
	stream := m.activeStream
	if stream == "" {
		stream = StreamPlugins
	}
	query := m.query
	rtFilter := m.runtimeFilter
	pageOffset := m.pageOffset
	statusMsg := m.statusBanner
	statusErr := m.statusIsErr
	profilerLine := m.profilerLine
	itemsVer := m.itemsVer
	themesVer := m.themesVer
	layoutsVer := m.layoutsVer
	var pendingEsc map[string]bool
	if len(m.pendingEscalation) > 0 {
		pendingEsc = make(map[string]bool, len(m.pendingEscalation))
		for k, v := range m.pendingEscalation {
			pendingEsc[k] = v
		}
	}
	if m.renderItemsVer != itemsVer || len(m.renderItems) != len(m.items) {
		m.renderItems = append(m.renderItems[:0], m.items...)
		m.renderItemsVer = itemsVer
	}
	if m.renderThemesVer != themesVer || len(m.renderThemes) != len(m.themes) {
		m.renderThemes = append(m.renderThemes[:0], m.themes...)
		m.renderThemesVer = themesVer
	}
	if m.renderLayoutsVer != layoutsVer || len(m.renderLayouts) != len(m.layouts) {
		m.renderLayouts = append(m.renderLayouts[:0], m.layouts...)
		m.renderLayoutsVer = layoutsVer
	}
	m.mu.RUnlock()

	// Reuse cached fuzzy-filtered indices whenever (query, rtFilter, itemsVer) are unchanged.
	if !m.cacheValid || m.cacheQuery != query || m.cacheRT != rtFilter || m.cacheItemsVer != itemsVer {
		m.matchedBuf = m.matchedBuf[:0]
		if strings.TrimSpace(query) == "" {
			for i := range m.renderItems {
				if rtFilter == "" || m.renderItems[i].Runtime == rtFilter {
					m.matchedBuf = append(m.matchedBuf, i)
				}
			}
		} else {
			m.cmdEntriesBuf = m.cmdEntriesBuf[:0]
			m.filteredIdxBuf = m.filteredIdxBuf[:0]
			for i := range m.renderItems {
				it := &m.renderItems[i]
				if rtFilter != "" && it.Runtime != rtFilter {
					continue
				}
				m.cmdEntriesBuf = append(m.cmdEntriesBuf, input.CommandEntry{
					Name:        it.PluginID,
					Description: it.Summary,
				})
				m.filteredIdxBuf = append(m.filteredIdxBuf, i)
			}
			m.rankedBuf = input.RankCommands(query, m.cmdEntriesBuf, m.rankedBuf[:0])
			for _, r := range m.rankedBuf {
				m.matchedBuf = append(m.matchedBuf, m.filteredIdxBuf[r.Index])
			}
		}
		m.cacheQuery = query
		m.cacheRT = rtFilter
		m.cacheItemsVer = itemsVer
		m.cacheValid = true
	}
	matched := m.matchedBuf

	// Bound visible cards per frame so 100+ plugin catalogs stay O(1) GPU memory and 120Hz-fluent.
	if pageOffset < 0 {
		pageOffset = 0
	}
	if len(matched) == 0 {
		pageOffset = 0
	} else if pageOffset >= len(matched) {
		pageOffset = ((len(matched) - 1) / maxVisibleMarketplaceCards) * maxVisibleMarketplaceCards
	}
	visibleEnd := min(len(matched), pageOffset+maxVisibleMarketplaceCards)
	visibleMatched := matched[pageOffset:visibleEnd]

	var (
		nextStream    MarketplaceStream
		clickedFilter *schema.PanelRuntimeKind
		nextPageOff   = pageOffset
		doRefresh     bool
		doSyncOff     bool
		doClose       bool
		doInstallID   string
		doUpgradeID   string
		armEscalateID string
		doRollbackID  string
		doDisableID   string
		doWakeID      string
		doSleepID     string
		applyTheme    *MarketplaceThemeItem
		applyLayout   *MarketplaceLayoutItem
	)

	dt := derivedThemeFromUITheme(t)
	officialOnly := m.OfficialOnly

	ui.Column(c).MinWidth(0).Gap(10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "MARLIN UI MARKETPLACE (HOST TIER-0)").FontSize(16).FontWeight(700).TextColor(toColor(dt.Ink))
			if officialOnly {
				AppleBadgePill(c, "OFFICIAL ONLY", dt.Success, dt)
			}
			ui.Spacer(c)
			if m.OnSyncOfficial != nil {
				if ApplePillButton(c, "mkt-sync-official", "Sync Official", BtnTinted, dt, nil, true).Clicked() {
					doSyncOff = true
				}
			}
			if m.OnRefresh != nil {
				if ApplePillButton(c, "mkt-refresh", "Refresh Catalog", BtnGlass, dt, nil, true).Clicked() {
					doRefresh = true
				}
			}
			if onClose != nil {
				if ApplePillButton(c, "mkt-close", "Close", BtnGlass, dt, svgIconXMark, true).Clicked() {
					doClose = true
				}
			}
		})

		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			chosenStream := AppleSegmentedControl(c, "mkt-stream", []SegmentedItem{
				{ID: string(StreamPlugins), Label: "Plugins"},
				{ID: string(StreamThemes), Label: "Themes"},
				{ID: string(StreamLayouts), Label: "Layouts"},
			}, string(stream), dt, true)
			if chosenStream != "" {
				nextStream = MarketplaceStream(chosenStream)
			}
			ui.Spacer(c)
			ui.Text(c, "tao3k/marlin-ui-marketplace · Ed25519 Verified").
				FontSize(11).TextColor(toColor(dt.InkMuted))
		})

		if profilerLine != "" && stream == StreamPlugins {
			ui.Text(c, profilerLine).FontSize(11).FontWeight(600).TextColor(toColor(dt.Accent))
		}

		if statusMsg != "" {
			col := toColor(dt.Accent)
			if statusErr {
				col = toColor(dt.Warning)
			}
			ui.Text(c, statusMsg).FontSize(12).FontWeight(600).TextColor(col)
		}

		switch stream {
		case StreamThemes:
			if len(m.renderThemes) == 0 {
				ui.Text(c, "No Official Marketplace themes loaded. Click Sync Official to fetch curated palettes.").
					FontSize(12).TextColor(toColor(dt.InkMuted))
			} else {
				ui.Column(c).Gap(8).Children(func() {
					for i := range m.renderThemes {
						th := m.renderThemes[i]
						admitted := !officialOnly || th.isOfficialAdmitted()
						AppleGlassCard(c, th.cardKey, dt, 16, 12, 6, func() {
							ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Text(c, th.titleLine).FontWeight(700).FontSize(13).TextColor(toColor(dt.Ink))
								ui.Spacer(c)
								tone := dt.Success
								if !admitted {
									tone = dt.Danger
								}
								AppleBadgePill(c, th.trustBadge, tone, dt)
							})
							if th.Summary != "" {
								ui.Text(c, th.Summary).FontSize(12).TextColor(toColor(dt.InkSecondary))
							}
							ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
								// 8-color semantic palette swatch preview bar.
								ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
									for _, sw := range th.swatches {
										ui.Box(c).Width(16).Height(16).Radius(4).
											Background(toColor(sw)).
											Border(1, glassBorderColor(dt))
									}
								})
								ui.Spacer(c)
								if !admitted {
									AppleBadgePill(c, "BLOCKED · OFFICIAL ONLY", dt.Danger, dt)
								} else if th.Active {
									AppleBadgePill(c, "ACTIVE THEME", dt.Success, dt)
								} else if m.OnApplyTheme != nil {
									if ApplePillButton(c, th.applyKey, th.applyLabel, BtnPrimary, dt, nil, true).Clicked() {
										cp := th
										applyTheme = &cp
									}
								}
							})
						})
					}
				})
			}

		case StreamLayouts:
			if len(m.renderLayouts) == 0 {
				ui.Text(c, "No Official Marketplace Emacs layouts loaded. Click Sync Official to fetch workspace specs.").
					FontSize(12).TextColor(toColor(dt.InkMuted))
			} else {
				ui.Column(c).Gap(8).Children(func() {
					for i := range m.renderLayouts {
						ly := m.renderLayouts[i]
						admitted := !officialOnly || ly.isOfficialAdmitted()
						AppleGlassCard(c, ly.cardKey, dt, 16, 12, 6, func() {
							ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Text(c, ly.titleLine).FontWeight(700).FontSize(13).TextColor(toColor(dt.Ink))
								ui.Spacer(c)
								tone := dt.Success
								if !admitted {
									tone = dt.Danger
								}
								AppleBadgePill(c, ly.trustBadge, tone, dt)
							})
							if ly.Summary != "" {
								ui.Text(c, ly.Summary).FontSize(12).TextColor(toColor(dt.InkSecondary))
							}
							ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Text(c, ly.topologyStr).FontSize(11).TextColor(toColor(dt.InkMuted))
								ui.Spacer(c)
								if !admitted {
									AppleBadgePill(c, "BLOCKED · OFFICIAL ONLY", dt.Danger, dt)
								} else if ly.Active {
									AppleBadgePill(c, "ACTIVE LAYOUT", dt.Success, dt)
								} else if m.OnApplyLayout != nil {
									if ApplePillButton(c, ly.applyKey, ly.applyLabel, BtnPrimary, dt, nil, true).Clicked() {
										cp := ly
										applyLayout = &cp
									}
								}
							})
						})
					}
				})
			}

		default:
			// Zero-alloc fuzzy search pill + Apple Segmented runtime filter.
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Grow(1).Padding(7, 12).Radius(999).
					Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
					ui.TextInputBase(c.Key("mkt-search"), &query).
						Placeholder("Search official plugins (fuzzy)...").
						FontSize(13).TextColor(toColor(dt.Ink)).Grow(1)
				})
				chosenRT := AppleSegmentedControl(c, "mkt-rt", []SegmentedItem{
					{ID: "all", Label: "All"},
					{ID: string(schema.RuntimeWasmUIIR), Label: "Go WASI"},
					{ID: string(schema.RuntimeWebViewUntrusted), Label: "TS WebView"},
					{ID: string(schema.RuntimeDeclarative), Label: "Declarative"},
				}, func() string {
					if rtFilter == "" {
						return "all"
					}
					return string(rtFilter)
				}(), dt, true)
				if chosenRT != "" {
					var k schema.PanelRuntimeKind
					if chosenRT != "all" {
						k = schema.PanelRuntimeKind(chosenRT)
					}
					clickedFilter = &k
				}
			})

			if len(matched) == 0 {
				ui.Text(c, "No matching Marketplace plugins found.").FontSize(12).TextColor(toColor(dt.InkMuted))
			} else {
				ui.Column(c).Gap(8).Children(func() {
					for _, idx := range visibleMatched {
						it := &m.renderItems[idx]
						admitted := !officialOnly || it.isOfficialAdmitted()
						AppleGlassCard(c, "mkt-card-"+it.PluginID, dt, 16, 12, 6, func() {
							ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Text(c, it.titleLine).FontWeight(700).FontSize(13).TextColor(toColor(dt.Ink))
								ui.Spacer(c)
								trustTone := dt.Success
								if !admitted {
									trustTone = dt.Danger
								} else if !it.SignatureVerified {
									trustTone = dt.Warning
								}
								AppleBadgePill(c, it.trustBadge, trustTone, dt)
							})
							if it.Summary != "" {
								ui.Text(c, it.Summary).FontSize(12).TextColor(toColor(dt.InkSecondary))
							}
							ui.Text(c, it.permLine).FontSize(11).TextColor(toColor(dt.InkMuted))
							if it.escalationWarn != "" {
								ui.Text(c, it.escalationWarn).FontSize(11).FontWeight(700).TextColor(toColor(dt.Warning))
							}
							ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Text(c, it.healthBadge).FontSize(11).FontWeight(600).TextColor(toColor(dt.Accent))
								ui.Spacer(c)
								if !admitted && !it.Installed {
									AppleBadgePill(c, "BLOCKED · OFFICIAL ONLY", dt.Danger, dt)
								} else if !it.Installed {
									if ApplePillButton(c, it.instKey, it.installLabel, BtnPrimary, dt, nil, true).Clicked() {
										doInstallID = it.PluginID
									}
								} else if it.Dormant {
									if m.OnWake != nil && admitted {
										if ApplePillButton(c, it.wakeKey, it.wakeLabel, BtnPrimary, dt, nil, true).Clicked() {
											doWakeID = it.PluginID
										}
									}
									if m.OnDisable != nil {
										if ApplePillButton(c, it.disKey, it.disableLabel, BtnDestructive, dt, nil, true).Clicked() {
											doDisableID = it.PluginID
										}
									}
								} else {
									if it.Version != "" && it.InstalledVersion != "" && it.Version != it.InstalledVersion && m.OnUpgrade != nil && admitted {
										if it.escalationWarn != "" && !pendingEsc[it.PluginID] {
											if ApplePillButton(c, it.upgKey, it.upgradeLabel, BtnPrimary, dt, nil, true).Clicked() {
												armEscalateID = it.PluginID
											}
										} else if it.escalationWarn != "" {
											if ApplePillButton(c, it.confirmUpgKey, it.confirmUpgradeLabel, BtnPrimary, dt, svgIconCheck, true).Clicked() {
												doUpgradeID = it.PluginID
											}
										} else {
											if ApplePillButton(c, it.upgKey, it.upgradeLabel, BtnPrimary, dt, nil, true).Clicked() {
												doUpgradeID = it.PluginID
											}
										}
									}
									if it.PreviousVersion != "" && m.OnRollback != nil {
										if ApplePillButton(c, it.rbKey, it.rollbackLabel, BtnTinted, dt, nil, true).Clicked() {
											doRollbackID = it.PluginID
										}
									}
									if m.OnSleep != nil {
										if ApplePillButton(c, it.sleepKey, it.sleepLabel, BtnGlass, dt, nil, true).Clicked() {
											doSleepID = it.PluginID
										}
									}
									if m.OnDisable != nil {
										if ApplePillButton(c, it.disKey, it.disableLabel, BtnDestructive, dt, nil, true).Clicked() {
											doDisableID = it.PluginID
										}
									}
								}
							})
						})
					}
				})
				if len(matched) > maxVisibleMarketplaceCards {
					if m.cachedPageInfo == "" || m.pageInfoStart != pageOffset+1 || m.pageInfoEnd != visibleEnd || m.pageInfoTotal != len(matched) {
						m.pageInfoStart = pageOffset + 1
						m.pageInfoEnd = visibleEnd
						m.pageInfoTotal = len(matched)
						m.cachedPageInfo = fmt.Sprintf("Showing %d-%d of %d matching plugins", m.pageInfoStart, m.pageInfoEnd, m.pageInfoTotal)
					}
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, m.cachedPageInfo).FontSize(11).TextColor(toColor(dt.InkMuted))
						ui.Spacer(c)
						if pageOffset > 0 {
							if ApplePillButton(c, "mkt-page-prev", "Prev Page", BtnGlass, dt, nil, true).Clicked() {
								nextPageOff = max(0, pageOffset-maxVisibleMarketplaceCards)
							}
						}
						if visibleEnd < len(matched) {
							if ApplePillButton(c, "mkt-page-next", "Next Page", BtnGlass, dt, nil, true).Clicked() {
								nextPageOff = visibleEnd
							}
						}
					})
				}
			}
		}
	})

	m.mu.Lock()
	if nextStream != "" && m.activeStream != nextStream {
		m.activeStream = nextStream
	}
	if m.query != query {
		m.query = query
		nextPageOff = 0
	}
	if clickedFilter != nil && m.runtimeFilter != *clickedFilter {
		m.runtimeFilter = *clickedFilter
		nextPageOff = 0
	}
	m.pageOffset = nextPageOff
	m.mu.Unlock()

	runAction := func(label string, fn func() error) {
		if fn == nil {
			return
		}
		if err := fn(); err != nil {
			m.SetStatus(fmt.Sprintf("%s failed: %v", label, err), true)
		} else {
			m.SetStatus(label+" succeeded", false)
		}
	}
	if doSyncOff {
		runAction("Sync Official UI Marketplace", m.OnSyncOfficial)
	}
	if doRefresh {
		runAction("Refresh catalog", m.OnRefresh)
	}
	if applyTheme != nil {
		th := *applyTheme
		runAction("Apply theme "+th.Name, func() error {
			if err := m.OnApplyTheme(th); err != nil {
				return err
			}
			m.MarkActiveTheme(th.ThemeID)
			return nil
		})
	}
	if applyLayout != nil {
		ly := *applyLayout
		runAction("Apply layout "+ly.Name, func() error {
			if err := m.OnApplyLayout(ly); err != nil {
				return err
			}
			m.MarkActiveLayout(ly.LayoutID)
			return nil
		})
	}
	if doInstallID != "" {
		runAction("Install "+doInstallID, func() error { return m.OnInstall(doInstallID) })
	}
	if armEscalateID != "" {
		m.mu.Lock()
		if m.pendingEscalation == nil {
			m.pendingEscalation = make(map[string]bool)
		}
		m.pendingEscalation[armEscalateID] = true
		m.mu.Unlock()
		m.SetStatus("Review added permissions for "+armEscalateID+" and click Confirm Upgrade to proceed", true)
	}
	if doUpgradeID != "" {
		m.mu.Lock()
		delete(m.pendingEscalation, doUpgradeID)
		m.mu.Unlock()
		runAction("Upgrade "+doUpgradeID, func() error { return m.OnUpgrade(doUpgradeID) })
	}
	if doRollbackID != "" {
		runAction("Rollback "+doRollbackID, func() error { return m.OnRollback(doRollbackID) })
	}
	if doDisableID != "" {
		runAction("Disable "+doDisableID, func() error { return m.OnDisable(doDisableID) })
	}
	if doWakeID != "" {
		runAction("Wake "+doWakeID, func() error { return m.OnWake(doWakeID) })
	}
	if doSleepID != "" {
		runAction("Sleep "+doSleepID, func() error { return m.OnSleep(doSleepID) })
	}
	if doClose && onClose != nil {
		onClose()
	}
}
