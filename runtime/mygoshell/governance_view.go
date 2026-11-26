// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/contextgov"
	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

// GovernanceController renders Marlin's Tier-0 UI Governance & Feature Security
// Console modal, surfacing compiled feature modules, trust-tier isolation,
// runtime enable/disable toggles governed by GovernancePolicy, AI/Tool E-Stop
// circuit-breaker controls, 5-scope Shared Context posture, and the immutable
// newest-first audit receipt trail.
type GovernanceController struct {
	mu               sync.RWMutex
	Registry         *features.Registry
	Ledger           *features.GovernanceLedger
	Host             *features.Host
	Breaker          *pluginhost.ExecutionBreaker
	PluginHost       *pluginhost.Host
	ContextGov       *contextgov.GovernanceStore
	BackendAuthority BackendGovernanceAuthority
	stepUpVerified   bool
	auditFilter      string // "all" | "admitted" | "denied" | "gate"
	lastErr          string

	OnFeatureChanged func(id features.FeatureID, enabled bool)
	OnEmergencyStop  func(tripped bool, epoch uint64)
}

// AttachSecurityControls links Marlin's AI/Tool ExecutionBreaker, pluginhost.Host,
// and 5-scope contextgov.GovernanceStore into the Tier-0 Governance Console.
func (g *GovernanceController) AttachSecurityControls(
	breaker *pluginhost.ExecutionBreaker,
	pluginHost *pluginhost.Host,
	contextGov *contextgov.GovernanceStore,
) {
	g.mu.Lock()
	g.Breaker = breaker
	g.PluginHost = pluginHost
	g.ContextGov = contextGov
	g.mu.Unlock()
}

