// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/promptgate"
	"github.com/tao3k/marlin-mygo/engine/shell"
	appv1 "github.com/tao3k/marlin-mygo/gen/marlin/app/v1"
	gatev1 "github.com/tao3k/marlin-mygo/gen/marlin/gate/v1"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/device"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
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

type targetConnection struct {
	liveTarget *grpcwire.Target
	provider   grpcwire.TargetProvider
	dispatcher pluginhost.ActionDispatcher
	closeFn    func()
}

func (c targetConnection) close() {
	if c.closeFn != nil {
		c.closeFn()
	}
}

func connectTarget(
	ctx context.Context,
	cfg appConfig,
	targets *transport.TargetRegistry,
	federation *transport.FederationSupervisor,
	wb *shell.WorkbenchController,
	defaultDispatcher pluginhost.ActionDispatcher,
	notifyBridge *grpcwire.NotifyBridge,
) (targetConnection, error) {
	// 1. Explicit remote or pre-started local Rust App Server (-host).
	if strings.TrimSpace(cfg.hostAddr) != "" {
		authHeader := cfg.token
		if authHeader != "" && !strings.HasPrefix(authHeader, "Bearer ") {
			authHeader = "Bearer " + authHeader
		}
		mode := transport.ModeLocalSidecar
		if !grpcwire.IsLoopbackAddress(cfg.hostAddr) {
			mode = transport.ModeRemoteGRPC
		}
		var tlsCfg *grpcwire.TLSConfig
		if cfg.caFile != "" || cfg.certFile != "" || cfg.keyFile != "" {
			tlsCfg = &grpcwire.TLSConfig{CAFile: cfg.caFile, CertFile: cfg.certFile, KeyFile: cfg.keyFile}
		}
		dialed, err := grpcwire.Dial(grpcwire.DialConfig{
			ID:          "live-app-server",
			DisplayName: "Marlin App Server",
			Mode:        mode,
			Address:     cfg.hostAddr,
			AuthToken:   authHeader,
			TLS:         tlsCfg,
		})
		if err != nil {
			return targetConnection{}, fmt.Errorf("dial live Rust App Server %s: %w", cfg.hostAddr, err)
		}
		if err := targets.Register(dialed); err != nil {
			_ = dialed.Close()
			return targetConnection{}, err
		}
		federation.ProbeAll(ctx, 2*time.Second)
		return targetConnection{
			liveTarget: dialed,
			provider:   dialed,
			dispatcher: &grpcwire.PanelActionDispatcher{
				Target:    dialed,
				Provider:  dialed,
				ProjectID: "default",
				SessionID: schema.SessionID(cfg.sessionID),
			},
			closeFn: func() { _ = dialed.Close() },
		}, nil
	}

	// 2. Default desktop architecture: automatically discover & hook the local
	// Rust Marlin App Server (`marlin-app-server`, falling back to legacy
	// `marlin-mygo-host`) unless -standalone or offline -showcase is requested.
	explicitBin := strings.TrimSpace(cfg.appServerBin)
	if explicitBin == "" {
		explicitBin = strings.TrimSpace(cfg.sidecarBin)
	}
	shouldAutoHook := !cfg.standalone && (!cfg.showcase || explicitBin != "")
	if shouldAutoHook {
		resolvedBin, source, found := transport.DiscoverAppServerBinary(transport.SidecarDiscoverOptions{
			ExplicitPath: explicitBin,
		})
		if explicitBin != "" && !found {
			return targetConnection{}, fmt.Errorf("Rust App Server binary %q not found or not executable", explicitBin)
		}
		if found {
			conn, err := launchSupervisedAppServer(ctx, cfg, resolvedBin, source, targets, federation, notifyBridge)
			if err == nil {
				return conn, nil
			}
			if explicitBin != "" {
				return targetConnection{}, err
			}
			log.Printf("auto-discovered Rust App Server (%s via %s) failed to boot (%v); falling back to standalone preview", resolvedBin, source, err)
		} else {
			log.Printf("no local marlin-app-server binary discovered; running standalone UI preview (pass -app-server <path> or -host <addr> to connect)")
		}
	}

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
	if !cfg.showcase {
		federation.UpdateMetrics("remote-linux", 1, 2, 1)
	}

	activeTheme := schema.DefaultUITheme()
	profileLabel := "desktop-dev"
	if cfg.showcase {
		activeTheme = mygoshell.StudioDarkPalette()
		profileLabel = "studio-showcase"
	}
	if err := wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation:   1,
		ProfileName:  profileLabel,
		SessionLabel: cfg.sessionID,
		Theme:        activeTheme,
		FrameTree: schema.UIElement{
			Kind: schema.ElemRows,
			Children: []schema.UIElement{
				{Kind: schema.ElemSlot, SlotID: "workbench.main", Flex: 1},
				{Kind: schema.ElemSlot, SlotID: "workbench.inspector"},
			},
		},
	}); err != nil {
		return targetConnection{}, err
	}
	return targetConnection{dispatcher: defaultDispatcher}, nil
}

