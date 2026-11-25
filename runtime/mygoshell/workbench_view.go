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
	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// PendingGateView is the shell overlay representation of marlin.gate.v1.PendingGate.
type PendingGateView struct {
	GateID      string
	ToolName    string
	Prompt      string
	Description string
}

// QuickPluginEntry defines an optional sidebar quick-switch button used in
// desktop showcase mode to hot-swap between interactive AI workflow plugins.
type QuickPluginEntry struct {
	PluginID string
	Label    string
}

// WorkbenchApp renders the full modular Marlin shell on MyGo's native GPU UI.
// Overlays (Command Palette, Approval Gate, Hydra chord bar) are drawn last so
// they float above any embedded Untrusted WebView plugin slot.
type WorkbenchApp struct {
	Targets           *transport.TargetRegistry
	Federation        *transport.FederationSupervisor
	Slots             *shell.SlotRegistry
	Workbench         *shell.WorkbenchController
	TargetWorkbenches map[string]*shell.WorkbenchController

	// Admitted Scheme/Marketplace command catalog and compiled Meow/Hydra KeyTrie.
	Commands []input.CommandEntry
	KeyTrie  *input.KeyTrie

	// Optional live Fenwick-virtualized streaming transcript + interactive composer.
	Transcript       *TranscriptController
	TranscriptHidden bool
	OnSendPrompt     func(prompt string)
	OnCancelTurn     func()

	// Optional studio brand heading displayed at the top of the sidebar.
	BrandTitle    string
	BrandSubtitle string

	// Optional built-in Tier-0 Host GPU Marketplace control drawer (floats above WebViews).
	Marketplace *MarketplaceController

	// Optional callbacks invoked by top-level chrome controls.
	OnGateDecision      func(gateID string, approve bool)
	OnCommandInvoke     func(commandName string)
	OnTogglePlugin      func()
	PluginMountedFn     func() bool
	QuickPlugins        []QuickPluginEntry
	ActiveQuickPluginID func() string
	OnQuickPluginSelect func(pluginID string)

	mu                sync.Mutex
	selectedID        string
	paletteOpen       bool
	paletteQuery      string
	paletteRankValid  bool
	paletteLastQuery  string
	paletteLastCmdLen int
	marketplaceOpen   bool
	rankedBuf         []input.RankedCommand
	activeChord       []string
	gateOpen          bool
	pendingGate       *PendingGateView
	cachedThemeKey    schema.UITheme
	cachedTheme       *ui.Theme
	cachedTopTargetID string
	cachedTopKind     schema.TargetKind
	cachedTopPhase    string
	cachedTopBadge    string
	cachedSnapProfile string
	cachedSnapGen     uint64
	cachedSnapBadge   string
}

func NewWorkbenchApp(
	targets *transport.TargetRegistry,
	slots *shell.SlotRegistry,
	wb *shell.WorkbenchController,
) *WorkbenchApp {
	app := &WorkbenchApp{
		Targets:   targets,
		Slots:     slots,
		Workbench: wb,
	}
	if active, ok := targets.Active(); ok {
		app.selectedID = active.Meta().ID
	}
	return app
}

// HandleKeyToken feeds one normalized key token (e.g. "SPC", "w", "v") through
// the compiled Scheme KeyTrie in O(chord_len). On MatchPrefix it retains the
// chord for Hydra hint rendering; on MatchExact it invokes OnCommandInvoke.
func (a *WorkbenchApp) HandleKeyToken(mode input.KeyScope, token string) (input.MatchKind, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.KeyTrie == nil || token == "" {
		a.activeChord = a.activeChord[:0]
		return input.MatchNone, ""
	}
	a.activeChord = append(a.activeChord, token)
	kind, binding := a.KeyTrie.Lookup(mode, a.activeChord)
	switch kind {
	case input.MatchPrefix:
		return input.MatchPrefix, strings.Join(a.activeChord, " ")
	case input.MatchExact:
		a.activeChord = a.activeChord[:0]
		if a.OnCommandInvoke != nil && binding.CommandName != "" {
			a.OnCommandInvoke(binding.CommandName)
		}
		return input.MatchExact, binding.CommandName
	default:
		a.activeChord = a.activeChord[:0]
		return input.MatchNone, ""
	}
}

// SetPendingGate registers an authoritative Approval Gate and optionally opens
// its modal immediately (openNow=false keeps it armed in the top bar).
func (a *WorkbenchApp) SetPendingGate(gate PendingGateView, openNow bool) {
	a.mu.Lock()
	cp := gate
	a.pendingGate = &cp
	a.gateOpen = openNow
	a.mu.Unlock()
}

