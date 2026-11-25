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
	"encoding/json"
	"flag"
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
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
		federation.UpdateMetrics("remote-linux", 1, 2, 1)

		if err := wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
			Generation:   1,
			ProfileName:  "desktop-dev",
			SessionLabel: *sessionID,
			Theme:        schema.DefaultUITheme(),
			FrameTree: schema.UIElement{
				Kind: schema.ElemRows,
				Children: []schema.UIElement{
					{Kind: schema.ElemSlot, SlotID: "workbench.main"},
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
	wasmEng, err := wazeroir.NewModuleEngine(ctx)
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
	transcript := mygoshell.NewTranscriptController()
	app.Transcript = transcript
	app.Commands = []input.CommandEntry{
		{Name: "workbench.mount-kanban", Description: "Hot-plug TS Org Kanban WebView"},
		{Name: "workbench.probe-federation", Description: "Probe all local, remote, and mobile agents"},
		{Name: "gate.review-pending", Description: "Inspect pending human approval gate"},
	}
	var mounted atomic.Bool
	app.PluginMountedFn = mounted.Load

	if liveTarget == nil {
		app.ShowPendingGate(mygoshell.PendingGateView{
			GateID:      "gate-demo-1",
			ToolName:    "device.ios.notify.send",
			Prompt:      "Allow Marlin to send notification on iPhone?",
			Description: "Overlay floats above Untrusted plugin WebViews (host.gate).",
		})
	}

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "Marlin MyGo Modular Engine",
			Width:     1120,
			Height:    720,
			MinWidth:  680,
			MinHeight: 440,
			Content:   ui.View(app.View),
		})
		wvRuntime.AttachWindow(win)

		if liveTarget != nil {
			liveDriver = grpcwire.NewSessionDriver(grpcwire.DriverConfig{
				Target:       liveTarget,
				TargetKind:   schema.TargetDesktopMacOS,
				SessionID:    schema.SessionID(*sessionID),
				Workbench:    wb,
				Federation:   federation,
				PluginHost:   host,
				DeviceBridge: device.NewBridge(schema.TargetDesktopMacOS),
				DeviceID:     "desktop-macos-app",
				DisplayName:  "Marlin Desktop",
				OnWorkbench:  func(schema.UIWorkbenchSnapshot) { win.Invalidate() },
				OnPendingGate: func(g *gatev1.PendingGate) {
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
				win.Invalidate()
			}
		}

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
		win.Invalidate()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
	if liveDriver != nil {
		liveDriver.Stop()
	}
}
