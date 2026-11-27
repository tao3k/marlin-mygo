// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package main

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/notify"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

func TestWireComposerWorkspaceHooksEndToEnd(t *testing.T) {
	targets := transport.NewTargetRegistry()
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)
	app.ActiveSessionID = "sess-main"
	app.Projection = mygoshell.NewProjectionController(sampleProjectionSnapshot())
	app.Notifications = mygoshell.NewNotificationController(notify.NewCenter(notify.CenterConfig{}))

	tc := mygoshell.NewTranscriptController()
	app.Transcript = tc

	var sentPrompts []string
	var projTopics []string
	app.OnSendPrompt = func(p string) { sentPrompts = append(sentPrompts, p) }

	var usedTokens atomic.Int64
	usedTokens.Store(12_000)
	wireComposerWorkspaceHooks(context.Background(), app, tc, nil, &usedTokens, nil, func(topic string, _ any) {
		projTopics = append(projTopics, topic)
	})

	// 1. Initial context window should be 200K from Marlin Core.
	if cu := tc.ContextUsage(); cu.UsedTokens != 12_000 || cu.MaxTokens != 200_000 {
		t.Fatalf("unexpected initial ContextUsage: %+v", cu)
	}

	// 2. Model switch to Marlin Pro updates context budget to 1M and publishes projection.
	if _, ok := tc.SelectModel("marlin-pro"); !ok {
		t.Fatal("expected SelectModel(marlin-pro) to succeed")
	}
	if cu := tc.ContextUsage(); cu.MaxTokens != 1_000_000 {
		t.Fatalf("expected ContextUsage max 1M after switching to Marlin Pro, got %+v", cu)
	}
	if len(projTopics) != 1 || projTopics[0] != "composer.model" {
		t.Fatalf("expected composer.model projection published, got %v", projTopics)
	}

	// 3. @-mention query resolves DiffFiles, GraphNodes, and Processes from Rust ProjectionSnapshot.
	mentions := tc.OnQueryMentions("")
	if len(mentions) < 3 {
		t.Fatalf("expected at least 3 mentions from projection snapshot, got %+v", mentions)
	}
	fileMentions := tc.OnQueryMentions("artifact_protocol")
	if len(fileMentions) != 1 || fileMentions[0].Kind != "file" {
		t.Fatalf("expected @file match for artifact_protocol, got %+v", fileMentions)
	}

	// 4. Retry Turn locates preceding user prompt and dispatches OnSendPrompt.
	tc.AppendUser("Audit the readiness gate")
	tc.BeginAssistantTurn(1)
	tc.ApplyCheckpoint(1, "ev-1", "Readiness gate verified cleanly.")
	tc.CompleteTurn(1, "COMPLETED")
	tc.OnRetryTurn(1)
	if len(sentPrompts) != 1 || sentPrompts[0] != "Audit the readiness gate" {
		t.Fatalf("expected OnRetryTurn to re-send user prompt, got %+v", sentPrompts)
	}

	// 5. Fork Turn creates and switches to a new forked session.
	tc.OnForkTurn(1)
	if !strings.HasPrefix(app.ActiveSessionID, "sess-main-fork-t1") || len(app.Sessions) != 1 {
		t.Fatalf("expected forked session active, got id=%q sessions=%+v", app.ActiveSessionID, app.Sessions)
	}

	// 6. Session search locates matching loaded rows.
	hits := tc.OnSearchSession("readiness")
	if len(hits) < 2 {
		t.Fatalf("expected >=2 search hits for 'readiness', got %+v", hits)
	}
}