// ShowPendingGate opens the authoritative Approval Gate modal above all slots.
func (a *WorkbenchApp) ShowPendingGate(gate PendingGateView) {
	a.SetPendingGate(gate, true)
}

// activeWorkbench resolves the WorkbenchController for the currently selected target,
// ensuring isolated multi-machine sessions never bleed snapshots across targets.
func (a *WorkbenchApp) activeWorkbench() *shell.WorkbenchController {
	if a.TargetWorkbenches != nil && a.selectedID != "" {
		if wb, ok := a.TargetWorkbenches[a.selectedID]; ok && wb != nil {
			return wb
		}
	}
	return a.Workbench
}

// View is the ui.View callback passed directly to mygo.NewWindow or ui.NewTester.
func (a *WorkbenchApp) View(c *ui.Context) {
	if c.Shortcut(ui.Cmd, ui.KeyK) {
		a.paletteOpen = !a.paletteOpen
		if a.paletteOpen {
			a.paletteQuery = ""
			a.paletteRankValid = false
			a.marketplaceOpen = false
			a.gateOpen = false
		}
	}

	if active, ok := a.Targets.Active(); ok {
		a.selectedID = active.Meta().ID
	}

	activeWB := a.activeWorkbench()
	var snap *schema.UIWorkbenchSnapshot
	var hasSnap bool
	if activeWB != nil {
		snap, hasSnap = activeWB.Snapshot()
	}
	theme := schema.DefaultUITheme()
	if hasSnap {
		theme = snap.Theme
	}
	var t *ui.Theme
	if a.cachedTheme != nil && a.cachedThemeKey == theme {
		t = a.cachedTheme
		c.SetTheme(t)
	} else {
		t = ApplyMarlinTheme(c, theme)
		a.cachedThemeKey = theme
		a.cachedTheme = t
	}

	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		// Left sidebar: studio brand, AI workflow quick-switcher, and federated nodes.
		ui.Column(c).Width(228).Padding(14).Gap(10).AlignItems(ui.Stretch).
			Background(t.Surface).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
			if a.BrandTitle != "" {
				ui.Column(c).Gap(2).Padding(0, 2, 6, 2).Children(func() {
					ui.Text(c, a.BrandTitle).FontSize(15).Bold().TextColor(t.Text)
					if a.BrandSubtitle != "" {
						ui.Text(c, a.BrandSubtitle).FontSize(11).TextColor(t.TextMuted)
					}
				})
			}
			if len(a.QuickPlugins) > 0 && a.OnQuickPluginSelect != nil {
				ui.Text(c, "AI WORKFLOW PLUGINS").FontSize(10).Bold().TextColor(t.TextMuted)
				activeID := ""
				if a.ActiveQuickPluginID != nil {
					activeID = a.ActiveQuickPluginID()
				}
				ui.Column(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
					for _, qp := range a.QuickPlugins {
						var clicked bool
						if qp.PluginID == activeID {
							clicked = ui.PrimaryButton(c.Key(qp.PluginID), qp.Label).Clicked()
						} else {
							clicked = ui.Button(c.Key(qp.PluginID), qp.Label).Clicked()
						}
						if clicked {
							a.OnQuickPluginSelect(qp.PluginID)
						}
					}
				})
			}
			a.renderTargetSidebar(c, t)
			if a.OnTogglePlugin != nil {
				label := "Mount Plugin"
				if a.PluginMountedFn != nil && a.PluginMountedFn() {
					label = "Unmount Plugin"
				}
				if ui.Button(c.Key("toggle-plugin"), label).Clicked() {
					a.OnTogglePlugin()
				}
			}
		})

		// Right workspace: crisp top chrome + Scheme UI IR frame tree + hot-pluggable slots.
		ui.Column(c).Grow(1).MinWidth(0).AlignItems(ui.Stretch).Children(func() {
			a.renderTopBar(c, t, snap)
			ui.Column(c).Grow(1).MinWidth(0).Padding(14).Gap(12).AlignItems(ui.Stretch).Children(func() {
				rctx := shell.RenderContext{
					Surface:  c,
					Theme:    theme,
					TargetID: a.selectedID,
				}
				if hasSnap {
					rctx.SessionID = snap.SessionLabel
					RenderUIElement(c, snap.FrameTree, a.Slots, rctx)
				} else if a.Transcript == nil {
					ui.Text(c, "Waiting for marlin.ui.v1 workbench snapshot...").TextColor(t.TextMuted)
				}
				if a.Transcript != nil && !a.TranscriptHidden {
					a.Transcript.Render(c, t, a.OnSendPrompt, a.OnCancelTurn)
				}
			})
		})
	})

	// Overlays painted last so they float above any embedded ui.WebView plugin.
	a.renderOverlays(c, t)
}

