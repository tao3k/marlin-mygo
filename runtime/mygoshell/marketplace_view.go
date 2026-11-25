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
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// MarketplaceStream selects which Official UI Marketplace catalog stream is displayed.
type MarketplaceStream string

const (
	StreamPlugins MarketplaceStream = "plugins"
	StreamThemes  MarketplaceStream = "themes"
	StreamLayouts MarketplaceStream = "layouts"
)

// MarketplaceThemeItem projects an Official UI Marketplace 8-color semantic
// theme pack into the Tier-0 Marketplace Themes stream.
type MarketplaceThemeItem struct {
	ThemeID     string
	Name        string
	Appearance  string // "dark" | "light"
	PublisherID string
	Summary     string
	TrustTier   string // "official" | "verified" | "unofficial"
	Base        schema.UITheme
	Active      bool

	// Precomputed labels & widget keys for zero-alloc 120Hz rendering.
	titleLine  string
	trustBadge string
	applyLabel string
	applyKey   string
	cardKey    string
	swatches   [8]schema.RGB
}

func (it *MarketplaceThemeItem) isOfficialAdmitted() bool {
	tier := strings.ToLower(strings.TrimSpace(it.TrustTier))
	return tier == "" || tier == "official"
}

func (it *MarketplaceThemeItem) refreshLabels() {
	app := strings.ToUpper(strings.TrimSpace(it.Appearance))
	if app == "" {
		app = "DARK"
	}
	it.titleLine = fmt.Sprintf("%s  (%s)  ·  %s", it.Name, app, it.ThemeID)
	if it.isOfficialAdmitted() {
		it.trustBadge = fmt.Sprintf("OFFICIAL THEME · %s", it.PublisherID)
	} else {
		it.trustBadge = fmt.Sprintf("UNOFFICIAL (%s) · %s", it.TrustTier, it.PublisherID)
	}
	it.applyLabel = "Apply " + it.Name
	it.applyKey = "mkt-theme-apply-" + it.ThemeID
	it.cardKey = "mkt-theme-card-" + it.ThemeID
	it.swatches = [8]schema.RGB{
		it.Base.Canvas,
		it.Base.Bar,
		it.Base.Ink,
		it.Base.Accent,
		it.Base.Info,
		it.Base.Success,
		it.Base.Warning,
		it.Base.Muted,
	}
}

// MarketplaceLayoutItem projects an Official UI Marketplace Emacs composable
// LayoutSpec pack into the Tier-0 Marketplace Layouts stream.
type MarketplaceLayoutItem struct {
	LayoutID    string
	Name        string
	Perspective string
	PublisherID string
	Summary     string
	TrustTier   string // "official" | "verified" | "unofficial"
	Spec        shell.LayoutSpec
	Active      bool

	// Precomputed labels & widget keys.
	titleLine   string
	trustBadge  string
	topologyStr string
	applyLabel  string
	applyKey    string
	cardKey     string
}

func (it *MarketplaceLayoutItem) isOfficialAdmitted() bool {
	tier := strings.ToLower(strings.TrimSpace(it.TrustTier))
	return tier == "" || tier == "official"
}

func (it *MarketplaceLayoutItem) refreshLabels() {
	panes := countLayoutSpecLeaves(it.Spec.Root)
	it.titleLine = fmt.Sprintf("%s  [%s · %d panes]", it.Name, it.Perspective, panes)
	if it.isOfficialAdmitted() {
		it.trustBadge = fmt.Sprintf("OFFICIAL LAYOUT · %s", it.PublisherID)
	} else {
		it.trustBadge = fmt.Sprintf("UNOFFICIAL (%s) · %s", it.TrustTier, it.PublisherID)
	}
	it.topologyStr = "Tree: " + summarizeLayoutSpecNode(it.Spec.Root)
	it.applyLabel = "Apply " + it.Name
	it.applyKey = "mkt-layout-apply-" + it.LayoutID
	it.cardKey = "mkt-layout-card-" + it.LayoutID
}

func countLayoutSpecLeaves(n *shell.LayoutNodeSpec) int {
	if n == nil {
		return 0
	}
	if n.First != nil && n.Second != nil {
		return countLayoutSpecLeaves(n.First) + countLayoutSpecLeaves(n.Second)
	}
	return 1
}

