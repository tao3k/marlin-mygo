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
	SidebarCollapsed bool
	OnSendPrompt     func(prompt string)
	OnCancelTurn     func()

	// Optional studio brand heading displayed at the top of the sidebar.
	BrandTitle    string
	BrandSubtitle string

	// Optional built-in Tier-0 Host GPU Marketplace control drawer (floats above WebViews).
	Marketplace *MarketplaceController

	// Optional Tier-0 Host GPU Authentication & Account Center modal (OAuth 2.1 PKCE / Email).
	Auth *AuthController

	// Optional Tier-0 Host Notification Toast Stack & Activity Center modal.
	Notifications *NotificationController

	// Optional native GPU marlin.projection.v1 inspector (Graph Receipts, Diff, Processes).
	Projection *ProjectionController

	// Optional Emacs-style composable binary split WindowTree. When non-nil, the
	// main workspace renders dynamic split panes with active/inactive modelines
	// instead of a single fixed frame.
	Windows             *shell.WindowTree
	WindowBuffers       []shell.PaneBuffer
	OnSelectPerspective func(name string)
	OnLayoutChanged     func(snap shell.WindowTreeSnapshot)

	// Optional callback invoked when the user adds a Remote TLS gRPC target via "+ Remote".
	OnAddRemoteTarget func(targetID, displayName, endpoint, bearerToken string) error

	// Optional callbacks invoked by top-level chrome controls.
	OnGateDecision      func(gateID string, approve bool)
	OnCommandInvoke     func(commandName string)
	OnEditCommand       func(commandName string)
	OnTogglePlugin      func()
	PluginMountedFn     func() bool
	QuickPlugins        []QuickPluginEntry
	ActiveQuickPluginID func() string
	OnQuickPluginSelect func(pluginID string)
	OnToggleTheme       func()
	IsLightThemeFn      func() bool
	ActiveThemeNameFn   func() string
	OnCycleTheme        func()
	SettingsSchema      *schema.BoundedSchema
	SettingsState       *SchemaFormState
	OnSaveSettings      func(values map[string]any)

	mu                sync.Mutex
	selectedID        string
	paletteOpen       bool
	paletteQuery      string
	paletteRankValid  bool
	paletteLastQuery  string
	paletteLastCmdLen int
	bufferPickerOpen  bool
	bufferPickerQuery string
	marketplaceOpen   bool
	authOpen          bool
	activityOpen      bool
	settingsOpen      bool
	projectionOpen    bool
	addRemoteOpen     bool
	addRemoteState    *SchemaFormState
	addRemoteStatus   string
	rankedBuf         []input.RankedCommand
	activeChord       []string
	activeKeyMode     input.KeyScope
	whichKeyOpen      bool
	gateOpen          bool
	pendingGate       *PendingGateView
	cachedThemeKey     schema.UITheme
	cachedTheme        *ui.Theme
	cachedDerivedTheme DerivedTheme
	cachedTopTargetID string
	cachedTopKind     schema.TargetKind
	cachedTopPhase    string
	cachedTopBadge    string
	cachedSnapProfile string
	cachedSnapGen     uint64
	cachedSnapBadge   string
	cachedTabVer      uint64
	cachedTabBadge    string
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

// ActiveKeyMode returns the current modal keymap layout (ScopeNormal SPC-leader
// by default, or ScopeInsert for Emacs Edit/Insert keybindings such as C-n/C-p).
func (a *WorkbenchApp) ActiveKeyMode() input.KeyScope {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeKeyMode == 0 {
		return input.ScopeNormal
	}
	return a.activeKeyMode
}

// SetKeyMode transitions between ScopeNormal (SPC leader) and ScopeInsert (Emacs edit).
func (a *WorkbenchApp) SetKeyMode(mode input.KeyScope) {
	a.mu.Lock()
	a.activeKeyMode = mode
	a.activeChord = a.activeChord[:0]
	a.mu.Unlock()
}

