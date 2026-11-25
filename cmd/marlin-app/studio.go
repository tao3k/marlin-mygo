// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
	"github.com/tao3k/marlin-mygo/runtime/wazeroir"
)

func runStudio(ctx context.Context, cfg appConfig) error {
	if cfg.showcase && strings.TrimSpace(cfg.catalogDir) == "" && strings.TrimSpace(cfg.catalogURL) == "" {
		cDir, pFile, clean, err := prepareShowcaseCatalog(ctx)
		if err != nil {
			return fmt.Errorf("prepare -showcase catalog failed: %w", err)
		}
		defer clean()
		cfg.catalogDir = cDir
		cfg.catalogPub = pFile
		cfg.catalogPublisher = "tao3k.official"
	}

	targets := transport.NewTargetRegistry()
	federation := transport.NewFederationSupervisor(targets)

	slots, err := declareDefaultSlots()
	if err != nil {
		return err
	}
	wb := shell.NewWorkbenchController(slots)

	var showcaseDisp *showcaseDispatcher
	var dispatcher pluginhost.ActionDispatcher = demoDispatcher{}
	repoCtx := detectInitialRepoContext(cfg.repoURL)
	if cfg.showcase {
		showcaseDisp = newShowcaseDispatcher(repoCtx)
		dispatcher = showcaseDisp
	}

	conn, err := connectTarget(ctx, cfg, targets, federation, wb, dispatcher)
	if err != nil {
		return err
	}
	defer conn.close()
	dispatcher = conn.dispatcher

	wvRuntime := mygoshell.NewWebViewRuntime(schema.RuntimeWebViewUntrusted)
	if err := wvRuntime.RegisterProtocol(); err != nil {
		return err
	}
	wasmEng, err := wazeroir.NewModuleEngineWithConfig(ctx, wazeroir.EngineConfig{
		CompilationCacheDir: strings.TrimSpace(cfg.wasmCacheDir),
	})
	if err != nil {
		return err
	}
	defer wasmEng.Close(ctx)

	host := pluginhost.NewHost(slots, dispatcher)
	host.RegisterRuntime(wvRuntime)
	host.RegisterRuntime(wazeroir.NewRuntime(slots, wasmEng))
	host.RegisterRuntime(&pluginhost.DeclarativeRuntimeFactory{})

	app, mktUI, transcript := buildWorkbenchApp(ctx, cfg, targets, federation, slots, wb, wvRuntime, conn.liveTarget)

	var liveDriver *grpcwire.SessionDriver
	var mounted atomic.Bool
	app.PluginMountedFn = mounted.Load

	mygo.App.WhenReady(func() {
		winTitle := "Marlin MyGo Modular Engine"
		winW, winH := 1120, 720
		if cfg.showcase {
			winTitle = "Marlin Studio — React 19 AI Workflows + Go WASI GPU"
			winW, winH = 1340, 860
		}
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     winTitle,
			Width:     winW,
			Height:    winH,
			MinWidth:  680,
			MinHeight: 440,
			Content:   ui.View(app.View),
		})
		wvRuntime.AttachWindow(win)

		lazySess, syncMarketplace := wireMarketplaceUI(
			ctx, mktUI, host, win,
			cfg.catalogURL, cfg.catalogDir, cfg.catalogPub, cfg.trustRingFile, cfg.catalogPublisher,
			cfg.pluginsProfile, cfg.maxActiveLazy, cfg.idleReapInterval,
		)

		if cfg.showcase && lazySess != nil {
			mountShowcaseStudio(ctx, cfg, showcaseDisp, host, app, win, slots, lazySess, syncMarketplace)
		}

		fireHook, publishProjection := wireLazyTriggers(ctx, app, lazySess, syncMarketplace, win)
		startDevPluginWatcher(ctx, cfg.devPluginDir, host, mktUI, win)

		if conn.liveTarget != nil {
			liveDriver = wireLiveSession(ctx, cfg, conn.liveTarget, wb, federation, host, app, transcript, win, fireHook, publishProjection)
		} else {
			wireStandaloneDemo(cfg.sessionID, app, transcript, win, fireHook, publishProjection)
		}

		if !cfg.showcase {
			wireDemoHotPlugToggle(ctx, app, host, &mounted, win)
		}
		win.Invalidate()
	})

	if err := mygo.App.Run(); err != nil {
		return err
	}
	if liveDriver != nil {
		liveDriver.Stop()
	}
	return nil
}

