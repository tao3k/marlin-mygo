// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

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
	PackageBytes      int64

	Installed        bool
	Dormant          bool // Emacs-style lazy-loaded: registered in profile with 0 runtime RAM until triggered
	ActivationPolicy string
	Triggers         []string
	InstalledVersion string
	PreviousVersion  string
	ActiveGeneration uint64
	HealthState      string // "HEALTHY", "BREAKER_OPEN", "HOT_RELOADING", "DORMANT", "IDLE"
	LastMessage      string

	// Precomputed display labels to minimize per-frame allocations.
	titleLine     string
	trustBadge    string
	permLine      string
	healthBadge   string
	installLabel  string
	upgradeLabel  string
	rollbackLabel string
	disableLabel  string
	wakeLabel     string
	sleepLabel    string
}

func (it *MarketplaceItem) refreshLabels() {
	it.titleLine = fmt.Sprintf("%s  v%s  [%s -> %s]", it.PluginID, it.Version, it.Runtime, it.SlotID)
	if it.SignatureVerified {
		it.trustBadge = fmt.Sprintf("VERIFIED · %s", it.PublisherID)
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
		it.healthBadge = fmt.Sprintf("ACTIVE v%s · gen:%d · %s", it.InstalledVersion, it.ActiveGeneration, health)
	} else {
		it.healthBadge = "NOT INSTALLED"
	}
	it.installLabel = "Install " + it.PluginID
	it.upgradeLabel = "Upgrade " + it.PluginID
	it.rollbackLabel = "Rollback " + it.PluginID
	it.disableLabel = "Disable " + it.PluginID
	it.wakeLabel = "Wake " + it.PluginID
	it.sleepLabel = "Sleep " + it.PluginID
}

// MarketplaceController owns the built-in Tier-0 Host GPU Marketplace drawer
// (painted above all Untrusted plugin WebViews so third-party plugins can never
// occlude or spoof Ed25519 trust badges, permission lists, or rollback controls).
type MarketplaceController struct {
	OnInstall  func(pluginID string) error
	OnUpgrade  func(pluginID string) error
	OnRollback func(pluginID string) error
	OnDisable  func(pluginID string) error
	OnWake     func(pluginID string) error
	OnSleep    func(pluginID string) error
	OnRefresh  func() error

	mu            sync.RWMutex
	items         []MarketplaceItem
	query         string
	runtimeFilter schema.PanelRuntimeKind // empty = All
	statusBanner  string
	statusIsErr   bool

	// Reused scratch slices to avoid per-frame allocations.
	renderMu      sync.Mutex
	renderItems   []MarketplaceItem
	cmdEntriesBuf []input.CommandEntry
	rankedBuf     []input.RankedCommand
}

func NewMarketplaceController() *MarketplaceController {
	return &MarketplaceController{}
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
	m.mu.Unlock()
}

// UpsertItem updates or inserts one Marketplace entry (e.g. after live Install,
// Upgrade, Rollback, Disable, or dev-plugin Hot-Reload).
func (m *MarketplaceController) UpsertItem(item MarketplaceItem) {
	item.refreshLabels()
	m.mu.Lock()
	defer m.mu.Unlock()
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
			return
		}
	}
}

func (m *MarketplaceController) SetQuery(q string) {
	m.mu.Lock()
	m.query = q
	m.mu.Unlock()
}

func (m *MarketplaceController) SetRuntimeFilter(rt schema.PanelRuntimeKind) {
	m.mu.Lock()
	m.runtimeFilter = rt
	m.mu.Unlock()
}

func (m *MarketplaceController) SetStatus(msg string, isErr bool) {
	m.mu.Lock()
	m.statusBanner = msg
	m.statusIsErr = isErr
	m.mu.Unlock()
}