// EnableEmacsWindowKeymap compiles both modal keymap layouts — Layout 1 (Normal
// SPC leader) and Layout 2 (Insert/Edit Emacs C-n/C-p/C-a/C-e/C-k/C-y) plus
// global C-x window chords — and registers them in the Cmd+K Command Palette.
func (a *WorkbenchApp) EnableEmacsWindowKeymap() error {
	kt, err := shell.CompileDualModalKeymap()
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.KeyTrie = kt
	if a.activeKeyMode == 0 {
		a.activeKeyMode = input.ScopeNormal
	}
	existing := make(map[string]struct{}, len(a.Commands))
	for _, c := range a.Commands {
		existing[c.Name] = struct{}{}
	}
	for _, c := range shell.DualModalCommands() {
		if _, ok := existing[c.Name]; !ok {
			a.Commands = append(a.Commands, c)
		}
	}
	a.paletteRankValid = false
	a.mu.Unlock()
	return nil
}

// ApplyLayoutSpec projects a declarative Scheme layout rule onto WorkbenchApp's
// Emacs WindowTree and notifies OnLayoutChanged listeners (e.g. WebView panels).
func (a *WorkbenchApp) ApplyLayoutSpec(spec shell.LayoutSpec) error {
	if a.Windows == nil {
		return shell.ErrInvalidLayoutSpec
	}
	a.mu.Lock()
	catalog := make(map[string]shell.PaneBuffer, len(a.WindowBuffers))
	for _, b := range a.WindowBuffers {
		catalog[b.BufferID] = b
	}
	a.mu.Unlock()
	if err := shell.ApplyLayoutSpec(a.Windows, spec, catalog); err != nil {
		return err
	}
	if a.OnLayoutChanged != nil {
		a.OnLayoutChanged(a.Windows.Snapshot())
	}
	return nil
}

// RegisterWindowBuffer dynamically publishes a newly hot-plugged Official
// Marketplace plugin buffer into WorkbenchApp.WindowBuffers so every Emacs
// split pane can display or cycle to it immediately.
func (a *WorkbenchApp) RegisterWindowBuffer(buf shell.PaneBuffer) {
	if buf.BufferID == "" {
		return
	}
	a.mu.Lock()
	updated := false
	for i := range a.WindowBuffers {
		if a.WindowBuffers[i].BufferID == buf.BufferID {
			a.WindowBuffers[i] = buf
			updated = true
			break
		}
	}
	if !updated {
		a.WindowBuffers = append(a.WindowBuffers, buf)
	}
	a.mu.Unlock()
	if a.Windows != nil && a.OnLayoutChanged != nil {
		a.OnLayoutChanged(a.Windows.Snapshot())
	}
}

// UnregisterWindowBuffer removes an unplugged plugin buffer from WindowBuffers
// and migrates any Emacs pane currently displaying it to fallback so hot-unplug
// never leaves a blank or dangling window.
func (a *WorkbenchApp) UnregisterWindowBuffer(bufferID string, fallback shell.PaneBuffer) int {
	if bufferID == "" {
		return 0
	}
	a.mu.Lock()
	filtered := a.WindowBuffers[:0]
	for _, b := range a.WindowBuffers {
		if b.BufferID != bufferID {
			filtered = append(filtered, b)
		}
	}
	a.WindowBuffers = filtered
	if fallback.BufferID == "" && len(a.WindowBuffers) > 0 {
		fallback = a.WindowBuffers[0]
	}
	a.mu.Unlock()

	migrated := 0
	if a.Windows != nil {
		migrated = a.Windows.ReplaceBuffer(bufferID, fallback)
		if a.OnLayoutChanged != nil {
			a.OnLayoutChanged(a.Windows.Snapshot())
		}
	}
	return migrated
}

