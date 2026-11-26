// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// renderStatusBar paints a sleek 28px Zed / Emacs-modeline footer bar carrying
// ambient telemetry (target phase, profile/gen, modal keymap, and lazy tab
// memory counts) so the top navigation bar stays clean and uncluttered.
func (a *WorkbenchApp) renderStatusBar(c *ui.Context, t *ui.Theme, dt DerivedTheme, snap *schema.UIWorkbenchSnapshot) {
	ui.Row(c).Padding(4, 12).Gap(10).AlignItems(ui.Center).
		Background(toColor(dt.Surface)).
		BorderWidth(1, 0, 0, 0).BorderColor(toColor(dt.Border)).Children(func() {
		if active, ok := a.Targets.Active(); ok {
			meta := active.Meta()
			phaseStr := string(meta.Phase)
			if a.cachedTopBadge == "" || a.cachedTopTargetID != meta.ID || a.cachedTopKind != meta.TargetKind || a.cachedTopPhase != phaseStr {
				a.cachedTopTargetID = meta.ID
				a.cachedTopKind = meta.TargetKind
				a.cachedTopPhase = phaseStr
				a.cachedTopBadge = fmt.Sprintf("[%s · %s]", meta.TargetKind, phaseStr)
			}
			ui.Box(c).Size(7, 7).Radius(3.5).Background(toColor(dt.Success))
			if a.Federation != nil || a.FederationUI != nil {
				if ApplePillButton(c, "status-federation", a.cachedTopBadge, BtnGlass, dt, nil, true).Clicked() {
					a.OpenFederation()
				}
			} else {
				ui.Text(c, a.cachedTopBadge).FontSize(11).TextColor(toColor(dt.InkMuted))
			}
		}
		if snap != nil {
			if a.cachedSnapBadge == "" || a.cachedSnapProfile != snap.ProfileName || a.cachedSnapGen != snap.Generation {
				a.cachedSnapProfile = snap.ProfileName
				a.cachedSnapGen = snap.Generation
				a.cachedSnapBadge = fmt.Sprintf("profile:%s gen:%d", snap.ProfileName, snap.Generation)
			}
			ui.Text(c, a.cachedSnapBadge).FontSize(11).TextColor(toColor(dt.Accent))
		}
		ui.Spacer(c)
		if a.Windows != nil {
			wsnap := a.Windows.Snapshot()
			if a.cachedTabBadge == "" || a.cachedTabVer != wsnap.Version {
				a.cachedTabVer = wsnap.Version
				a.cachedTabBadge = fmt.Sprintf("Tabs %d (%d sleep 0MB)", wsnap.TotalTabs, wsnap.HibernatedTabs)
			}
			if ApplePillButton(c, "status-tabs", a.cachedTabBadge, BtnGlass, dt, nil, true).Clicked() {
				a.bufferPickerOpen = true
				a.bufferPickerQuery = ""
			}
		}
		if a.Notifications != nil {
			bellVar := BtnGlass
			if a.Notifications.Center != nil && a.Notifications.Center.Snapshot().AlertCount > 0 {
				bellVar = BtnTinted
			}
			if ApplePillButton(c, "status-activity", a.Notifications.BellLabel(), bellVar, dt, a.Notifications.BellIcon(), true).Clicked() {
				a.OpenActivityCenter()
			}
		}
		if a.GovernanceUI != nil {
			if ApplePillButton(c, "status-governance", "Gov", BtnGlass, dt, svgIconShield, true).Clicked() {
				a.OpenGovernance()
			}
		}
		if a.KeyTrie != nil {
			mode := a.ActiveKeyMode()
			modeLbl := "NORMAL · SPC"
			modeVar := BtnTinted
			nextMode := input.ScopeInsert
			if mode == input.ScopeInsert {
				modeLbl = "INSERT · EMACS"
				modeVar = BtnSuccess
				nextMode = input.ScopeNormal
			}
			if ApplePillButton(c, "key-mode", modeLbl, modeVar, dt, nil, true).Clicked() {
				a.SetKeyMode(nextMode)
			}
			if ApplePillButton(c, "status-whichkey", "SPC ?", BtnGlass, dt, nil, true).Clicked() {
				a.mu.Lock()
				a.whichKeyOpen = !a.whichKeyOpen
				a.mu.Unlock()
			}
			if ApplePillButton(c, "status-keymap", "Keys", BtnGlass, dt, svgIconKey, true).Clicked() {
				a.OpenKeymapInspector()
			}
		}
	})
}