func summarizeLayoutSpecNode(n *shell.LayoutNodeSpec) string {
	if n == nil {
		return "empty"
	}
	if n.First != nil && n.Second != nil {
		pct := int(n.Ratio * 100)
		if pct <= 0 {
			pct = 50
		}
		sep := " | "
		if n.Axis == shell.SplitColumn {
			sep = " / "
		}
		return fmt.Sprintf("%s %d%% [%s%s%s]", n.Axis, pct, summarizeLayoutSpecNode(n.First), sep, summarizeLayoutSpecNode(n.Second))
	}
	if n.Title != "" {
		return n.Title
	}
	return n.BufferID
}

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
	OfficialOnly   bool
	OnInstall      func(pluginID string) error
	OnUpgrade      func(pluginID string) error
	OnRollback     func(pluginID string) error
	OnDisable      func(pluginID string) error
	OnWake         func(pluginID string) error
	OnSleep        func(pluginID string) error
	OnRefresh      func() error
	OnSyncOfficial func() error
	OnApplyTheme   func(item MarketplaceThemeItem) error
	OnApplyLayout  func(item MarketplaceLayoutItem) error

	mu                sync.RWMutex
	activeStream      MarketplaceStream
	items             []MarketplaceItem
	itemsVer          uint64
	themes            []MarketplaceThemeItem
	themesVer         uint64
	layouts           []MarketplaceLayoutItem
	layoutsVer        uint64
	query             string
	runtimeFilter     schema.PanelRuntimeKind // empty = All
	pageOffset        int
	statusBanner      string
	statusIsErr       bool
	profilerLine      string
	pendingEscalation map[string]bool

	// Reused scratch slices & query cache to avoid per-frame allocations at 120Hz.
	renderMu         sync.Mutex
	renderItems      []MarketplaceItem
	renderItemsVer   uint64
	renderThemes     []MarketplaceThemeItem
	renderThemesVer  uint64
	renderLayouts    []MarketplaceLayoutItem
	renderLayoutsVer uint64
	cmdEntriesBuf    []input.CommandEntry
	filteredIdxBuf   []int
	rankedBuf        []input.RankedCommand
	matchedBuf       []int
	cacheValid       bool
	cacheQuery       string
	cacheRT          schema.PanelRuntimeKind
	cacheItemsVer    uint64
	cachedPageInfo   string
	pageInfoStart    int
	pageInfoEnd      int
	pageInfoTotal    int
}

func NewMarketplaceController() *MarketplaceController {
	return &MarketplaceController{
		OfficialOnly: true,
		activeStream: StreamPlugins,
	}
}

// ActiveStream returns the currently displayed Official Marketplace stream.
func (m *MarketplaceController) ActiveStream() MarketplaceStream {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.activeStream == "" {
		return StreamPlugins
	}
	return m.activeStream
}

// SetActiveStream switches the active Marketplace tab ("plugins" | "themes" | "layouts").
func (m *MarketplaceController) SetActiveStream(stream MarketplaceStream) {
	switch stream {
	case StreamPlugins, StreamThemes, StreamLayouts:
	default:
		stream = StreamPlugins
	}
	m.mu.Lock()
	m.activeStream = stream
	m.mu.Unlock()
}

// SetThemes replaces the curated Official UI Marketplace Themes stream snapshot.
func (m *MarketplaceController) SetThemes(themes []MarketplaceThemeItem) {
	cp := make([]MarketplaceThemeItem, len(themes))
	copy(cp, themes)
	for i := range cp {
		cp[i].refreshLabels()
	}
	m.mu.Lock()
	m.themes = cp
	m.themesVer++
	m.mu.Unlock()
}

// MarkActiveTheme highlights the active Official Marketplace theme in the Themes stream.
func (m *MarketplaceController) MarkActiveTheme(themeID string) {
	norm := strings.ToLower(strings.TrimSpace(themeID))
	m.mu.Lock()
	for i := range m.themes {
		m.themes[i].Active = strings.ToLower(m.themes[i].ThemeID) == norm ||
			strings.ToLower(m.themes[i].Name) == norm
	}
	m.themesVer++
	m.mu.Unlock()
}

// SetLayouts replaces the curated Official UI Marketplace Emacs Layouts stream snapshot.
func (m *MarketplaceController) SetLayouts(layouts []MarketplaceLayoutItem) {
	cp := make([]MarketplaceLayoutItem, len(layouts))
	copy(cp, layouts)
	for i := range cp {
		cp[i].refreshLabels()
	}
	m.mu.Lock()
	m.layouts = cp
	m.layoutsVer++
	m.mu.Unlock()
}