func (a *WorkbenchApp) invokeCommand(commandName string) {
	switch commandName {
	case shell.CmdModeInsert:
		a.SetKeyMode(input.ScopeInsert)
	case shell.CmdModeNormal:
		a.SetKeyMode(input.ScopeNormal)
	case shell.CmdStudioOpenMarketplace:
		a.marketplaceOpen = true
	case "studio.open-auth":
		a.OpenAuth()
	case "studio.open-notifications":
		a.OpenActivityCenter()
	case shell.CmdStudioOpenProjection:
		a.projectionOpen = true
	case shell.CmdStudioOpenPalette:
		a.paletteOpen = true
	case shell.CmdStudioOpenGate:
		if a.pendingGate != nil {
			a.gateOpen = true
		}
	case shell.CmdStudioCycleTheme:
		if a.OnCycleTheme != nil {
			a.OnCycleTheme()
		}
	case shell.CmdStudioToggleWhichKey:
		a.mu.Lock()
		a.whichKeyOpen = !a.whichKeyOpen
		a.mu.Unlock()
	case shell.CmdStudioToggleSidebar:
		a.ToggleSidebar()
	case shell.CmdBufferPick:
		a.bufferPickerOpen = true
		a.bufferPickerQuery = ""
		a.paletteOpen = false
		a.marketplaceOpen = false
		a.settingsOpen = false
		a.projectionOpen = false
		a.gateOpen = false
	default:
		if strings.HasPrefix(commandName, "edit.") && a.OnEditCommand != nil {
			a.OnEditCommand(commandName)
		}
	}
	if a.Windows != nil {
		if handled, err := shell.ExecuteWindowCommand(a.Windows, commandName, a.WindowBuffers); handled && err == nil {
			if a.OnLayoutChanged != nil {
				a.OnLayoutChanged(a.Windows.Snapshot())
			}
		}
	}
	if a.OnCommandInvoke != nil && commandName != "" {
		a.OnCommandInvoke(commandName)
	}
}

// HandleActiveKeyToken feeds token using the currently active modal layout
// (ScopeNormal SPC-leader or ScopeInsert Emacs-editing).
func (a *WorkbenchApp) HandleActiveKeyToken(token string) (input.MatchKind, string) {
	return a.HandleKeyToken(a.ActiveKeyMode(), token)
}

