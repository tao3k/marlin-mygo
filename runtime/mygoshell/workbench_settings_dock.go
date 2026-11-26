// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// SettingsTab selects the active section inside Marlin's unified Tier-0
// Settings & Authorization modal.
type SettingsTab string

const (
	SettingsTabGeneral    SettingsTab = "general"
	SettingsTabAuth       SettingsTab = "auth"
	SettingsTabGovernance SettingsTab = "governance"
)

// OpenSettings opens Marlin's unified Tier-0 Settings & Authorization modal on
// the primary available tab (General when a SettingsSchema is bound, otherwise
// Account & Auth or Governance).
func (a *WorkbenchApp) OpenSettings() {
	if a == nil {
		return
	}
	tab := SettingsTabGeneral
	if a.SettingsSchema == nil || a.SettingsState == nil {
		if a.Auth != nil {
			tab = SettingsTabAuth
		} else if a.GovernanceUI != nil {
			tab = SettingsTabGovernance
		}
	}
	a.OpenSettingsTab(tab)
}

// OpenSettingsTab opens Marlin's unified Tier-0 Settings & Authorization modal
// directly on the requested tab.
func (a *WorkbenchApp) OpenSettingsTab(tab SettingsTab) {
	if a == nil || !a.hasAnySettingsSection() {
		return
	}
	a.settingsTab = a.resolveSettingsTab(tab)
	a.settingsOpen = true
	a.paletteOpen = false
	a.bufferPickerOpen = false
	a.marketplaceOpen = false
	a.authOpen = false
	a.activityOpen = false
	a.keymapOpen = false
	a.projectionOpen = false
	a.federationOpen = false
	a.governanceOpen = false
	a.addRemoteOpen = false
	a.gateOpen = false
}

// IsSettingsOpen reports whether the unified Settings & Authorization modal is visible.
func (a *WorkbenchApp) IsSettingsOpen() bool {
	return a != nil && a.settingsOpen
}

// ActiveSettingsTab reports the active tab inside the Settings & Authorization modal.
func (a *WorkbenchApp) ActiveSettingsTab() SettingsTab {
	if a == nil {
		return SettingsTabGeneral
	}
	return a.resolveSettingsTab(a.settingsTab)
}

func (a *WorkbenchApp) hasAnySettingsSection() bool {
	return (a.SettingsSchema != nil && a.SettingsState != nil) || a.Auth != nil || a.GovernanceUI != nil
}

func (a *WorkbenchApp) resolveSettingsTab(want SettingsTab) SettingsTab {
	hasGeneral := a.SettingsSchema != nil && a.SettingsState != nil
	switch want {
	case SettingsTabAuth:
		if a.Auth != nil {
			return SettingsTabAuth
		}
	case SettingsTabGovernance:
		if a.GovernanceUI != nil {
			return SettingsTabGovernance
		}
	case SettingsTabGeneral:
		if hasGeneral {
			return SettingsTabGeneral
		}
	}
	if hasGeneral {
		return SettingsTabGeneral
	}
	if a.Auth != nil {
		return SettingsTabAuth
	}
	if a.GovernanceUI != nil {
		return SettingsTabGovernance
	}
	return SettingsTabGeneral
}

// renderSidebarFooterDock paints the compact bottom-left Account, Vault, and
// Settings dock anchored at the foot of the navigation sidebar.
func (a *WorkbenchApp) renderSidebarFooterDock(c *ui.Context, dt DerivedTheme) {
	if !a.hasAnySettingsSection() {
		return
	}
	ui.Column(c.Key("sidebar-footer-dock")).Padding(10, 0, 0, 0).Gap(8).AlignItems(ui.Stretch).
		BorderWidth(1, 0, 0, 0).BorderColor(glassBorderColor(dt)).Children(func() {
		if a.Auth != nil {
			vi := a.Auth.CurrentVaultInfo()
			accountTitle := "Account & Vault"
			if a.Auth.IsSignedIn() {
				accountTitle = a.Auth.TopBarLabel()
			}
			vaultSub := "Sign in & vault"
			tone := dt.InkMuted
			if a.Auth.IsSignedIn() {
				vaultSub = "Signed in · OS Vault"
				tone = dt.Success
			}
			if vi.Locked {
				vaultSub = "Vault locked · Tap to unlock"
				tone = dt.Warning
			} else if vi.BackendName != "" {
				if a.Auth.IsSignedIn() {
					vaultSub = fmt.Sprintf("Signed in · %s", vi.BackendName)
				} else {
					vaultSub = fmt.Sprintf("Vault ready · %s", vi.BackendName)
				}
			}
			card := ui.ButtonBase(c.Key("sidebar-account-dock")).Label("Account and Authorization").
				Padding(8, 10).Radius(12).AlignItems(ui.Center).Justify(ui.Start).Gap(8).
				Background(toColor(dt.SurfaceElevated)).Border(1, glassBorderColor(dt))
			card.Children(func() {
				ui.Icon(c, svgIconShield).Size(15, 15).TextColor(toColor(tone))
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, accountTitle).FontSize(12).FontWeight(600).TextColor(toColor(dt.Ink)).MaxLines(1)
					ui.Text(c, vaultSub).FontSize(10).TextColor(toColor(dt.InkMuted)).MaxLines(1)
				})
			})
			if card.Clicked() {
				a.OpenSettingsTab(SettingsTabAuth)
			}
		}

		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			if ApplePillButton(c, "sidebar-open-settings", "Settings", BtnGlass, dt, nil, true).Clicked() {
				a.OpenSettings()
			}
			if a.GovernanceUI != nil {
				if ApplePillButton(c, "sidebar-open-governance", "Gov", BtnTinted, dt, svgIconShield, true).Clicked() {
					a.OpenSettingsTab(SettingsTabGovernance)
				}
			}
		})
	})
}