// MarkActiveLayout highlights the active Official Marketplace Emacs layout in the Layouts stream.
func (m *MarketplaceController) MarkActiveLayout(layoutID string) {
	norm := strings.ToLower(strings.TrimSpace(layoutID))
	m.mu.Lock()
	for i := range m.layouts {
		m.layouts[i].Active = strings.ToLower(m.layouts[i].LayoutID) == norm ||
			strings.ToLower(m.layouts[i].Perspective) == norm
	}
	m.layoutsVer++
	m.mu.Unlock()
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

// DefaultOfficialMarketplaceThemes returns the built-in curated Official UI
// Marketplace theme packs projected into the Marketplace Themes stream.
func DefaultOfficialMarketplaceThemes() []MarketplaceThemeItem {
	presets := BuiltinThemes()
	out := make([]MarketplaceThemeItem, 0, len(presets))
	for i, p := range presets {
		out = append(out, MarketplaceThemeItem{
			ThemeID:     "tao3k.theme-" + p.ID,
			Name:        p.Name,
			Appearance:  string(p.Appearance),
			PublisherID: "tao3k.official",
			Summary:     fmt.Sprintf("Official 8-color semantic palette (%s) projected into 34 --marlin-* tokens.", p.Appearance),
			TrustTier:   "official",
			Base:        p.Base,
			Active:      i == 0,
		})
	}
	return out
}

// DefaultOfficialMarketplaceLayouts returns the built-in curated Official UI
// Marketplace Emacs LayoutSpec packs projected into the Marketplace Layouts stream.
func DefaultOfficialMarketplaceLayouts() []MarketplaceLayoutItem {
	return []MarketplaceLayoutItem{
		{
			LayoutID:    "tao3k.layout-focus",
			Name:        "Deep Focus",
			Perspective: "focus",
			PublisherID: "tao3k.official",
			Summary:     "Clean 2-pane Emacs workspace pairing primary plugin with resident GPU dock.",
			TrustTier:   "official",
			Active:      true,
			Spec: shell.LayoutSpec{
				Perspective: "focus",
				Root: &shell.LayoutNodeSpec{
					Axis:  shell.SplitRow,
					Ratio: 0.72,
					First: &shell.LayoutNodeSpec{
						BufferID: "slot:workbench.main",
						Title:    "Workspace Panel",
						Kind:     "webview",
					},
					Second: &shell.LayoutNodeSpec{
						BufferID: "slot:workbench.inspector",
						Title:    "Focus Clock",
						Kind:     "wasm-gpu",
					},
				},
			},
		},
		{
			LayoutID:    "tao3k.layout-cockpit",
			Name:        "AI Engineering Cockpit",
			Perspective: "cockpit",
			PublisherID: "tao3k.official",
			Summary:     "3-pane Emacs layout combining Workspace Panel, AI Transcript, and WASI Focus Clock.",
			TrustTier:   "official",
			Spec: shell.LayoutSpec{
				Perspective: "cockpit",
				Root: &shell.LayoutNodeSpec{
					Axis:  shell.SplitRow,
					Ratio: 0.62,
					First: &shell.LayoutNodeSpec{
						BufferID: "slot:workbench.main",
						Title:    "Workspace Panel",
						Kind:     "webview",
					},
					Second: &shell.LayoutNodeSpec{
						Axis:  shell.SplitColumn,
						Ratio: 0.66,
						First: &shell.LayoutNodeSpec{
							BufferID: "builtin:transcript",
							Title:    "AI Transcript",
							Kind:     "native-gpu",
						},
						Second: &shell.LayoutNodeSpec{
							BufferID: "slot:workbench.inspector",
							Title:    "Focus Clock",
							Kind:     "wasm-gpu",
						},
					},
				},
			},
		},
		{
			LayoutID:    "tao3k.layout-review-triage",
			Name:        "Review & Triage Studio",
			Perspective: "review",
			PublisherID: "tao3k.official",
			Summary:     "3-pane review studio combining Workspace Panel, Projection Inspector, and AI Transcript.",
			TrustTier:   "official",
			Spec: shell.LayoutSpec{
				Perspective: "review",
				Root: &shell.LayoutNodeSpec{
					Axis:  shell.SplitRow,
					Ratio: 0.56,
					First: &shell.LayoutNodeSpec{
						BufferID: "slot:workbench.main",
						Title:    "Workspace Panel",
						Kind:     "webview",
					},
					Second: &shell.LayoutNodeSpec{
						Axis:  shell.SplitColumn,
						Ratio: 0.52,
						First: &shell.LayoutNodeSpec{
							BufferID: "builtin:projection",
							Title:    "Projection Inspector",
							Kind:     "native-gpu",
						},
						Second: &shell.LayoutNodeSpec{
							BufferID: "builtin:transcript",
							Title:    "AI Transcript",
							Kind:     "native-gpu",
						},
					},
				},
			},
		},
	}
}