func (a *WorkbenchApp) renderWhichKeyHUD(c *ui.Context, t *ui.Theme, dt DerivedTheme) {
	if a.KeyTrie == nil {
		return
	}
	a.mu.Lock()
	chord := append([]string(nil), a.activeChord...)
	mode := a.activeKeyMode
	if mode == 0 {
		mode = input.ScopeNormal
	}
	openCheatsheet := a.whichKeyOpen
	a.mu.Unlock()

	if len(chord) == 0 && !openCheatsheet && mode != input.ScopeInsert {
		return
	}

	var clickedToken string
	var closeSheet bool

	ui.Column(c).Padding(8, 12).Gap(6).
		Background(toColor(dt.SurfaceElevated)).
		BorderWidth(1, 0, 0, 0).BorderColor(toColor(dt.Border)).Children(func() {
		if len(chord) > 0 {
			hints := a.KeyTrie.Hints(mode, chord)
			prefixLabel := strings.Join(chord, " ")
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				AppleBadgePill(c, "WHICH-KEY · "+prefixLabel+" …", dt.Warning, dt)
				ui.Text(c, "Select next key or press ESC to cancel").FontSize(11).TextColor(toColor(dt.InkMuted))
				ui.Spacer(c)
				if ApplePillButton(c, "wk-cancel", "Cancel", BtnGlass, dt, nil, true).Clicked() {
					closeSheet = true
				}
			})
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				limit := min(len(hints), 9)
				for i := 0; i < limit; i++ {
					h := hints[i]
					desc := h.CommandName
					if h.IsPrefix {
						desc = fmt.Sprintf("+%s (%d)", whichKeyGroupName(h.Key), h.ChildCount)
					}
					label := fmt.Sprintf("[%s] %s", h.Key, desc)
					if ApplePillButton(c, "wk-"+h.Key, label, BtnGlass, dt, nil, true).Clicked() {
						clickedToken = h.Key
					}
				}
			})
			return
		}

		if mode == input.ScopeInsert && !openCheatsheet {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				AppleBadgePill(c, "EMACS EDIT · INSERT", dt.Success, dt)
				ui.Text(c, "C-n next  ·  C-p prev  ·  C-f/C-b char  ·  C-a/C-e line  ·  M-f/M-b word  ·  C-k kill  ·  C-y yank  ·  ESC/C-g Normal").
					FontSize(11).TextColor(toColor(dt.InkSecondary))
			})
			return
		}

		// Full Dual-Layout Cheatsheet (toggled via SPC ?)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			AppleBadgePill(c, "DUAL KEYMAP CHEATSHEET", dt.Accent, dt)
			ui.Text(c, "Layout 1: SPC Normal Leader  |  Layout 2: Emacs Insert/Edit").
				FontSize(11).TextColor(toColor(dt.InkMuted))
			ui.Spacer(c)
			if ApplePillButton(c, "wk-close-sheet", "Close", BtnGlass, dt, svgIconXMark, true).Clicked() {
				closeSheet = true
			}
		})
		ui.Text(c, "NORMAL (SPC):  SPC w [v/s/d/z/o/=/b/x] windows  ·  SPC o [r/c/d] spaces (Room/Compare/Canvas)  ·  SPC G gov  ·  SPC p [1-4] perspectives  ·  SPC m market  ·  i/a -> Insert").
			FontSize(11).TextColor(toColor(dt.Ink))
		ui.Text(c, "INSERT (EMACS):  C-n/C-p line  ·  C-f/C-b char  ·  C-a/C-e bol/eol  ·  M-f/M-b word  ·  C-d/C-h del  ·  C-k kill  ·  C-y yank  ·  C-x [0/1/2/3/o]  ·  ESC/C-g -> Normal").
			FontSize(11).TextColor(toColor(dt.InkSecondary))
	})

	if closeSheet {
		a.mu.Lock()
		a.activeChord = a.activeChord[:0]
		a.whichKeyOpen = false
		a.mu.Unlock()
	} else if clickedToken != "" {
		a.HandleActiveKeyToken(clickedToken)
	}
}

