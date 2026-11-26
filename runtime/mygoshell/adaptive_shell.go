// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// ShellFormFactor controls whether AdaptiveShell renders Desktop Emacs multi-pane
// chrome, iOS HIG phone chrome, Android Material 3 phone chrome, or adapts
// automatically to the live viewport width (`< 720px` -> phone, `>= 720px` -> desktop).
type ShellFormFactor string

const (
	FormFactorAuto    ShellFormFactor = "auto"
	FormFactorDesktop ShellFormFactor = "desktop"
	FormFactorIOS     ShellFormFactor = "ios"
	FormFactorAndroid ShellFormFactor = "android"

	DefaultCompactBreakpoint = 720
)

// AdaptiveShellConfig configures a single unified Marlin UI application that runs
// identically across macOS/Linux/Windows Desktop, iOS, Android, and Web (WASM).
type AdaptiveShellConfig struct {
	BrandTitle       string
	BrandSubtitle    string
	InitialThemeID   string
	FormFactor       ShellFormFactor
	MobilePlatform   MobilePlatform
	CompactThreshold float32
	Targets          *transport.TargetRegistry
	Slots            *shell.SlotRegistry
	Workbench        *shell.WorkbenchController
	Projection       *ProjectionController
	Auth             *AuthController
	Notifications    *NotificationController
	OnThemeChange    func(DerivedTheme)
}

// AdaptiveShell is the single high-level library facade uniting Marlin's Desktop
// Emacs WindowTree shell, iOS/Android Mobile Companion shell, and 34-token
// DerivedTheme engine so apps target Desktop, iOS, Android, and Web from one API.
type AdaptiveShell struct {
	Desktop          *WorkbenchApp
	Mobile           *MobileCompanionApp
	Windows          *shell.WindowTree
	CompactThreshold float32

	mu         sync.RWMutex
	formFactor ShellFormFactor
	theme      ThemePreset
	wbGen      uint64
	onTheme    func(DerivedTheme)
}

// SetFeatureHost attaches an opt-in FeatureHost (compiled via build tags and
// governed by GovernanceLedger) identically across Desktop and Mobile views.
func (a *AdaptiveShell) SetFeatureHost(host *features.Host, govUI *GovernanceController) {
	if a == nil {
		return
	}
	if a.Desktop != nil {
		a.Desktop.FeatureHost = host
		if govUI != nil {
			a.Desktop.GovernanceUI = govUI
		}
		if host != nil {
			for _, buf := range host.Buffers() {
				a.Desktop.RegisterWindowBuffer(buf)
			}
			existing := make(map[string]struct{}, len(a.Desktop.Commands))
			for _, c := range a.Desktop.Commands {
				existing[c.Name] = struct{}{}
			}
			for _, cmd := range host.Commands() {
				if _, ok := existing[cmd.Name]; !ok {
					a.Desktop.Commands = append(a.Desktop.Commands, cmd)
				}
			}
		}
	}
	if a.Mobile != nil {
		a.Mobile.FeatureHost = host
	}
}

// SetAuth attaches a Tier-0 AuthController simultaneously to Desktop and Mobile.
func (a *AdaptiveShell) SetAuth(auth *AuthController) {
	if a == nil {
		return
	}
	if a.Desktop != nil {
		a.Desktop.Auth = auth
	}
	if a.Mobile != nil {
		a.Mobile.Auth = auth
	}
}

// SetSettings attaches a typed BoundedSchema settings form and save callback
// simultaneously to Desktop (bottom-left dock + Cmd+,) and Mobile.
func (a *AdaptiveShell) SetSettings(spec *schema.BoundedSchema, state *SchemaFormState, onSave func(values map[string]any)) {
	if a == nil {
		return
	}
	if a.Desktop != nil {
		a.Desktop.SettingsSchema = spec
		a.Desktop.SettingsState = state
		a.Desktop.OnSaveSettings = onSave
	}
	if a.Mobile != nil {
		a.Mobile.SettingsSchema = spec
		a.Mobile.SettingsState = state
		a.Mobile.OnSaveSettings = onSave
	}
}

