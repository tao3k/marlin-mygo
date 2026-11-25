// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"sync"
	"time"

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
