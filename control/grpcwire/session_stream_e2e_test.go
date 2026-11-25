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

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	appv1 "github.com/tao3k/marlin-mygo/gen/marlin/app/v1"
	uiv1 "github.com/tao3k/marlin-mygo/gen/marlin/ui/v1"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

// TestRealDurableSessionStreamingToMyGoTranscript launches the real compiled
// Rust `marlin-mygo-host` binary with MARLIN_BRIDGE_REAL_SESSION=1 (co-hosting
// the real durable AppServer<InMemoryAgentStorage> alongside MyGoBridgeHost on
// one HTTP/2 port), streams multi-chunk ConversationJournal TextCheckpoints
// directly into MyGo's Fenwick-virtualized TranscriptController rendered by
// WorkbenchApp (ui.NewTester), verifies durable GetSessionSummary /
// GetSessionTurnStatus / resumable cursor replay / multi-turn continuity, and
// confirms co-hosted UiWorkbenchService locked host.gate + graceful exit 0.
func TestRealDurableSessionStreamingToMyGoTranscript(t *testing.T) {
	binPath := os.Getenv("MARLIN_RUST_SIDECAR_BIN")
	if binPath == "" {
		t.Skip("MARLIN_RUST_SIDECAR_BIN not set; run via `marlin-mygo-tool e2e-rust`")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var stderr bytes.Buffer
	managed, err := grpcwire.LaunchSidecarTarget(ctx, "rust-real-session", "Rust Durable Session Host", transport.SidecarConfig{
		BinaryPath:   binPath,
		ExtraEnv:     []string{"MARLIN_BRIDGE_REAL_SESSION=1", "MARLIN_BRIDGE_CHUNK_DELAY_MS=2"},
		StartTimeout: 8 * time.Second,
		Stderr:       &stderr,
	})
	if err != nil {
		t.Fatalf("LaunchSidecarTarget(REAL_SESSION=1) failed: %v\nstderr:\n%s", err, stderr.String())
	}

	// 1. Verify Ping against real AppServer::get_capabilities with ephemeral Bearer token.
	if err := managed.Ping(ctx); err != nil {
		_ = managed.Close()
		t.Fatalf("Ping against real AppServer failed: %v", err)
	}
	if managed.Meta().Phase != transport.PhaseReady {
		_ = managed.Close()
		t.Fatalf("expected PhaseReady, got %s", managed.Meta().Phase)
	}

	// 2. Verify co-hosted UiWorkbenchService on the exact same HTTP/2 port.
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	wbResp, err := managed.UI().OpenWorkbench(ctx, &uiv1.OpenWorkbenchRequest{
		SessionId:        "durable-gui-session",
		ClientTargetKind: grpcwire.TargetKindToProto(schema.TargetDesktopMacOS),
	})
	if err != nil {
		_ = managed.Close()
		t.Fatalf("co-hosted OpenWorkbench failed: %v", err)
	}
	snap, err := grpcwire.WorkbenchFromProto(wbResp.GetWorkbench())
	if err != nil {
		_ = managed.Close()
		t.Fatalf("WorkbenchFromProto failed: %v", err)
	}
	foundLockedGate := false
	for _, decl := range snap.Slots {
		if decl.SlotID == "approval.gate" && decl.FallbackRenderer == "host.gate" && !decl.AllowUntrustedPlugin {
			foundLockedGate = true
		}
	}
	if !foundLockedGate {
		_ = managed.Close()
		t.Fatalf("expected locked approval.gate (host.gate) from co-hosted UiWorkbenchService, got %+v", snap.Slots)
	}
	if err := wb.AdmitSnapshot(snap); err != nil {
		_ = managed.Close()
		t.Fatalf("AdmitSnapshot failed: %v", err)
	}

	// 3. Open durable session via real AppServer::open_session (Create + duplicate guard + Existing).
	turnCtrl := grpcwire.NewTurnStreamController(managed.Target)
	openedID, err := turnCtrl.OpenSession(ctx, "durable-gui-session", true)
	if err != nil || openedID != "durable-gui-session" {
		_ = managed.Close()
		t.Fatalf("OpenSession(create=true) failed: id=%q err=%v", openedID, err)
	}
	if _, err := turnCtrl.OpenSession(ctx, "durable-gui-session", true); err == nil {
		_ = managed.Close()
		t.Fatalf("expected duplicate OpenSession(create=true) to fail")
	}
	if existingID, err := turnCtrl.OpenSession(ctx, "durable-gui-session", false); err != nil || existingID != "durable-gui-session" {
		_ = managed.Close()
		t.Fatalf("OpenSession(create=false) failed: id=%q err=%v", existingID, err)
	}

	// 4. Stream Turn #1 from Rust ConversationJournal straight into MyGo's Fenwick TranscriptController.
	transcript := mygoshell.NewTranscriptController()
	thinkRow := transcript.AppendThinking("Rust Authority Planner", "Inspecting durable journal state\nStreaming chunks over HTTP/2", false)
	prompt1 := "Summarize Marlin Phase H"
	transcript.AppendUser(prompt1)

	var checkpointCount atomic.Int32
	var firstCheckpointEventID string
	var startedSeen bool

	turn1, lastEv1, outcome1, err := turnCtrl.StartAndWatchTurn(ctx, "durable-gui-session", prompt1, 0, "sub-phase-h-1", func(upd grpcwire.TurnStreamUpdate) {
		if upd.Started {
			startedSeen = true
			transcript.BeginAssistantTurn(upd.TurnOrdinal)
		}
		if upd.TextDelta != "" {
			if checkpointCount.Add(1) == 1 {
				firstCheckpointEventID = upd.EventID
			}
			transcript.ApplyCheckpoint(upd.TurnOrdinal, upd.EventID, upd.TextDelta)
		}
		if upd.HasOutcome {
			transcript.CompleteTurn(upd.TurnOrdinal, upd.Outcome.String())
		}
	})
	if err != nil {
		_ = managed.Close()
		t.Fatalf("StartAndWatchTurn #1 failed: %v", err)
	}
	if !startedSeen || turn1.GetOrdinal() != 1 {
		_ = managed.Close()
		t.Fatalf("expected turn #1 started, got started=%v ordinal=%d", startedSeen, turn1.GetOrdinal())
	}
	if checkpointCount.Load() != 3 {
		_ = managed.Close()
		t.Fatalf("expected 3 streamed ConversationJournal checkpoints, got %d", checkpointCount.Load())
	}
	if outcome1 != appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_COMPLETED {
		_ = managed.Close()
		t.Fatalf("expected SESSION_TURN_OUTCOME_COMPLETED, got %v", outcome1)
	}
	if lastEv1 == "" || firstCheckpointEventID == "" {
		_ = managed.Close()
		t.Fatalf("expected non-empty durable event IDs (first=%q last=%q)", firstCheckpointEventID, lastEv1)
	}

	wantReply1 := `[marlin-core] processed "Summarize Marlin Phase H" via durable ConversationJournal.`
	entry1, ok := transcript.Entry(2) // row 0: thinking, row 1: user, row 2: assistant turn #1
	if !ok || entry1.Text != wantReply1 {
		_ = managed.Close()
		t.Fatalf("expected streamed assistant text %q, got ok=%v text=%q", wantReply1, ok, entry1.Text)
	}

	// 5. Render WorkbenchApp via headless MyGo GPU tester and verify streamed Rust text appears live.
	targets := transport.NewTargetRegistry()
	if err := targets.Register(managed); err != nil {
		_ = managed.Close()
		t.Fatalf("Register managed target failed: %v", err)
	}
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)
	app.Transcript = transcript

	tt := ui.NewTester(app.View, 1180, 760)
	if !tt.HasText("LIVE TRANSCRIPT") || !tt.HasText(wantReply1) {
		_ = managed.Close()
		t.Fatalf("expected streamed Rust reply %q in MyGo GPU frame, got %q", wantReply1, tt.Texts())
	}

	// Fold thinking block in the live UI and verify Fenwick TotalHeight shrinks cleanly.
	heightBeforeFold := transcript.TotalHeight()
	if err := tt.Click("Collapse thinking-1"); err != nil {
		_ = managed.Close()
		t.Fatalf("clicking Collapse thinking-1 failed: %v", err)
	}
	tt.Frame()
	if transcript.TotalHeight() >= heightBeforeFold {
		_ = managed.Close()
		t.Fatalf("expected Fenwick height to shrink after folding thinking block: before=%d after=%d",
			heightBeforeFold, transcript.TotalHeight())
	}
	_ = thinkRow

	// 6. Verify durable GetSessionSummary & GetSessionTurnStatus persisted in Rust ConversationJournal.
	summary, err := turnCtrl.GetSessionSummary(ctx, "durable-gui-session")
	if err != nil {
		_ = managed.Close()
		t.Fatalf("GetSessionSummary failed: %v", err)
	}
	if summary.GetRecoveryState() != appv1.SessionRecoveryState_SESSION_RECOVERY_STATE_FINISHED ||
		summary.GetLatestTurn().GetOrdinal() != 1 {
		_ = managed.Close()
		t.Fatalf("unexpected durable session summary: %+v", summary)
	}
	statusResp, err := turnCtrl.GetSessionTurnStatus(ctx, turn1)
	if err != nil {
		_ = managed.Close()
		t.Fatalf("GetSessionTurnStatus failed: %v", err)
	}
	if statusResp.GetOutcome() != appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_COMPLETED {
		_ = managed.Close()
		t.Fatalf("expected COMPLETED turn status, got %v", statusResp.GetOutcome())
	}

	// 7. Verify resumable cursor WatchTurn replay from firstCheckpointEventID is suffix-only & idempotent.
	var replayedCheckpoints int
	_, replayOutcome, err := turnCtrl.WatchTurn(ctx, turn1, firstCheckpointEventID, func(upd grpcwire.TurnStreamUpdate) {
		if upd.TextDelta != "" {
			replayedCheckpoints++
			transcript.ApplyCheckpoint(upd.TurnOrdinal, upd.EventID, upd.TextDelta)
		}
	})
	if err != nil {
		_ = managed.Close()
		t.Fatalf("resumable WatchTurn failed: %v", err)
	}
	if replayedCheckpoints != 2 || replayOutcome != appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_COMPLETED {
		_ = managed.Close()
		t.Fatalf("expected 2 suffix checkpoints + COMPLETED on cursor resume, got %d / %v", replayedCheckpoints, replayOutcome)
	}
	entryAfterReplay, _ := transcript.Entry(2)
	if entryAfterReplay.Text != wantReply1 {
		_ = managed.Close()
		t.Fatalf("expected idempotent replay to keep exact text %q, got %q", wantReply1, entryAfterReplay.Text)
	}

	// 8. Verify multi-turn continuity (Turn #2 after Turn #1).
	prompt2 := "Confirm multi-turn journal"
	transcript.AppendUser(prompt2)
	turn2, _, outcome2, err := turnCtrl.StartAndWatchTurn(ctx, "durable-gui-session", prompt2, 1, "sub-phase-h-2", func(upd grpcwire.TurnStreamUpdate) {
		if upd.TextDelta != "" {
			transcript.ApplyCheckpoint(upd.TurnOrdinal, upd.EventID, upd.TextDelta)
		}
		if upd.HasOutcome {
			transcript.CompleteTurn(upd.TurnOrdinal, upd.Outcome.String())
		}
	})
	if err != nil || turn2.GetOrdinal() != 2 || outcome2 != appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_COMPLETED {
		_ = managed.Close()
		t.Fatalf("Turn #2 failed: turn=%+v outcome=%v err=%v", turn2, outcome2, err)
	}
	summary2, err := turnCtrl.GetSessionSummary(ctx, "durable-gui-session")
	if err != nil || summary2.GetLatestTurn().GetOrdinal() != 2 {
		_ = managed.Close()
		t.Fatalf("expected latest turn ordinal 2, got summary=%+v err=%v", summary2, err)
	}
	tt.Frame()
	if !tt.HasText(`processed "Confirm multi-turn journal"`) {
		_ = managed.Close()
		t.Fatalf("expected turn #2 streamed text in GPU frame, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 9. Graceful SIGINT shutdown -> clean exit code 0.
	if err := managed.Close(); err != nil {
		t.Fatalf("managed.Close() failed: %v", err)
	}
	if exitErr := managed.Sidecar.ExitErr(); exitErr != nil {
		t.Fatalf("expected clean exit code 0 from Rust marlin-mygo-host, got %v\nstderr:\n%s", exitErr, stderr.String())
	}
}

// TestRealTursoCrashRestartRecoveryAndCancellationReceipt launches two sequential
// real Rust `marlin-mygo-host` processes backed by the SAME Turso SQLite database:
//   - Process #1 opens a durable session, completes Turn #1, starts a held Turn #2,
//     observes Chunk 1 durably, issues authoritative CancelSessionTurn, and verifies
//     the live RuntimeShutdownReceipt + MyGo GPU transcript card.
//   - Process #1 terminates completely; Process #2 (fresh OS PID, zero RAM state)
//     starts against the same Turso file, rehydrates the session via OpenSession(Existing),
//     proves duplicate Create still rejects across restarts, recovers the exact persisted
//     RuntimeShutdownReceipt via GetSessionTurnCancellation, and replays Turn #1 + Turn #2
//     history into a fresh MyGo WorkbenchApp rendered via ui.NewTester.
func TestRealTursoCrashRestartRecoveryAndCancellationReceipt(t *testing.T) {
	binPath := os.Getenv("MARLIN_RUST_SIDECAR_BIN")
	if binPath == "" {
		t.Skip("MARLIN_RUST_SIDECAR_BIN not set; run via `marlin-mygo-tool e2e-rust`")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tursoPath := filepath.Join(t.TempDir(), "marlin-durable.turso")
	const sessionID = "turso-durable-session"

	// --- PROCESS #1: Complete Turn #1 + Cancel Turn #2 mid-flight + persist to Turso ---
	var stderr1 bytes.Buffer
	proc1, err := grpcwire.LaunchSidecarTarget(ctx, "rust-turso-p1", "Rust Turso Host #1", transport.SidecarConfig{
		BinaryPath: binPath,
		ExtraEnv: []string{
			"MARLIN_BRIDGE_REAL_SESSION=1",
			"MARLIN_BRIDGE_TURSO_PATH=" + tursoPath,
			"MARLIN_BRIDGE_CHUNK_DELAY_MS=2",
		},
		StartTimeout: 8 * time.Second,
		Stderr:       &stderr1,
	})
	if err != nil {
		t.Fatalf("Process #1 launch failed: %v\nstderr:\n%s", err, stderr1.String())
	}
	pid1 := proc1.Sidecar.PID()

	ctrl1 := grpcwire.NewTurnStreamController(proc1.Target)
	if id, err := ctrl1.OpenSession(ctx, sessionID, true); err != nil || id != sessionID {
		_ = proc1.Close()
		t.Fatalf("Process #1 OpenSession(Create) failed: id=%q err=%v", id, err)
	}

	prompt1 := "Persist Turn One in Turso"
	turn1, _, outcome1, err := ctrl1.StartAndWatchTurn(ctx, sessionID, prompt1, 0, "turso-sub-1", nil)
	if err != nil || turn1.GetOrdinal() != 1 || outcome1 != appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_COMPLETED {
		_ = proc1.Close()
		t.Fatalf("Turn #1 failed: turn=%+v outcome=%v err=%v", turn1, outcome1, err)
	}

	// Start Turn #2 with deterministic hold-after-chunk-1 so we cancel strictly after Chunk 1 commits.
	prompt2 := "[hold-after-chunk-1] analyze turso recovery"
	turn2, err := ctrl1.StartTurn(ctx, sessionID, prompt2, 1, "turso-sub-2")
	if err != nil || turn2.GetOrdinal() != 2 {
		_ = proc1.Close()
		t.Fatalf("Turn #2 StartTurn failed: turn=%+v err=%v", turn2, err)
	}

	watchCtx, cancelWatch := context.WithCancel(ctx)
	chunk1Seen := make(chan string, 1)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		_, _, _ = ctrl1.WatchTurn(watchCtx, turn2, "", func(upd grpcwire.TurnStreamUpdate) {
			if upd.TextDelta != "" {
				select {
				case chunk1Seen <- upd.TextDelta:
				default:
				}
			}
		})
	}()

	select {
	case delta := <-chunk1Seen:
		if delta != "[marlin-core] " {
			cancelWatch()
			_ = proc1.Close()
			t.Fatalf("unexpected Turn #2 chunk 1 delta: %q", delta)
		}
	case <-ctx.Done():
		cancelWatch()
		_ = proc1.Close()
		t.Fatalf("timed out waiting for Turn #2 chunk 1 before cancel")
	}

	cancelResp, err := ctrl1.CancelTurn(ctx, turn2)
	cancelWatch()
	<-watchDone
	if err != nil {
		_ = proc1.Close()
		t.Fatalf("CancelTurn failed: %v", err)
	}
	rt1 := cancelResp.GetRuntime()
	if rt1 == nil || !rt1.GetCompleted() || !rt1.GetCancellationRequested() || !rt1.GetTrackerClosed() ||
		rt1.GetStatus() != appv1.RuntimeRetirementStatus_RUNTIME_RETIREMENT_STATUS_COMPLETED ||
		cancelResp.GetOutcome() != appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_REMOTE_UNCERTAIN {
		_ = proc1.Close()
		t.Fatalf("unexpected live CancelSessionTurnResponse: %+v", cancelResp)
	}

	// Fully terminate Process #1 so zero in-memory Rust state survives.
	if err := proc1.Close(); err != nil {
		t.Fatalf("Process #1 Close failed: %v", err)
	}
	if exitErr := proc1.Sidecar.ExitErr(); exitErr != nil {
		t.Fatalf("Process #1 expected clean exit 0, got %v\nstderr:\n%s", exitErr, stderr1.String())
	}

	// --- PROCESS #2: Fresh OS Process recovering 100% from Turso SQLite ---
	var stderr2 bytes.Buffer
	proc2, err := grpcwire.LaunchSidecarTarget(ctx, "rust-turso-p2", "Rust Turso Host #2", transport.SidecarConfig{
		BinaryPath: binPath,
		ExtraEnv: []string{
			"MARLIN_BRIDGE_REAL_SESSION=1",
			"MARLIN_BRIDGE_TURSO_PATH=" + tursoPath,
		},
		StartTimeout: 8 * time.Second,
		Stderr:       &stderr2,
	})
	if err != nil {
		t.Fatalf("Process #2 launch failed: %v\nstderr:\n%s", err, stderr2.String())
	}
	defer func() {
		if closeErr := proc2.Close(); closeErr != nil {
			t.Errorf("Process #2 Close failed: %v", closeErr)
		}
	}()
	if proc2.Sidecar.PID() == pid1 {
		t.Fatalf("expected fresh Rust host PID across restart, still got %d", pid1)
	}

	ctrl2 := grpcwire.NewTurnStreamController(proc2.Target)

	// Duplicate Create must still fail across restarts (durable admission record in Turso).
	if _, err := ctrl2.OpenSession(ctx, sessionID, true); err == nil {
		t.Fatalf("expected OpenSession(Create) to reject duplicate session across Turso restart")
	}
	// Existing must rehydrate the session binding directly from Turso!
	if id, err := ctrl2.OpenSession(ctx, sessionID, false); err != nil || id != sessionID {
		t.Fatalf("Process #2 OpenSession(Existing) failed to rehydrate from Turso: id=%q err=%v", id, err)
	}

	// Verify durable statuses and exact RuntimeShutdownReceipt survived the process restart.
	status1, err := ctrl2.GetSessionTurnStatus(ctx, turn1)
	if err != nil || status1.GetRecoveryState() != appv1.SessionRecoveryState_SESSION_RECOVERY_STATE_FINISHED ||
		status1.GetOutcome() != appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_COMPLETED {
		t.Fatalf("recovered Turn #1 status mismatch: resp=%+v err=%v", status1, err)
	}
	status2, err := ctrl2.GetSessionTurnStatus(ctx, turn2)
	if err != nil || status2.GetRecoveryState() != appv1.SessionRecoveryState_SESSION_RECOVERY_STATE_UNRESOLVED ||
		status2.GetOutcome() != appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_REMOTE_UNCERTAIN {
		t.Fatalf("recovered Turn #2 status mismatch: resp=%+v err=%v", status2, err)
	}

	cancelRecovered, err := ctrl2.GetSessionTurnCancellation(ctx, turn2)
	if err != nil {
		t.Fatalf("Process #2 GetSessionTurnCancellation failed: %v", err)
	}
	rt2 := cancelRecovered.GetRecorded()
	if rt2 == nil || !rt2.GetCompleted() || !rt2.GetCancellationRequested() || !rt2.GetTrackerClosed() ||
		rt2.GetStatus() != appv1.RuntimeRetirementStatus_RUNTIME_RETIREMENT_STATUS_COMPLETED ||
		rt2.GetTrackedTasksAtShutdown() != rt1.GetTrackedTasksAtShutdown() ||
		rt2.GetRemainingTasks() != rt1.GetRemainingTasks() ||
		cancelRecovered.GetOutcome() != appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_REMOTE_UNCERTAIN {
		t.Fatalf("recovered Turso cancellation receipt mismatch:\n  before=%+v\n  after =%+v", rt1, cancelRecovered)
	}

	// Replay full Turn #1 + Turn #2 history from Process #2 into a brand-new Fenwick TranscriptController!
	recoveredTranscript := mygoshell.NewTranscriptController()
	latestOrd, summary2, err := ctrl2.ReplayTurnHistory(ctx, sessionID, func(upd grpcwire.TurnStreamUpdate) {
		if upd.Started {
			recoveredTranscript.BeginAssistantTurn(upd.TurnOrdinal)
		}
		if upd.TextDelta != "" {
			recoveredTranscript.ApplyCheckpoint(upd.TurnOrdinal, upd.EventID, upd.TextDelta)
		}
		if upd.HasOutcome {
			recoveredTranscript.CompleteTurn(upd.TurnOrdinal, upd.Outcome.String())
		}
	})
	if err != nil || latestOrd != 2 || summary2.GetLatestTurn().GetOrdinal() != 2 {
		t.Fatalf("ReplayTurnHistory from Process #2 failed: latest=%d summary=%+v err=%v", latestOrd, summary2, err)
	}
	recoveredTranscript.ApplyCancellationReceipt(mygoshell.CancellationReceiptView{
		TurnOrdinal:           turn2.GetOrdinal(),
		Completed:             rt2.GetCompleted(),
		CancellationRequested: rt2.GetCancellationRequested(),
		TrackerClosed:         rt2.GetTrackerClosed(),
		TrackedTasks:          rt2.GetTrackedTasksAtShutdown(),
		RemainingTasks:        rt2.GetRemainingTasks(),
		Status:                rt2.GetStatus().String(),
		Outcome:               cancelRecovered.GetOutcome().String(),
	})

	// Render the recovered transcript on a fresh MyGo WorkbenchApp and assert GPU frame parity!
	targets := transport.NewTargetRegistry()
	_ = targets.Register(proc2)
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)
	app.Transcript = recoveredTranscript

	tt := ui.NewTester(app.View, 1180, 760)
	wantTurn1 := `[marlin-core] processed "Persist Turn One in Turso" via durable ConversationJournal.`
	if !tt.HasText(wantTurn1) {
		t.Fatalf("expected Turso-recovered Turn #1 reply %q in fresh GPU frame, got %q", wantTurn1, strings.Join(tt.Texts(), " | "))
	}
	if !tt.HasText("CANCELLATION RECEIPT · status=RUNTIME_RETIREMENT_STATUS_COMPLETED") {
		t.Fatalf("expected Turso-recovered cancellation receipt in fresh GPU frame, got %q", strings.Join(tt.Texts(), " | "))
	}
}

// TestRealSIGKILLAutoRestartAndTursoSelfHealing proves autonomous crash
// self-healing across a REAL abrupt SIGKILL of the Rust `marlin-mygo-host`
// sidecar process:
//   - Launches ResilientSidecarTarget backed by Turso SQLite and registered in
//     TargetRegistry + FederationSupervisor.
//   - Completes Turn #1 ("Before unexpected SIGKILL").
//   - Abruptly terminates Process #1 with OS SIGKILL (zero graceful shutdown).
//   - Verifies ResilientSidecarTarget detects the crash, rotates a brand-new
//     64-hex ephemeral Bearer token, respawns Process #2 (fresh PID), invokes
//     OnRecovered to rehydrate the Turso session via OpenSession(Existing) +
//     ReplayTurnHistory, refreshes FederationSupervisor to PhaseReady, and
//     executes Turn #2 ("After automatic self-healing") rendered in MyGo GPU UI.
func TestRealSIGKILLAutoRestartAndTursoSelfHealing(t *testing.T) {
	binPath := os.Getenv("MARLIN_RUST_SIDECAR_BIN")
	if binPath == "" {
		t.Skip("MARLIN_RUST_SIDECAR_BIN not set; run via `marlin-mygo-tool e2e-rust`")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tursoPath := filepath.Join(t.TempDir(), "sigkill-heal.turso")
	const sessionID = "sigkill-heal-session"

	targets := transport.NewTargetRegistry()
	federation := transport.NewFederationSupervisor(targets)
	healedTranscript := mygoshell.NewTranscriptController()

	recoveredCh := make(chan error, 1)
	var stderr bytes.Buffer
	resilient, err := grpcwire.LaunchResilientSidecar(ctx, grpcwire.ResilientSidecarConfig{
		ID:             "local-self-healing",
		DisplayName:    "Self-Healing Rust Turso Sidecar",
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
		OnRecovered: func(rc context.Context, newTarget *grpcwire.Target, _ int) {
			ctrl := grpcwire.NewTurnStreamController(newTarget)
			if _, err := ctrl.OpenSession(rc, sessionID, false); err != nil {
				recoveredCh <- err
				return
			}
			_, _, err := ctrl.ReplayTurnHistory(rc, sessionID, func(upd grpcwire.TurnStreamUpdate) {
				if upd.Started {
					healedTranscript.BeginAssistantTurn(upd.TurnOrdinal)
				}
				if upd.TextDelta != "" {
					healedTranscript.ApplyCheckpoint(upd.TurnOrdinal, upd.EventID, upd.TextDelta)
				}
				if upd.HasOutcome {
					healedTranscript.CompleteTurn(upd.TurnOrdinal, upd.Outcome.String())
				}
			})
			if err != nil {
				recoveredCh <- err
				return
			}
			federation.ProbeAll(rc, 3*time.Second)
			recoveredCh <- nil
		},
	})
	if err != nil {
		t.Fatalf("LaunchResilientSidecar failed: %v\nstderr:\n%s", err, stderr.String())
	}

	if err := targets.Register(resilient); err != nil {
		_ = resilient.Close()
		t.Fatalf("Register resilient target failed: %v", err)
	}
	federation.ProbeAll(ctx, 3*time.Second)
	if resilient.Meta().Phase != transport.PhaseReady {
		_ = resilient.Close()
		t.Fatalf("expected initial PhaseReady, got %s", resilient.Meta().Phase)
	}

	pid1 := resilient.PID()
	token1 := resilient.Sidecar().Token()

	ctrl1 := grpcwire.NewTurnStreamController(resilient.CurrentTarget())
	if _, err := ctrl1.OpenSession(ctx, sessionID, true); err != nil {
		_ = resilient.Close()
		t.Fatalf("OpenSession(Create) failed: %v", err)
	}
	turn1, _, outcome1, err := ctrl1.StartAndWatchTurn(ctx, sessionID, "Before unexpected SIGKILL", 0, "heal-sub-1", nil)
	if err != nil || turn1.GetOrdinal() != 1 || outcome1 != appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_COMPLETED {
		_ = resilient.Close()
		t.Fatalf("Turn #1 before SIGKILL failed: turn=%+v outcome=%v err=%v", turn1, outcome1, err)
	}

	// Abruptly kill Rust Process #1 with real OS SIGKILL!
	proc1, err := os.FindProcess(pid1)
	if err != nil {
		_ = resilient.Close()
		t.Fatalf("FindProcess(%d): %v", pid1, err)
	}
	if err := proc1.Kill(); err != nil {
		_ = resilient.Close()
		t.Fatalf("SIGKILL(%d): %v", pid1, err)
	}

	// Wait for autonomous recovery, token rotation, Turso rehydration, and federation re-probe.
	select {
	case healErr := <-recoveredCh:
		if healErr != nil {
			_ = resilient.Close()
			t.Fatalf("OnRecovered self-healing failed: %v\nstderr:\n%s", healErr, stderr.String())
		}
	case <-ctx.Done():
		_ = resilient.Close()
		t.Fatalf("timed out waiting for SIGKILL auto-recovery\nstderr:\n%s", stderr.String())
	}

	pid2 := resilient.PID()
	token2 := resilient.Sidecar().Token()
	if resilient.Restarts() != 1 || pid2 == 0 || pid2 == pid1 {
		_ = resilient.Close()
		t.Fatalf("expected 1 restart with fresh PID: restarts=%d pid1=%d pid2=%d", resilient.Restarts(), pid1, pid2)
	}
	if token2 == "" || token2 == token1 {
		_ = resilient.Close()
		t.Fatalf("expected fresh rotated ephemeral Bearer token after SIGKILL respawn")
	}
	if resilient.Meta().Phase != transport.PhaseReady {
		_ = resilient.Close()
		t.Fatalf("expected PhaseReady after recovery, got %s", resilient.Meta().Phase)
	}

	// Continue seamlessly with Turn #2 on the newly healed Process #2!
	ctrl2 := grpcwire.NewTurnStreamController(resilient.CurrentTarget())
	turn2, _, outcome2, err := ctrl2.StartAndWatchTurn(ctx, sessionID, "After automatic self-healing", 1, "heal-sub-2", func(upd grpcwire.TurnStreamUpdate) {
		if upd.Started {
			healedTranscript.BeginAssistantTurn(upd.TurnOrdinal)
		}
		if upd.TextDelta != "" {
			healedTranscript.ApplyCheckpoint(upd.TurnOrdinal, upd.EventID, upd.TextDelta)
		}
		if upd.HasOutcome {
			healedTranscript.CompleteTurn(upd.TurnOrdinal, upd.Outcome.String())
		}
	})
	if err != nil || turn2.GetOrdinal() != 2 || outcome2 != appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_COMPLETED {
		_ = resilient.Close()
		t.Fatalf("Turn #2 after SIGKILL self-healing failed: turn=%+v outcome=%v err=%v", turn2, outcome2, err)
	}

	// Render live WorkbenchApp with ui.NewTester confirming both pre-crash & post-heal turns appear!
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)
	app.Federation = federation
	app.Transcript = healedTranscript

	tt := ui.NewTester(app.View, 1180, 760)
	want1 := `[marlin-core] processed "Before unexpected SIGKILL" via durable ConversationJournal.`
	want2 := `[marlin-core] processed "After automatic self-healing" via durable ConversationJournal.`
	if !tt.HasText(want1) || !tt.HasText(want2) {
		_ = resilient.Close()
		t.Fatalf("expected both pre-SIGKILL and post-healing turns in GPU frame, got %q", strings.Join(tt.Texts(), " | "))
	}

	// Clean graceful shutdown of healed Process #2 must exit 0 with zero extra restarts.
	sidecar2 := resilient.Sidecar()
	if err := resilient.Close(); err != nil {
		t.Fatalf("resilient.Close() failed: %v", err)
	}
	if exitErr := sidecar2.ExitErr(); exitErr != nil {
		t.Fatalf("expected clean exit 0 from healed Process #2, got %v", exitErr)
	}
	if resilient.Restarts() != 1 {
		t.Fatalf("expected no spurious restart on intentional Close, got %d", resilient.Restarts())
	}
}