func launchSupervisedAppServer(
	ctx context.Context,
	cfg appConfig,
	binPath string,
	source string,
	targets *transport.TargetRegistry,
	federation *transport.FederationSupervisor,
	notifyBridge *grpcwire.NotifyBridge,
) (targetConnection, error) {
	var pubHex string
	if strings.TrimSpace(cfg.pubKeyFile) != "" {
		raw, err := os.ReadFile(cfg.pubKeyFile)
		if err != nil {
			return targetConnection{}, fmt.Errorf("read -pubkey %s: %w", cfg.pubKeyFile, err)
		}
		pubHex = strings.TrimSpace(string(raw))
	}
	var extraEnv []string
	if cfg.realSession {
		extraEnv = append(extraEnv, "MARLIN_BRIDGE_REAL_SESSION=1")
	}
	if strings.TrimSpace(cfg.tursoPath) != "" {
		extraEnv = append(extraEnv, "MARLIN_BRIDGE_TURSO_PATH="+strings.TrimSpace(cfg.tursoPath))
	}
	resilient, err := grpcwire.LaunchResilientSidecar(ctx, grpcwire.ResilientSidecarConfig{
		ID:          "local-app-server",
		DisplayName: "Marlin App Server",
		Sidecar: transport.SidecarConfig{
			BinaryPath:      binPath,
			PublisherKeyHex: pubHex,
			ExtraEnv:        extraEnv,
			Stderr:          os.Stderr,
		},
		OnCrash: func(attempt int, exitErr error) {
			log.Printf("supervised Marlin App Server exited unexpectedly (attempt %d): %v", attempt, exitErr)
			notifyBridge.OnSidecarCrash("Marlin App Server", attempt, exitErr)
		},
		OnRecovered: func(rc context.Context, newTarget *grpcwire.Target, restarts int) {
			log.Printf("supervised Marlin App Server auto-recovered (restart #%d) at %s", restarts, newTarget.Meta().Address)
			federation.ProbeAll(rc, 2*time.Second)
			notifyBridge.OnSidecarRecovered("Marlin App Server", newTarget.Meta().Address, restarts)
		},
	})
	if err != nil {
		return targetConnection{}, fmt.Errorf("launch supervised Rust App Server %s: %w", binPath, err)
	}
	if err := targets.Register(resilient); err != nil {
		_ = resilient.Close()
		return targetConnection{}, err
	}
	federation.ProbeAll(ctx, 2*time.Second)
	live := resilient.CurrentTarget()
	log.Printf("hooked local Rust Marlin App Server (%s via %s) at %s", binPath, source, live.Meta().Address)
	return targetConnection{
		liveTarget: live,
		provider:   resilient,
		dispatcher: &grpcwire.PanelActionDispatcher{
			Target:    live,
			Provider:  resilient,
			ProjectID: "default",
			SessionID: schema.SessionID(cfg.sessionID),
		},
		closeFn: func() { _ = resilient.Close() },
	}, nil
}