func whichKeyGroupName(k string) string {
	switch k {
	case "w":
		return "window"
	case "b":
		return "buffer/tab"
	case "p":
		return "perspective"
	case "o":
		return "spaces"
	case "s":
		return "search"
	case "t":
		return "theme"
	default:
		return "group"
	}
}

func (a *WorkbenchApp) renderSessionSidebar(c *ui.Context, dt DerivedTheme) {
	if len(a.Sessions) == 0 && a.OnNewSession == nil {
		return
	}
	ui.Column(c).Gap(5).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Text(c, "SESSIONS").FontSize(10.5).FontWeight(700).LetterSpacing(0.4).TextColor(toColor(dt.InkMuted))
			ui.Spacer(c)
			if a.OnNewSession != nil {
				if ApplePillButton(c, "sidebar-new-session", "+ New", BtnTinted, dt, nil, true).Clicked() {
					a.OnNewSession()
				}
			}
		})
		limit := min(len(a.Sessions), 8)
		for i := 0; i < limit; i++ {
			sess := a.Sessions[i]
			active := sess.SessionID == a.ActiveSessionID
			v := BtnGlass
			if active {
				v = BtnTinted
			}
			title := sess.Title
			if title == "" {
				title = sess.SessionID
			}
			label := fmt.Sprintf("%s (%d)", title, sess.TurnCount)
			if ApplePillButton(c, "sidebar-sess-"+sess.SessionID, label, v, dt, nil, true).Clicked() {
				a.ActiveSessionID = sess.SessionID
				if a.OnSelectSession != nil {
					a.OnSelectSession(sess.SessionID)
				}
			}
		}
	})
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
		sbVar := BtnGlass
		if a.IsSidebarCollapsed() {
			sbVar = BtnTinted
		}
		if ApplePillButton(c, "toggle-sidebar", "Sidebar", sbVar, a.cachedDerivedTheme, nil, true).Clicked() {
			a.ToggleSidebar()
		}
		if active, ok := a.Targets.Active(); ok {
			ui.Text(c, active.Meta().DisplayName).Bold()
		}
		if a.Windows != nil && a.OnSelectPerspective != nil {
			activePersp := a.Windows.Snapshot().Perspective
			if chosen := AppleSegmentedControl(c, "persp", []SegmentedItem{
				{ID: "focus", Label: "Focus"},
				{ID: "split", Label: "Split"},
				{ID: "cockpit", Label: "Cockpit"},
				{ID: "zen", Label: "Zen"},
			}, activePersp, a.cachedDerivedTheme, true); chosen != "" {
				a.OnSelectPerspective(chosen)
			}
		}
		a.mu.Lock()
		chord := strings.Join(a.activeChord, " ")
		a.mu.Unlock()
		if chord != "" {
			ui.Textf(c, "HYDRA: %s …", chord).FontSize(12).Bold().TextColor(t.Warning)
		}
		ui.Spacer(c)
		if a.Windows == nil && a.Transcript != nil && len(a.QuickPlugins) > 0 {
			chatLabel := "Show Chat"
			if !a.TranscriptHidden {
				chatLabel = "Hide Chat"
			}
			if ApplePillButton(c, "toggle-chat", chatLabel, BtnGlass, a.cachedDerivedTheme, nil, true).Clicked() {
				a.TranscriptHidden = !a.TranscriptHidden
			}
		}
		if a.OnCycleTheme != nil {
			themeLbl := "Theme"
			if a.ActiveThemeNameFn != nil && a.ActiveThemeNameFn() != "" {
				themeLbl = a.ActiveThemeNameFn()
			}
			if AppleSwatchPill(c, "cycle-theme", themeLbl, a.cachedDerivedTheme.Accent, false, a.cachedDerivedTheme).Clicked() {
				a.OnCycleTheme()
			}
		} else if a.OnToggleTheme != nil {
			themeLbl := "Light"
			if a.IsLightThemeFn != nil && a.IsLightThemeFn() {
				themeLbl = "Dark"
			}
			if ApplePillButton(c, "toggle-theme", themeLbl, BtnGlass, a.cachedDerivedTheme, nil, true).Clicked() {
				a.OnToggleTheme()
			}
		}
		if a.Projection != nil {
			if ApplePillButton(c, "open-projection", "Inspect", BtnTinted, a.cachedDerivedTheme, nil, true).Clicked() {
				a.projectionOpen = true
				a.paletteOpen = false
				a.marketplaceOpen = false
				a.settingsOpen = false
				a.addRemoteOpen = false
				a.gateOpen = false
			}
		}
		if a.SettingsSchema != nil && a.SettingsState != nil && (a.IsSidebarCollapsed() || a.OnCycleTheme == nil) {
			if ApplePillButton(c, "open-settings", "Settings", BtnGlass, a.cachedDerivedTheme, nil, true).Clicked() {
				a.OpenSettings()
			}
		}
		if a.Marketplace != nil {
			if ApplePillButton(c, "open-marketplace", "Marketplace", BtnGlass, a.cachedDerivedTheme, nil, true).Clicked() {
				a.marketplaceOpen = true
			}
		}
		if a.Auth != nil {
			authVar := BtnGlass
			if a.Auth.IsSignedIn() {
				authVar = BtnTinted
			}
			if ApplePillButton(c, "open-auth", a.Auth.TopBarLabel(), authVar, a.cachedDerivedTheme, svgIconShield, true).Clicked() {
				a.OpenAuth()
			}
		}
		if ApplePillButton(c, "open-palette", "Cmd+K Commands", BtnGlass, a.cachedDerivedTheme, nil, true).Clicked() {
			a.paletteOpen = true
			a.paletteQuery = ""
			a.paletteRankValid = false
			a.marketplaceOpen = false
			a.gateOpen = false
		}
		if a.pendingGate != nil {
			if ApplePillButton(c, "open-gate", "Review Gate", BtnPrimary, a.cachedDerivedTheme, svgIconShield, true).Clicked() {
				a.gateOpen = true
			}
		}
	})
}

