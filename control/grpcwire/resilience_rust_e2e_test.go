// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/promptgate"
	"github.com/tao3k/marlin-mygo/engine/resilience"
	"github.com/tao3k/marlin-mygo/engine/shell"
	appv1 "github.com/tao3k/marlin-mygo/gen/marlin/app/v1"
	gatev1 "github.com/tao3k/marlin-mygo/gen/marlin/gate/v1"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

// TestRealRustSidecarPowerLossDraftOutboxAutoDrainAndPromptGateE2E runs a live
// cross-process integration test against the real compiled Rust marlin-mygo-host
// binary exercising:
//  1. Atomic fsync power-loss draft + @ mention recovery across cold restart.
//  2. Real Rust durable Turn #1 streaming with EventID deduplication tracking.
//  3. Real OS SIGKILL on Rust PID #1 -> offline Outbox enqueue -> automatic
//     ResilientSidecarTarget restart + ContinuityCoordinator exponential backoff
//     -> ReplayTurnHistory duplicate suppression -> automatic idempotent Outbox
//     drain of Turn #2 into recovered Rust PID #2.
//  4. Live cross-process Tier-0 PromptGate approval via DispatchPromptResponseToDriver.
func TestRealRustSidecarPowerLossDraftOutboxAutoDrainAndPromptGateE2E(t *testing.T) {
	binPath := os.Getenv("MARLIN_RUST_SIDECAR_BIN")
	if binPath == "" {
		t.Skip("MARLIN_RUST_SIDECAR_BIN not set; run via `marlin-mygo-tool e2e-rust`")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	tursoPath := filepath.Join(tmpDir, "continuity-e2e.turso")
	recoveryFile := filepath.Join(tmpDir, "frontend-recovery.json")
	const sessionID = "continuity-rust-e2e"
	const scope = "chat:" + sessionID

	// --- Phase 1: Power-loss fsync draft + @ mention recovery across cold frontend boot ---
	tc1 := mygoshell.NewTranscriptController()
	tc1.ResilienceScope = scope
	tc1.SetComposerText("Unsent turn draft before sudden laptop power loss")
	tc1.Composer.AddMention(composer.ContextMention{
		Kind: "file", Target: "control/grpcwire/resilience_sync.go", Label: "@resilience_sync.go",
	})
	tc1.SyncDraftMentionsToResilience()
	if err := tc1.Resilience.SaveFile(recoveryFile); err != nil {
		t.Fatalf("SaveFile (fsync atomic): %v", err)
	}

	transcript := mygoshell.NewTranscriptController()
	if err := transcript.Resilience.LoadFile(recoveryFile); err != nil {
		t.Fatalf("LoadFile after cold boot: %v", err)
	}
	if restored := transcript.RestoreDraftFromResilience(scope); restored != "Unsent turn draft before sudden laptop power loss" {
		t.Fatalf("expected restored draft, got %q", restored)
	}
	if ms := transcript.Composer.Snapshot().Mentions; len(ms) != 1 || ms[0].Target != "control/grpcwire/resilience_sync.go" {
		t.Fatalf("expected restored @ mention, got %+v", ms)
	}

	// --- Phase 2: Real Rust marlin-mygo-host sidecar + SIGKILL + ContinuityCoordinator auto-drain ---
	var stderr bytes.Buffer
	resilient, err := grpcwire.LaunchResilientSidecar(ctx, grpcwire.ResilientSidecarConfig{
		ID:             "local-continuity-rust",
		DisplayName:    "Rust Continuity E2E Host",
		InitialBackoff: 40 * time.Millisecond,
		MaxBackoff:     200 * time.Millisecond,
		MaxRestarts:    3,
		Sidecar: transport.SidecarConfig{
			BinaryPath: binPath,
			ExtraEnv: []string{
				"MARLIN_BRIDGE_REAL_SESSION=1",
				"MARLIN_BRIDGE_TURSO_PATH=" + tursoPath,
				"MARLIN_BRIDGE_CHUNK_DELAY_MS=2",
			},
			StartTimeout: 8 * time.Second,
			Stderr:       &stderr,
		},
	})
	if err != nil {
		t.Fatalf("LaunchResilientSidecar failed: %v\nstderr:\n%s", err, stderr.String())
	}
	defer func() { _ = resilient.Close() }()

	switcher := grpcwire.NewSwitchableTargetProvider("local-continuity-rust", resilient)
	turnCtrl := grpcwire.NewTurnStreamControllerWithProvider(switcher)

	if _, err := turnCtrl.OpenSession(ctx, sessionID, true); err != nil {
		t.Fatalf("OpenSession(create=true) failed: %v", err)
	}

	var admittedEvents atomic.Int32
	var dupSuppressed atomic.Int32
	var lastTurnOrdinal atomic.Uint64

	applyUpdate := func(upd grpcwire.TurnStreamUpdate) {
		if upd.EventID != "" {
			if dup, _ := transcript.Resilience.AdmitEvent(scope, upd.EventID, upd.TurnOrdinal, 0); dup {
				dupSuppressed.Add(1)
				return
			}
			admittedEvents.Add(1)
		}
		if upd.Started {
			transcript.BeginAssistantTurn(upd.TurnOrdinal)
		}
		if upd.TextDelta != "" {
			transcript.ApplyCheckpoint(upd.TurnOrdinal, upd.EventID, upd.TextDelta)
		}
		if upd.HasOutcome {
			transcript.CompleteTurn(upd.TurnOrdinal, upd.Outcome.String())
			if upd.TurnOrdinal > lastTurnOrdinal.Load() {
				lastTurnOrdinal.Store(upd.TurnOrdinal)
			}
		}
	}

	// Stream Turn #1 live from Rust PID #1.
	turn1, _, outcome1, err := turnCtrl.StartAndWatchTurn(ctx, sessionID, "Turn 1 before SIGKILL", 0, "e2e-sub-1", applyUpdate)
	if err != nil || turn1.GetOrdinal() != 1 || outcome1 != appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_COMPLETED {
		t.Fatalf("Turn #1 failed: turn=%+v outcome=%v err=%v", turn1, outcome1, err)
	}
	if admittedEvents.Load() == 0 {
		t.Fatal("expected Turn #1 events admitted into SessionResilience dedup ring")
	}

	// Wire ContinuityCoordinator to auto-resume cursor + idempotently drain Outbox into Rust.
	var turn2Completed atomic.Bool
	coord := grpcwire.NewContinuityCoordinator(grpcwire.ContinuityConfig{
		ScopeID:        scope,
		Resilience:     transcript.Resilience,
		Provider:       switcher,
		InitialBackoff: 30 * time.Millisecond,
		MaxBackoff:     150 * time.Millisecond,
		ResumeCursorFn: func(rc context.Context, _ string, _ string) error {
			if _, err := turnCtrl.OpenSession(rc, sessionID, false); err != nil {
				return err
			}
			_, _, err := turnCtrl.ReplayTurnHistory(rc, sessionID, applyUpdate)
			return err
		},
		DeliverFn: func(dc context.Context, item resilience.OutboxItem) error {
			ref, _, out, err := turnCtrl.StartAndWatchTurn(
				dc, sessionID, item.Text, lastTurnOrdinal.Load(), item.SubmissionID, applyUpdate,
			)
			if err != nil {
				return err
			}
			if ref.GetOrdinal() >= 2 && out == appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_COMPLETED {
				turn2Completed.Store(true)
			}
			return nil
		},
	})
	coordCtx, stopCoord := context.WithCancel(ctx)
	defer stopCoord()
	go coord.Run(coordCtx)

	// Send real OS SIGKILL to Rust PID #1, mark offline, and enqueue Turn #2 in Outbox!
	pid1 := resilient.PID()
	proc1, err := os.FindProcess(pid1)
	if err != nil {
		t.Fatalf("FindProcess(%d): %v", pid1, err)
	}
	transcript.Resilience.MarkOffline("rust sidecar killed by SIGKILL")
	if err := proc1.Signal(os.Kill); err != nil {
		t.Fatalf("SIGKILL(%d): %v", pid1, err)
	}

	_, sentImmediately, err := coord.SubmitOrQueue(ctx, "you", "", "Turn 2 queued during Rust SIGKILL outage")
	if err != nil {
		t.Fatalf("SubmitOrQueue during outage: %v", err)
	}
	if sentImmediately {
		t.Fatal("expected Turn 2 to queue in Outbox while marked offline")
	}

	// Wait for ResilientSidecarTarget auto-restart + ContinuityCoordinator replay & outbox auto-drain!
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		snap := transcript.Resilience.Snapshot(scope)
		if turn2Completed.Load() && snap.State == resilience.ConnOnline && len(snap.PendingOutbox) == 0 {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if !turn2Completed.Load() {
		t.Fatalf("timed out waiting for ContinuityCoordinator to auto-drain Turn #2 to recovered Rust PID; state=%+v", transcript.Resilience.Snapshot(scope))
	}
	if resilient.PID() == pid1 {
		t.Fatalf("expected Rust sidecar to restart with a new PID after SIGKILL (still %d)", pid1)
	}
	if dupSuppressed.Load() == 0 {
		t.Fatal("expected ReplayTurnHistory to suppress replayed Turn #1 duplicate EventIDs")
	}

	// Verify Turn #2 response actually landed in TranscriptController without duplicating Turn #1.
	foundTurn2 := false
	for i := 0; i < transcript.Len(); i++ {
		if e, ok := transcript.Entry(i); ok && e.TurnOrdinal == 2 && strings.TrimSpace(e.Text) != "" {
			foundTurn2 = true
			break
		}
	}
	if !foundTurn2 {
		t.Fatal("expected recovered Rust Turn #2 assistant response rendered in TranscriptController")
	}

	// --- Phase 3: Live cross-process Tier-0 PromptGate approval receipt over HTTP/2 ---
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	pendingCh := make(chan *gatev1.PendingGate, 4)
	driver := grpcwire.NewSessionDriver(grpcwire.DriverConfig{
		Provider:    switcher,
		TargetKind:  schema.TargetDesktopMacOS,
		SessionID:   schema.SessionID(sessionID),
		Workbench:   wb,
		DeviceID:    "desktop-e2e-continuity",
		DisplayName: "Continuity E2E Desktop",
		OnPendingGate: func(g *gatev1.PendingGate) {
			select {
			case pendingCh <- g:
			default:
			}
		},
	})
	if _, err := driver.Start(ctx); err != nil {
		t.Fatalf("SessionDriver.Start against recovered Rust sidecar failed: %v", err)
	}
	defer driver.Stop()

	pgQueue := promptgate.NewQueue()
	// 3a. Forged/unknown gate ID MUST be denied fail-closed by Rust marlin.gate.v1.
	forgedCall := "call-forged"
	forgedReq := grpcwire.PromptRequestFromPendingGate(&gatev1.PendingGate{
		GateId:         "gate-forged-unknown",
		CallId:         &forgedCall,
		ToolOrNodeName: "workspace.apply_patch",
		Prompt:         "Forged gate",
		Kind:           gatev1.GateKind_GATE_KIND_MODEL_TOOL_REVIEW,
	})
	if err := pgQueue.Enqueue(forgedReq); err != nil {
		t.Fatalf("PromptGate.Enqueue forged: %v", err)
	}
	forgedResolved, forgedResp, err := pgQueue.Resolve(forgedReq.ID, promptgate.PromptResponse{Action: promptgate.ActionApproveOnce})
	if err != nil {
		t.Fatalf("PromptGate.Resolve forged: %v", err)
	}
	forgedReceipt, err := grpcwire.DispatchPromptResponseToDriver(ctx, driver, forgedResolved, forgedResp)
	if err != nil {
		t.Fatalf("DispatchPromptResponseToDriver(forged) transport error: %v", err)
	}
	if forgedReceipt.GetDenial() == nil || forgedReceipt.GetClaimed() {
		t.Fatalf("expected Rust marlin.gate.v1 fail-closed denial for forged gate ID, got %+v", forgedReceipt)
	}

	// 3b. Real Rust-emitted PendingGate -> PromptRequestFromPendingGate -> PromptGate.Resolve -> Claimed Receipt!
	var liveGate *gatev1.PendingGate
	select {
	case liveGate = <-pendingCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for real PendingGate from recovered Rust marlin-mygo-host")
	}
	realReq := grpcwire.PromptRequestFromPendingGate(liveGate)
	if err := pgQueue.Enqueue(realReq); err != nil {
		t.Fatalf("PromptGate.Enqueue real gate: %v", err)
	}
	resolvedReq, resp, err := pgQueue.Resolve(realReq.ID, promptgate.PromptResponse{
		Action:    promptgate.ActionApproveOnce,
		TextInput: "Verified live after SIGKILL recovery",
	})
	if err != nil {
		t.Fatalf("PromptGate.Resolve real gate: %v", err)
	}
	receipt, err := grpcwire.DispatchPromptResponseToDriver(ctx, driver, resolvedReq, resp)
	if err != nil {
		t.Fatalf("DispatchPromptResponseToDriver over live Rust gRPC failed: %v", err)
	}
	if receipt == nil || !receipt.GetClaimed() || strings.TrimSpace(receipt.GetReceipt().GetReceiptId()) == "" {
		t.Fatalf("expected authoritative Claimed Rust GateDecisionReceipt, got %+v", receipt)
	}
}
