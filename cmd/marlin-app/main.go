// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

// Command marlin-app launches the modular Marlin MyGo GPU workbench with
// multi-machine federated sidebar switching (FederationSupervisor), live
// HTTP/2 gRPC SessionDriver support (connects to Rust mygo_bridge_server via
// -host/-token/-ca or runs standalone showcase mode), Scheme UI-IR rendering,
// Approval Gate overlays, and all 3 plugin runtimes (TS Untrusted WebView,
// Go wazero WASM-IR Reactor, and Declarative packs).
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
	appv1 "github.com/tao3k/marlin-mygo/gen/marlin/app/v1"
	gatev1 "github.com/tao3k/marlin-mygo/gen/marlin/gate/v1"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/device"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
	"github.com/tao3k/marlin-mygo/runtime/wazeroir"
)

type demoTarget struct{ meta transport.TargetMeta }

func (t *demoTarget) Meta() transport.TargetMeta { return t.meta }
func (t *demoTarget) Ping(context.Context) error { return nil }
func (t *demoTarget) Close() error               { return nil }

type demoDispatcher struct{}

func (demoDispatcher) SubmitScopedPanelAction(_ context.Context, req pluginhost.ScopedActionRequest) (pluginhost.ScopedActionReceipt, error) {
	return pluginhost.ScopedActionReceipt{
		Admitted:  true,
		ReceiptID: "rcpt-demo-" + req.IdempotencyKey,
		Result:    json.RawMessage(`{"status":"admitted"}`),
	}, nil
}

