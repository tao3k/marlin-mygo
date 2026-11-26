// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/engine/promptgate"
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

	// Thin projection of Rust AppServer durable sessions rendered in the left sidebar.
	Sessions        []SessionSummaryView
	ActiveSessionID string
	OnSelectSession func(sessionID string)
	OnNewSession    func()

	// Optional studio brand heading displayed at the top of the sidebar.
	BrandTitle    string
	BrandSubtitle string

	// Optional built-in Tier-0 Host GPU Marketplace control drawer (floats above WebViews).
	Marketplace *MarketplaceController

	// Optional Tier-0 Host GPU Authentication & Account Center modal (OAuth 2.1 PKCE / Email).
	Auth *AuthController

	// Optional Tier-0 Host Notification Toast Stack & Activity Center modal.
	Notifications *NotificationController

	// Optional Tier-0 searchable Keymap Inspector controller (lazy-created on open).
	KeymapInspector *KeymapInspectorController

	// Optional native GPU marlin.projection.v1 inspector (Graph Receipts, Diff, Processes).
	Projection *ProjectionController

	// Optional Tier-0 native GPU Federation Cockpit topology inspector (lazy-created on open).
	FederationUI *FederationController

	// Optional Tier-0 UI Governance & Feature Security Console and active FeatureHost.
	GovernanceUI *GovernanceController
	FeatureHost  *features.Host

	// Tier-0 Ask, Option, and Authorization modal queue floating above all WebViews.
	PromptGate *PromptGateController

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

	mu                 sync.Mutex
	selectedID         string
	paletteOpen        bool
	paletteQuery       string
	paletteRankValid   bool
	paletteLastQuery   string
	paletteLastCmdLen  int
	bufferPickerOpen   bool
	bufferPickerQuery  string
	marketplaceOpen    bool
	authOpen           bool
	activityOpen       bool
	keymapOpen         bool
	settingsOpen       bool
	projectionOpen     bool
	federationOpen     bool
	governanceOpen     bool
	addRemoteOpen      bool
	addRemoteState     *SchemaFormState
	addRemoteStatus    string
	rankedBuf          []input.RankedCommand
	activeChord        []string
	activeKeyMode      input.KeyScope
	whichKeyOpen       bool
	gateOpen           bool
	pendingGate        *PendingGateView
	cachedThemeKey     schema.UITheme
	cachedTheme        *ui.Theme
	cachedDerivedTheme DerivedTheme
	cachedTopTargetID  string
	cachedTopKind      schema.TargetKind
	cachedTopPhase     string
	cachedTopBadge     string
	cachedSnapProfile  string
	cachedSnapGen      uint64
	cachedSnapBadge    string
	cachedTabVer       uint64
	cachedTabBadge     string
}

