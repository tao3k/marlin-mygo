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

// WorkbenchApp renders the full modular Marlin shell on MyGo's native GPU UI.
// Overlays (Command Palette, Approval Gate, Hydra chord bar) are drawn last so
// they float above any embedded Untrusted WebView plugin slot.
type WorkbenchApp struct {
	Targets   *transport.TargetRegistry
	Slots     *shell.SlotRegistry
	Workbench *shell.WorkbenchController

	// Admitted Scheme/Marketplace command catalog and compiled Meow/Hydra KeyTrie.
	Commands []input.CommandEntry
	KeyTrie  *input.KeyTrie

	// Optional callbacks invoked by top-level chrome controls.
	OnGateDecision  func(gateID string, approve bool)
	OnCommandInvoke func(commandName string)
	OnTogglePlugin  func()
	PluginMountedFn func() bool

	mu           sync.Mutex
	selectedID   string
	paletteOpen  bool
	paletteQuery string
	rankedBuf    []input.RankedCommand
	activeChord  []string
	gateOpen     bool
	pendingGate  *PendingGateView
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

// ShowPendingGate opens the authoritative Approval Gate modal above all slots.
func (a *WorkbenchApp) ShowPendingGate(gate PendingGateView) {
	a.mu.Lock()
	cp := gate
	a.pendingGate = &cp
	a.gateOpen = true
	a.mu.Unlock()
}

// View is the ui.View callback passed directly to mygo.NewWindow or ui.NewTester.
func (a *WorkbenchApp) View(c *ui.Context) {
	if c.Shortcut(ui.Cmd, ui.KeyK) {
		a.paletteOpen = !a.paletteOpen
	}

	snap, hasSnap := a.Workbench.Snapshot()
	theme := schema.DefaultUITheme()
	if hasSnap {
		theme = snap.Theme
	}
	t := ApplyMarlinTheme(c, theme)

	targets := a.Targets.List()
	if active, ok := a.Targets.Active(); ok {
		a.selectedID = active.Meta().ID
	}

	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		// Left sidebar: unified multi-target switcher (Local + Mobile + Remote gRPC).
		ui.Column(c).Width(220).Padding(10).Gap(8).
			Background(t.Surface).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
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

		// Right workspace: top chrome + Scheme UI IR frame tree + hot-pluggable slots.
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			a.renderTopBar(c, t, snap)
			ui.Column(c).Grow(1).MinWidth(0).Padding(12).Gap(10).Children(func() {
				rctx := shell.RenderContext{
					Surface:  c,
					Theme:    theme,
					TargetID: a.selectedID,
				}
				if hasSnap {
					rctx.SessionID = snap.SessionLabel
					RenderUIElement(c, snap.FrameTree, a.Slots, rctx)
				} else {
					ui.Text(c, "Waiting for marlin.ui.v1 workbench snapshot...").TextColor(t.TextMuted)
				}
			})
		})
	})

	// Overlays painted last so they float above any embedded ui.WebView plugin.
	a.renderOverlays(c, t)
}

func (a *WorkbenchApp) renderTopBar(c *ui.Context, t *ui.Theme, snap *schema.UIWorkbenchSnapshot) {
	ui.Row(c).Padding(8, 12).Gap(10).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		if active, ok := a.Targets.Active(); ok {
			meta := active.Meta()
			ui.Text(c, meta.DisplayName).Bold()
			ui.Textf(c, "[%s · %s]", meta.TargetKind, meta.Phase).FontSize(12).TextColor(t.TextMuted)
		}
		if snap != nil {
			ui.Textf(c, "profile:%s gen:%d", snap.ProfileName, snap.Generation).FontSize(12).TextColor(t.Accent)
		}
		a.mu.Lock()
		chord := strings.Join(a.activeChord, " ")
		a.mu.Unlock()
		if chord != "" {
			ui.Textf(c, "HYDRA: %s …", chord).FontSize(12).Bold().TextColor(t.Warning)
		}
		ui.Spacer(c)
		if ui.Button(c.Key("open-palette"), "Cmd+K Commands").Clicked() {
			a.paletteOpen = true
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
			a.rankedBuf = input.RankCommands(a.paletteQuery, a.Commands, a.rankedBuf[:0])
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
