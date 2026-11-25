// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire_test

import (
	"bytes"
	"context"
	"os"
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