func NewWorkbenchApp(
	targets *transport.TargetRegistry,
	slots *shell.SlotRegistry,
	wb *shell.WorkbenchController,
) *WorkbenchApp {
	app := &WorkbenchApp{
		Targets:    targets,
		Slots:      slots,
		Workbench:  wb,
		PromptGate: NewPromptGateController(),
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
		a.OpenMarketplace()
	case shell.CmdStudioOpenAuth:
		a.OpenAuth()
	case shell.CmdStudioOpenNotifications:
		a.OpenActivityCenter()
	case shell.CmdStudioOpenKeymap:
		a.OpenKeymapInspector()
	case shell.CmdStudioOpenFederation:
		a.OpenFederation()
	case shell.CmdStudioOpenGovernance:
		a.OpenGovernance()
	case shell.CmdStudioOpenSearch:
		if a.Transcript != nil {
			a.Transcript.OpenSearch()
		}
	case shell.CmdStudioOpenModelPicker:
		if a.Transcript != nil {
			a.Transcript.ToggleModelPicker()
		}
	case shell.CmdStudioToggleVoice:
		if a.Transcript != nil {
			if a.Transcript.Voice != nil && a.Transcript.Voice.Snapshot().Phase != composer.VoiceIdle {
				a.Transcript.StopVoiceRecording()
			} else {
				a.Transcript.StartVoiceRecording()
			}
		}
	case shell.CmdStudioNewSession:
		if a.OnNewSession != nil {
			a.OnNewSession()
		}
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

// ShowAskPrompt enqueues a Tier-0 Ask question modal floating above all WebViews.
func (a *WorkbenchApp) ShowAskPrompt(req promptgate.PromptRequest) error {
	if a.PromptGate == nil {
		a.PromptGate = NewPromptGateController()
	}
	req.Kind = promptgate.PromptAsk
	return a.PromptGate.Enqueue(req)
}

// ShowOptionPrompt enqueues a Tier-0 single/multi-select Option modal above all WebViews.
func (a *WorkbenchApp) ShowOptionPrompt(req promptgate.PromptRequest) error {
	if a.PromptGate == nil {
		a.PromptGate = NewPromptGateController()
	}
	req.Kind = promptgate.PromptOption
	return a.PromptGate.Enqueue(req)
}

// ShowAuthPrompt enqueues a Tier-0 Authorization gate modal above all WebViews.
func (a *WorkbenchApp) ShowAuthPrompt(req promptgate.PromptRequest) error {
	if a.PromptGate == nil {
		a.PromptGate = NewPromptGateController()
	}
	req.Kind = promptgate.PromptAuth
	return a.PromptGate.Enqueue(req)
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
	if a.Transcript != nil && c.Shortcut(ui.Cmd, ui.KeyF) {
		a.Transcript.OpenSearch()
	}
	if a.OnCycleTheme != nil && c.Shortcut(ui.Cmd, ui.KeyT) {
		a.OnCycleTheme()
	}
	if c.Shortcut(0, ui.KeyEscape) {
		a.DismissTopOverlay()
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
				a.renderSessionSidebar(c, dt)
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

// OpenKeymapInspector opens the searchable Tier-0 Keymap Inspector modal.
func (a *WorkbenchApp) OpenKeymapInspector() {
	if a.KeyTrie == nil {
		return
	}
	if a.KeymapInspector == nil {
		a.KeymapInspector = NewKeymapInspectorController()
	}
	a.keymapOpen = true
	a.paletteOpen = false
	a.bufferPickerOpen = false
	a.marketplaceOpen = false
	a.authOpen = false
	a.activityOpen = false
	a.settingsOpen = false
	a.projectionOpen = false
	a.addRemoteOpen = false
	a.gateOpen = false
}

// IsKeymapInspectorOpen reports whether the Tier-0 Keymap Inspector modal is visible.
func (a *WorkbenchApp) IsKeymapInspectorOpen() bool { return a.keymapOpen }

// OpenMarketplace opens the Tier-0 Official Marketplace drawer above all slots.
func (a *WorkbenchApp) OpenMarketplace() {
	if a.Marketplace == nil {
		return
	}
	a.marketplaceOpen = true
	a.paletteOpen = false
	a.bufferPickerOpen = false
	a.keymapOpen = false
	a.activityOpen = false
	a.authOpen = false
	a.settingsOpen = false
	a.projectionOpen = false
	a.federationOpen = false
	a.addRemoteOpen = false
	a.gateOpen = false
}

// OpenFederation opens the Tier-0 Multi-Target Federation Cockpit modal.
func (a *WorkbenchApp) OpenFederation() {
	if a.Federation == nil && a.FederationUI == nil {
		return
	}
	if a.FederationUI == nil {
		a.FederationUI = NewFederationController(a.Federation, a.Targets)
	}
	a.federationOpen = true
	a.paletteOpen = false
	a.bufferPickerOpen = false
	a.keymapOpen = false
	a.activityOpen = false
	a.authOpen = false
	a.marketplaceOpen = false
	a.settingsOpen = false
	a.projectionOpen = false
	a.addRemoteOpen = false
	a.gateOpen = false
}

// IsFederationOpen reports whether the Tier-0 Federation Cockpit modal is visible.
func (a *WorkbenchApp) IsFederationOpen() bool { return a.federationOpen }

// OpenGovernance opens the Tier-0 UI Governance & Feature Security Console modal.
func (a *WorkbenchApp) OpenGovernance() {
	if a.GovernanceUI == nil {
		a.GovernanceUI = NewGovernanceController(nil, nil, a.FeatureHost)
	}
	a.governanceOpen = true
	a.paletteOpen = false
	a.bufferPickerOpen = false
	a.keymapOpen = false
	a.activityOpen = false
	a.authOpen = false
	a.marketplaceOpen = false
	a.settingsOpen = false
	a.projectionOpen = false
	a.federationOpen = false
	a.addRemoteOpen = false
	a.gateOpen = false
}

// IsGovernanceOpen reports whether the Tier-0 Governance Console modal is visible.
func (a *WorkbenchApp) IsGovernanceOpen() bool { return a.governanceOpen }

// DismissTopOverlay handles global ESC keyboard accessibility: closes the
// active non-gate Tier-0 modal or cancels an in-progress Hydra chord / Which-Key HUD.
// Security invariant: Approval Gates (gateOpen) are intentionally NOT bypassed by ESC.
func (a *WorkbenchApp) DismissTopOverlay() bool {
	switch {
	case a.paletteOpen:
		a.paletteOpen = false
		return true
	case a.bufferPickerOpen:
		a.bufferPickerOpen = false
		return true
	case a.keymapOpen:
		a.keymapOpen = false
		return true
	case a.activityOpen:
		a.activityOpen = false
		return true
	case a.authOpen:
		if a.Auth != nil {
			a.Auth.ClearSensitiveInputs()
		}
		a.authOpen = false
		return true
	case a.marketplaceOpen:
		a.marketplaceOpen = false
		return true
	case a.settingsOpen:
		a.settingsOpen = false
		return true
	case a.projectionOpen:
		a.projectionOpen = false
		return true
	case a.federationOpen:
		a.federationOpen = false
		return true
	case a.governanceOpen:
		a.governanceOpen = false
		return true
	case a.addRemoteOpen:
		a.addRemoteOpen = false
		return true
	case a.Transcript != nil && a.Transcript.IsAttachmentPreviewOpen():
		a.Transcript.CloseAttachmentPreview()
		return true
	case a.Transcript != nil && a.Transcript.IsModelPickerOpen():
		a.Transcript.CloseModelPicker()
		return true
	case a.Transcript != nil && a.Transcript.IsSearchOpen():
		a.Transcript.CloseSearch()
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.activeChord) > 0 || a.whichKeyOpen {
		a.activeChord = a.activeChord[:0]
		a.whichKeyOpen = false
		return true
	}
	return false
}

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