func (a *WorkbenchApp) renderTargetSidebar(c *ui.Context, t *ui.Theme) {
	if a.Federation != nil {
		fed := a.Federation.Snapshot()
		header := "MARLIN FEDERATION"
		if fed.TotalPendingGates > 0 {
			header = fmt.Sprintf("MARLIN FEDERATION [%d GATE]", fed.TotalPendingGates)
		}
		ui.Text(c, header).FontSize(11).Bold().TextColor(t.TextMuted)
		if gateTarget, hasRemoteGate := a.Federation.NextGateTarget(); hasRemoteGate && gateTarget != a.selectedID {
			if ui.PrimaryButton(c.Key("jump-gate"), fmt.Sprintf("Jump to Gate (%s)", gateTarget)).Clicked() {
				_, _ = a.Targets.Switch(gateTarget)
				a.selectedID = gateTarget
			}
		}
		prev := a.selectedID
		ui.Sidebar(c, &a.selectedID, func() {
			var lastRole transport.NodeRole
			for _, node := range fed.Nodes {
				if node.Role != lastRole {
					ui.Text(c, roleSectionHeading(node.Role)).FontSize(10).Bold().TextColor(t.TextMuted)
					lastRole = node.Role
				}
				label := formatFederatedNodeLabel(node)
				ui.SidebarItem(c, node.Meta.ID, nil, label)
			}
		}).Grow(1)
		if a.selectedID != prev && a.selectedID != "" {
			_, _ = a.Targets.Switch(a.selectedID)
		}
		return
	}

	targets := a.Targets.List()
	ui.Text(c, "MARLIN TARGETS").FontSize(11).Bold().TextColor(t.TextMuted)
	prev := a.selectedID
	ui.Sidebar(c, &a.selectedID, func() {
		for _, tm := range targets {
			label := fmt.Sprintf("%s (%s)", tm.DisplayName, tm.Mode)
			ui.SidebarItem(c, tm.ID, nil, label)
		}
	}).Grow(1)
	if a.selectedID != prev && a.selectedID != "" {
		_, _ = a.Targets.Switch(a.selectedID)
	}
}

func roleSectionHeading(role transport.NodeRole) string {
	switch role {
	case transport.RoleLocalWorkstation:
		return "LOCAL WORKSTATION"
	case transport.RoleRemoteAgent:
		return "REMOTE AGENTS"
	case transport.RoleMobileCompanion:
		return "MOBILE COMPANIONS"
	default:
		return "TARGETS"
	}
}

func formatFederatedNodeLabel(n transport.FederatedNode) string {
	var b strings.Builder
	b.WriteString(n.Meta.DisplayName)
	b.WriteString(" · ")
	b.WriteString(string(n.Health.Phase))
	if n.Health.LatencyMS > 0 {
		fmt.Fprintf(&b, " %dms", n.Health.LatencyMS)
	}
	if n.Health.PendingGates > 0 {
		fmt.Fprintf(&b, " [%d GATE]", n.Health.PendingGates)
	}
	return b.String()
}