// renderSettingsModal paints Marlin's unified Tier-0 Settings & Authorization
// modal unifying typed BoundedSchema preferences, OS-Keychain AuthController,
// and Tier-0 Governance / Federation shortcuts.
func (a *WorkbenchApp) renderSettingsModal(c *ui.Context, t *ui.Theme, dt DerivedTheme) {
	if !a.hasAnySettingsSection() {
		return
	}
	ui.Modal(c, &a.settingsOpen, func() {
		activeTab := a.resolveSettingsTab(a.settingsTab)
		var tabs []SegmentedItem
		if a.SettingsSchema != nil && a.SettingsState != nil {
			tabs = append(tabs, SegmentedItem{ID: string(SettingsTabGeneral), Label: "General"})
		}
		if a.Auth != nil {
			tabs = append(tabs, SegmentedItem{ID: string(SettingsTabAuth), Label: "Account & Auth"})
		}
		if a.GovernanceUI != nil {
			tabs = append(tabs, SegmentedItem{ID: string(SettingsTabGovernance), Label: "Governance"})
		}

		ui.Column(c.Key("settings-center-modal")).Width(560).Gap(12).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				AppleBadgePill(c, "TIER-0 SETTINGS", dt.Accent, dt)
				ui.Text(c, "Settings & Authorization").FontSize(16.5).FontWeight(700).TextColor(toColor(dt.Ink))
				ui.Spacer(c)
				if ApplePillButton(c, "close-settings", "Close", BtnGlass, dt, svgIconXMark, true).Clicked() {
					a.settingsOpen = false
				}
			})

			if len(tabs) > 1 {
				if chosen := AppleSegmentedControl(c, "settings-tab", tabs, string(activeTab), dt, true); chosen != "" {
					a.settingsTab = SettingsTab(chosen)
					activeTab = a.resolveSettingsTab(a.settingsTab)
				}
			}

			switch activeTab {
			case SettingsTabGeneral:
				if a.SettingsSchema != nil && a.SettingsState != nil {
					RenderBoundedForm(c, *a.SettingsSchema, a.SettingsState, func(vals map[string]any) {
						if a.OnSaveSettings != nil {
							a.OnSaveSettings(vals)
						}
						a.settingsOpen = false
					})
				}

			case SettingsTabAuth:
				if a.Auth != nil {
					a.Auth.RenderBody(c, t, dt)
				}

			case SettingsTabGovernance:
				AppleGlassCard(c, "settings-gov-card", dt, 14, 14, 10, func() {
					ui.Text(c, "Tier-0 Governance & Host Security").FontSize(14).FontWeight(700).TextColor(toColor(dt.Ink))
					ui.Text(c, "Manage compile-time UI feature modules, human-only PromptGate authorization boundaries, and federated Marlin Core gRPC targets.").
						FontSize(12).TextColor(toColor(dt.InkSecondary))
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						if a.GovernanceUI != nil {
							if ApplePillButton(c, "settings-launch-gov", "Open Governance Console", BtnPrimary, dt, svgIconShield, true).Clicked() {
								a.OpenGovernance()
							}
						}
						if a.FederationUI != nil {
							if ApplePillButton(c, "settings-launch-fed", "Federation Cockpit", BtnTinted, dt, nil, true).Clicked() {
								a.OpenFederation()
							}
						}
					})
				})
			}
		})
	})
}

// OpenSettings opens the Mobile Companion's Settings & Account modal.
func (m *MobileCompanionApp) OpenSettings() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.settingsOpen = true
	m.mu.Unlock()
}

// IsSettingsOpen reports whether the Mobile Companion settings modal is open.
func (m *MobileCompanionApp) IsSettingsOpen() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settingsOpen
}

func (m *MobileCompanionApp) renderMobileSettingsModal(c *ui.Context, _ *ui.Theme, dt DerivedTheme) {
	if m.SettingsSchema == nil || m.SettingsState == nil {
		return
	}
	ui.Modal(c, &m.settingsOpen, func() {
		ui.Column(c.Key("mobile-settings-modal")).Gap(10).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				AppleBadgePill(c, "SETTINGS", dt.Accent, dt)
				ui.Spacer(c)
				if ApplePillButton(c, "mob-close-settings", "Close", BtnGlass, dt, svgIconXMark, true).Clicked() {
					m.settingsOpen = false
				}
			})
			RenderBoundedForm(c, *m.SettingsSchema, m.SettingsState, func(vals map[string]any) {
				if m.OnSaveSettings != nil {
					m.OnSaveSettings(vals)
				}
				m.settingsOpen = false
			})
		})
	})
}