// HandleKeyToken feeds one normalized key token (e.g. "SPC", "w", "v") through
// the compiled Scheme KeyTrie in O(chord_len). On MatchPrefix it retains the
// chord for Hydra hint rendering; on MatchExact it executes any bound Emacs
// WindowTree command and invokes OnCommandInvoke.
func (a *WorkbenchApp) HandleKeyToken(mode input.KeyScope, token string) (input.MatchKind, string) {
	a.mu.Lock()
	if a.KeyTrie == nil || token == "" {
		a.activeChord = a.activeChord[:0]
		a.mu.Unlock()
		return input.MatchNone, ""
	}
	a.activeChord = append(a.activeChord, token)
	kind, binding := a.KeyTrie.Lookup(mode, a.activeChord)
	switch kind {
	case input.MatchPrefix:
		prefix := strings.Join(a.activeChord, " ")
		a.mu.Unlock()
		return input.MatchPrefix, prefix
	case input.MatchExact:
		a.activeChord = a.activeChord[:0]
		a.mu.Unlock()
		a.invokeCommand(binding.CommandName)
		return input.MatchExact, binding.CommandName
	default:
		a.activeChord = a.activeChord[:0]
		a.mu.Unlock()
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
			a.settingsOpen = false
			a.gateOpen = false
		}
	}
	if a.SettingsSchema != nil && a.SettingsState != nil && c.Shortcut(ui.Cmd, ui.KeyComma) {
		a.settingsOpen = !a.settingsOpen
		if a.settingsOpen {
			a.paletteOpen = false
			a.marketplaceOpen = false
			a.projectionOpen = false
			a.addRemoteOpen = false
			a.gateOpen = false
		}
	}
	if a.Projection != nil && c.Shortcut(ui.Cmd, ui.KeyI) {
		a.projectionOpen = !a.projectionOpen
		if a.projectionOpen {
			a.paletteOpen = false
			a.marketplaceOpen = false
			a.settingsOpen = false
			a.addRemoteOpen = false
			a.gateOpen = false
		}
	}
	if a.Transcript != nil && c.Shortcut(ui.Cmd, ui.KeyJ) {
		a.TranscriptHidden = !a.TranscriptHidden
	}
	if c.Shortcut(ui.Cmd, ui.KeyB) {
		a.ToggleSidebar()
	}
	if a.OnCycleTheme != nil && c.Shortcut(ui.Cmd, ui.KeyT) {
		a.OnCycleTheme()
	}
	if a.OnQuickPluginSelect != nil && len(a.QuickPlugins) > 0 {
		if c.Shortcut(ui.Cmd, ui.Key1) {
			a.OnQuickPluginSelect(a.QuickPlugins[0].PluginID)
		} else if len(a.QuickPlugins) > 1 && c.Shortcut(ui.Cmd, ui.Key2) {
			a.OnQuickPluginSelect(a.QuickPlugins[1].PluginID)
		} else if len(a.QuickPlugins) > 2 && c.Shortcut(ui.Cmd, ui.Key3) {
			a.OnQuickPluginSelect(a.QuickPlugins[2].PluginID)
		} else if len(a.QuickPlugins) > 3 && c.Shortcut(ui.Cmd, ui.Key4) {
			a.OnQuickPluginSelect(a.QuickPlugins[3].PluginID)
		} else if len(a.QuickPlugins) > 4 && c.Shortcut(ui.Cmd, ui.Key5) {
			a.OnQuickPluginSelect(a.QuickPlugins[4].PluginID)
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
	var dt DerivedTheme
	if a.cachedTheme != nil && a.cachedThemeKey == theme {
		t = a.cachedTheme
		dt = a.cachedDerivedTheme
		c.SetTheme(t)
	} else {
		dt = DeriveTheme("active", "Active", theme)
		t = ApplyMarlinTheme(c, theme)
		a.cachedThemeKey = theme
		a.cachedTheme = t
		a.cachedDerivedTheme = dt
	}

	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		// Left sidebar (toggleable via Cmd+B / SPC w B / top-bar Sidebar pill).
		if !a.IsSidebarCollapsed() {
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
					ui.Text(c, "AI WORKFLOW PLUGINS").FontSize(10).FontWeight(700).LetterSpacing(0.5).TextColor(t.TextMuted)
					activeID := ""
					if a.ActiveQuickPluginID != nil {
						activeID = a.ActiveQuickPluginID()
					}
					ui.Column(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
						for _, qp := range a.QuickPlugins {
							active := qp.PluginID == activeID
							rowBtn := ui.ButtonBase(c.Key(qp.PluginID)).Label(qp.Label).
								Padding(8, 12).Radius(12).AlignItems(ui.Center).Justify(ui.Start).Gap(8)
							txtCol := toColor(dt.Ink)
							if active {
								topCol := toColor(mixRGB(dt.Accent, schema.RGB{R: 255, G: 255, B: 255}, 0.12))
								rowBtn.Gradient(topCol, toColor(dt.Accent), 180).
									InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.24)).
									Shadow(0, 3, 10, -2, rgbaFromRGB(dt.Accent, 0.34))
								txtCol = toColor(dt.InkOnAccent)
							} else {
								rowBtn.Background(toColor(dt.SurfaceElevated)).
									Border(1, glassBorderColor(dt))
							}
							rowBtn.Children(func() {
								ui.Text(c, qp.Label).FontSize(12).FontWeight(600).TextColor(txtCol).MaxLines(1)
							})
							if rowBtn.Clicked() {
								a.OnQuickPluginSelect(qp.PluginID)
							}
						}
					})
				}
				a.renderTargetSidebar(c, t)
				if a.OnAddRemoteTarget != nil {
					if ApplePillButton(c, "open-add-remote", "+ Remote Target", BtnTinted, dt, nil, true).Clicked() {
						if a.addRemoteState == nil {
							a.addRemoteState = NewSchemaFormState(remoteTargetFormSchema(), nil)
						}
						a.addRemoteStatus = ""
						a.addRemoteOpen = true
						a.paletteOpen = false
						a.marketplaceOpen = false
						a.settingsOpen = false
						a.projectionOpen = false
						a.gateOpen = false
					}
				}
				if a.OnTogglePlugin != nil {
					label := "Mount Plugin"
					if a.PluginMountedFn != nil && a.PluginMountedFn() {
						label = "Unmount Plugin"
					}
					if ApplePillButton(c, "toggle-plugin", label, BtnGlass, dt, nil, true).Clicked() {
						a.OnTogglePlugin()
					}
				}
			})
		}

		// Right workspace: crisp top chrome + Emacs composable WindowTree (or Scheme UI IR fallback).
		ui.Column(c).Grow(1).MinWidth(0).AlignItems(ui.Stretch).Children(func() {
			a.renderTopBar(c, t, snap)
			ui.Column(c).Grow(1).MinWidth(0).Padding(10).Gap(10).AlignItems(ui.Stretch).Children(func() {
				rctx := shell.RenderContext{
					Surface:  c,
					Theme:    theme,
					TargetID: a.selectedID,
				}
				if hasSnap {
					rctx.SessionID = snap.SessionLabel
				}
				if a.Windows != nil {
					a.renderComposableWorkspace(c, t, dt, snap, hasSnap, rctx)
				} else {
					if hasSnap {
						RenderUIElement(c, snap.FrameTree, a.Slots, rctx)
					} else if a.Transcript == nil {
						ui.Text(c, "Waiting for marlin.ui.v1 workbench snapshot...").TextColor(t.TextMuted)
					}
					if a.Transcript != nil && !a.TranscriptHidden {
						a.Transcript.Render(c, t, a.OnSendPrompt, a.OnCancelTurn)
					}
				}
			})
			a.renderWhichKeyHUD(c, t, dt)
			a.renderStatusBar(c, t, dt, snap)
		})
	})

	// Overlays painted last so they float above any embedded ui.WebView plugin.
	a.renderOverlays(c, t)
}

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
			ui.Text(c, a.cachedTopBadge).FontSize(11).TextColor(toColor(dt.InkMuted))
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
		ui.Text(c, "NORMAL (SPC):  SPC w [v/s/d/z/o/=/b/x] windows  ·  SPC p [1-4] perspectives  ·  SPC m marketplace  ·  SPC i inspect  ·  SPC t t theme  ·  i/a -> Insert").
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
	case "t":
		return "theme"
	default:
		return "group"
	}
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

