// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/resilience"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

func TestPowerLossChatDraftPersistenceAndRestoreAcrossRestart(t *testing.T) {
	recoveryFile := filepath.Join(t.TempDir(), "recovery-state.json")
	scope := "chat:sess-power"

	// 1. User types a long message + @ context mention in Session #1 before sudden power loss.
	tc1 := mygoshell.NewTranscriptController()
	tc1.ResilienceScope = scope
	tc1.SetComposerText("Draft before sudden power cut: preserve this exact sentence")
	tc1.Composer.AddMention(composer.ContextMention{Kind: "file", Target: "engine/resilience/cursor.go", Label: "@cursor.go"})
	tc1.SyncDraftMentionsToResilience()
	if err := tc1.Resilience.SaveFile(recoveryFile); err != nil {
		t.Fatalf("SaveFile (fsync atomic): %v", err)
	}

	// 2. Cold boot brand-new TranscriptController #2 -> loads recovery file -> input box & @ mentions restore!
	tc2 := mygoshell.NewTranscriptController()
	if err := tc2.Resilience.LoadFile(recoveryFile); err != nil {
		t.Fatalf("LoadFile after restart: %v", err)
	}
	restored := tc2.RestoreDraftFromResilience(scope)
	if restored != "Draft before sudden power cut: preserve this exact sentence" {
		t.Fatalf("expected restored draft, got %q", restored)
	}
	if tc2.ComposerText() != restored {
		t.Fatalf("expected ComposerText()=%q, got %q", restored, tc2.ComposerText())
	}
	restoredMentions := tc2.Composer.Snapshot().Mentions
	if len(restoredMentions) != 1 || restoredMentions[0].Target != "engine/resilience/cursor.go" {
		t.Fatalf("expected restored @ mention, got %+v", restoredMentions)
	}

	var sentPrompt string
	theme := ui.DarkTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		tc2.Render(c, theme, func(p string) { sentPrompt = p }, nil)
	}, 1100, 720)
	tt.Frame()
	if tc2.ComposerText() != restored {
		t.Fatalf("expected restored draft preserved across GPU render, got %q", tc2.ComposerText())
	}
	if err := tt.Click("Send"); err != nil {
		t.Fatalf("click Send on restored draft: %v", err)
	}
	tt.Frame()
	if sentPrompt != "Draft before sudden power cut: preserve this exact sentence" {
		t.Fatalf("expected Send to submit restored power-loss draft, got %q", sentPrompt)
	}
}

