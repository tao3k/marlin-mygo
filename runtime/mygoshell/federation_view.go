// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// FederationController manages the Tier-0 native GPU Federation Cockpit modal,
// projecting live multi-machine health, latency, pending gates, sessions, and
// 1-click target switching / reconnection without storing any credentials.
type FederationController struct {
	mu         sync.RWMutex
	Supervisor *transport.FederationSupervisor
	Targets    *transport.TargetRegistry
	roleFilter string // "all" | "local" | "remote" | "mobile"

	OnProbeAll        func()
	OnReconnectTarget func(targetID string)
	OnSelectTarget    func(targetID string)
}

// NewFederationController constructs a Tier-0 Federation Cockpit controller.
func NewFederationController(sup *transport.FederationSupervisor, targets *transport.TargetRegistry) *FederationController {
	return &FederationController{
		Supervisor: sup,
		Targets:    targets,
		roleFilter: "all",
	}
}

// SetRoleFilter filters visible federation nodes ("all", "local", "remote", "mobile").
func (f *FederationController) SetRoleFilter(role string) {
	f.mu.Lock()
	f.roleFilter = strings.ToLower(strings.TrimSpace(role))
	if f.roleFilter == "" {
		f.roleFilter = "all"
	}
	f.mu.Unlock()
}