func declareDefaultSlots() (*shell.SlotRegistry, error) {
	slots := shell.NewSlotRegistry()
	declare := func(slotID schema.SlotID, hostID, title, body string, allowUntrusted bool) error {
		fb := mygoshell.NewUIElementRenderer(hostID, true, slots, func() schema.UIElement {
			return schema.UIElement{
				Kind:  schema.ElemPanel,
				Title: title,
				Tone:  schema.ToneInfo,
				Children: []schema.UIElement{
					{Kind: schema.ElemText, Spans: []schema.UISpan{
						{Text: "Status: ", Tone: schema.ToneMuted},
						{Text: body, Tone: schema.ToneSuccess},
					}},
				},
			}
		})
		return slots.Declare(schema.SlotDeclaration{
			SlotID: slotID, Cardinality: schema.SlotSingle,
			FallbackRenderer: hostID, AllowUntrustedPlugin: allowUntrusted,
		}, fb)
	}
	if err := declare("workbench.main", "host.main", "Host Core UI-IR Fallback (workbench.main)",
		"Ready — open Official Marketplace or click 'Mount Plugin' to hot-plug a panel.", true); err != nil {
		return nil, err
	}
	if err := declare("workbench.secondary", "host.secondary", "Composable Secondary Slot (workbench.secondary)",
		"Ready — split Emacs windows to compose Git Workbench + GitHub Workbench side-by-side.", true); err != nil {
		return nil, err
	}
	if err := declare("workbench.inspector", "host.inspector", "Host Inspector Fallback (workbench.inspector)",
		"Ready — mount a signed Go WASM-IR Reactor (.marlinpkg) for 25us GPU rendering.", true); err != nil {
		return nil, err
	}
	if err := declare("approval.gate", "host.gate", "Host Approval Gate", "Restricted Tier-0 host slot.", false); err != nil {
		return nil, err
	}
	return slots, nil
}