func (a *WorkbenchApp) renderComposableWorkspace(
	c *ui.Context,
	t *ui.Theme,
	dt DerivedTheme,
	snap *schema.UIWorkbenchSnapshot,
	hasSnap bool,
	rctx shell.RenderContext,
) {
	defaultSplit := shell.PaneBuffer{BufferID: "builtin:transcript", Title: "AI Transcript", Kind: "native-gpu"}
	if len(a.WindowBuffers) > 1 {
		defaultSplit = a.WindowBuffers[1]
	}
	RenderWindowTree(c, t, dt, a.Windows, WindowRenderCallbacks{
		OnLayoutChanged: a.OnLayoutChanged,
		DefaultSplitTo:  func() shell.PaneBuffer { return defaultSplit },
		NextBufferFor: func(cur shell.PaneBuffer) shell.PaneBuffer {
			if len(a.WindowBuffers) == 0 {
				return cur
			}
			for i, b := range a.WindowBuffers {
				if b.BufferID == cur.BufferID {
					return a.WindowBuffers[(i+1)%len(a.WindowBuffers)]
				}
			}
			return a.WindowBuffers[0]
		},
		RenderBuffer: func(c *ui.Context, t *ui.Theme, _ DerivedTheme, _ shell.WindowID, buf shell.PaneBuffer) {
			switch buf.BufferID {
			case "builtin:transcript":
				if a.Transcript != nil {
					a.Transcript.Render(c, t, a.OnSendPrompt, a.OnCancelTurn)
				} else {
					ui.Text(c, "Transcript controller unavailable.").TextColor(t.TextMuted)
				}
			case "builtin:projection":
				if a.Projection != nil {
					a.Projection.Render(c, t, nil)
				} else {
					ui.Text(c, "Projection inspector unavailable.").TextColor(t.TextMuted)
				}
			case "slot:workbench.main":
				for _, r := range a.Slots.Resolve("workbench.main") {
					r.Render(rctx)
				}
			case "slot:workbench.inspector":
				for _, r := range a.Slots.Resolve("workbench.inspector") {
					r.Render(rctx)
				}
			default:
				if a.FeatureHost != nil {
					if fn, ok := a.FeatureHost.Renderer(buf.BufferID); ok {
						fn(c, t)
						return
					}
				}
				if strings.HasPrefix(buf.BufferID, "slot:") {
					slotID := schema.SlotID(strings.TrimPrefix(buf.BufferID, "slot:"))
					for _, r := range a.Slots.Resolve(slotID) {
						r.Render(rctx)
					}
				} else if hasSnap {
					RenderUIElement(c, snap.FrameTree, a.Slots, rctx)
				}
			}
		},
	})
}