// Render paints the Tier-0 Federation Cockpit modal with live KPI summary,
// role filter, latency bars, pending-gate badges, and 1-click Switch/Reconnect.
func (f *FederationController) Render(c *ui.Context, t *ui.Theme, onClose func()) {
	f.mu.RLock()
	sup := f.Supervisor
	targets := f.Targets
	roleFilter := f.roleFilter
	onProbe := f.OnProbeAll
	onReconnect := f.OnReconnectTarget
	onSelect := f.OnSelectTarget
	f.mu.RUnlock()

	dt := derivedThemeFromUITheme(t)
	var snap transport.FederationSnapshot
	if sup != nil {
		snap = sup.Snapshot()
	}

	var readyCount, totalSessions, totalPlugins int
	var maxLat int64 = 1
	for _, n := range snap.Nodes {
		if n.Health.Phase == transport.PhaseReady {
			readyCount++
		}
		totalSessions += n.Health.ActiveSessions
		totalPlugins += n.Health.MountedPlugins
		if n.Health.LatencyMS > maxLat {
			maxLat = n.Health.LatencyMS
		}
	}

	var (
		probeClicked  bool
		switchID      string
		reconnectID   string
		nextRoleFilt  = roleFilter
	)

	ui.Column(c.Key("federation-cockpit")).Width(720).Gap(12).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			AppleBadgePill(c, "TIER-0 FEDERATION", dt.Accent, dt)
			ui.Text(c, "Multi-Target Federation Cockpit").FontSize(15).FontWeight(700).TextColor(toColor(dt.Ink))
			ui.Spacer(c)
			if onProbe != nil {
				if ApplePillButton(c, "fed-probe-all", "Probe All", BtnTinted, dt, svgIconBolt, true).Clicked() {
					probeClicked = true
				}
			}
			if onClose != nil {
				if ApplePillButton(c, "fed-close", "Close", BtnGlass, dt, svgIconXMark, true).Clicked() {
					onClose()
				}
			}
		})

		// KPI Summary Strip.
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			AppleBadgePill(c, fmt.Sprintf("Targets: %d", len(snap.Nodes)), dt.Info, dt)
			AppleBadgePill(c, fmt.Sprintf("Ready: %d", readyCount), dt.Success, dt)
			gateTone := dt.InkMuted
			if snap.TotalPendingGates > 0 {
				gateTone = dt.Warning
			}
			AppleBadgePill(c, fmt.Sprintf("Pending Gates: %d", snap.TotalPendingGates), gateTone, dt)
			AppleBadgePill(c, fmt.Sprintf("Sessions: %d", totalSessions), dt.Accent, dt)
			AppleBadgePill(c, fmt.Sprintf("Plugins: %d", totalPlugins), dt.Info, dt)
		})

		// Role filter segmented control.
		if chosen := AppleSegmentedControl(c, "fed-role-filter", []SegmentedItem{
			{ID: "all", Label: "All"},
			{ID: "local", Label: "Local"},
			{ID: "remote", Label: "Remote"},
			{ID: "mobile", Label: "Mobile"},
		}, roleFilter, dt, true); chosen != "" {
			nextRoleFilt = chosen
		}

		// Node topology cards.
		shown := 0
		for _, n := range snap.Nodes {
			if !matchesFederationRoleFilter(n.Role, nextRoleFilt) {
				continue
			}
			shown++
			node := n
			phaseTone := federationPhaseTone(node.Health.Phase, dt)
			AppleGlassCard(c, "fed-node-"+node.Meta.ID, dt, 14, 12, 6, func() {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Box(c).Size(8, 8).Radius(4).Background(toColor(phaseTone))
					ui.Text(c, node.Meta.DisplayName).FontSize(13.5).FontWeight(700).TextColor(toColor(dt.Ink))
					if node.Active {
						AppleBadgePill(c, "ACTIVE", dt.Accent, dt)
					}
					AppleBadgePill(c, strings.ToUpper(string(node.Health.Phase)), phaseTone, dt)
					AppleBadgePill(c, roleSectionHeading(node.Role), dt.Info, dt)
					ui.Spacer(c)
					if !node.Active {
						if ApplePillButton(c, "fed-switch-"+node.Meta.ID, "Switch", BtnTinted, dt, nil, true).Clicked() {
							switchID = node.Meta.ID
						}
					}
					if onReconnect != nil {
						if ApplePillButton(c, "fed-reconn-"+node.Meta.ID, "Reconnect", BtnGlass, dt, nil, true).Clicked() {
							reconnectID = node.Meta.ID
						}
					}
				})

				ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
					ui.Textf(c, "%s · %s", node.Meta.TargetKind, node.Meta.Address).
						FontSize(11.5).TextColor(toColor(dt.InkMuted))
					ui.Spacer(c)
					ui.Textf(c, "%dms · %d sess · %d plugins", node.Health.LatencyMS, node.Health.ActiveSessions, node.Health.MountedPlugins).
						FontSize(11).TextColor(toColor(dt.InkSecondary))
					if node.Health.PendingGates > 0 {
						AppleBadgePill(c, fmt.Sprintf("%d GATE", node.Health.PendingGates), dt.Warning, dt)
					}
					barW := float32(60) * float32(node.Health.LatencyMS) / float32(maxLat)
					if barW < 6 {
						barW = 6
					}
					ui.Row(c).Width(60).Height(6).Radius(3).Background(toColor(dt.SurfaceRecessed)).Children(func() {
						ui.Box(c).Width(barW).Height(6).Radius(3).Background(toColor(phaseTone))
					})
				})

				if node.Health.LastError != "" {
					ui.Textf(c, "Last error: %s", node.Health.LastError).FontSize(11).TextColor(toColor(dt.Danger))
				}
			})
		}
		if shown == 0 {
			ui.Text(c, "No federated targets match this filter.").FontSize(12).TextColor(toColor(dt.InkMuted))
		}
	})

	f.mu.Lock()
	f.roleFilter = nextRoleFilt
	f.mu.Unlock()

	if probeClicked && onProbe != nil {
		onProbe()
	}
	if switchID != "" {
		if targets != nil {
			_, _ = targets.Switch(switchID)
		}
		if onSelect != nil {
			onSelect(switchID)
		}
	}
	if reconnectID != "" && onReconnect != nil {
		onReconnect(reconnectID)
	}
}

func matchesFederationRoleFilter(role transport.NodeRole, filter string) bool {
	switch filter {
	case "local":
		return role == transport.RoleLocalWorkstation
	case "remote":
		return role == transport.RoleRemoteAgent
	case "mobile":
		return role == transport.RoleMobileCompanion
	default:
		return true
	}
}

func federationPhaseTone(phase transport.ConnPhase, dt DerivedTheme) schema.RGB {
	switch phase {
	case transport.PhaseReady:
		return dt.Success
	case transport.PhaseConnecting:
		return dt.Warning
	case transport.PhaseError:
		return dt.Danger
	default:
		return dt.InkMuted
	}
}
