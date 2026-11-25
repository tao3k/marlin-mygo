// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package notify_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/engine/notify"
)

func TestNotificationDeduplicationTTLAndDNDBreakthrough(t *testing.T) {
	now := time.Date(2026, 11, 25, 23, 0, 0, 0, time.UTC)
	c := notify.NewCenter(notify.CenterConfig{
		MaxHistory:        20,
		MaxFloatingToasts: 3,
		Now:               func() time.Time { return now },
	})

	// 1. Toast storm deduplication collapses 10 reconnect notices into 1 item with Count == 10!
	for i := 0; i < 10; i++ {
		if _, err := c.Publish(notify.NotificationSpec{
			Severity:  notify.SeverityWarning,
			Source:    notify.SourceGRPC,
			Title:     "Reconnecting to Remote H100 Agent",
			Message:   "Transient HTTP/2 failure",
			DedupeKey: "grpc:reconnect:h100",
			TTL:       6 * time.Second,
		}); err != nil {
			t.Fatal(err)
		}
	}
	snap := c.Snapshot()
	if len(snap.History) != 1 || len(snap.FloatingToasts) != 1 || snap.FloatingToasts[0].Count != 10 {
		t.Fatalf("expected 1 deduplicated toast with Count=10, got %+v", snap)
	}

	// 2. TTL expires floating toast cleanly while keeping history intact!
	now = now.Add(7 * time.Second)
	snap = c.Snapshot()
	if len(snap.FloatingToasts) != 0 || len(snap.History) != 1 {
		t.Fatalf("expected expired toast removed from floating stack but retained in history, got %+v", snap)
	}

	// 3. Do-Not-Disturb suppresses Info/Success toasts, while Error & Tier-0 Gate ALWAYS break through!
	c.SetDoNotDisturb(true)
	_, _ = c.Publish(notify.NotificationSpec{
		Severity: notify.SeverityInfo,
		Source:   notify.SourceHost,
		Title:    "Routine sync complete",
	})
	_, _ = c.Publish(notify.NotificationSpec{
		Severity: notify.SeverityError,
		Source:   notify.SourceAuth,
		Title:    "OAuth token revoked",
		Actions:  []notify.Action{{ID: "reauth", Label: "Sign In", Primary: true}},
	})
	_, _ = c.Publish(notify.NotificationSpec{
		Severity: notify.SeverityWarning,
		Source:   notify.SourceGate,
		Title:    "Approval Gate pending",
		Actions:  []notify.Action{{ID: "review-gate", Label: "Review Gate", Primary: true}},
	})

	snap = c.Snapshot()
	if len(snap.FloatingToasts) != 2 {
		t.Fatalf("expected exactly 2 DND-breakthrough toasts (Gate + Error), got %+v", snap.FloatingToasts)
	}
	if snap.FloatingToasts[0].Source != notify.SourceGate || snap.FloatingToasts[1].Severity != notify.SeverityError {
		t.Fatalf("unexpected DND floating order: %+v", snap.FloatingToasts)
	}

	// 4. Triggering an action marks read, dismisses floating toast, and invokes handler.
	var triggeredAction string
	c.SetActionHandler(func(_ notify.Item, actionID string) {
		triggeredAction = actionID
	})
	c.TriggerAction(snap.FloatingToasts[1].ID, "reauth")
	if triggeredAction != "reauth" {
		t.Fatalf("expected action handler to receive 'reauth', got %q", triggeredAction)
	}
	if len(c.Snapshot().FloatingToasts) != 1 {
		t.Fatalf("expected triggered toast to dismiss, got %d floating", len(c.Snapshot().FloatingToasts))
	}
}

func TestPluginNotificationAntiSpoofingSanitizationAndRateLimit(t *testing.T) {
	now := time.Date(2026, 11, 25, 23, 0, 0, 0, time.UTC)
	c := notify.NewCenter(notify.CenterConfig{
		PluginRateBurst:  2,
		PluginRateWindow: 10 * time.Second,
		Now:              func() time.Time { return now },
	})

	// 1. Untrusted plugin trying to spoof SourceAuth or SourceGate MUST fail closed!
	if _, err := c.PublishFromPlugin("evil.plugin", notify.NotificationSpec{
		Source: notify.SourceAuth,
		Title:  "Enter your Google password",
	}); !errors.Is(err, notify.ErrSpoofedHostSource) {
		t.Fatalf("expected ErrSpoofedHostSource, got %v", err)
	}
	if _, err := c.PublishFromPlugin("", notify.NotificationSpec{
		Title: "Missing plugin ID",
	}); !errors.Is(err, notify.ErrPluginIDRequired) {
		t.Fatalf("expected ErrPluginIDRequired, got %v", err)
	}

	// 2. Valid plugin notification strips control chars, forces non-persistent, and enforces rate limit!
	it, err := c.PublishFromPlugin("tao3k.git-workbench", notify.NotificationSpec{
		Severity:   notify.SeveritySuccess,
		Title:      "PR #42 merged\n\r\x07cleanly",
		Message:    "Branch synced",
		Persistent: true, // MUST be stripped for untrusted plugins
	})
	if err != nil {
		t.Fatalf("expected first plugin notification to succeed, got %v", err)
	}
	if it.Source != notify.SourcePlugin || it.Persistent || strings.ContainsAny(it.Title, "\n\r\x07") {
		t.Fatalf("unexpected sanitized plugin notification: %+v", it)
	}

	// Second within burst succeeds; third MUST hit ErrPluginRateLimited!
	if _, err := c.PublishFromPlugin("tao3k.git-workbench", notify.NotificationSpec{Title: "Second"}); err != nil {
		t.Fatalf("expected 2nd plugin toast within burst, got %v", err)
	}
	if _, err := c.PublishFromPlugin("tao3k.git-workbench", notify.NotificationSpec{Title: "Spam 3"}); !errors.Is(err, notify.ErrPluginRateLimited) {
		t.Fatalf("expected ErrPluginRateLimited on 3rd burst toast, got %v", err)
	}

	// After rate window elapses, plugin is allowed again.
	now = now.Add(11 * time.Second)
	if _, err := c.PublishFromPlugin("tao3k.git-workbench", notify.NotificationSpec{Title: "After window"}); err != nil {
		t.Fatalf("expected plugin allowed after rate window, got %v", err)
	}
}

func TestProgressToastAndClearHistoryPreservesUnreadGates(t *testing.T) {
	c := notify.NewCenter(notify.CenterConfig{})
	prog, err := c.Publish(notify.NotificationSpec{
		Severity:    notify.SeverityInfo,
		Source:      notify.SourceHost,
		Title:       "Syncing Official Marketplace",
		Message:     "Downloading manifest...",
		HasProgress: true,
		Progress:    0.25,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !c.UpdateProgress(prog.ID, 0.85, "Verifying Ed25519 signature...") {
		t.Fatal("expected UpdateProgress to succeed")
	}
	_, _ = c.Publish(notify.NotificationSpec{
		Severity: notify.SeverityWarning,
		Source:   notify.SourceGate,
		Title:    "Approval Gate Required",
	})

	c.ClearHistory()
	snap := c.Snapshot()
	if len(snap.History) != 1 || snap.History[0].Source != notify.SourceGate {
		t.Fatalf("expected ClearHistory to preserve unread Tier-0 Gate alerts, got %+v", snap.History)
	}
}
