// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/composer"
)

// TestComposerBridgeLiveRustAppServerSmoke runs an opt-in end-to-end live test
// against a real compiled Rust marlin-app-server / marlin-mygo-host binary
// (supplied via MARLIN_LIVE_HOST_BIN) verifying:
//  1. ResilientSidecarTarget boots and handshakes over real HTTP/2 gRPC
//  2. Multimodal ComposerBridge.StartMultimodalTurn dispatches a real PNG CAS
//     URI + @mention into Rust AppServer.StartSessionTurn
//  3. WatchTurnResilient streams real committed Rust checkpoints + outcome
//  4. FetchSessionSummary projects real durable Rust session state
func TestComposerBridgeLiveRustAppServerSmoke(t *testing.T) {
	binPath := strings.TrimSpace(os.Getenv("MARLIN_LIVE_HOST_BIN"))
	if binPath == "" {
		t.Skip("set MARLIN_LIVE_HOST_BIN=/path/to/marlin-mygo-host to run live Rust smoke test")
	}
	if _, err := os.Stat(binPath); err != nil {
		t.Skipf("MARLIN_LIVE_HOST_BIN not accessible: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	resilient, err := grpcwire.LaunchResilientSidecar(ctx, grpcwire.ResilientSidecarConfig{
		ID:          "live-smoke-host",
		DisplayName: "Live Rust Host",
		Sidecar: transport.SidecarConfig{
			BinaryPath: binPath,
		},
	})
	if err != nil {
		t.Fatalf("LaunchResilientSidecar failed: %v", err)
	}
	defer resilient.Close()

	turns := grpcwire.NewTurnStreamControllerWithProvider(resilient)
	sessionID := "live-composer-smoke-" + time.Now().Format("150405")
	if _, err := turns.OpenSession(ctx, sessionID, true); err != nil {
		t.Fatalf("OpenSession failed: %v", err)
	}

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, image.NewRGBA(image.Rect(0, 0, 64, 48))); err != nil {
		t.Fatal(err)
	}
	st := composer.NewState(composer.DefaultAttachmentPolicy())
	if _, err := st.AddAttachment(composer.AttachmentInput{Filename: "live.png", Data: pngBuf.Bytes()}); err != nil {
		t.Fatal(err)
	}
	st.AddMention(composer.ContextMention{Kind: "file", Target: "runtime/mygoshell/richtext_view.go", Label: "richtext_view.go"})
	sub, ok := st.BuildAndResetSubmission("Live multimodal smoke check")
	if !ok {
		t.Fatal("expected valid TurnSubmission")
	}

	bridge := grpcwire.NewComposerBridge(turns, nil)
	turnRef, refs, err := bridge.StartMultimodalTurn(ctx, sessionID, sub, 0, "live-sub-1", nil)
	if err != nil {
		t.Fatalf("StartMultimodalTurn against live Rust host failed: %v", err)
	}
	if len(refs) != 1 || turnRef.GetOrdinal() < 1 {
		t.Fatalf("unexpected live turn ref=%+v refs=%+v", turnRef, refs)
	}

	var receivedEvents int
	_, outcome, err := turns.WatchTurnResilient(ctx, turnRef, "", grpcwire.WatchRetryPolicy{MaxRetries: 3}, func(upd grpcwire.TurnStreamUpdate) {
		receivedEvents++
	})
	if err != nil {
		t.Fatalf("WatchTurnResilient against live Rust host failed: %v", err)
	}
	t.Logf("live Rust host streamed %d events with outcome=%s", receivedEvents, outcome)

	summary, err := bridge.FetchSessionSummary(ctx, sessionID, "Live Smoke")
	if err != nil {
		t.Fatalf("FetchSessionSummary against live Rust host failed: %v", err)
	}
	if summary.SessionID != sessionID || summary.TurnCount < 1 {
		t.Fatalf("unexpected live SessionSummaryView: %+v", summary)
	}
	t.Logf("live Rust SessionSummaryView: %+v", summary)
}