func main() {
	sidecarBin := flag.String("sidecar", "", "optional local Rust marlin-mygo-host binary to spawn with ephemeral token")
	realSession := flag.Bool("real-session", false, "enable MARLIN_BRIDGE_REAL_SESSION=1 durable ConversationJournal streaming on supervised sidecar")
	tursoPath := flag.String("turso", "", "optional Turso SQLite file path for crash-resilient session persistence (MARLIN_BRIDGE_TURSO_PATH)")
	pubKeyFile := flag.String("pubkey", "", "optional hex Ed25519 publisher pubkey file passed to supervised sidecar")
	hostAddr := flag.String("host", "", "optional live Rust marlin-agent-app-server gRPC address (e.g. 127.0.0.1:50051)")
	token := flag.String("token", "marlin-dev-token", "Bearer token for live Rust host")
	caFile := flag.String("ca", "", "optional TLS CA PEM file for remote gRPC hosts")
	certFile := flag.String("cert", "", "optional mTLS client certificate PEM file")
	keyFile := flag.String("key", "", "optional mTLS client private key PEM file")
	sessionID := flag.String("session", "gui-session", "Marlin session ID")
	catalogURL := flag.String("catalog-url", "", "optional HTTP/HTTPS signed Marketplace catalog URL (e.g. http://127.0.0.1:8787)")
	catalogDir := flag.String("catalog-dir", "", "optional local directory containing signed catalog.json and .marlinpkg archives")
	catalogPub := flag.String("catalog-pub", "", "optional hex Ed25519 public key file verifying the Marketplace catalog")
	trustRingFile := flag.String("trust-ring", "", "optional multi-publisher trust-ring.toml overriding single -catalog-pub key")
	catalogPublisher := flag.String("catalog-publisher", "tao3k.official", "trusted Marketplace publisher identifier")
	pluginsProfile := flag.String("plugins-profile", "", "optional declarative marlin-plugins.toml lockfile persisted across restarts (Emacs-style lazy loading)")
	maxActiveLazy := flag.Int("max-active-lazy", 8, "maximum simultaneously active lazy plugins in O(1) LRU working set (0 = unbounded)")
	idleReapInterval := flag.Duration("idle-reap-interval", 15*time.Second, "background O(1) idle plugin reclamation check interval")
	wasmCacheDir := flag.String("wasm-cache-dir", "", "optional persistent disk directory for wazero AOT compiled WASM cache (defaults to user cache dir)")
	devPluginDir := flag.String("dev-plugin", "", "optional local plugin directory to watch and hot-reload live into the GPU workbench")
	showcase := flag.Bool("showcase", false, "one-command macOS/desktop showcase: auto-build & mount React 19 AI workflow + Go WASI GPU Focus Clock with live cross-runtime sync")
	showcasePlugin := flag.String("showcase-plugin", "agenda", "initial showcase main plugin: agenda | memory | workflow")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var showcaseCleanup func()
	if *showcase && strings.TrimSpace(*catalogDir) == "" && strings.TrimSpace(*catalogURL) == "" {
		cDir, pFile, clean, err := prepareShowcaseCatalog(ctx)
		if err != nil {
			log.Fatalf("prepare -showcase catalog failed: %v", err)
		}
		showcaseCleanup = clean
		defer showcaseCleanup()
		*catalogDir = cDir
		*catalogPub = pFile
		*catalogPublisher = "tao3k.official"
	}

	targets := transport.NewTargetRegistry()
	federation := transport.NewFederationSupervisor(targets)

	slots := shell.NewSlotRegistry()
	declareHostFallback := func(slotID schema.SlotID, hostID, title, body string, allowUntrusted bool) {
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
		if err := slots.Declare(schema.SlotDeclaration{
			SlotID: slotID, Cardinality: schema.SlotSingle,
			FallbackRenderer: hostID, AllowUntrustedPlugin: allowUntrusted,
		}, fb); err != nil {
			log.Fatal(err)
		}
	}
	declareHostFallback("workbench.main", "host.main", "Host Core UI-IR Fallback (workbench.main)",
		"Ready — click 'Mount Plugin' in the sidebar to hot-plug the Untrusted Org Kanban WebView.", true)
	declareHostFallback("workbench.inspector", "host.inspector", "Host Inspector Fallback (workbench.inspector)",
		"Ready — mount a signed Go WASM-IR Reactor (.marlinpkg) for 25us GPU rendering.", true)
	declareHostFallback("approval.gate", "host.gate", "Host Approval Gate", "Restricted Tier-0 host slot.", false)

	wb := shell.NewWorkbenchController(slots)
	var dispatcher pluginhost.ActionDispatcher = demoDispatcher{}
	var showcaseDisp *showcaseDispatcher
	if *showcase {
		showcaseDisp = &showcaseDispatcher{}
		dispatcher = showcaseDisp
	}
	var liveTarget *grpcwire.Target
	var liveDriver *grpcwire.SessionDriver

	if strings.TrimSpace(*sidecarBin) != "" {
		var pubHex string
		if strings.TrimSpace(*pubKeyFile) != "" {
			raw, err := os.ReadFile(*pubKeyFile)
			if err != nil {
				log.Fatalf("read -pubkey %s: %v", *pubKeyFile, err)
			}
			pubHex = strings.TrimSpace(string(raw))
		}
		var extraEnv []string
		if *realSession {
			extraEnv = append(extraEnv, "MARLIN_BRIDGE_REAL_SESSION=1")
		}
		if strings.TrimSpace(*tursoPath) != "" {
			extraEnv = append(extraEnv, "MARLIN_BRIDGE_TURSO_PATH="+strings.TrimSpace(*tursoPath))
		}
		resilient, err := grpcwire.LaunchResilientSidecar(ctx, grpcwire.ResilientSidecarConfig{
			ID:          "local-rust-sidecar",
			DisplayName: "Rust Authority Sidecar",
			Sidecar: transport.SidecarConfig{
				BinaryPath:      *sidecarBin,
				PublisherKeyHex: pubHex,
				ExtraEnv:        extraEnv,
				Stderr:          os.Stderr,
			},
			OnCrash: func(attempt int, exitErr error) {
				log.Printf("supervised sidecar exited unexpectedly (attempt %d): %v", attempt, exitErr)
			},
			OnRecovered: func(rc context.Context, newTarget *grpcwire.Target, restarts int) {
				log.Printf("supervised sidecar auto-recovered (restart #%d) at %s", restarts, newTarget.Meta().Address)
				federation.ProbeAll(rc, 2*time.Second)
			},
		})
		if err != nil {
			log.Fatalf("launch supervised sidecar %s: %v", *sidecarBin, err)
		}
		defer resilient.Close()
		liveTarget = resilient.CurrentTarget()
		if err := targets.Register(resilient); err != nil {
			log.Fatal(err)
		}
		federation.ProbeAll(ctx, 2*time.Second)
		dispatcher = &grpcwire.PanelActionDispatcher{
			Target:    liveTarget,
			ProjectID: "default",
			SessionID: schema.SessionID(*sessionID),
		}
	} else if strings.TrimSpace(*hostAddr) != "" {
		authHeader := *token
		if authHeader != "" && !strings.HasPrefix(authHeader, "Bearer ") {
			authHeader = "Bearer " + authHeader
		}
		mode := transport.ModeLocalSidecar
		if !grpcwire.IsLoopbackAddress(*hostAddr) {
			mode = transport.ModeRemoteGRPC
		}
		var tlsCfg *grpcwire.TLSConfig
		if *caFile != "" || *certFile != "" || *keyFile != "" {
			tlsCfg = &grpcwire.TLSConfig{CAFile: *caFile, CertFile: *certFile, KeyFile: *keyFile}
		}
		dialed, err := grpcwire.Dial(grpcwire.DialConfig{
			ID:          "live-rust-host",
			DisplayName: "Rust Authority Core",
			Mode:        mode,
			Address:     *hostAddr,
			AuthToken:   authHeader,
			TLS:         tlsCfg,
		})
		if err != nil {
			log.Fatalf("dial live Rust host %s: %v", *hostAddr, err)
		}
		defer dialed.Close()
		liveTarget = dialed
		if err := targets.Register(dialed); err != nil {
			log.Fatal(err)
		}
		federation.ProbeAll(ctx, 2*time.Second)
		dispatcher = &grpcwire.PanelActionDispatcher{
			Target:    liveTarget,
			ProjectID: "default",
			SessionID: schema.SessionID(*sessionID),
		}
	} else {
		_ = targets.Register(&demoTarget{meta: transport.TargetMeta{
			ID: "local-mac", DisplayName: "Local Sidecar", Mode: transport.ModeLocalSidecar,
			TargetKind: schema.TargetDesktopMacOS, Address: "127.0.0.1:50051", Phase: transport.PhaseReady,
		}})
		_ = targets.Register(&demoTarget{meta: transport.TargetMeta{
			ID: "remote-linux", DisplayName: "Remote H100 Agent", Mode: transport.ModeRemoteGRPC,
			TargetKind: schema.TargetDesktopLinux, Address: "10.0.0.42:50051", Phase: transport.PhaseReady,
		}})
		_ = targets.Register(&demoTarget{meta: transport.TargetMeta{
			ID: "iphone-personal", DisplayName: "iPhone Personal", Mode: transport.ModeRemoteGRPC,
			TargetKind: schema.TargetMobileIOS, Address: "tailnet:50051", Phase: transport.PhaseReady,
		}})
		federation.ProbeAll(ctx, time.Second)
		if !*showcase {
			federation.UpdateMetrics("remote-linux", 1, 2, 1)
		}

		activeTheme := schema.DefaultUITheme()
		profileLabel := "desktop-dev"
		if *showcase {
			activeTheme = mygoshell.StudioDarkPalette()
			profileLabel = "studio-showcase"
		}
		if err := wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
			Generation:   1,
			ProfileName:  profileLabel,
			SessionLabel: *sessionID,
			Theme:        activeTheme,
			FrameTree: schema.UIElement{
				Kind: schema.ElemRows,
				Children: []schema.UIElement{
					{Kind: schema.ElemSlot, SlotID: "workbench.main", Flex: 1},
					{Kind: schema.ElemSlot, SlotID: "workbench.inspector"},
				},
			},
		}); err != nil {
			log.Fatal(err)
		}
	}

	wvRuntime := mygoshell.NewWebViewRuntime(schema.RuntimeWebViewUntrusted)
	if err := wvRuntime.RegisterProtocol(); err != nil {
		log.Fatal(err)
	}
	wasmEng, err := wazeroir.NewModuleEngineWithConfig(ctx, wazeroir.EngineConfig{
		CompilationCacheDir: strings.TrimSpace(*wasmCacheDir),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer wasmEng.Close(ctx)

	host := pluginhost.NewHost(slots, dispatcher)
	host.RegisterRuntime(wvRuntime)
	host.RegisterRuntime(wazeroir.NewRuntime(slots, wasmEng))
	host.RegisterRuntime(&pluginhost.DeclarativeRuntimeFactory{})

	app := mygoshell.NewWorkbenchApp(targets, slots, wb)
	app.Federation = federation
	app.BrandTitle = "Marlin Studio"
	app.BrandSubtitle = "MyGo GPU · React 19 + WASI"
	transcript := mygoshell.NewTranscriptController()
	app.Transcript = transcript
	if *showcase {
		app.TranscriptHidden = true
	}
	studioSettingsSchema := schema.BoundedSchema{
		ID:    "studio.preferences",
		Title: "Marlin Studio Preferences (Cmd+,)",
		Fields: []schema.FieldSpec{
			{Key: "theme", Label: "Appearance", Kind: schema.FieldEnum, EnumValues: []string{"obsidian", "paper"}, Default: "obsidian"},
			{Key: "focus_min", Label: "Focus Block Minutes", Kind: schema.FieldInt, MinInt: 5, MaxInt: 90, Default: 25},
			{Key: "lazy_cap", Label: "Max Active Lazy Plugins", Kind: schema.FieldInt, MinInt: 1, MaxInt: 32, Default: *maxActiveLazy},
		},
	}
	studioSettingsState := mygoshell.NewSchemaFormState(studioSettingsSchema, nil)
	app.SettingsSchema = &studioSettingsSchema
	app.SettingsState = studioSettingsState
	app.Projection = mygoshell.NewProjectionController(mygoshell.ProjectionSnapshot{
		GraphNodes: []mygoshell.GraphNodeReceiptView{
			{
				NodeID:      "node-admit",
				Kind:        "readiness-gate",
				Phase:       "succeeded",
				DurationMS:  4,
				ReceiptID:   "rcpt-graph-admit-001",
				GatewayNote: "no-live-llm gateway denial receipt preserved",
				Summary:     "Rust native ABI readiness gate verified",
			},
			{
				NodeID:     "node-org-plan",
				Kind:       "org-agenda",
				Phase:      "succeeded",
				DurationMS: 11,
				ReceiptID:  "rcpt-graph-plan-002",
				Summary:    "Projected daily org agenda & time blocks",
			},
		},
		DiffFiles: []mygoshell.WorkspaceDiffFileView{
			{
				Path:      "runtime/pluginhost/artifact_protocol.go",
				Status:    "modified",
				Additions: 24,
				Deletions: 2,
				HunkLines: []string{"+marlin-artifact://<sha256> content-addressed CAS enabled"},
			},
		},
		Processes: []mygoshell.ManagedProcessView{
			{
				ProcessID: "proc-marlin-host",
				Command:   "marlin-mygo-host",
				State:     "running",
				ExitCode:  0,
				UptimeSec: 60,
				TailLines: []string{"marlin.projection.v1 read-only surface active (secrets hidden)"},
			},
		},
	})
	app.OnAddRemoteTarget = func(targetID, displayName, endpoint, bearerToken string) error {
		id := strings.TrimSpace(targetID)
		addr := strings.TrimSpace(endpoint)
		if id == "" || addr == "" {
			return fmt.Errorf("target ID and gRPC address are required")
		}
		if displayName == "" {
			displayName = id
		}
		authHeader := strings.TrimSpace(bearerToken)
		if authHeader != "" && !strings.HasPrefix(authHeader, "Bearer ") {
			authHeader = "Bearer " + authHeader
		}
		dialed, err := grpcwire.Dial(grpcwire.DialConfig{
			ID:          id,
			DisplayName: displayName,
			Mode:        transport.ModeRemoteGRPC,
			Address:     addr,
			AuthToken:   authHeader,
		})
		if err != nil {
			return err
		}
		if err := targets.Register(dialed); err != nil {
			_ = dialed.Close()
			return err
		}
		federation.ProbeAll(ctx, 2*time.Second)
		return nil
	}
	var themeLight atomic.Bool
	var wbGen atomic.Uint64
	wbGen.Store(1)
	applyStudioTheme := func(light bool) {
		themeLight.Store(light)
		th := mygoshell.StudioDarkPalette()
		if light {
			th = mygoshell.StudioLightPalette()
		}
		if cur, ok := wb.Snapshot(); ok {
			next := *cur
			next.Generation = wbGen.Add(1)
			next.Theme = th
			_ = wb.AdmitSnapshot(next)
		}
		wvRuntime.BroadcastTheme(th)
	}
	app.IsLightThemeFn = themeLight.Load
	app.OnToggleTheme = func() {
		applyStudioTheme(!themeLight.Load())
	}
	app.OnSaveSettings = func(vals map[string]any) {
		if mode, ok := vals["theme"].(string); ok {
			applyStudioTheme(mode == "paper")
		}
	}
	mktUI := mygoshell.NewMarketplaceController()
	app.Marketplace = mktUI
	app.Commands = []input.CommandEntry{
		{Name: "workbench.mount-kanban", Description: "Hot-plug TS Org Kanban WebView"},
		{Name: "workbench.open-marketplace", Description: "Open Tier-0 built-in Marketplace drawer"},
		{Name: "workbench.probe-federation", Description: "Probe all local, remote, and mobile agents"},
		{Name: "gate.review-pending", Description: "Inspect pending human approval gate"},
	}
	var mounted atomic.Bool
	app.PluginMountedFn = mounted.Load

	if liveTarget == nil {
		openGateNow := !*showcase && strings.TrimSpace(*devPluginDir) == ""
		app.SetPendingGate(mygoshell.PendingGateView{
			GateID:      "gate-demo-1",
			ToolName:    "device.ios.notify.send",
			Prompt:      "Allow Marlin to send notification on iPhone?",
			Description: "Overlay floats above Untrusted plugin WebViews (host.gate).",
		}, openGateNow)
	}

	mygo.App.WhenReady(func() {
		winTitle := "Marlin MyGo Modular Engine"
		winW, winH := 1120, 720
		if *showcase {
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
			*catalogURL, *catalogDir, *catalogPub, *trustRingFile, *catalogPublisher,
			*pluginsProfile, *maxActiveLazy, *idleReapInterval,
		)
		if *showcase && lazySess != nil {
			showcaseDisp.attach(host, app, win)
			showcaseDisp.startTickLoop(ctx)

			initialMain := resolveShowcasePluginID(*showcasePlugin)
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
			}
			app.ActiveQuickPluginID = switcher.ActiveID
			app.OnQuickPluginSelect = switcher.Select
			switcher.Select(string(initialMain))
			go func() {
				if _, err := lazySess.InstallOrUpgrade(ctx, "tao3k.focus-clock-reactor"); err != nil {
					log.Printf("[showcase] mount inspector focus-clock failed: %v", err)
				}
				syncMarketplace()
				win.Invalidate()
			}()
		}
		fireHook := func(hookName string) {
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
		publishProjection := func(topic string, payload any) {
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

		if strings.TrimSpace(*devPluginDir) != "" {
			reloader, err := marketplace.NewDevReloader(marketplace.DevReloaderConfig{
				PluginDir: *devPluginDir,
				Host:      host,
				Target:    schema.TargetDesktopMacOS,
			})
			if err != nil {
				log.Printf("dev-plugin init failed: %v", err)
			} else {
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
		}

		if liveTarget != nil {
			deskBridge := device.NewBridge(schema.TargetDesktopMacOS)
			_ = device.RegisterDesktopMyGoAdapters(ctx, deskBridge)
			liveDriver = grpcwire.NewSessionDriver(grpcwire.DriverConfig{
				Target:       liveTarget,
				TargetKind:   schema.TargetDesktopMacOS,
				SessionID:    schema.SessionID(*sessionID),
				Workbench:    wb,
				Federation:   federation,
				PluginHost:   host,
				DeviceBridge: deskBridge,
				DeviceID:     "desktop-macos-app",
				DisplayName:  "Marlin Desktop",
				OnWorkbench:  func(schema.UIWorkbenchSnapshot) { win.Invalidate() },
				OnPendingGate: func(g *gatev1.PendingGate) {
					fireHook("before-gate-open")
					app.ShowPendingGate(mygoshell.PendingGateView{
						GateID:      g.GetGateId(),
						ToolName:    g.GetToolOrNodeName(),
						Prompt:      g.GetPrompt(),
						Description: g.GetDetailsHeading(),
					})
					win.Invalidate()
				},
			})
			if _, err := liveDriver.Start(ctx); err != nil {
				log.Printf("live SessionDriver start failed: %v", err)
			}
			app.OnGateDecision = func(gateID string, approve bool) {
				if approve {
					_, _ = liveDriver.ApproveGateOnce(ctx, gateID, "call-"+gateID, "ui-"+gateID)
				}
				win.Invalidate()
			}

			turnCtrl := grpcwire.NewTurnStreamController(liveTarget)
			var (
				turnMu     sync.Mutex
				sessionUp  bool
				lastTurn   uint64
				activeRef  *appv1.SessionTurnRef
				subCounter atomic.Uint64
			)
			app.OnSendPrompt = func(prompt string) {
				transcript.AppendUser(prompt)
				win.Invalidate()
				go func() {
					turnMu.Lock()
					if !sessionUp {
						if _, err := turnCtrl.OpenSession(ctx, *sessionID, true); err != nil {
							_, _ = turnCtrl.OpenSession(ctx, *sessionID, false)
						}
						sessionUp = true
					}
					after := lastTurn
					subID := fmt.Sprintf("ui-sub-%d", subCounter.Add(1))
					turnMu.Unlock()

					turnRef, err := turnCtrl.StartTurn(ctx, *sessionID, prompt, after, subID)
					if err != nil {
						log.Printf("StartSessionTurn failed: %v", err)
						return
					}
					turnMu.Lock()
					activeRef = turnRef
					turnMu.Unlock()
					transcript.BeginAssistantTurn(turnRef.GetOrdinal())
					win.Invalidate()

					_, outcome, watchErr := turnCtrl.WatchTurn(ctx, turnRef, "", func(upd grpcwire.TurnStreamUpdate) {
						if upd.TextDelta != "" {
							transcript.ApplyCheckpoint(upd.TurnOrdinal, upd.EventID, upd.TextDelta)
							win.Invalidate()
						}
						if upd.HasOutcome {
							transcript.CompleteTurn(upd.TurnOrdinal, upd.Outcome.String())
							fireHook("after-turn-complete")
							publishProjection("session.summary", map[string]any{
								"sessionId":   *sessionID,
								"turnOrdinal": upd.TurnOrdinal,
								"outcome":     upd.Outcome.String(),
							})
							win.Invalidate()
						}
					})
					if watchErr != nil {
						log.Printf("WatchSessionTurn error: %v", watchErr)
						transcript.CompleteTurn(turnRef.GetOrdinal(), "INTERRUPTED")
						win.Invalidate()
					} else if outcome == appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_COMPLETED {
						turnMu.Lock()
						if turnRef.GetOrdinal() > lastTurn {
							lastTurn = turnRef.GetOrdinal()
						}
						activeRef = nil
						turnMu.Unlock()
					}
				}()
			}
			app.OnCancelTurn = func() {
				turnMu.Lock()
				ref := activeRef
				turnMu.Unlock()
				if ref == nil {
					return
				}
				go func() {
					resp, err := turnCtrl.CancelTurn(ctx, ref)
					if err != nil {
						log.Printf("CancelSessionTurn failed: %v", err)
						return
					}
					if rt := resp.GetRuntime(); rt != nil {
						transcript.ApplyCancellationReceipt(mygoshell.CancellationReceiptView{
							TurnOrdinal:           ref.GetOrdinal(),
							Completed:             rt.GetCompleted(),
							CancellationRequested: rt.GetCancellationRequested(),
							TrackerClosed:         rt.GetTrackerClosed(),
							TrackedTasks:          rt.GetTrackedTasksAtShutdown(),
							RemainingTasks:        rt.GetRemainingTasks(),
							Status:                rt.GetStatus().String(),
							Outcome:               resp.GetOutcome().String(),
						})
					}
					win.Invalidate()
				}()
			}
		} else {
			var demoTurn atomic.Uint64
			app.OnSendPrompt = func(prompt string) {
				transcript.AppendUser(prompt)
				ord := demoTurn.Add(1)
				transcript.BeginAssistantTurn(ord)
				transcript.ApplyCheckpoint(ord, fmt.Sprintf("demo-%d", ord),
					fmt.Sprintf("[standalone demo] Received %q — launch with -sidecar <marlin-mygo-host> -real-session for live Rust streaming.", prompt))
				transcript.CompleteTurn(ord, "DEMO_COMPLETED")
				fireHook("after-turn-complete")
				publishProjection("session.summary", map[string]any{
					"sessionId":   *sessionID,
					"turnOrdinal": ord,
					"outcome":     "DEMO_COMPLETED",
				})
				win.Invalidate()
			}
		}

		if !*showcase {
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
		win.Invalidate()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
	if liveDriver != nil {
		liveDriver.Stop()
	}
}

func wireMarketplaceUI(
	ctx context.Context,
	mktUI *mygoshell.MarketplaceController,
	host *pluginhost.Host,
	win *mygo.Window,
	catalogURL, catalogDir, catalogPubFile, trustRingFile, publisherID, profilePath string,
	maxActiveLazy int,
	idleReapInterval time.Duration,
) (*marketplace.LazySession, func()) {
	noopSync := func() {}
	hasSource := strings.TrimSpace(catalogURL) != "" || strings.TrimSpace(catalogDir) != ""
	hasTrust := strings.TrimSpace(trustRingFile) != "" || strings.TrimSpace(catalogPubFile) != ""
	if hasSource && hasTrust {
		var (
			trust *schema.ReleaseTrust
			pub   ed25519.PublicKey
		)
		if strings.TrimSpace(trustRingFile) != "" {
			ring, err := marketplace.LoadTrustRingFile(strings.TrimSpace(trustRingFile))
			if err != nil {
				mktUI.SetStatus(fmt.Sprintf("read -trust-ring failed: %v", err), true)
				return nil, noopSync
			}
			trust = ring.ReleaseTrust()
			k, ok := ring.CatalogKey(schema.PublisherID(publisherID))
			if !ok {
				mktUI.SetStatus("no catalog verification key found in -trust-ring", true)
				return nil, noopSync
			}
			pub = k
		} else {
			rawHex, err := os.ReadFile(catalogPubFile)
			if err != nil {
				mktUI.SetStatus(fmt.Sprintf("read -catalog-pub failed: %v", err), true)
				return nil, noopSync
			}
			pubBytes, err := hex.DecodeString(strings.TrimSpace(string(rawHex)))
			if err != nil || len(pubBytes) != ed25519.PublicKeySize {
				mktUI.SetStatus("invalid Ed25519 public key in -catalog-pub", true)
				return nil, noopSync
			}
			pub = ed25519.PublicKey(pubBytes)
			trust = schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{
				schema.PublisherID(publisherID): pub,
			})
		}
		regClient, err := marketplace.NewRegistryClient(marketplace.RegistryConfig{
			BaseURL:       catalogURL,
			LocalDir:      catalogDir,
			CacheDir:      filepath.Join(os.TempDir(), "marlin-mygo-cas"),
			CatalogPubKey: pub,
		})
		if err != nil {
			mktUI.SetStatus(fmt.Sprintf("marketplace registry init failed: %v", err), true)
			return nil, noopSync
		}
		sess, err := marketplace.NewLazySession(ctx, marketplace.LazySessionConfig{
			Registry:      regClient,
			Trust:         trust,
			Host:          host,
			Target:        schema.TargetDesktopMacOS,
			ProfilePath:   strings.TrimSpace(profilePath),
			MaxActiveLazy: maxActiveLazy,
		})
		if err != nil {
			mktUI.SetStatus(fmt.Sprintf("lazy plugin session init failed: %v", err), true)
			return nil, noopSync
		}

		syncUI := func() {
			st := sess.Stats()
			mktUI.SetProfilerSummary(mygoshell.LazyProfilerSummary{
				TotalDeclared:     st.TotalDeclared,
				ActiveCount:       st.ActiveCount,
				DormantCount:      st.DormantCount,
				MaxActiveLazy:     st.MaxActiveLazy,
				TotalWakes:        st.TotalWakes,
				TotalReaped:       st.TotalReaped,
				TotalEvicted:      st.TotalEvicted,
				ReconcileDuration: st.LastReconcileDuration,
			})
			idx := sess.CachedCatalog()
			if idx == nil {
				return
			}
			statuses := sess.StatusMap()
			items := make([]mygoshell.MarketplaceItem, 0, len(idx.Entries))
			for _, e := range idx.Entries {
				rt := schema.RuntimeDeclarative
				if len(e.Runtimes) > 0 {
					rt = e.Runtimes[0]
				}
				it := mygoshell.MarketplaceItem{
					PluginID:          string(e.PluginID),
					Version:           e.Version,
					PublisherID:       string(e.PublisherID),
					Summary:           e.Summary,
					SlotID:            string(e.SlotID),
					Runtime:           rt,
					SignatureVerified: true,
					PackageBytes:      e.PackageBytes,
				}
				if prevVer, hasRB := sess.HasRollback(e.PluginID); hasRB {
					it.PreviousVersion = prevVer
				}
				if lstat, declared := statuses[e.PluginID]; declared && lstat.Spec.Enabled {
					it.Installed = true
					it.InstalledVersion = lstat.Spec.Version
					it.ActivationPolicy = string(lstat.Spec.Activation)
					it.Triggers = lstat.Spec.Triggers
					it.LastWakeDuration = lstat.LastWakeDuration
					if lstat.State == marketplace.LazyStateDormant {
						it.Dormant = true
						it.HealthState = "DORMANT"
					} else if lstat.State == marketplace.LazyStateActive {
						it.ActiveGeneration = lstat.ActiveGeneration
						it.HealthState = "HEALTHY"
						if active, ok := host.ActiveScope(e.PluginID); ok {
							it.ReadScopes = active.ReadScopes
							it.DeclaredActions = active.DeclaredActions
						}
					}
				} else if active, ok := host.ActiveScope(e.PluginID); ok {
					it.Installed = true
					it.ActiveGeneration = active.ActivationGeneration
					it.ReadScopes = active.ReadScopes
					it.DeclaredActions = active.DeclaredActions
					it.HealthState = "HEALTHY"
				}
				items = append(items, it)
			}
			mktUI.SetItems(items)
		}

		refresh := func() error {
			if _, err := sess.RefreshCatalog(ctx); err != nil {
				return err
			}
			syncUI()
			win.Invalidate()
			return nil
		}
		install := func(pluginID string) error {
			if _, err := sess.InstallOrUpgrade(ctx, schema.PluginID(pluginID)); err != nil {
				return err
			}
			syncUI()
			win.Invalidate()
			return nil
		}

		mktUI.OnRefresh = refresh
		mktUI.OnInstall = install
		mktUI.OnUpgrade = install
		mktUI.OnRollback = func(pluginID string) error {
			if _, err := sess.Rollback(ctx, schema.PluginID(pluginID)); err != nil {
				return err
			}
			syncUI()
			win.Invalidate()
			return nil
		}
		mktUI.OnWake = func(pluginID string) error {
			if _, err := sess.Wake(ctx, schema.PluginID(pluginID)); err != nil {
				return err
			}
			syncUI()
			win.Invalidate()
			return nil
		}
		mktUI.OnSleep = func(pluginID string) error {
			if err := sess.Sleep(schema.PluginID(pluginID)); err != nil {
				return err
			}
			syncUI()
			win.Invalidate()
			return nil
		}
		mktUI.OnDisable = func(pluginID string) error {
			if err := sess.Disable(ctx, schema.PluginID(pluginID)); err != nil {
				return err
			}
			syncUI()
			win.Invalidate()
			return nil
		}

		sess.StartIdleReaper(ctx, idleReapInterval, func(reaped []schema.PluginID) {
			mktUI.SetStatus(fmt.Sprintf("Reclaimed %d idle plugin(s) to 0MB RAM: %v", len(reaped), reaped), false)
			syncUI()
			win.Invalidate()
		})

		if err := refresh(); err != nil {
			syncUI()
			mktUI.SetStatus(fmt.Sprintf("initial catalog refresh failed: %v", err), true)
		}
		return sess, syncUI
	}

	// Standalone showcase items for immediate desktop Marketplace exploration.
	mktUI.SetItems([]mygoshell.MarketplaceItem{
		{
			PluginID:          "tao3k.org-kanban",
			Version:           "0.2.0",
			PublisherID:       "tao3k.official",
			Summary:           "Org-mode Kanban board panel (TypeScript Untrusted WebView)",
			SlotID:            "workbench.main",
			Runtime:           schema.RuntimeWebViewUntrusted,
			ReadScopes:        []string{"org.tasks", "session.summary"},
			DeclaredActions:   []string{"org.task.move-state"},
			SignatureVerified: true,
		},
		{
			PluginID:          "tao3k.graph-inspector",
			Version:           "0.2.0",
			PublisherID:       "tao3k.official",
			Summary:           "Resident Go 1.27 WASI Reactor with 22us binary DrawList GPU rendering",
			SlotID:            "workbench.inspector",
			Runtime:           schema.RuntimeWasmUIIR,
			ReadScopes:        []string{"graph.nodes", "graph.edges"},
			DeclaredActions:   []string{"graph.node.inspect"},
			SignatureVerified: true,
		},
		{
			PluginID:          "tao3k.ios-personal-capture",
			Version:           "0.2.0",
			PublisherID:       "tao3k.official",
			Summary:           "App Store-safe Declarative theme & mobile capture overlay pack",
			SlotID:            "workbench.inspector",
			Runtime:           schema.RuntimeDeclarative,
			SignatureVerified: true,
		},
	})
	var genSeq atomic.Uint64
	mktUI.OnInstall = func(pluginID string) error {
		if pluginID == "tao3k.org-kanban" {
			gen := genSeq.Add(1)
			_, err := host.Activate(ctx, pluginhost.ScopedScope{
				PluginID:             "tao3k.org-kanban",
				ContributionID:       "kanban_panel",
				ActivationGeneration: gen,
				Runtime:              schema.RuntimeWebViewUntrusted,
				SlotID:               "workbench.main",
				EntryPath:            "index.html",
				ReadScopes:           []string{"org.tasks", "session.summary"},
				DeclaredActions:      []string{"org.task.move-state"},
				Assets:               os.DirFS("examples/plugins/ts-org-kanban"),
			})
			if err != nil {
				return err
			}
			mktUI.MarkInstalled(pluginID, "0.2.0", gen, "HEALTHY")
			win.Invalidate()
			return nil
		}
		return fmt.Errorf("pass -catalog-url/-catalog-dir + -catalog-pub (or run `marlin-mygo-tool build-examples`) to install %s", pluginID)
	}
	mktUI.OnDisable = func(pluginID string) error {
		if err := host.Deactivate(schema.PluginID(pluginID)); err != nil {
			return err
		}
		mktUI.MarkDisabled(pluginID)
		win.Invalidate()
		return nil
	}
	return nil, noopSync
}
