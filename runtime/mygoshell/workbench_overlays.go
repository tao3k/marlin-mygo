// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func (a *WorkbenchApp) renderOverlays(c *ui.Context, t *ui.Theme) {
	dt := a.cachedDerivedTheme

	ui.Modal(c, &a.paletteOpen, func() {
		ui.Text(c, "Command Palette (marlin.ui.v1)").FontSize(16).FontWeight(700).TextColor(toColor(dt.Ink))
		ui.Row(c).Padding(8, 12).Radius(14).
			Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
			ui.TextInputBase(c.Key("palette-input"), &a.paletteQuery).
				Placeholder("Type a Scheme or Emacs window command...").
				FontSize(13.5).TextColor(toColor(dt.Ink)).Grow(1).AutoFocus()
		})
		if len(a.Commands) > 0 {
			if !a.paletteRankValid || a.paletteLastQuery != a.paletteQuery || a.paletteLastCmdLen != len(a.Commands) {
				a.rankedBuf = input.RankCommands(a.paletteQuery, a.Commands, a.rankedBuf[:0])
				a.paletteLastQuery = a.paletteQuery
				a.paletteLastCmdLen = len(a.Commands)
				a.paletteRankValid = true
			}
			limit := min(len(a.rankedBuf), 8)
			ui.Column(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
				for i := 0; i < limit; i++ {
					cmd := a.Commands[a.rankedBuf[i].Index]
					if ApplePillButton(c, "cmd-"+cmd.Name, cmd.Name, BtnGlass, dt, nil, true).Clicked() {
						a.paletteOpen = false
						a.invokeCommand(cmd.Name)
					}
				}
			})
		}
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			if ApplePillButton(c, "close-palette", "Close", BtnGlass, dt, svgIconXMark, true).Clicked() {
				a.paletteOpen = false
			}
		})
	})

	if a.Windows != nil {
		ui.Modal(c, &a.bufferPickerOpen, func() {
			wsnap := a.Windows.Snapshot()
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, "Buffer & Tab Minibuffer (SPC b b)").FontSize(16).FontWeight(700).TextColor(toColor(dt.Ink))
				ui.Spacer(c)
				AppleBadgePill(c, fmt.Sprintf("%d active · %d paused · %d hibernated (0MB)",
					wsnap.ActiveTabs, wsnap.PausedTabs, wsnap.HibernatedTabs), dt.Accent, dt)
			})
			ui.Row(c).Padding(8, 12).Radius(14).
				Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
				ui.TextInputBase(c.Key("bufpick-input"), &a.bufferPickerQuery).
					Placeholder("Filter open tabs or stack a buffer into focused pane...").
					FontSize(13).TextColor(toColor(dt.Ink)).Grow(1).AutoFocus()
			})

			q := strings.ToLower(strings.TrimSpace(a.bufferPickerQuery))
			tabs := a.Windows.AllTabs()
			ui.Text(c, "OPEN TABS ACROSS PANES").FontSize(10.5).FontWeight(700).TextColor(toColor(dt.InkMuted))
			ui.Column(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
				for _, entry := range tabs {
					title := entry.Tab.Buffer.Title
					if title == "" {
						title = entry.Tab.Buffer.BufferID
					}
					if q != "" && !strings.Contains(strings.ToLower(title+" "+entry.Tab.Buffer.BufferID), q) {
						continue
					}
					stateLbl := strings.ToUpper(string(entry.Tab.State))
					tone := dt.InkMuted
					if entry.Tab.State == shell.TabActive {
						tone = dt.Success
					} else if entry.Tab.State == shell.TabHibernated {
						stateLbl = "HIBERNATED · 0MB"
						tone = dt.Info
					}
					if entry.Tab.Pinned {
						stateLbl = "PINNED · " + stateLbl
					}
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						btnKey := fmt.Sprintf("bufpick-tab-%s-%d", entry.WindowID, entry.TabIndex)
						btnLbl := fmt.Sprintf("%s [%s]", title, entry.WindowID)
						if ApplePillButton(c, btnKey, btnLbl, BtnGlass, dt, nil, true).Clicked() {
							a.Windows.SelectTab(entry.WindowID, entry.TabIndex)
							if a.OnLayoutChanged != nil {
								a.OnLayoutChanged(a.Windows.Snapshot())
							}
							a.bufferPickerOpen = false
						}
						AppleBadgePill(c, stateLbl, tone, dt)
						ui.Spacer(c)
						if entry.Tab.State != shell.TabHibernated {
							sleepKey := fmt.Sprintf("bufpick-sleep-%s-%d", entry.WindowID, entry.TabIndex)
							if ApplePillButton(c, sleepKey, "Sleep", BtnGlass, dt, nil, true).Clicked() {
								a.Windows.HibernateTab(entry.WindowID, entry.TabIndex)
								if a.OnLayoutChanged != nil {
									a.OnLayoutChanged(a.Windows.Snapshot())
								}
							}
						}
					})
				}
			})

			if len(a.WindowBuffers) > 0 {
				ui.Text(c, "STACK BUFFER INTO FOCUSED PANE").FontSize(10.5).FontWeight(700).TextColor(toColor(dt.InkMuted))
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					for _, buf := range a.WindowBuffers {
						lbl := buf.Title
						if lbl == "" {
							lbl = buf.BufferID
						}
						if q != "" && !strings.Contains(strings.ToLower(lbl+" "+buf.BufferID), q) {
							continue
						}
						if ApplePillButton(c, "bufpick-open-"+buf.BufferID, "+ "+lbl, BtnTinted, dt, nil, true).Clicked() {
							_, _ = a.Windows.OpenTabInFocused(buf)
							if a.OnLayoutChanged != nil {
								a.OnLayoutChanged(a.Windows.Snapshot())
							}
							a.bufferPickerOpen = false
						}
					}
				})
			}

			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				if ApplePillButton(c, "close-bufpick", "Close", BtnGlass, dt, svgIconXMark, true).Clicked() {
					a.bufferPickerOpen = false
				}
			})
		})
	}

	if a.Marketplace != nil {
		ui.Modal(c, &a.marketplaceOpen, func() {
			a.Marketplace.Render(c, t, func() {
				a.marketplaceOpen = false
			})
		})
	}

	if a.Auth != nil {
		ui.Modal(c, &a.authOpen, func() {
			a.Auth.Render(c, t, dt, func() {
				a.authOpen = false
			})
		})
	}

	if a.Notifications != nil {
		a.Notifications.RenderFloatingToasts(c, dt)
		ui.Modal(c, &a.activityOpen, func() {
			a.Notifications.RenderActivityCenter(c, t, dt, func() {
				a.activityOpen = false
			})
		})
	}

	if a.KeyTrie != nil && a.KeymapInspector != nil {
		ui.Modal(c, &a.keymapOpen, func() {
			a.KeymapInspector.Render(c, dt, a.KeyTrie, a.Commands, func(cmdName string) {
				a.invokeCommand(cmdName)
			}, func() {
				a.keymapOpen = false
			})
		})
	}

	if a.SettingsSchema != nil && a.SettingsState != nil {
		ui.Modal(c, &a.settingsOpen, func() {
			RenderBoundedForm(c, *a.SettingsSchema, a.SettingsState, func(vals map[string]any) {
				if a.OnSaveSettings != nil {
					a.OnSaveSettings(vals)
				}
				a.settingsOpen = false
			})
			ui.Row(c).Justify(ui.End).Children(func() {
				if ApplePillButton(c, "close-settings", "Close", BtnGlass, dt, svgIconXMark, true).Clicked() {
					a.settingsOpen = false
				}
			})
		})
	}

	if a.Projection != nil {
		ui.Modal(c, &a.projectionOpen, func() {
			a.Projection.Render(c, t, func() {
				a.projectionOpen = false
			})
		})
	}

	if a.FederationUI != nil {
		ui.Modal(c, &a.federationOpen, func() {
			a.FederationUI.Render(c, t, func() {
				a.federationOpen = false
			})
		})
	}

	if a.GovernanceUI != nil {
		ui.Modal(c, &a.governanceOpen, func() {
			a.GovernanceUI.Render(c, t, func() {
				a.governanceOpen = false
			})
		})
	}

	if a.OnAddRemoteTarget != nil && a.addRemoteState != nil {
		ui.Modal(c, &a.addRemoteOpen, func() {
			RenderBoundedForm(c, remoteTargetFormSchema(), a.addRemoteState, func(vals map[string]any) {
				targetID, _ := vals["target_id"].(string)
				displayName, _ := vals["display_name"].(string)
				endpoint, _ := vals["endpoint"].(string)
				token, _ := vals["bearer_token"].(string)
				if err := a.OnAddRemoteTarget(targetID, displayName, endpoint, token); err != nil {
					a.addRemoteStatus = err.Error()
					return
				}
				a.addRemoteStatus = ""
				a.addRemoteOpen = false
			})
			if a.addRemoteStatus != "" {
				ui.Text(c, a.addRemoteStatus).FontSize(12).TextColor(toColor(dt.Danger))
			}
			ui.Row(c).Justify(ui.End).Children(func() {
				if ApplePillButton(c, "close-add-remote", "Close", BtnGlass, dt, svgIconXMark, true).Clicked() {
					a.addRemoteOpen = false
				}
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
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			AppleBadgePill(c, "TIER-0 GATE", dt.Warning, dt)
			ui.Text(c, "Approval Gate (marlin.gate.v1)").FontSize(16).FontWeight(700).TextColor(toColor(dt.Ink))
		})
		ui.Textf(c, "Tool: %s", gate.ToolName).FontWeight(600).TextColor(toColor(dt.Warning))
		ui.Text(c, gate.Prompt).FontSize(15).FontWeight(600).TextColor(toColor(dt.Ink))
		if gate.Description != "" {
			ui.Text(c, gate.Description).FontSize(13).TextColor(toColor(dt.InkSecondary))
		}
		ui.Row(c).Justify(ui.End).Gap(10).Children(func() {
			if ApplePillButton(c, "gate-reject", "Reject", BtnDestructive, dt, svgIconXMark, false).Clicked() {
				a.gateOpen = false
				if a.OnGateDecision != nil {
					a.OnGateDecision(gate.GateID, false)
				}
			}
			if ApplePillButton(c, "gate-approve", "Approve Once", BtnPrimary, dt, svgIconCheck, false).Clicked() {
				a.gateOpen = false
				if a.OnGateDecision != nil {
					a.OnGateDecision(gate.GateID, true)
				}
			}
		})
	})

	// 120Hz non-destructive Helm / Minibuffer Live Search surface (Overlay or BottomDock).
	if a.helmSearchOpen && a.HelmSearch != nil {
		a.HelmSearch.Render(c, dt, 0, func() {
			a.helmSearchOpen = false
		})
	}

	// Tier-0 Ask / Option / Authorization queue floats at the highest z-order above all slots and modals.
	if a.PromptGate != nil {
		a.PromptGate.RenderDesktopModal(c, dt)
	}
}

func remoteTargetFormSchema() schema.BoundedSchema {
	return schema.BoundedSchema{
		ID:    "marlin.remote.target.v1",
		Title: "Connect Remote Marlin Host (TLS 1.3 gRPC)",
		Fields: []schema.FieldSpec{
			{Key: "target_id", Label: "Target ID", Kind: schema.FieldString, Default: "remote-gpu-1", MaxLen: 64},
			{Key: "display_name", Label: "Display Name", Kind: schema.FieldString, Default: "Remote Workstation", MaxLen: 64},
			{Key: "endpoint", Label: "gRPC Address", Kind: schema.FieldString, Default: "10.0.0.12:50051", MaxLen: 128},
			{Key: "bearer_token", Label: "Bearer Token", Kind: schema.FieldString, Default: "", MaxLen: 256},
		},
	}
}