// NewAdaptiveShell constructs a complete 4-target Marlin shell sharing one
// TranscriptController, ProjectionController, WindowTree, and 5-theme engine.
func NewAdaptiveShell(cfg AdaptiveShellConfig) *AdaptiveShell {
	if cfg.Targets == nil {
		cfg.Targets = transport.NewTargetRegistry()
	}
	if cfg.Slots == nil {
		cfg.Slots = shell.NewSlotRegistry()
	}
	if cfg.Workbench == nil {
		cfg.Workbench = shell.NewWorkbenchController(cfg.Slots)
	}
	if cfg.Projection == nil {
		cfg.Projection = NewProjectionController(ProjectionSnapshot{})
	}
	if cfg.Notifications == nil {
		cfg.Notifications = NewNotificationController(nil)
	}
	if cfg.CompactThreshold <= 0 {
		cfg.CompactThreshold = DefaultCompactBreakpoint
	}
	if cfg.FormFactor == "" {
		cfg.FormFactor = FormFactorAuto
	}
	if cfg.MobilePlatform == "" {
		cfg.MobilePlatform = MobilePlatformIOS
	}
	preset := ThemeByID(cfg.InitialThemeID)

	bufMain := shell.PaneBuffer{BufferID: "slot:workbench.main", Title: "Workspace", Kind: "panel"}
	bufSecondary := shell.PaneBuffer{BufferID: "slot:workbench.secondary", Title: "Secondary Panel", Kind: "panel"}
	bufDock := shell.PaneBuffer{BufferID: "slot:workbench.inspector", Title: "Focus Dock", Kind: "wasm-gpu"}
	bufChat := shell.PaneBuffer{BufferID: "builtin:transcript", Title: "AI Transcript", Kind: "native-gpu"}
	bufProj := shell.PaneBuffer{BufferID: "builtin:projection", Title: "Projection", Kind: "native-gpu"}

	tree := shell.NewWindowTree(bufMain)
	tree.ApplyBuiltinPerspective("split", bufMain, bufDock, bufChat, bufProj)

	transcript := NewTranscriptController()
	desktop := NewWorkbenchApp(cfg.Targets, cfg.Slots, cfg.Workbench)
	desktop.BrandTitle = cfg.BrandTitle
	if desktop.BrandTitle == "" {
		desktop.BrandTitle = "Marlin"
	}
	desktop.BrandSubtitle = cfg.BrandSubtitle
	if desktop.BrandSubtitle == "" {
		desktop.BrandSubtitle = "Unified Desktop · iOS · Android · Web"
	}
	desktop.Transcript = transcript
	desktop.Projection = cfg.Projection
	desktop.Auth = cfg.Auth
	desktop.Notifications = cfg.Notifications
	desktop.Windows = tree
	desktop.WindowBuffers = []shell.PaneBuffer{bufMain, bufSecondary, bufChat, bufProj, bufDock}
	desktop.OnSelectPerspective = func(name string) {
		tree.ApplyBuiltinPerspective(name, bufMain, bufDock, bufChat, bufProj)
	}
	_ = desktop.EnableEmacsWindowKeymap()
	BindDefaultHelmSources(desktop, nil)

	mobile := NewMobileCompanionAppForPlatform(cfg.MobilePlatform, "", cfg.BrandTitle)
	mobile.Transcript = transcript
	mobile.PromptGate = desktop.PromptGate
	mobile.HelmSearch = desktop.HelmSearch
	mobile.Windows = tree
	mobile.Auth = cfg.Auth
	mobile.SetThemePreset(preset.ID)

	as := &AdaptiveShell{
		Desktop:          desktop,
		Mobile:           mobile,
		Windows:          tree,
		CompactThreshold: cfg.CompactThreshold,
		formFactor:       cfg.FormFactor,
		theme:            preset,
		wbGen:            1,
		onTheme:          cfg.OnThemeChange,
	}

	mkt := NewMarketplaceController()
	mkt.SetThemes(DefaultOfficialMarketplaceThemes())
	mkt.SetLayouts(DefaultOfficialMarketplaceLayouts())
	mkt.OnApplyTheme = func(item MarketplaceThemeItem) error {
		app := AppearanceDark
		if item.Appearance == "light" {
			app = AppearanceLight
		}
		as.ApplyCustomTheme(ThemePreset{
			ID:         item.ThemeID,
			Name:       item.Name,
			Appearance: app,
			Base:       item.Base,
		})
		return nil
	}
	mkt.OnApplyLayout = func(item MarketplaceLayoutItem) error {
		return desktop.ApplyLayoutSpec(item.Spec)
	}
	desktop.Marketplace = mkt

	desktop.ActiveThemeNameFn = func() string {
		as.mu.RLock()
		defer as.mu.RUnlock()
		return as.theme.Name
	}
	desktop.OnCycleTheme = func() {
		as.CycleTheme()
	}

	// Ensure workbench starts with the initial theme snapshot if empty.
	if _, ok := cfg.Workbench.Snapshot(); !ok {
		_ = cfg.Workbench.AdmitSnapshot(schema.UIWorkbenchSnapshot{
			Generation:  1,
			ProfileName: "adaptive-" + preset.ID,
			Theme:       preset.Base,
			FrameTree:   schema.UIElement{Kind: schema.ElemSlot, SlotID: "workbench.main"},
		})
	}
	return as
}