// AttachBackendAuthority binds the authoritative Rust+Scheme Higher-Order Governance
// control plane (`marlin-agent-core`) so this UI console surfaces live ASCENT fenced
// actors, Bi-Temporal Truth Maintenance retractions, synthesized Cedar forbid rules,
// and RFC 9470 step-up E-Stop re-arm.
func (g *GovernanceController) AttachBackendAuthority(auth BackendGovernanceAuthority) {
	g.mu.Lock()
	g.BackendAuthority = auth
	g.mu.Unlock()
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
	breaker := g.Breaker
	pluginHost := g.PluginHost
	ctxGov := g.ContextGov
	backendAuth := g.BackendAuthority
	stepUpOK := g.stepUpVerified
	filter := g.auditFilter
	lastErr := g.lastErr
	onChanged := g.OnFeatureChanged
	onEStop := g.OnEmergencyStop
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
		toggleID      features.FeatureID
		toggleEnable  bool
		tripEStop     bool
		rearmEStop    bool
		verifyStepUp  bool
		backendTrip   bool
		backendRearm  bool
		nextFilter    = filter
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

		// Authoritative Rust + Gerbil Scheme Higher-Order Governance Control Plane.
		if backendAuth != nil {
			bsnap := backendAuth.Snapshot()
			AppleGlassCard(c, "gov-backend-ho-card", dt, 12, 10, 6, func() {
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Text(c, "RUST + SCHEME HIGHER-ORDER GOVERNANCE AUTHORITY").
						FontSize(10.5).FontWeight(700).LetterSpacing(0.5).TextColor(toColor(dt.InkMuted))
					if bsnap.Admitted && !bsnap.BreakerTripped {
						AppleBadgePill(c, "BACKEND ADMITTED", dt.Success, dt)
					} else {
						AppleBadgePill(c, "BACKEND FAIL-CLOSED", dt.Danger, dt)
					}
					AppleBadgePill(c, fmt.Sprintf("Proof Epoch: %d", bsnap.RevocationEpoch), dt.Info, dt)
					AppleBadgePill(c, fmt.Sprintf("Cedar Rules: %d", len(bsnap.SynthesizedCedarRules)), dt.Accent, dt)
					ui.Spacer(c)
					if bsnap.BreakerTripped {
						if !stepUpOK {
							if ApplePillButton(c, "gov-stepup-verify", "Verify Step-Up", BtnGlass, dt, nil, true).Clicked() {
								verifyStepUp = true
							}
						} else {
							AppleBadgePill(c, "RFC 9470 STEP-UP READY", dt.Success, dt)
						}
						if ApplePillButton(c, "gov-backend-rearm", "Step-Up Backend Re-Arm", BtnTinted, dt, nil, true).Clicked() {
							backendRearm = true
						}
					} else {
						if ApplePillButton(c, "gov-backend-trip", "Trip Backend E-Stop", BtnDestructive, dt, nil, true).Clicked() {
							backendTrip = true
						}
					}
				})
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					if len(bsnap.FencedActors) > 0 {
						AppleBadgePill(c, "ASCENT Fenced: "+strings.Join(bsnap.FencedActors, ", "), dt.Danger, dt)
					} else {
						AppleBadgePill(c, "ASCENT Fenced: 0", dt.Success, dt)
					}
					if len(bsnap.RetractedConclusions) > 0 {
						AppleBadgePill(c, "TM Retracted: "+strings.Join(bsnap.RetractedConclusions, ", "), dt.Warning, dt)
					} else {
						AppleBadgePill(c, "TM Retracted: 0", dt.Success, dt)
					}
					if len(bsnap.BlockedTools) > 0 {
						AppleBadgePill(c, "Blocked Tools: "+strings.Join(bsnap.BlockedTools, ", "), dt.Danger, dt)
					}
				})
				if len(bsnap.RejectionReasons) > 0 {
					ui.Textf(c, "Backend rejections: %s", strings.Join(bsnap.RejectionReasons, " · ")).
						FontSize(11).FontWeight(600).TextColor(toColor(dt.Danger))
				}
				if len(bsnap.SynthesizedCedarRules) > 0 {
					ids := make([]string, 0, len(bsnap.SynthesizedCedarRules))
					for _, r := range bsnap.SynthesizedCedarRules {
						ids = append(ids, r.PolicyID)
					}
					ui.Textf(c, "Synthesized Cedar: %s", strings.Join(ids, " · ")).
						FontSize(11).TextColor(toColor(dt.InkSecondary))
				}
			})
		}

		// AI & Tool Execution Circuit Breaker (E-Stop / 脱扣) Control Plane.
		if breaker != nil {
			tripped, tripEp, budgetUsed, activeLeases := breaker.State()
			AppleGlassCard(c, "gov-estop-card", dt, 12, 10, 6, func() {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Text(c, "AI & TOOL EXECUTION CIRCUIT BREAKER").
						FontSize(10.5).FontWeight(700).LetterSpacing(0.5).TextColor(toColor(dt.InkMuted))
					if tripped {
						AppleBadgePill(c, "TRIPPED · FAIL-CLOSED", dt.Danger, dt)
					} else {
						AppleBadgePill(c, "ARMED", dt.Success, dt)
					}
					AppleBadgePill(c, fmt.Sprintf("Epoch: %d", tripEp), dt.Info, dt)
					AppleBadgePill(c, fmt.Sprintf("Budget: %d · Leases: %d", budgetUsed, activeLeases), dt.Accent, dt)
					ui.Spacer(c)
					if tripped {
						if ApplePillButton(c, "gov-estop-rearm", "Human Re-Arm", BtnTinted, dt, nil, true).Clicked() {
							rearmEStop = true
						}
					} else {
						if ApplePillButton(c, "gov-estop-trip", "Trip E-Stop", BtnDestructive, dt, nil, true).Clicked() {
							tripEStop = true
						}
					}
				})
			})
		}

		// 5-Scope Shared Context Governance Posture Strip.
		if ctxGov != nil {
			counts := ctxGov.ScopeCounts()
			taintedCh := ctxGov.TaintedChannelCount()
			AppleGlassCard(c, "gov-context-scopes", dt, 12, 10, 6, func() {
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Text(c, "SHARED CONTEXT SCOPES").
						FontSize(10.5).FontWeight(700).LetterSpacing(0.5).TextColor(toColor(dt.InkMuted))
					AppleBadgePill(c, fmt.Sprintf("Private: %d", counts[contextgov.ScopePrivate]), dt.Danger, dt)
					AppleBadgePill(c, fmt.Sprintf("DM: %d", counts[contextgov.ScopeDM]), dt.Warning, dt)
					AppleBadgePill(c, fmt.Sprintf("Channel: %d", counts[contextgov.ScopeChannel]), dt.Info, dt)
					AppleBadgePill(c, fmt.Sprintf("Workspace: %d", counts[contextgov.ScopeWorkspace]), dt.Success, dt)
					AppleBadgePill(c, fmt.Sprintf("External: %d", counts[contextgov.ScopeExternal]), dt.InkMuted, dt)
					if taintedCh > 0 {
						AppleBadgePill(c, fmt.Sprintf("Tainted Ch: %d", taintedCh), dt.Danger, dt)
					}
				})
			})
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
	if verifyStepUp {
		g.stepUpVerified = true
		g.lastErr = ""
	}
	g.mu.Unlock()

	if backendTrip && backendAuth != nil {
		snap, err := backendAuth.TripBackendBreaker("human-console", "manual backend E-Stop")
		if breaker != nil {
			breaker.Trip("human-console", "synced backend E-Stop")
		}
		if pluginHost != nil {
			pluginHost.TripEmergencyStop()
		}
		g.mu.Lock()
		if err != nil {
			g.lastErr = err.Error()
		} else {
			g.stepUpVerified = false
			g.lastErr = ""
		}
		g.mu.Unlock()
		if err == nil && onEStop != nil {
			onEStop(true, snap.BreakerTripEpoch)
		}
	}
	if backendRearm && backendAuth != nil {
		snap, err := backendAuth.RearmBackendBreakerWithStepUp("human-console", stepUpOK)
		g.mu.Lock()
		if err != nil {
			g.lastErr = err.Error()
		} else {
			g.stepUpVerified = false
			g.lastErr = ""
		}
		g.mu.Unlock()
		if err == nil {
			if breaker != nil {
				_, _ = breaker.Rearm("human-console", false, true)
			}
			if onEStop != nil {
				onEStop(false, snap.BreakerTripEpoch)
			}
		}
	}

	if tripEStop && breaker != nil {
		breaker.Trip("human-console", "manual Tier-0 Governance Console E-Stop")
		if pluginHost != nil {
			pluginHost.TripEmergencyStop()
		}
		_, ep, _, _ := breaker.State()
		if onEStop != nil {
			onEStop(true, ep)
		}
	}
	if rearmEStop && breaker != nil {
		ep, err := breaker.Rearm("human-console", false, true)
		g.mu.Lock()
		if err != nil {
			g.lastErr = err.Error()
		} else {
			g.lastErr = ""
		}
		g.mu.Unlock()
		if err == nil && onEStop != nil {
			onEStop(false, ep)
		}
	}

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