func buildWorkbenchApp(
	ctx context.Context,
	cfg appConfig,
	targets *transport.TargetRegistry,
	federation *transport.FederationSupervisor,
	slots *shell.SlotRegistry,
	wb *shell.WorkbenchController,
	wvRuntime *mygoshell.WebViewRuntime,
	liveTarget *grpcwire.Target,
) (*mygoshell.WorkbenchApp, *mygoshell.MarketplaceController, *mygoshell.TranscriptController) {
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)
	app.Federation = federation
	app.BrandTitle = "Marlin Studio"
	app.BrandSubtitle = "MyGo GPU · React 19 + WASI"

	transcript := mygoshell.NewTranscriptController()
	app.Transcript = transcript
	if cfg.showcase {
		app.TranscriptHidden = true
	}

	settingsSchema := schema.BoundedSchema{
		ID:    "studio.preferences",
		Title: "Marlin Studio Preferences (Cmd+,)",
		Fields: []schema.FieldSpec{
			{Key: "theme", Label: "Theme Preset", Kind: schema.FieldEnum, EnumValues: []string{"midnight", "paper", "doom-one", "tokyo-night", "catppuccin"}, Default: "midnight"},
			{Key: "focus_min", Label: "Focus Block Minutes", Kind: schema.FieldInt, MinInt: 5, MaxInt: 90, Default: 25},
			{Key: "lazy_cap", Label: "Max Active Lazy Plugins", Kind: schema.FieldInt, MinInt: 1, MaxInt: 32, Default: cfg.maxActiveLazy},
		},
	}
	app.SettingsSchema = &settingsSchema
	app.SettingsState = mygoshell.NewSchemaFormState(settingsSchema, nil)

	initialProjection := mygoshell.ProjectionSnapshot{}
	if liveTarget == nil {
		initialProjection = sampleProjectionSnapshot()
	}
	app.Projection = mygoshell.NewProjectionController(initialProjection)
	app.OnAddRemoteTarget = func(targetID, displayName, endpoint, bearerToken string) error {
		return registerRemoteTarget(ctx, targets, federation, targetID, displayName, endpoint, bearerToken)
	}

	applyThemePreset := wireThemeControls(app, wb, wvRuntime)

	// Emacs-style composable window tree & built-in buffer catalog.
	bufMain := shell.PaneBuffer{BufferID: "slot:workbench.main", Title: "Workspace Panel", Kind: "webview"}
	bufSecondary := shell.PaneBuffer{BufferID: "slot:workbench.secondary", Title: "Secondary Panel", Kind: "webview"}
	bufDock := shell.PaneBuffer{BufferID: "slot:workbench.inspector", Title: "Focus Clock", Kind: "wasm-gpu"}
	bufChat := shell.PaneBuffer{BufferID: "builtin:transcript", Title: "AI Transcript", Kind: "native-gpu"}
	bufProj := shell.PaneBuffer{BufferID: "builtin:projection", Title: "Projection Inspector", Kind: "native-gpu"}
	winTree := shell.NewWindowTree(bufMain)
	winTree.ApplyBuiltinPerspective("focus", bufMain, bufDock, bufChat, bufProj)
	app.Windows = winTree
	app.WindowBuffers = []shell.PaneBuffer{bufMain, bufSecondary, bufChat, bufProj, bufDock}
	app.OnLayoutChanged = wvRuntime.BroadcastLayoutSnapshot
	app.OnSelectPerspective = func(name string) {
		winTree.ApplyBuiltinPerspective(name, bufMain, bufDock, bufChat, bufProj)
		wvRuntime.BroadcastLayoutSnapshot(winTree.Snapshot())
	}

	mktUI := mygoshell.NewMarketplaceController()
	mktUI.SetThemes(mygoshell.DefaultOfficialMarketplaceThemes())
	mktUI.SetLayouts(mygoshell.DefaultOfficialMarketplaceLayouts())
	mktUI.OnApplyTheme = func(item mygoshell.MarketplaceThemeItem) error {
		appearance := mygoshell.AppearanceDark
		if strings.EqualFold(item.Appearance, "light") {
			appearance = mygoshell.AppearanceLight
		}
		applyThemePreset(mygoshell.ThemePreset{
			ID:         item.ThemeID,
			Name:       item.Name,
			Appearance: appearance,
			Base:       item.Base,
		})
		return nil
	}
	mktUI.OnApplyLayout = func(item mygoshell.MarketplaceLayoutItem) error {
		return app.ApplyLayoutSpec(item.Spec)
	}
	mktUI.OnSyncOfficial = func() error {
		syncClient, err := marketplace.NewOfficialSyncClient(marketplace.OfficialSyncConfig{
			RegistryURL: marketplace.DefaultOfficialRegistryURL,
		})
		if err != nil {
			return err
		}
		syncCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		snap, err := syncClient.Sync(syncCtx)
		if err != nil {
			return err
		}
		applyOfficialSnapshotToMarketplaceUI(mktUI, snap)
		return nil
	}
	app.Marketplace = mktUI
	app.Commands = []input.CommandEntry{
		{Name: "workbench.mount-kanban", Description: "Hot-plug TS Org Kanban WebView"},
		{Name: "workbench.open-marketplace", Description: "Open Tier-0 built-in Marketplace drawer"},
		{Name: "workbench.probe-federation", Description: "Probe all local, remote, and mobile agents"},
		{Name: "gate.review-pending", Description: "Inspect pending human approval gate"},
	}
	_ = app.EnableEmacsWindowKeymap()

	if liveTarget == nil {
		openGateNow := !cfg.showcase && strings.TrimSpace(cfg.devPluginDir) == ""
		app.SetPendingGate(mygoshell.PendingGateView{
			GateID:      "gate-demo-1",
			ToolName:    "device.ios.notify.send",
			Prompt:      "Allow Marlin to send notification on iPhone?",
			Description: "Overlay floats above Untrusted plugin WebViews (host.gate).",
		}, openGateNow)
	}
	return app, mktUI, transcript
}

