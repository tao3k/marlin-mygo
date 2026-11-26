// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"github.com/tao3k/marlin-mygo/engine/notify"
	"github.com/tao3k/marlin-mygo/engine/resilience"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

func recoveryStateFilePath() string {
	if env := strings.TrimSpace(os.Getenv("MARLIN_RECOVERY_STATE_FILE")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "marlin-recovery-state.json"
	}
	return filepath.Join(home, ".config", "marlin", "recovery-state.json")
}

// wireDurableContinuity restores crash/power-loss persisted composer drafts,
// pending offline outbox turns, and checkpoint cursors on boot, and schedules
// debounced fsync-backed atomic saves whenever drafts or cursors change.
func wireDurableContinuity(
	ctx context.Context,
	sessionID string,
	app *mygoshell.WorkbenchApp,
	transcript *mygoshell.TranscriptController,
	notifyCenter *notify.Center,
	win *mygo.Window,
) func() {
	if transcript == nil || transcript.Resilience == nil {
		return func() {}
	}
	scope := "chat:" + strings.TrimSpace(sessionID)
	if scope == "chat:" {
		scope = "chat:default"
	}
	path := recoveryStateFilePath()

	// Restore unsent draft, pending outbox, and resume cursor from last fsync snapshot.
	if err := transcript.Resilience.LoadFile(path); err == nil {
		restoredDraft := transcript.RestoreDraftFromResilience(scope)
		pending := transcript.Resilience.Outbox.Len()
		if (restoredDraft != "" || pending > 0) && notifyCenter != nil {
			_, _ = notifyCenter.Publish(notify.NotificationSpec{
				Severity: notify.SeverityInfo,
				Source:   notify.SourceHost,
				Title:    "Draft & Session Continuity Restored",
				Message:  "Recovered unsent input and checkpoint state after last exit",
			})
		}
	} else {
		transcript.ResilienceScope = scope
	}

	var (
		saveMu    sync.Mutex
		saveTimer *time.Timer
	)
	saveNow := func() {
		if err := transcript.Resilience.SaveFile(path); err != nil {
			log.Printf("durable recovery snapshot save failed: %v", err)
		}
	}
	scheduleSave := func() {
		saveMu.Lock()
		if saveTimer != nil {
			saveTimer.Reset(150 * time.Millisecond)
		} else {
			saveTimer = time.AfterFunc(150*time.Millisecond, saveNow)
		}
		saveMu.Unlock()
	}

	transcript.OnDraftChanged = func(_, _ string) {
		scheduleSave()
	}
	transcript.OnDiscardOutbox = func(_ string) {
		saveNow()
		if win != nil {
			win.Invalidate()
		}
	}
	transcript.OnRetryOutbox = func() {
		go func() {
			if app == nil || app.OnSendPrompt == nil {
				return
			}
			transcript.Resilience.Outbox.Flush(scope, func(it resilience.OutboxItem) error {
				app.OnSendPrompt(it.Text)
				return nil
			})
			transcript.Resilience.MarkOnline()
			saveNow()
			if win != nil {
				win.Invalidate()
			}
		}()
	}

	go func() {
		<-ctx.Done()
		saveNow()
	}()

	return func() {
		saveMu.Lock()
		if saveTimer != nil {
			saveTimer.Stop()
		}
		saveMu.Unlock()
		saveNow()
	}
}
