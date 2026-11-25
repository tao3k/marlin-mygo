// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// LazyProfilerSummary projects real-time LazyLoader telemetry into the Tier-0
// Marketplace header (inspired by lazy.nvim's `:Lazy` profiler strip).
type LazyProfilerSummary struct {
	TotalDeclared     int
	ActiveCount       int
	DormantCount      int
	MaxActiveLazy     int
	TotalWakes        uint64
	TotalReaped       uint64
	TotalEvicted      uint64
	ReconcileDuration time.Duration
}

// MarketplaceItem projects a cryptographically verified Marketplace plugin
// entry and its live host lifecycle state into the Tier-0 GPU Marketplace drawer.
type MarketplaceItem struct {
	PluginID          string
	Version           string
	PublisherID       string
	Summary           string
	SlotID            string
	Runtime           schema.PanelRuntimeKind
	ReadScopes        []string
	DeclaredActions   []string
	SignatureVerified bool
	TrustTier         string // "official" (default when SignatureVerified), "verified", or "unofficial"
	PackageBytes      int64

	Installed            bool
	Dormant              bool // Emacs-style lazy-loaded: registered in profile with 0 runtime RAM until triggered
	ActivationPolicy     string
	Triggers             []string
	InstalledVersion     string
	PreviousVersion      string
	ActiveGeneration     uint64
	LastWakeDuration     time.Duration
	HealthState          string // "HEALTHY", "BREAKER_OPEN", "HOT_RELOADING", "DORMANT", "IDLE"
	LastMessage          string
	AddedReadScopes      []string // non-empty when an upgrade expands read_scopes
	AddedDeclaredActions []string // non-empty when an upgrade expands declared_actions

	// Precomputed display labels & stable widget keys to eliminate per-frame allocations.
	titleLine           string
	trustBadge          string
	permLine            string
	escalationWarn      string
	healthBadge         string
	installLabel        string
	upgradeLabel        string
	confirmUpgradeLabel string
	rollbackLabel       string
	disableLabel        string
	wakeLabel           string
	sleepLabel          string
	instKey             string
	upgKey              string
	confirmUpgKey       string
	rbKey               string
	disKey              string
	wakeKey             string
	sleepKey            string
}

func (it *MarketplaceItem) isOfficialAdmitted() bool {
	if !it.SignatureVerified {
		return false
	}
	tier := strings.ToLower(strings.TrimSpace(it.TrustTier))
	return tier == "" || tier == "official"
}

func (it *MarketplaceItem) refreshLabels() {
	it.titleLine = fmt.Sprintf("%s  v%s  [%s -> %s]", it.PluginID, it.Version, it.Runtime, it.SlotID)
	if it.isOfficialAdmitted() {
		it.trustBadge = fmt.Sprintf("OFFICIAL VERIFIED · %s", it.PublisherID)
	} else if it.SignatureVerified {
		it.trustBadge = fmt.Sprintf("UNOFFICIAL TIER (%s) · %s", it.TrustTier, it.PublisherID)
	} else {
		it.trustBadge = fmt.Sprintf("UNVERIFIED · %s", it.PublisherID)
	}
	scopes := "none"
	if len(it.ReadScopes) > 0 {
		scopes = strings.Join(it.ReadScopes, ", ")
	}
	actions := "none"
	if len(it.DeclaredActions) > 0 {
		actions = strings.Join(it.DeclaredActions, ", ")
	}
	it.permLine = fmt.Sprintf("scopes:[%s]  actions:[%s]", scopes, actions)
	if len(it.AddedReadScopes) > 0 || len(it.AddedDeclaredActions) > 0 {
		var parts []string
		if len(it.AddedReadScopes) > 0 {
			parts = append(parts, "+scopes:["+strings.Join(it.AddedReadScopes, ", ")+"]")
		}
		if len(it.AddedDeclaredActions) > 0 {
			parts = append(parts, "+actions:["+strings.Join(it.AddedDeclaredActions, ", ")+"]")
		}
		it.escalationWarn = "NEW PERMISSIONS ON UPGRADE: " + strings.Join(parts, "  ")
	} else {
		it.escalationWarn = ""
	}
	if it.Installed && it.Dormant {
		pol := it.ActivationPolicy
		if pol == "" {
			pol = "on-slot"
		}
		it.healthBadge = fmt.Sprintf("DORMANT v%s · lazy(%s) · 0MB RAM", it.InstalledVersion, pol)
	} else if it.Installed {
		health := it.HealthState
		if health == "" {
			health = "HEALTHY"
		}
		if it.LastWakeDuration > 0 {
			it.healthBadge = fmt.Sprintf("ACTIVE v%s · gen:%d · wake:%s · %s", it.InstalledVersion, it.ActiveGeneration, formatDurationCompact(it.LastWakeDuration), health)
		} else {
			it.healthBadge = fmt.Sprintf("ACTIVE v%s · gen:%d · %s", it.InstalledVersion, it.ActiveGeneration, health)
		}
	} else {
		it.healthBadge = "NOT INSTALLED"
	}
	it.installLabel = "Install " + it.PluginID
	it.upgradeLabel = "Upgrade " + it.PluginID
	it.confirmUpgradeLabel = "Confirm Upgrade " + it.PluginID
	it.rollbackLabel = "Rollback " + it.PluginID
	it.disableLabel = "Disable " + it.PluginID
	it.wakeLabel = "Wake " + it.PluginID
	it.sleepLabel = "Sleep " + it.PluginID
	it.instKey = "mkt-inst-" + it.PluginID
	it.upgKey = "mkt-upg-" + it.PluginID
	it.confirmUpgKey = "mkt-upg-confirm-" + it.PluginID
	it.rbKey = "mkt-rb-" + it.PluginID
	it.disKey = "mkt-dis-" + it.PluginID
	it.wakeKey = "mkt-wake-" + it.PluginID
	it.sleepKey = "mkt-sleep-" + it.PluginID
}