func wireThemeControls(app *mygoshell.WorkbenchApp, wb *shell.WorkbenchController, wvRuntime *mygoshell.WebViewRuntime) func(mygoshell.ThemePreset) {
	var (
		themeMu     sync.RWMutex
		activeTheme = mygoshell.ThemeByID("midnight")
		wbGen       atomic.Uint64
	)
	wbGen.Store(1)
	applyThemePreset := func(preset mygoshell.ThemePreset) {
		themeMu.Lock()
		activeTheme = preset
		themeMu.Unlock()
		dt := mygoshell.DeriveTheme(preset.ID, preset.Name, preset.Base)
		if cur, ok := wb.Snapshot(); ok {
			next := *cur
			next.Generation = wbGen.Add(1)
			next.Theme = preset.Base
			_ = wb.AdmitSnapshot(next)
		}
		wvRuntime.BroadcastDerivedTheme(dt)
	}
	app.ActiveThemeNameFn = func() string {
		themeMu.RLock()
		defer themeMu.RUnlock()
		return activeTheme.Name
	}
	app.OnCycleTheme = func() {
		themeMu.RLock()
		curID := activeTheme.ID
		themeMu.RUnlock()
		applyThemePreset(mygoshell.NextThemePreset(curID))
	}
	app.OnSaveSettings = func(vals map[string]any) {
		if id, ok := vals["theme"].(string); ok {
			applyThemePreset(mygoshell.ThemeByID(id))
		}
	}
	return applyThemePreset
}

func mountShowcaseStudio(
	ctx context.Context,
	cfg appConfig,
	showcaseDisp *showcaseDispatcher,
	host *pluginhost.Host,
	app *mygoshell.WorkbenchApp,
	win *mygo.Window,
	slots *shell.SlotRegistry,
	lazySess *marketplace.LazySession,
	syncMarketplace func(),
) {
	showcaseDisp.attach(host, app, win)
	showcaseDisp.startTickLoop(ctx)

	initialMain := resolveShowcasePluginID(cfg.showcasePlugin)
	switcher := mygoshell.NewQuickPluginSwitcher(mygoshell.QuickPluginSwitcherConfig{
		Context:   ctx,
		Slots:     slots,
		SlotID:    "workbench.main",
		InitialID: string(initialMain),
		Activate: func(actCtx context.Context, id schema.PluginID) error {
			_, err := lazySess.InstallOrUpgrade(actCtx, id)
			return err
		},
		OnSync:       syncMarketplace,
		OnInvalidate: win.Invalidate,
		OnError: func(pluginID string, err error) {
			log.Printf("[showcase] switch to %s failed: %v", pluginID, err)
		},
	})
	app.QuickPlugins = []mygoshell.QuickPluginEntry{
		{PluginID: "tao3k.org-agenda-planner", Label: "Org Agenda (React 19)"},
		{PluginID: "tao3k.ai-memory-inbox", Label: "AI Memory Inbox (React 19)"},
		{PluginID: "tao3k.ai-workflow-cockpit", Label: "AI Workflow Cockpit (React 19)"},
		{PluginID: "tao3k.git-workbench", Label: "Git Workbench (React 19)"},
		{PluginID: "tao3k.github-workbench", Label: "GitHub Workbench (React 19)"},
	}
	app.ActiveQuickPluginID = switcher.ActiveID
	app.OnQuickPluginSelect = switcher.Select
	switcher.Select(string(initialMain))

	go func() {
		if _, err := lazySess.InstallOrUpgrade(ctx, "tao3k.focus-clock-reactor"); err != nil {
			log.Printf("[showcase] mount inspector focus-clock failed: %v", err)
		}
		showcaseDisp.publishRepoProjections()
		syncMarketplace()
		win.Invalidate()
	}()
}