func registerRemoteTarget(
	ctx context.Context,
	targets *transport.TargetRegistry,
	federation *transport.FederationSupervisor,
	authTokenSource func(context.Context) (string, error),
	targetID, displayName, endpoint, bearerToken string,
) error {
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
	dialCfg := grpcwire.DialConfig{
		ID:          id,
		DisplayName: displayName,
		Mode:        transport.ModeRemoteGRPC,
		Address:     addr,
		AuthToken:   authHeader,
	}
	if authHeader == "" && authTokenSource != nil {
		dialCfg.AuthTokenSource = authTokenSource
	}
	dialed, err := grpcwire.Dial(dialCfg)
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

func sampleProjectionSnapshot() mygoshell.ProjectionSnapshot {
	return mygoshell.ProjectionSnapshot{
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
				ProcessID: "proc-marlin-app-server",
				Command:   "marlin-app-server",
				State:     "running",
				ExitCode:  0,
				UptimeSec: 60,
				TailLines: []string{"marlin.projection.v1 read-only surface active (secrets hidden)"},
			},
		},
	}
}

func wireLiveSession(
	ctx context.Context,
	cfg appConfig,
	provider grpcwire.TargetProvider,
	wb *shell.WorkbenchController,
	federation *transport.FederationSupervisor,
	host *pluginhost.Host,
	app *mygoshell.WorkbenchApp,
	transcript *mygoshell.TranscriptController,
	win *mygo.Window,
	fireHook func(string),
	publishProjection func(string, any),
) func() {
	// Coalesce bursty Rust HTTP/2 stream events into bounded 120Hz GPU frames
	// so 2,000+ msg/sec streams never saturate the UI invalidation queue.
	coalescer := grpcwire.NewFrameCoalescer(grpcwire.DefaultFrameInterval, win.Invalidate)
	markDirty := coalescer.MarkDirty

	deskBridge := device.NewBridge(schema.TargetDesktopMacOS)
	_ = device.RegisterDesktopMyGoAdapters(ctx, deskBridge)
	liveDriver := grpcwire.NewSessionDriver(grpcwire.DriverConfig{
		Provider:     provider,
		TargetKind:   schema.TargetDesktopMacOS,
		SessionID:    schema.SessionID(cfg.sessionID),
		Workbench:    wb,
		Federation:   federation,
		PluginHost:   host,
		DeviceBridge: deskBridge,
		DeviceID:     "desktop-macos-app",
		DisplayName:  "Marlin Desktop",
		OnWorkbench:  func(schema.UIWorkbenchSnapshot) { markDirty() },
		OnPendingGate: func(g *gatev1.PendingGate) {
			fireHook("before-gate-open")
			if app.Notifications != nil {
				grpcwire.NewNotifyBridge(app.Notifications.Center).OnPendingGate(g)
			}
			if app.PromptGate != nil {
				_ = app.ShowAuthPrompt(grpcwire.PromptRequestFromPendingGate(g))
			} else {
				app.ShowPendingGate(mygoshell.PendingGateView{
					GateID:      g.GetGateId(),
					ToolName:    g.GetToolOrNodeName(),
					Prompt:      g.GetPrompt(),
					Description: g.GetDetailsHeading(),
				})
			}
			markDirty()
		},
	})
	if _, err := liveDriver.Start(ctx); err != nil {
		log.Printf("live SessionDriver start failed: %v", err)
	}
	projBridge := grpcwire.NewProjectionStreamBridge(grpcwire.ProjectionBridgeConfig{
		Provider:   provider,
		ProjectID:  "default",
		SessionID:  cfg.sessionID,
		Controller: app.Projection,
		OnUpdate:   func(mygoshell.ProjectionSnapshot) { markDirty() },
	})
	if err := projBridge.Start(ctx); err != nil {
		log.Printf("live ProjectionStreamBridge start failed: %v", err)
	}
	app.OnGateDecision = func(gateID string, approve bool) {
		if approve {
			_, _ = liveDriver.ApproveGateOnce(ctx, gateID, "call-"+gateID, "ui-approve-"+gateID)
		} else {
			_, _ = liveDriver.RejectGate(ctx, gateID, "call-"+gateID, "ui-reject-"+gateID)
		}
		markDirty()
	}
	if app.PromptGate != nil {
		app.PromptGate.OnResolve = func(req promptgate.PromptRequest, resp promptgate.PromptResponse) {
			go func() {
				switch req.Kind {
				case promptgate.PromptAuth:
					if _, err := grpcwire.DispatchPromptResponseToDriver(ctx, liveDriver, req, resp); err != nil {
						log.Printf("DispatchPromptResponseToDriver(%s): %v", req.ID, err)
					}
				case promptgate.PromptAsk:
					if resp.Action == promptgate.ActionSubmit && resp.TextInput != "" && app.OnSendPrompt != nil {
						app.OnSendPrompt(resp.TextInput)
					}
				case promptgate.PromptOption:
					if resp.Action == promptgate.ActionSubmit && app.OnSendPrompt != nil {
						summary := strings.Join(resp.SelectedIDs, ", ")
						if resp.TextInput != "" {
							if summary != "" {
								summary += " (" + resp.TextInput + ")"
							} else {
								summary = resp.TextInput
							}
						}
						if summary != "" {
							app.OnSendPrompt(summary)
						}
					}
				}
				markDirty()
			}()
		}
	}

	turnCtrl := grpcwire.NewTurnStreamControllerWithProvider(provider)
	var usedTokens atomic.Int64
	recordTokens := func(text string) {
		add := composer.EstimateTokenCount(text)
		if add <= 0 {
			return
		}
		total := int(usedTokens.Add(int64(add)))
		maxTok := transcript.ContextUsage().MaxTokens
		if maxTok <= 0 {
			maxTok = 200_000
		}
		transcript.SetContextUsage(total, maxTok)
	}
	wireComposerWorkspaceHooks(ctx, app, transcript, turnCtrl, &usedTokens, win, publishProjection)

	var (
		turnMu     sync.Mutex
		sessionUp  bool
		lastTurn   uint64
		activeRef  *appv1.SessionTurnRef
		subCounter atomic.Uint64
	)
	app.OnSendPrompt = func(prompt string) {
		transcript.AppendUser(prompt)
		recordTokens(prompt)
		markDirty()
		go func() {
			turnMu.Lock()
			if !sessionUp {
				if _, err := turnCtrl.OpenSession(ctx, cfg.sessionID, true); err != nil {
					_, _ = turnCtrl.OpenSession(ctx, cfg.sessionID, false)
				}
				sessionUp = true
			}
			after := lastTurn
			subID := fmt.Sprintf("ui-sub-%d", subCounter.Add(1))
			turnMu.Unlock()

			turnRef, err := turnCtrl.StartTurn(ctx, cfg.sessionID, prompt, after, subID)
			if err != nil {
				log.Printf("StartSessionTurn failed: %v", err)
				if transcript.Resilience != nil {
					scope := "chat:" + cfg.sessionID
					transcript.Resilience.MarkOffline(err.Error())
					_, _ = transcript.Resilience.Outbox.Enqueue(scope, "user", "", prompt)
					if transcript.OnRetryOutbox != nil {
						// Kick the ContinuityCoordinator exponential-backoff drain loop automatically.
						transcript.OnRetryOutbox()
					}
					markDirty()
				}
				return
			}
			if transcript.Resilience != nil {
				transcript.Resilience.MarkOnline()
			}
			turnMu.Lock()
			activeRef = turnRef
			turnMu.Unlock()
			transcript.BeginAssistantTurn(turnRef.GetOrdinal())
			markDirty()

			_, outcome, watchErr := turnCtrl.WatchTurnResilient(ctx, turnRef, "", grpcwire.WatchRetryPolicy{
				InitialBackoff: 50 * time.Millisecond,
				MaxBackoff:     2 * time.Second,
				MaxRetries:     8,
				OnReconnect: func(attempt int, afterEventID string, rErr error) {
					if transcript.Resilience != nil {
						transcript.Resilience.MarkReconnecting("chat:"+cfg.sessionID, attempt, afterEventID, rErr)
						markDirty()
					}
				},
			}, func(upd grpcwire.TurnStreamUpdate) {
				if transcript.Resilience != nil {
					transcript.Resilience.MarkOnline()
					if upd.EventID != "" {
						if dup, _ := transcript.Resilience.AdmitEvent("chat:"+cfg.sessionID, upd.EventID, upd.TurnOrdinal, 0); dup {
							return
						}
					}
				}
				if upd.TextDelta != "" {
					transcript.ApplyCheckpoint(upd.TurnOrdinal, upd.EventID, upd.TextDelta)
					recordTokens(upd.TextDelta)
					markDirty()
				}
				if tc := upd.ToolCall; tc != nil {
					card := mygoshell.ToolCallCardView{
						CallID:    tc.GetCallId(),
						ToolName:  tc.GetToolName(),
						Phase:     tc.GetPhase().String(),
						GateID:    tc.GetGateId(),
						ReceiptID: tc.GetReceipt().GetReceiptId(),
					}
					if c := tc.GetCard(); c != nil {
						card.Kind = c.GetKind().String()
						card.Title = c.GetTitle()
						card.Summary = c.GetSummary()
					}
					if d := tc.GetDenial(); d != nil {
						card.DenialMessage = d.GetMessage()
					}
					transcript.UpsertToolCall(card, false)
					markDirty()
				}
				if hg := upd.HumanGate; hg != nil {
					transcript.UpsertHumanGate(mygoshell.HumanGateCardView{
						GateID:    hg.GetGateId(),
						CallID:    hg.GetCallId(),
						Prompt:    hg.GetPrompt(),
						Resolved:  hg.GetResolved(),
						ReceiptID: hg.GetReceipt().GetReceiptId(),
					})
					if !hg.GetResolved() {
						if app.PromptGate != nil {
							_ = app.ShowAuthPrompt(promptgate.PromptRequest{
								ID:         hg.GetGateId(),
								Kind:       promptgate.PromptAuth,
								Title:      "Authoritative Human Gate",
								Prompt:     hg.GetPrompt(),
								ToolName:   hg.GetCallId(),
								CallID:     hg.GetCallId(),
								AuthFamily: promptgate.AuthGraphHumanGate,
								Risk:       promptgate.RiskHigh,
							})
						} else {
							app.SetPendingGate(mygoshell.PendingGateView{
								GateID:   hg.GetGateId(),
								ToolName: hg.GetCallId(),
								Prompt:   hg.GetPrompt(),
							}, true)
						}
					}
					markDirty()
				}
				if upd.HasOutcome {
					transcript.CompleteTurn(upd.TurnOrdinal, upd.Outcome.String())
					fireHook("after-turn-complete")
					publishProjection("session.summary", map[string]any{
						"sessionId":   cfg.sessionID,
						"turnOrdinal": upd.TurnOrdinal,
						"outcome":     upd.Outcome.String(),
					})
					markDirty()
				}
			})
			if watchErr != nil {
				log.Printf("WatchSessionTurn error: %v", watchErr)
				if transcript.Resilience != nil {
					transcript.Resilience.MarkOffline(watchErr.Error())
					if transcript.OnDraftChanged != nil {
						transcript.OnDraftChanged("chat:"+cfg.sessionID, transcript.ComposerText())
					}
				}
				transcript.CompleteTurn(turnRef.GetOrdinal(), "INTERRUPTED")
				markDirty()
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
			markDirty()
		}()
	}
	return func() {
		liveDriver.Stop()
		projBridge.Stop()
		coalescer.Close()
	}
}

func wireStandaloneDemo(
	sessionID string,
	app *mygoshell.WorkbenchApp,
	transcript *mygoshell.TranscriptController,
	win *mygo.Window,
	fireHook func(string),
	publishProjection func(string, any),
) {
	var demoTurn atomic.Uint64
	var usedTokens atomic.Int64
	wireComposerWorkspaceHooks(context.Background(), app, transcript, nil, &usedTokens, win, publishProjection)
	app.OnSendPrompt = func(prompt string) {
		transcript.AppendUser(prompt)
		ord := demoTurn.Add(1)
		transcript.BeginAssistantTurn(ord)
		reply := fmt.Sprintf("[standalone preview] Received %q — launch with a local marlin-app-server binary (auto-hooked on boot) or -host <addr> for live Rust streaming.", prompt)
		transcript.ApplyCheckpoint(ord, fmt.Sprintf("demo-%d", ord), reply)
		transcript.CompleteTurn(ord, "DEMO_COMPLETED")
		total := int(usedTokens.Add(int64(composer.EstimateTokenCount(prompt) + composer.EstimateTokenCount(reply))))
		maxTok := transcript.ContextUsage().MaxTokens
		if maxTok <= 0 {
			maxTok = 200_000
		}
		transcript.SetContextUsage(total, maxTok)
		fireHook("after-turn-complete")
		publishProjection("session.summary", map[string]any{
			"sessionId":   sessionID,
			"turnOrdinal": ord,
			"outcome":     "DEMO_COMPLETED",
		})
		win.Invalidate()
	}
}