func TestTranscriptOfflineBannerReconnectingCursorAndRetryQueue(t *testing.T) {
	tc := mygoshell.NewTranscriptController()
	tc.ResilienceScope = "chat:sess-net"

	// Simulate offline state with 1 queued outbound turn.
	tc.Resilience.MarkOffline("socket closed")
	_, err := tc.Resilience.Outbox.Enqueue("chat:sess-net", "you", "", "Queued while on airplane Wi-Fi")
	if err != nil {
		t.Fatalf("Outbox.Enqueue: %v", err)
	}
	var retried int
	tc.OnRetryOutbox = func() {
		retried++
		tc.Resilience.Outbox.Flush("chat:sess-net", func(_ resilience.OutboxItem) error { return nil })
		tc.Resilience.MarkOnline()
	}

	theme := ui.DarkTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		tc.Render(c, theme, nil, nil)
	}, 1100, 720)
	tt.Frame()

	for _, want := range []string{"OFFLINE", "1 message(s) safely queued", "Queued while on airplane Wi-Fi", "Retry Queue"} {
		if !tt.HasText(want) {
			t.Fatalf("expected offline continuity banner %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}

	// Simulate Reconnecting state with exact checkpoint cursor ev-42.
	tc.Resilience.MarkReconnecting("chat:sess-net", 2, "ev-42", errors.New("eof"))
	tt.Frame()
	if !tt.HasText("RECONNECTING") || !tt.HasText("cursor ev-42") {
		t.Fatalf("expected RECONNECTING banner with cursor ev-42, got %q", strings.Join(tt.Texts(), " | "))
	}

	// Click Retry Queue -> flushes outbox and clears banner!
	if err := tt.Click("Retry Queue"); err != nil {
		t.Fatalf("click Retry Queue: %v", err)
	}
	tt.Frame()
	if retried != 1 || tc.Resilience.Outbox.Len() != 0 || tt.HasText("Retry Queue") {
		t.Fatalf("expected outbox flushed cleanly on Retry Queue, retried=%d len=%d", retried, tc.Resilience.Outbox.Len())
	}
}

func TestMobileCompanionPowerLossDraftRecoveryAndOfflineBanner(t *testing.T) {
	for _, tc := range []struct {
		plat mygoshell.MobilePlatform
		dev  string
		w, h int
	}{
		{mygoshell.MobilePlatformIOS, "iphone16", 393, 852},
		{mygoshell.MobilePlatformAndroid, "pixel9", 412, 915},
	} {
		recoveryFile := filepath.Join(t.TempDir(), "mobile-recovery.json")

		// 1. User types on phone before OS kills app or battery dies -> fsync atomic save.
		mob1 := mygoshell.NewMobileCompanionAppForPlatform(tc.plat, tc.dev, "")
		mob1.SetChatDraft("Phone draft before battery died")
		if _, err := mob1.Transcript.Resilience.Outbox.Enqueue("mobile:chat", "you", "", "Offline mobile prompt"); err != nil {
			t.Fatalf("Outbox.Enqueue (%s): %v", tc.plat, err)
		}
		if err := mob1.Transcript.Resilience.SaveFile(recoveryFile); err != nil {
			t.Fatalf("SaveFile (%s): %v", tc.plat, err)
		}

		// 2. Cold launch MobileCompanionApp #2 -> restores exact draft + shows compact Offline/Retry banner!
		mob2 := mygoshell.NewMobileCompanionAppForPlatform(tc.plat, tc.dev, "")
		if err := mob2.Transcript.Resilience.LoadFile(recoveryFile); err != nil {
			t.Fatalf("LoadFile (%s): %v", tc.plat, err)
		}
		chatRestored, _ := mob2.RestoreDraftsFromResilience()
		if chatRestored != "Phone draft before battery died" {
			t.Fatalf("expected mobile chat draft restored (%s), got %q", tc.plat, chatRestored)
		}
		mob2.Transcript.Resilience.MarkOffline("cellular drop")

		var retried int
		var sentPrompt string
		mob2.OnSendPrompt = func(p string) { sentPrompt = p }
		mob2.Transcript.OnRetryOutbox = func() {
			retried++
			mob2.Transcript.Resilience.Outbox.Flush("mobile:chat", func(_ resilience.OutboxItem) error { return nil })
			mob2.Transcript.Resilience.MarkOnline()
		}

		tt := ui.NewTester(mob2.View, tc.w, tc.h)
		tt.Frame()
		if !tt.HasText("OFFLINE") || !tt.HasText("Retry") {
			t.Fatalf("expected mobile offline banner with Retry (%s), got %q", tc.plat, strings.Join(tt.Texts(), " | "))
		}
		if err := tt.Click("Retry"); err != nil {
			t.Fatalf("click mobile Retry (%s): %v", tc.plat, err)
		}
		tt.Frame()
		if retried != 1 || mob2.Transcript.Resilience.Outbox.Len() != 0 {
			t.Fatalf("expected mobile outbox flushed (%s), retried=%d len=%d", tc.plat, retried, mob2.Transcript.Resilience.Outbox.Len())
		}

		// Tap Send -> submits the exact restored phone draft!
		if err := tt.Click("Send"); err != nil {
			t.Fatalf("click mobile Send (%s): %v", tc.plat, err)
		}
		tt.Frame()
		if sentPrompt != "Phone draft before battery died" {
			t.Fatalf("expected restored phone draft sent (%s), got %q", tc.plat, sentPrompt)
		}
	}
}