func (a *WorkbenchApp) renderTopBar(c *ui.Context, t *ui.Theme, snap *schema.UIWorkbenchSnapshot) {
	ui.Row(c).Padding(8, 12).Gap(10).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		if active, ok := a.Targets.Active(); ok {
			meta := active.Meta()
			ui.Text(c, meta.DisplayName).Bold()
			phaseStr := string(meta.Phase)
			if a.cachedTopBadge == "" || a.cachedTopTargetID != meta.ID || a.cachedTopKind != meta.TargetKind || a.cachedTopPhase != phaseStr {
				a.cachedTopTargetID = meta.ID
				a.cachedTopKind = meta.TargetKind
				a.cachedTopPhase = phaseStr
				a.cachedTopBadge = fmt.Sprintf("[%s · %s]", meta.TargetKind, phaseStr)
			}
			ui.Text(c, a.cachedTopBadge).FontSize(12).TextColor(t.TextMuted)
		}
		if snap != nil {
			if a.cachedSnapBadge == "" || a.cachedSnapProfile != snap.ProfileName || a.cachedSnapGen != snap.Generation {
				a.cachedSnapProfile = snap.ProfileName
				a.cachedSnapGen = snap.Generation
				a.cachedSnapBadge = fmt.Sprintf("profile:%s gen:%d", snap.ProfileName, snap.Generation)
			}
			ui.Text(c, a.cachedSnapBadge).FontSize(12).TextColor(t.Accent)
		}
		a.mu.Lock()
		chord := strings.Join(a.activeChord, " ")
		a.mu.Unlock()
		if chord != "" {
			ui.Textf(c, "HYDRA: %s …", chord).FontSize(12).Bold().TextColor(t.Warning)
		}
		ui.Spacer(c)
		if a.Transcript != nil && len(a.QuickPlugins) > 0 {
			chatLabel := "Show Chat"
			if !a.TranscriptHidden {
				chatLabel = "Hide Chat"
			}
			if ui.Button(c.Key("toggle-chat"), chatLabel).Clicked() {
				a.TranscriptHidden = !a.TranscriptHidden
			}
		}
		if a.Marketplace != nil {
			if ui.Button(c.Key("open-marketplace"), "Marketplace").Clicked() {
				a.marketplaceOpen = true
			}
		}
		if ui.Button(c.Key("open-palette"), "Cmd+K Commands").Clicked() {
			a.paletteOpen = true
			a.paletteQuery = ""
			a.paletteRankValid = false
			a.marketplaceOpen = false
			a.gateOpen = false
		}
		if a.pendingGate != nil {
			if ui.PrimaryButton(c.Key("open-gate"), "Review Gate").Clicked() {
				a.gateOpen = true
			}
		}
	})
}

func (a *WorkbenchApp) renderOverlays(c *ui.Context, t *ui.Theme) {
	ui.Modal(c, &a.paletteOpen, func() {
		ui.Text(c, "Command Palette (marlin.ui.v1)").FontSize(16).Bold()
		ui.TextInput(c, &a.paletteQuery).Placeholder("Type a Scheme command...").AutoFocus()
		if len(a.Commands) > 0 {
			if !a.paletteRankValid || a.paletteLastQuery != a.paletteQuery || a.paletteLastCmdLen != len(a.Commands) {
				a.rankedBuf = input.RankCommands(a.paletteQuery, a.Commands, a.rankedBuf[:0])
				a.paletteLastQuery = a.paletteQuery
				a.paletteLastCmdLen = len(a.Commands)
				a.paletteRankValid = true
			}
			limit := min(len(a.rankedBuf), 8)
			ui.Column(c).Gap(4).Children(func() {
				for i := 0; i < limit; i++ {
					cmd := a.Commands[a.rankedBuf[i].Index]
					if ui.Button(c.Key("cmd-"+cmd.Name), cmd.Name).Clicked() {
						a.paletteOpen = false
						if a.OnCommandInvoke != nil {
							a.OnCommandInvoke(cmd.Name)
						}
					}
				}
			})
		}
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c.Key("close-palette"), "Close").Clicked() {
				a.paletteOpen = false
			}
		})
	})

	if a.Marketplace != nil {
		ui.Modal(c, &a.marketplaceOpen, func() {
			a.Marketplace.Render(c, t, func() {
				a.marketplaceOpen = false
			})
		})
	}

	ui.Modal(c, &a.gateOpen, func() {
		a.mu.Lock()
		gate := a.pendingGate
		a.mu.Unlock()
		if gate == nil {
			a.gateOpen = false
			return
		}
		ui.Text(c, "Approval Gate (marlin.gate.v1)").FontSize(16).Bold()
		ui.Textf(c, "Tool: %s", gate.ToolName).Bold().TextColor(t.Warning)
		ui.Text(c, gate.Prompt)
		if gate.Description != "" {
			ui.Text(c, gate.Description).TextColor(t.TextMuted)
		}
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c.Key("gate-reject"), "Reject").Clicked() {
				a.gateOpen = false
				if a.OnGateDecision != nil {
					a.OnGateDecision(gate.GateID, false)
				}
			}
			if ui.PrimaryButton(c.Key("gate-approve"), "Approve Once").Clicked() {
				a.gateOpen = false
				if a.OnGateDecision != nil {
					a.OnGateDecision(gate.GateID, true)
				}
			}
		})
	})
}