// SetFormFactor overrides automatic viewport adaptation (e.g. force iOS or Android).
func (a *AdaptiveShell) SetFormFactor(ff ShellFormFactor) {
	a.mu.Lock()
	a.formFactor = ff
	a.mu.Unlock()
	if ff == FormFactorIOS {
		a.Mobile.SwitchPlatform(MobilePlatformIOS)
	} else if ff == FormFactorAndroid {
		a.Mobile.SwitchPlatform(MobilePlatformAndroid)
	}
}

// FormFactor returns the current form-factor policy.
func (a *AdaptiveShell) FormFactor() ShellFormFactor {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.formFactor
}

// ApplyThemePreset applies one curated built-in theme preset across Desktop,
// Mobile, and any registered WebView callback simultaneously.
func (a *AdaptiveShell) ApplyThemePreset(id string) DerivedTheme {
	return a.ApplyCustomTheme(ThemeByID(id))
}

// ApplyCustomTheme projects any Official Marketplace or custom ThemePreset
// simultaneously across Desktop, Mobile (iOS/Android), and Web/WebView targets.
func (a *AdaptiveShell) ApplyCustomTheme(preset ThemePreset) DerivedTheme {
	a.mu.Lock()
	a.theme = preset
	a.wbGen++
	gen := a.wbGen
	cb := a.onTheme
	a.mu.Unlock()

	dt := DeriveTheme(preset.ID, preset.Name, preset.Base)
	if a.Desktop != nil && a.Desktop.Workbench != nil {
		if cur, ok := a.Desktop.Workbench.Snapshot(); ok {
			next := *cur
			next.Generation = gen
			next.Theme = preset.Base
			_ = a.Desktop.Workbench.AdmitSnapshot(next)
		}
	}
	if a.Mobile != nil {
		a.Mobile.ApplyCustomTheme(preset)
	}
	if cb != nil {
		cb(dt)
	}
	return dt
}

// CycleTheme advances Desktop + Mobile simultaneously to the next curated theme.
func (a *AdaptiveShell) CycleTheme() DerivedTheme {
	a.mu.RLock()
	curID := a.theme.ID
	a.mu.RUnlock()
	return a.ApplyThemePreset(NextThemePreset(curID).ID)
}

// EffectiveFormFactor resolves which shell renders at viewportWidth.
func (a *AdaptiveShell) EffectiveFormFactor(viewportWidth float32) ShellFormFactor {
	ff := a.FormFactor()
	if ff != FormFactorAuto {
		return ff
	}
	if viewportWidth > 0 && viewportWidth < a.CompactThreshold {
		if a.Mobile != nil && a.Mobile.Platform == MobilePlatformAndroid {
			return FormFactorAndroid
		}
		return FormFactorIOS
	}
	return FormFactorDesktop
}

// View is the single ui.View callback passed to mygo.NewWindow on Desktop, iOS,
// Android, or Web (GOOS=js GOARCH=wasm).
func (a *AdaptiveShell) View(c *ui.Context) {
	w, _ := c.Size()
	switch a.EffectiveFormFactor(w) {
	case FormFactorIOS, FormFactorAndroid:
		a.Mobile.View(c)
	default:
		a.Desktop.View(c)
	}
}
