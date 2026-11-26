// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// GovernanceController renders Marlin's Tier-0 UI Governance & Feature Security
// Console modal, surfacing compiled feature modules, trust-tier isolation,
// runtime enable/disable toggles governed by GovernancePolicy, and the
// immutable newest-first audit receipt trail.
type GovernanceController struct {
	mu          sync.RWMutex
	Registry    *features.Registry
	Ledger      *features.GovernanceLedger
	Host        *features.Host
	auditFilter string // "all" | "admitted" | "denied" | "gate"
	lastErr     string

	OnFeatureChanged func(id features.FeatureID, enabled bool)
}

// NewGovernanceController constructs a Tier-0 UI Governance Console controller.
func NewGovernanceController(reg *features.Registry, ledger *features.GovernanceLedger, host *features.Host) *GovernanceController {
	if reg == nil {
		reg = features.NewRegistry()
	}
	if ledger == nil {
		ledger = features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	}
	if host == nil {
		host = features.NewHost()
	}
	return &GovernanceController{
		Registry:    reg,
		Ledger:      ledger,
		Host:        host,
		auditFilter: "all",
	}
}

// Render paints the Tier-0 UI Governance & Feature Security Console modal.
func (g *GovernanceController) Render(c *ui.Context, t *ui.Theme, onClose func()) {
	g.mu.RLock()
	reg := g.Registry
	ledger := g.Ledger
	host := g.Host
	filter := g.auditFilter
	lastErr := g.lastErr
	onChanged := g.OnFeatureChanged
	g.mu.RUnlock()

	dt := derivedThemeFromUITheme(t)
	statuses := reg.Snapshot()
	trail := ledger.AuditTrail()
	pol := ledger.Policy()

	var enabledCount, deniedCount int
	for _, st := range statuses {
		if st.Enabled {
			enabledCount++
		}
		if st.Denied {
			deniedCount++
		}
	}

	var (
		toggleID     features.FeatureID
		toggleEnable bool
		nextFilter   = filter
	)

	ui.Column(c.Key("governance-console")).Width(720).Gap(12).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			AppleBadgePill(c, "TIER-0 GOVERNANCE", dt.Accent, dt)
			ui.Text(c, "UI Governance & Feature Security").FontSize(15).FontWeight(700).TextColor(toColor(dt.Ink))
			ui.Spacer(c)
			if onClose != nil {
				if ApplePillButton(c, "gov-close", "Close", BtnGlass, dt, svgIconXMark, true).Clicked() {
					onClose()
				}
			}
		})

		// KPI & Security Posture Strip.
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			AppleBadgePill(c, fmt.Sprintf("Compiled: %d", len(statuses)), dt.Info, dt)
			AppleBadgePill(c, fmt.Sprintf("Active: %d", enabledCount), dt.Success, dt)
			denTone := dt.InkMuted
			if deniedCount > 0 {
				denTone = dt.Danger
			}
			AppleBadgePill(c, fmt.Sprintf("Denied: %d", deniedCount), denTone, dt)
			AppleBadgePill(c, fmt.Sprintf("Audit Receipts: %d", len(trail)), dt.Accent, dt)
			ui.Spacer(c)
			if pol.RequireGateForMutate {
				AppleBadgePill(c, "FAIL-CLOSED GATES", dt.Success, dt)
			}
		})

		if lastErr != "" {
			ui.Text(c, lastErr).FontSize(11.5).FontWeight(600).TextColor(toColor(dt.Danger))
		}

		// Compiled Optional Features Matrix.
		ui.Text(c, "COMPILED FEATURE MODULES (BUILD-TAG + RUNTIME GATED)").
			FontSize(10.5).FontWeight(700).LetterSpacing(0.5).TextColor(toColor(dt.InkMuted))
		if len(statuses) == 0 {
			AppleGlassCard(c, "gov-no-features", dt, 12, 10, 4, func() {
				ui.Text(c, "Minimal Core build active — zero optional interaction features linked.").
					FontSize(12).TextColor(toColor(dt.InkSecondary))
			})
		} else {
			ui.Column(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
				for _, st := range statuses {
					item := st
					AppleGlassCard(c, "gov-feat-"+string(item.Meta.ID), dt, 12, 10, 4, func() {
						ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
							ui.Text(c, item.Meta.Name).FontSize(13).FontWeight(700).TextColor(toColor(dt.Ink))
							AppleBadgePill(c, string(item.Meta.ID), dt.Info, dt)
							AppleBadgePill(c, strings.ToUpper(string(item.Meta.TrustTier)), trustTierTone(item.Meta.TrustTier, dt), dt)
							if item.Enabled {
								AppleBadgePill(c, "ACTIVE", dt.Success, dt)
							} else if item.Denied {
								AppleBadgePill(c, "DENIED", dt.Danger, dt)
							} else {
								AppleBadgePill(c, "OFF", dt.InkMuted, dt)
							}
							ui.Spacer(c)
							if item.Enabled {
								if ApplePillButton(c, "gov-off-"+string(item.Meta.ID), "Disable "+string(item.Meta.ID), BtnGlass, dt, nil, true).Clicked() {
									toggleID = item.Meta.ID
									toggleEnable = false
								}
							} else {
								if ApplePillButton(c, "gov-on-"+string(item.Meta.ID), "Enable "+string(item.Meta.ID), BtnTinted, dt, nil, true).Clicked() {
									toggleID = item.Meta.ID
									toggleEnable = true
								}
							}
						})
						if item.Meta.Description != "" {
							ui.Text(c, item.Meta.Description).FontSize(11.5).TextColor(toColor(dt.InkSecondary))
						}
						if item.Denied && item.DenialReason != "" {
							ui.Textf(c, "Policy denial: %s", item.DenialReason).FontSize(11).FontWeight(600).TextColor(toColor(dt.Danger))
						}
					})
				}
			})
		}

		// Immutable Governance & Authorization Audit Receipt Timeline.
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "AUDIT RECEIPT TIMELINE").
				FontSize(10.5).FontWeight(700).LetterSpacing(0.5).TextColor(toColor(dt.InkMuted))
			ui.Spacer(c)
			if chosen := AppleSegmentedControl(c, "gov-audit-filter", []SegmentedItem{
				{ID: "all", Label: "All"},
				{ID: "admitted", Label: "Admitted"},
				{ID: "denied", Label: "Denied"},
				{ID: "gate", Label: "Gates"},
			}, filter, dt, true); chosen != "" {
				nextFilter = chosen
			}
		})

		shown := 0
		ui.Column(c).Gap(4).AlignItems(ui.Stretch).Children(func() {
			for _, ev := range trail {
				if !matchesAuditFilter(ev.Outcome, nextFilter) {
					continue
				}
				if shown >= 8 {
					break
				}
				shown++
				tone := dt.Success
				if ev.Outcome == features.AuditDenied {
					tone = dt.Danger
				} else if ev.Outcome == features.AuditGateClaimed {
					tone = dt.Warning
				}
				ui.Row(c).Padding(6, 10).Gap(8).Radius(8).AlignItems(ui.Center).
					Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
					AppleBadgePill(c, ev.ReceiptID, dt.Info, dt)
					AppleBadgePill(c, string(ev.Outcome), tone, dt)
					ui.Textf(c, "%s · %s", ev.Subject, ev.Action).FontSize(11.5).FontWeight(600).TextColor(toColor(dt.Ink))
					ui.Spacer(c)
					if ev.Detail != "" {
						ui.Text(c, ev.Detail).FontSize(11).TextColor(toColor(dt.InkMuted))
					}
				})
			}
			if shown == 0 {
				ui.Text(c, "No governance audit receipts match this filter.").FontSize(11.5).TextColor(toColor(dt.InkMuted))
			}
		})
	})

	g.mu.Lock()
	g.auditFilter = nextFilter
	g.mu.Unlock()

	if toggleID != "" {
		err := reg.SetEnabled(toggleID, toggleEnable, ledger, host)
		g.mu.Lock()
		if err != nil {
			g.lastErr = err.Error()
		} else {
			g.lastErr = ""
		}
		g.mu.Unlock()
		if err == nil && onChanged != nil {
			onChanged(toggleID, toggleEnable)
		}
	}
}

func trustTierTone(tier features.TrustTier, dt DerivedTheme) schema.RGB {
	switch tier {
	case features.Tier0HostNative:
		return dt.Success
	case features.Tier1SandboxWASI:
		return dt.Info
	default:
		return dt.Warning
	}
}

func matchesAuditFilter(outcome features.AuditOutcome, filter string) bool {
	switch filter {
	case "admitted":
		return outcome == features.AuditAdmitted
	case "denied":
		return outcome == features.AuditDenied
	case "gate":
		return outcome == features.AuditGateClaimed
	default:
		return true
	}
}