const maxVisibleMarketplaceCards = 12

// MarketplaceController owns the built-in Tier-0 Host GPU Marketplace drawer
// (painted above all Untrusted plugin WebViews so third-party plugins can never
// occlude or spoof Ed25519 trust badges, permission lists, or rollback controls).
// By default OfficialOnly=true enforces that only Official Marketplace verified
// publishers can be installed or awakened from the UI.
type MarketplaceController struct {
	OfficialOnly bool
	OnInstall    func(pluginID string) error
	OnUpgrade    func(pluginID string) error
	OnRollback   func(pluginID string) error
	OnDisable    func(pluginID string) error
	OnWake       func(pluginID string) error
	OnSleep      func(pluginID string) error
	OnRefresh    func() error

	mu                sync.RWMutex
	items             []MarketplaceItem
	itemsVer          uint64
	query             string
	runtimeFilter     schema.PanelRuntimeKind // empty = All
	pageOffset        int
	statusBanner      string
	statusIsErr       bool
	profilerLine      string
	pendingEscalation map[string]bool

	// Reused scratch slices & query cache to avoid per-frame allocations at 120Hz.
	renderMu       sync.Mutex
	renderItems    []MarketplaceItem
	renderItemsVer uint64
	cmdEntriesBuf  []input.CommandEntry
	filteredIdxBuf []int
	rankedBuf      []input.RankedCommand
	matchedBuf     []int
	cacheValid     bool
	cacheQuery     string
	cacheRT        schema.PanelRuntimeKind
	cacheItemsVer  uint64
	cachedPageInfo string
	pageInfoStart  int
	pageInfoEnd    int
	pageInfoTotal  int
}

func NewMarketplaceController() *MarketplaceController {
	return &MarketplaceController{
		OfficialOnly: true,
	}
}

// SetItems replaces the Marketplace catalog snapshot and precomputes display labels.
func (m *MarketplaceController) SetItems(items []MarketplaceItem) {
	m.mu.Lock()
	cp := make([]MarketplaceItem, len(items))
	copy(cp, items)
	for i := range cp {
		cp[i].refreshLabels()
	}
	m.items = cp
	m.itemsVer++
	m.pageOffset = 0
	m.mu.Unlock()
}

// UpsertItem updates or inserts one Marketplace entry (e.g. after live Install,
// Upgrade, Rollback, Disable, or dev-plugin Hot-Reload).
func (m *MarketplaceController) UpsertItem(item MarketplaceItem) {
	item.refreshLabels()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.itemsVer++
	for i := range m.items {
		if m.items[i].PluginID == item.PluginID {
			m.items[i] = item
			return
		}
	}
	m.items = append(m.items, item)
}

// MarkInstalled updates live generation, version history, and health badge for pluginID.
func (m *MarketplaceController) MarkInstalled(pluginID, version string, gen uint64, health string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.items {
		if m.items[i].PluginID == pluginID {
			if m.items[i].Installed && m.items[i].InstalledVersion != "" && m.items[i].InstalledVersion != version {
				m.items[i].PreviousVersion = m.items[i].InstalledVersion
			}
			m.items[i].Installed = true
			m.items[i].Dormant = false
			m.items[i].InstalledVersion = version
			m.items[i].ActiveGeneration = gen
			if health != "" {
				m.items[i].HealthState = health
			} else {
				m.items[i].HealthState = "HEALTHY"
			}
			m.items[i].refreshLabels()
			m.itemsVer++
			return
		}
	}
}