// Render paints the built-in Tier-0 Marketplace drawer onto MyGo's GPU scene graph.
func (m *MarketplaceController) Render(c *ui.Context, t *ui.Theme, onClose func()) {
	m.renderMu.Lock()
	defer m.renderMu.Unlock()

	m.mu.RLock()
	query := m.query
	rtFilter := m.runtimeFilter
	statusMsg := m.statusBanner
	statusErr := m.statusIsErr
	m.renderItems = append(m.renderItems[:0], m.items...)
	m.mu.RUnlock()

	// Filter by runtime kind first, then rank with zero-alloc engine/input.FuzzyScore.
	m.cmdEntriesBuf = m.cmdEntriesBuf[:0]
	for i := range m.renderItems {
		it := &m.renderItems[i]
		if rtFilter != "" && it.Runtime != rtFilter {
			continue
		}
		m.cmdEntriesBuf = append(m.cmdEntriesBuf, input.CommandEntry{
			Name:        it.PluginID,
			Description: it.Summary,
		})
	}

	var matched []int
	if strings.TrimSpace(query) == "" {
		for i := range m.renderItems {
			if rtFilter == "" || m.renderItems[i].Runtime == rtFilter {
				matched = append(matched, i)
			}
		}
	} else {
		// Map filtered index back to m.renderItems index.
		filteredIdx := make([]int, 0, len(m.cmdEntriesBuf))
		for i := range m.renderItems {
			if rtFilter == "" || m.renderItems[i].Runtime == rtFilter {
				filteredIdx = append(filteredIdx, i)
			}
		}
		m.rankedBuf = input.RankCommands(query, m.cmdEntriesBuf, m.rankedBuf[:0])
		for _, r := range m.rankedBuf {
			matched = append(matched, filteredIdx[r.Index])
		}
	}

	var (
		clickedFilter *schema.PanelRuntimeKind
		doRefresh     bool
		doClose       bool
		doInstallID   string
		doUpgradeID   string
		doRollbackID  string
		doDisableID   string
		doWakeID      string
		doSleepID     string
	)

	ui.Column(c).MinWidth(0).Gap(10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "MARLIN MARKETPLACE (HOST TIER-0)").FontSize(16).Bold()
			ui.Spacer(c)
			if m.OnRefresh != nil {
				if ui.Button(c.Key("mkt-refresh"), "Refresh Catalog").Clicked() {
					doRefresh = true
				}
			}
			if onClose != nil {
				if ui.Button(c.Key("mkt-close"), "Close").Clicked() {
					doClose = true
				}
			}
		})

		ui.Text(c, "Ed25519-verified catalog & hot-plug lifecycle rendered inside the trusted host GPU plane.").
			FontSize(12).TextColor(t.TextMuted)

		// Zero-alloc fuzzy search + runtime filter chips.
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("mkt-search"), &query).Placeholder("Search plugins (fuzzy)...")
			filterButtons := []struct {
				label string
				kind  schema.PanelRuntimeKind
			}{
				{"All", ""},
				{"Go WASI", schema.RuntimeWasmUIIR},
				{"TS WebView", schema.RuntimeWebViewUntrusted},
				{"Declarative", schema.RuntimeDeclarative},
			}
			for _, fb := range filterButtons {
				k := fb.kind
				if rtFilter == k {
					if ui.PrimaryButton(c.Key("mkt-rt-"+fb.label), fb.label).Clicked() {
						clickedFilter = &k
					}
				} else {
					if ui.Button(c.Key("mkt-rt-"+fb.label), fb.label).Clicked() {
						clickedFilter = &k
					}
				}
			}
		})

		if statusMsg != "" {
			col := t.Accent
			if statusErr {
				col = t.Warning
			}
			ui.Text(c, statusMsg).FontSize(12).Bold().TextColor(col)
		}

		if len(matched) == 0 {
			ui.Text(c, "No matching Marketplace plugins found.").FontSize(12).TextColor(t.TextMuted)
		} else {
			ui.Column(c).Gap(8).Children(func() {
				for _, idx := range matched {
					it := &m.renderItems[idx]
					border := t.Border
					if it.Installed {
						border = t.Accent
					}
					ui.Column(c).Padding(10).Gap(4).Radius(6).Border(1, border).Children(func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, it.titleLine).Bold().FontSize(13)
							ui.Spacer(c)
							trustColor := t.Accent
							if !it.SignatureVerified {
								trustColor = t.Warning
							}
							ui.Text(c, it.trustBadge).FontSize(11).Bold().TextColor(trustColor)
						})
						if it.Summary != "" {
							ui.Text(c, it.Summary).FontSize(12)
						}
						ui.Text(c, it.permLine).FontSize(11).TextColor(t.TextMuted)
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, it.healthBadge).FontSize(11).Bold().TextColor(t.Accent)
							ui.Spacer(c)
							if !it.Installed {
								if ui.PrimaryButton(c.Key("mkt-inst-"+it.PluginID), it.installLabel).Clicked() {
									doInstallID = it.PluginID
								}
							} else if it.Dormant {
								if m.OnWake != nil {
									if ui.PrimaryButton(c.Key("mkt-wake-"+it.PluginID), it.wakeLabel).Clicked() {
										doWakeID = it.PluginID
									}
								}
								if m.OnDisable != nil {
									if ui.Button(c.Key("mkt-dis-"+it.PluginID), it.disableLabel).Clicked() {
										doDisableID = it.PluginID
									}
								}
							} else {
								if it.Version != "" && it.InstalledVersion != "" && it.Version != it.InstalledVersion && m.OnUpgrade != nil {
									if ui.PrimaryButton(c.Key("mkt-upg-"+it.PluginID), it.upgradeLabel).Clicked() {
										doUpgradeID = it.PluginID
									}
								}
								if it.PreviousVersion != "" && m.OnRollback != nil {
									if ui.Button(c.Key("mkt-rb-"+it.PluginID), it.rollbackLabel).Clicked() {
										doRollbackID = it.PluginID
									}
								}
								if m.OnSleep != nil {
									if ui.Button(c.Key("mkt-sleep-"+it.PluginID), it.sleepLabel).Clicked() {
										doSleepID = it.PluginID
									}
								}
								if m.OnDisable != nil {
									if ui.Button(c.Key("mkt-dis-"+it.PluginID), it.disableLabel).Clicked() {
										doDisableID = it.PluginID
									}
								}
							}
						})
					})
				}
			})
		}
	})

	m.mu.Lock()
	m.query = query
	if clickedFilter != nil {
		m.runtimeFilter = *clickedFilter
	}
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
	if doUpgradeID != "" {
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
