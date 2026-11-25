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
) (targetConnection, error) {
	if strings.TrimSpace(cfg.sidecarBin) != "" {
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
			ID:          "local-rust-sidecar",
			DisplayName: "Rust Authority Sidecar",
			Sidecar: transport.SidecarConfig{
				BinaryPath:      cfg.sidecarBin,
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
			return targetConnection{}, fmt.Errorf("launch supervised sidecar %s: %w", cfg.sidecarBin, err)
		}
		if err := targets.Register(resilient); err != nil {
			_ = resilient.Close()
			return targetConnection{}, err
		}
		federation.ProbeAll(ctx, 2*time.Second)
		live := resilient.CurrentTarget()
		return targetConnection{
			liveTarget: live,
			dispatcher: &grpcwire.PanelActionDispatcher{
				Target:    live,
				ProjectID: "default",
				SessionID: schema.SessionID(cfg.sessionID),
			},
			closeFn: func() { _ = resilient.Close() },
		}, nil
	}

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
			ID:          "live-rust-host",
			DisplayName: "Rust Authority Core",
			Mode:        mode,
			Address:     cfg.hostAddr,
			AuthToken:   authHeader,
			TLS:         tlsCfg,
		})
		if err != nil {
			return targetConnection{}, fmt.Errorf("dial live Rust host %s: %w", cfg.hostAddr, err)
		}
		if err := targets.Register(dialed); err != nil {
			_ = dialed.Close()
			return targetConnection{}, err
		}
		federation.ProbeAll(ctx, 2*time.Second)
		return targetConnection{
			liveTarget: dialed,
			dispatcher: &grpcwire.PanelActionDispatcher{
				Target:    dialed,
				ProjectID: "default",
				SessionID: schema.SessionID(cfg.sessionID),
			},
			closeFn: func() { _ = dialed.Close() },
		}, nil
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

func registerRemoteTarget(
	ctx context.Context,
	targets *transport.TargetRegistry,
	federation *transport.FederationSupervisor,
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
				ProcessID: "proc-marlin-host",
				Command:   "marlin-mygo-host",
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
	liveTarget *grpcwire.Target,
	wb *shell.WorkbenchController,
	federation *transport.FederationSupervisor,
	host *pluginhost.Host,
	app *mygoshell.WorkbenchApp,
	transcript *mygoshell.TranscriptController,
	win *mygo.Window,
	fireHook func(string),
	publishProjection func(string, any),
) *grpcwire.SessionDriver {
	deskBridge := device.NewBridge(schema.TargetDesktopMacOS)
	_ = device.RegisterDesktopMyGoAdapters(ctx, deskBridge)
	liveDriver := grpcwire.NewSessionDriver(grpcwire.DriverConfig{
		Target:       liveTarget,
		TargetKind:   schema.TargetDesktopMacOS,
		SessionID:    schema.SessionID(cfg.sessionID),
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
	projBridge := grpcwire.NewProjectionStreamBridge(grpcwire.ProjectionBridgeConfig{
		Target:     liveTarget,
		ProjectID:  "default",
		SessionID:  cfg.sessionID,
		Controller: app.Projection,
		OnUpdate:   func(mygoshell.ProjectionSnapshot) { win.Invalidate() },
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
					win.Invalidate()
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
						app.SetPendingGate(mygoshell.PendingGateView{
							GateID:   hg.GetGateId(),
							ToolName: hg.GetCallId(),
							Prompt:   hg.GetPrompt(),
						}, true)
					}
					win.Invalidate()
				}
				if upd.HasOutcome {
					transcript.CompleteTurn(upd.TurnOrdinal, upd.Outcome.String())
					fireHook("after-turn-complete")
					publishProjection("session.summary", map[string]any{
						"sessionId":   cfg.sessionID,
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
	return liveDriver
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
	app.OnSendPrompt = func(prompt string) {
		transcript.AppendUser(prompt)
		ord := demoTurn.Add(1)
		transcript.BeginAssistantTurn(ord)
		transcript.ApplyCheckpoint(ord, fmt.Sprintf("demo-%d", ord),
			fmt.Sprintf("[standalone demo] Received %q — launch with -sidecar <marlin-mygo-host> -real-session for live Rust streaming.", prompt))
		transcript.CompleteTurn(ord, "DEMO_COMPLETED")
		fireHook("after-turn-complete")
		publishProjection("session.summary", map[string]any{
			"sessionId":   sessionID,
			"turnOrdinal": ord,
			"outcome":     "DEMO_COMPLETED",
		})
		win.Invalidate()
	}
}