// MarkDormant transitions an installed plugin to lazy dormant state (0MB runtime RAM).
func (m *MarketplaceController) MarkDormant(pluginID, version, policy string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.items {
		if m.items[i].PluginID == pluginID {
			m.items[i].Installed = true
			m.items[i].Dormant = true
			if version != "" {
				m.items[i].InstalledVersion = version
			}
			if policy != "" {
				m.items[i].ActivationPolicy = policy
			}
			m.items[i].ActiveGeneration = 0
			m.items[i].HealthState = "DORMANT"
			m.items[i].refreshLabels()
			m.itemsVer++
			return
		}
	}
}

// MarkDisabled records plugin deactivation (host.* fallback restored).
func (m *MarketplaceController) MarkDisabled(pluginID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.items {
		if m.items[i].PluginID == pluginID {
			m.items[i].Installed = false
			m.items[i].ActiveGeneration = 0
			m.items[i].HealthState = "IDLE"
			m.items[i].refreshLabels()
			m.itemsVer++
			return
		}
	}
}

func (m *MarketplaceController) SetQuery(q string) {
	m.mu.Lock()
	if m.query != q {
		m.query = q
		m.pageOffset = 0
	}
	m.mu.Unlock()
}

func (m *MarketplaceController) SetRuntimeFilter(rt schema.PanelRuntimeKind) {
	m.mu.Lock()
	if m.runtimeFilter != rt {
		m.runtimeFilter = rt
		m.pageOffset = 0
	}
	m.mu.Unlock()
}

func (m *MarketplaceController) SetStatus(msg string, isErr bool) {
	m.mu.Lock()
	m.statusBanner = msg
	m.statusIsErr = isErr
	m.mu.Unlock()
}

// SetProfilerSummary precomputes the live lazy-loader profiler strip rendered in
// the Tier-0 Marketplace drawer (zero per-frame string formatting).
func (m *MarketplaceController) SetProfilerSummary(s LazyProfilerSummary) {
	line := fmt.Sprintf("Lazy Engine: %d active / %d dormant (0MB RAM) · LRU cap:%d · wakes:%d · reaped:%d · evicted:%d · startup:%s",
		s.ActiveCount, s.DormantCount, s.MaxActiveLazy, s.TotalWakes, s.TotalReaped, s.TotalEvicted, formatDurationCompact(s.ReconcileDuration))
	m.mu.Lock()
	m.profilerLine = line
	m.mu.Unlock()
}

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
	query := m.query
	rtFilter := m.runtimeFilter
	pageOffset := m.pageOffset
	statusMsg := m.statusBanner
	statusErr := m.statusIsErr
	profilerLine := m.profilerLine
	itemsVer := m.itemsVer
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
		clickedFilter *schema.PanelRuntimeKind
		nextPageOff   = pageOffset
		doRefresh     bool
		doClose       bool
		doInstallID   string
		doUpgradeID   string
		armEscalateID string
		doRollbackID  string
		doDisableID   string
		doWakeID      string
		doSleepID     string
	)

	dt := derivedThemeFromUITheme(t)
	officialOnly := m.OfficialOnly

	ui.Column(c).MinWidth(0).Gap(10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "MARLIN MARKETPLACE (HOST TIER-0)").FontSize(16).FontWeight(700).TextColor(toColor(dt.Ink))
			if officialOnly {
				AppleBadgePill(c, "OFFICIAL ONLY", dt.Success, dt)
			}
			ui.Spacer(c)
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

		ui.Text(c, "Official Ed25519-signed Marketplace catalog & hot-plug lifecycle managed inside the trusted host GPU plane.").
			FontSize(12).TextColor(toColor(dt.InkMuted))

		if profilerLine != "" {
			ui.Text(c, profilerLine).FontSize(11).FontWeight(600).TextColor(toColor(dt.Accent))
		}

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

		if statusMsg != "" {
			col := toColor(dt.Accent)
			if statusErr {
				col = toColor(dt.Warning)
			}
			ui.Text(c, statusMsg).FontSize(12).FontWeight(600).TextColor(col)
		}

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
	})

	m.mu.Lock()
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
	if doRefresh {
		runAction("Refresh catalog", m.OnRefresh)
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
