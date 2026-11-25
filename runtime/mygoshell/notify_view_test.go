// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell_test

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/notify"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

func TestTier0FloatingToastsActivityCenterDNDAndPluginBadges(t *testing.T) {
	targets := transport.NewTargetRegistry()
	_ = targets.Register(&authUITestTarget{meta: transport.TargetMeta{
		ID: "local-app-server", DisplayName: "Marlin App Server", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Address: "127.0.0.1:50051", Phase: transport.PhaseReady,
	}})
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)

	center := notify.NewCenter(notify.CenterConfig{
		MaxFloatingToasts: 3,
	})
	var clickedAction string
	center.SetActionHandler(func(_ notify.Item, actionID string) {
		clickedAction = actionID
	})
	app.Notifications = mygoshell.NewNotificationController(center)

	// 1. Publish deduplicated gRPC warning, progress toast, and sandboxed plugin toast!
	for i := 0; i < 3; i++ {
		_, _ = center.Publish(notify.NotificationSpec{
			Severity:  notify.SeverityWarning,
			Source:    notify.SourceGRPC,
			Title:     "Remote H100 Reconnecting",
			Message:   "Resuming HTTP/2 stream from cursor",
			DedupeKey: "grpc:h100",
			TTL:       10 * time.Second,
			Actions:   []notify.Action{{ID: "retry-grpc", Label: "Retry Now", Primary: true}},
		})
	}
	_, _ = center.Publish(notify.NotificationSpec{
		Severity:    notify.SeverityInfo,
		Source:      notify.SourceHost,
		Title:       "Syncing Official Marketplace",
		Message:     "Verifying Ed25519 manifest",
		HasProgress: true,
		Progress:    0.75,
	})
	_, _ = center.PublishFromPlugin("tao3k.git-workbench", notify.NotificationSpec{
		Severity: notify.SeveritySuccess,
		Title:    "Branch rebased cleanly",
	})

	tt := ui.NewTester(app.View, 1280, 800)

	// Verify floating toasts, dedupe count x3, 75% progress, plugin badge, and status bar Activity pill!
	for _, want := range []string{
		"Remote H100 Reconnecting",
		"x3",
		"Syncing Official Marketplace",
		"75%",
		"PLUGIN · tao3k.git-workbench",
		"Branch rebased cleanly",
		"Activity · 3",
	} {
		if !tt.HasText(want) {
			t.Fatalf("expected WorkbenchApp frame to show %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}

	// 2. Click inline CTA action on floating toast -> fires ActionHandler and dismisses toast!
	if err := tt.Click("Retry Now"); err != nil {
		t.Fatalf("click Retry Now failed: %v", err)
	}
	tt.Frame()
	if clickedAction != "retry-grpc" {
		t.Fatalf("expected ActionHandler to receive 'retry-grpc', got %q", clickedAction)
	}

	// 3. Open Tier-0 Activity Center modal from status bar pill -> test Plugins filter & DND toggle!
	if err := tt.Click("Activity · 2"); err != nil {
		t.Fatalf("click Activity pill failed: %v", err)
	}
	tt.Frame()
	if !app.IsActivityCenterOpen() || !tt.HasText("TIER-0 ACTIVITY") || !tt.HasText("Notifications & System Activity") {
		t.Fatalf("expected Tier-0 Activity Center modal, got %q", strings.Join(tt.Texts(), " | "))
	}

	if err := tt.Click("DND: Off"); err != nil {
		t.Fatalf("click DND toggle failed: %v", err)
	}
	tt.Frame()
	if !center.DoNotDisturb() || !tt.HasText("DND: On") {
		t.Fatalf("expected Do Not Disturb enabled, got %q", strings.Join(tt.Texts(), " | "))
	}

	if err := tt.Click("Mark All Read"); err != nil {
		t.Fatalf("click Mark All Read failed: %v", err)
	}
	tt.Frame()
	if center.Snapshot().UnreadCount != 0 {
		t.Fatalf("expected 0 unread after Mark All Read, got %d", center.Snapshot().UnreadCount)
	}
}
