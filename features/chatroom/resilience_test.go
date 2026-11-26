// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package chatroom_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/resilience"
	"github.com/tao3k/marlin-mygo/features/chatroom"
)

func TestChatRoomPowerLossDraftOfflineQueueReplayDedupAndGapResync(t *testing.T) {
	recoveryPath := filepath.Join(t.TempDir(), "room-recovery.json")

	// 1. Type an unsent room draft & post one message while OFFLINE -> atomic fsync save!
	ctrl1 := chatroom.NewController(nil)
	view1 := chatroom.NewView(ctrl1)
	ctrl1.Resilience.MarkOffline("tailnet disconnected")
	view1.SetDraft("Unsent room note before power loss @architect")

	queuedMsg, err := ctrl1.PostMessage("ch-workspace", "human-owner", "Queued offline room check", "")
	if err != nil || queuedMsg.Delivery != resilience.DeliveryQueued || queuedMsg.SubmissionID == "" {
		t.Fatalf("expected offline room message QUEUED with SubmissionID, got %+v err=%v", queuedMsg, err)
	}
	if err := ctrl1.Resilience.SaveFile(recoveryPath); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	// 2. Cold restart into Controller #2 + View #2 -> restores unsent room draft & queued outbox!
	ctrl2 := chatroom.NewController(nil)
	if err := ctrl2.Resilience.LoadFile(recoveryPath); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	view2 := chatroom.NewView(ctrl2)
	view2.RestoreDraftsFromResilience()
	ctrl2.Resilience.MarkOffline("reconnecting")

	var flushedIDs []string
	ctrl2.OnRetryOutbox = func(chID string) {
		ctrl2.FlushOutbox(chID, func(it resilience.OutboxItem) error {
			flushedIDs = append(flushedIDs, it.SubmissionID)
			return nil
		})
	}

	theme := ui.DarkTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		view2.Render(c, theme, false)
	}, 1220, 820)
	tt.Frame()

	for _, want := range []string{"OFFLINE", "1 message(s) safely queued", "Retry Queue"} {
		if !tt.HasText(want) {
			t.Fatalf("expected offline continuity banner %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}
	if got := view2.Draft(); got != "Unsent room note before power loss @architect" {
		t.Fatalf("expected restored room draft preserved after frame render, got %q", got)
	}
	if err := tt.Click("Retry Queue"); err != nil {
		t.Fatalf("click Retry Queue in Chat Room: %v", err)
	}
	tt.Frame()
	if len(flushedIDs) != 1 || flushedIDs[0] != queuedMsg.SubmissionID {
		t.Fatalf("expected exact idempotent SubmissionID %q flushed, got %v", queuedMsg.SubmissionID, flushedIDs)
	}
	// Clicking Post submits the power-loss-restored draft directly out of the GPU input box!
	if err := tt.Click("Post"); err != nil {
		t.Fatalf("click Post for restored draft: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Unsent room note before power loss @architect") {
		t.Fatalf("expected restored draft posted into room timeline, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 3. Remote replay deduplication & sequence-gap resync callback!
	var resyncChannel, resyncCursor string
	ctrl2.OnNeedResync = func(chID, afterEventID string) {
		resyncChannel = chID
		resyncCursor = afterEventID
	}
	if dup, gap := ctrl2.IngestRemoteMessage("rm-10", 10, chatroom.RoomMessage{
		ChannelID: "ch-workspace", AuthorID: "agent-reviewer", Body: "Checkpoint 10",
	}); dup || gap {
		t.Fatalf("expected rm-10 admitted cleanly, got dup=%v gap=%v", dup, gap)
	}
	if dup, _ := ctrl2.IngestRemoteMessage("rm-10", 10, chatroom.RoomMessage{
		ChannelID: "ch-workspace", AuthorID: "agent-reviewer", Body: "Checkpoint 10",
	}); !dup {
		t.Fatal("expected duplicate replay rm-10 suppressed")
	}
	if dup, gap := ctrl2.IngestRemoteMessage("rm-13", 13, chatroom.RoomMessage{
		ChannelID: "ch-workspace", AuthorID: "agent-reviewer", Body: "Checkpoint 13 after gap",
	}); dup || !gap {
		t.Fatalf("expected sequence gap detected on 10->13, got dup=%v gap=%v", dup, gap)
	}
	if resyncChannel != "ch-workspace" || resyncCursor == "" {
		t.Fatalf("expected OnNeedResync triggered for ch-workspace, got ch=%q cursor=%q", resyncChannel, resyncCursor)
	}
}