func wireLazyTriggers(
	ctx context.Context,
	app *mygoshell.WorkbenchApp,
	lazySess *marketplace.LazySession,
	syncMarketplace func(),
	win *mygo.Window,
) (fireHook func(string), publishProjection func(string, any)) {
	fireHook = func(hookName string) {
		if lazySess == nil {
			return
		}
		go func() {
			if woken, err := lazySess.TriggerHook(ctx, hookName); err == nil && len(woken) > 0 {
				syncMarketplace()
				win.Invalidate()
			}
		}()
	}
	publishProjection = func(topic string, payload any) {
		if lazySess == nil {
			return
		}
		go func() {
			if woken, delivered, err := lazySess.PublishProjection(ctx, topic, payload); err == nil && (len(woken) > 0 || len(delivered) > 0) {
				syncMarketplace()
				win.Invalidate()
			}
		}()
	}
	app.OnCommandInvoke = func(commandName string) {
		if lazySess == nil {
			return
		}
		go func() {
			if woken, err := lazySess.TriggerCommand(ctx, commandName); err == nil && len(woken) > 0 {
				syncMarketplace()
				win.Invalidate()
			}
		}()
	}
	return fireHook, publishProjection
}

func startDevPluginWatcher(
	ctx context.Context,
	devPluginDir string,
	host *pluginhost.Host,
	mktUI *mygoshell.MarketplaceController,
	win *mygo.Window,
) {
	if strings.TrimSpace(devPluginDir) == "" {
		return
	}
	reloader, err := marketplace.NewDevReloader(marketplace.DevReloaderConfig{
		PluginDir: devPluginDir,
		Host:      host,
		Target:    schema.TargetDesktopMacOS,
	})
	if err != nil {
		log.Printf("dev-plugin init failed: %v", err)
		return
	}
	go func() {
		_ = reloader.Watch(ctx, 300*time.Millisecond, func(res marketplace.ReloadResult, reloadErr error) {
			if reloadErr != nil {
				mktUI.SetStatus(fmt.Sprintf("dev-plugin reload failed (active gen:%d): %v", res.ActiveGeneration, reloadErr), true)
			} else {
				mktUI.UpsertItem(mygoshell.MarketplaceItem{
					PluginID:          string(res.PluginID),
					Version:           res.Version,
					PublisherID:       "dev.local",
					Summary:           "Live dev-plugin hot-reload session",
					SlotID:            string(res.SlotID),
					Runtime:           res.Runtime,
					SignatureVerified: true,
					Installed:         true,
					InstalledVersion:  res.Version,
					ActiveGeneration:  res.ActiveGeneration,
					HealthState:       "HOT_RELOADING",
				})
				mktUI.SetStatus(fmt.Sprintf("Hot-reloaded %s v%s gen:%d (%s)", res.PluginID, res.Version, res.ActiveGeneration, res.Elapsed.Round(time.Millisecond)), false)
			}
			win.Invalidate()
		})
	}()
}

func wireDemoHotPlugToggle(
	ctx context.Context,
	app *mygoshell.WorkbenchApp,
	host *pluginhost.Host,
	mounted *atomic.Bool,
	win *mygo.Window,
) {
	app.OnTogglePlugin = func() {
		if mounted.Load() {
			_ = host.Deactivate("tao3k.org-kanban")
			mounted.Store(false)
		} else {
			_, err := host.Activate(ctx, pluginhost.ScopedScope{
				PluginID:             "tao3k.org-kanban",
				ContributionID:       "kanban_panel",
				ActivationGeneration: 1,
				Runtime:              schema.RuntimeWebViewUntrusted,
				SlotID:               "workbench.main",
				EntryPath:            "index.html",
				ReadScopes:           []string{"org.tasks", "session.summary"},
				DeclaredActions:      []string{"org.task.move-state"},
				Assets:               os.DirFS("examples/plugins/ts-org-kanban"),
			})
			if err != nil {
				log.Printf("hot-plug failed: %v", err)
				return
			}
			mounted.Store(true)
		}
		win.Invalidate()
	}
}