// OpenAuth opens the Tier-0 Host Authentication & Account Center modal above all slots.
func (a *WorkbenchApp) OpenAuth() {
	if a.Auth == nil {
		return
	}
	a.authOpen = true
	a.paletteOpen = false
	a.bufferPickerOpen = false
	a.marketplaceOpen = false
	a.settingsOpen = false
	a.projectionOpen = false
	a.addRemoteOpen = false
	a.gateOpen = false
}

// IsAuthOpen reports whether the Tier-0 Host Auth modal is currently visible.
func (a *WorkbenchApp) IsAuthOpen() bool { return a.authOpen }

// OpenActivityCenter opens the Tier-0 Notification & Activity Center modal.
func (a *WorkbenchApp) OpenActivityCenter() {
	if a.Notifications == nil {
		return
	}
	a.activityOpen = true
	a.paletteOpen = false
	a.bufferPickerOpen = false
	a.marketplaceOpen = false
	a.authOpen = false
	a.settingsOpen = false
	a.projectionOpen = false
	a.addRemoteOpen = false
	a.gateOpen = false
}

// IsActivityCenterOpen reports whether the Tier-0 Activity Center modal is open.
func (a *WorkbenchApp) IsActivityCenterOpen() bool { return a.activityOpen }

// ToggleSidebar collapses or expands the persistent left navigation sidebar
// (`Cmd+B` / `SPC w B`) to reclaim 228px of split workspace width on demand.
func (a *WorkbenchApp) ToggleSidebar() bool {
	a.mu.Lock()
	a.SidebarCollapsed = !a.SidebarCollapsed
	collapsed := a.SidebarCollapsed
	a.mu.Unlock()
	return collapsed
}

// IsSidebarCollapsed reports whether the persistent left sidebar is collapsed.
func (a *WorkbenchApp) IsSidebarCollapsed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.SidebarCollapsed
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
		if a.SettingsSchema != nil && a.SettingsState != nil && a.OnCycleTheme == nil {
			if ApplePillButton(c, "open-settings", "Settings", BtnGlass, a.cachedDerivedTheme, nil, true).Clicked() {
				a.settingsOpen = true
				a.paletteOpen = false
				a.marketplaceOpen = false
				a.gateOpen = false
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
