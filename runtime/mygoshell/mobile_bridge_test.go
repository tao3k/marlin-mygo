// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/runtime/device"
)

type mockMobileBridgeTransport struct {
	sentNotifications []device.NotificationRequest
	drained           bool
}

func (t *mockMobileBridgeTransport) Call(_ context.Context, method string, argsJSON []byte) ([]byte, error) {
	switch method {
	case "permissionState":
		return []byte(`{"state":"granted"}`), nil
	case "photos.recentScreenshots":
		return []byte(`{"items":[]}`), nil
	case "vision.extractOCR":
		return []byte(`{"text":"OCR ok"}`), nil
	case "voice.latestTranscript":
		return []byte(`{"transcriptId":"vt-live-1","locale":"en-US","text":"Review PR #1 tonight","durationMs":1800}`), nil
	case "contacts.search":
		return []byte(`{"contacts":[]}`), nil
	case "capture.drain":
		if t.drained {
			return []byte(`{"captures":[]}`), nil
		}
		t.drained = true
		return []byte(`{"captures":[{"sourceChannel":"ios.share-extension","text":"Clipped design spec"}]}`), nil
	case "notify.send":
		var req device.NotificationRequest
		_ = json.Unmarshal(argsJSON, &req)
		t.sentNotifications = append(t.sentNotifications, req)
		return []byte(`{"ok":true}`), nil
	default:
		return []byte(`{"ok":true}`), nil
	}
}

func TestMobileCompanionLiveDeviceBridgeWiringEndToEnd(t *testing.T) {
	ctx := context.Background()
	for _, plat := range []MobilePlatform{MobilePlatformIOS, MobilePlatformAndroid} {
		tr := &mockMobileBridgeTransport{}
		mob := NewMobileCompanionAppForPlatform(plat, "", "")
		if _, err := mob.WireNativeTransport(ctx, tr); err != nil {
			t.Fatalf("WireNativeTransport(%s): %v", plat, err)
		}
		toolName := string(device.CapIOSNotifySend)
		if plat == MobilePlatformAndroid {
			toolName = string(device.CapAndroidNotifySend)
		}
		mob.SetPendingGates([]PendingGateView{
			{GateID: "g-live-1", ToolName: toolName, Prompt: "Send summary notification?"},
		})

		tt := ui.NewTester(mob.View, 393, 852)

		// 1. Packs tab shows live introspected capability descriptors ("6 LIVE").
		if err := tt.Click("Packs"); err != nil {
			t.Fatalf("click Packs (%s): %v", plat, err)
		}
		if !tt.HasText("6 LIVE") || !tt.HasText(toolName) {
			t.Fatalf("expected live bridge descriptors on Packs tab (%s), got %q", plat, tt.Texts())
		}

		// 2. Capture tab drains real native queue and pulls real voice transcript.
		if err := tt.Click("Capture"); err != nil {
			t.Fatalf("click Capture (%s): %v", plat, err)
		}
		if err := tt.Click("Drain Native Queue"); err != nil {
			t.Fatalf("click Drain Native Queue (%s): %v", plat, err)
		}
		if !tt.HasText("Drained 1 native capture(s)") {
			t.Fatalf("expected drain status (%s), got %q", plat, tt.Texts())
		}
		if err := tt.Click("Pull Voice"); err != nil {
			t.Fatalf("click Pull Voice (%s): %v", plat, err)
		}
		if !tt.HasText("Voice transcript (en-US · 1800ms)") {
			t.Fatalf("expected voice transcript status (%s), got %q", plat, tt.Texts())
		}

		// 3. Gates tab approval dispatches gated notification with one-shot GateReceiptID.
		if err := tt.Click("Gates (1)"); err != nil {
			t.Fatalf("click Gates (%s): %v", plat, err)
		}
		if err := tt.Click("Approve Once"); err != nil {
			t.Fatalf("click Approve Once (%s): %v", plat, err)
		}
		if len(tr.sentNotifications) != 1 || tr.sentNotifications[0].Body != "Send summary notification?" {
			t.Fatalf("expected live bridge notification delivered (%s), got %+v", plat, tr.sentNotifications)
		}
	}
}

type mockMobileMediaBridgeTransport struct {
	mockMobileBridgeTransport
}

func (t *mockMobileMediaBridgeTransport) Call(ctx context.Context, method string, argsJSON []byte) ([]byte, error) {
	if method == "photos.recentScreenshots" {
		return []byte(`{"items":[{"localId":"shot-42","width":1179,"height":2556,"isScreenshot":true,"mimeType":"image/png"}]}`), nil
	}
	return t.mockMobileBridgeTransport.Call(ctx, method, argsJSON)
}

func TestMobileCompanionBridgeChatVoicePhotoAndModelSheetEndToEnd(t *testing.T) {
	ctx := context.Background()
	for _, plat := range []MobilePlatform{MobilePlatformIOS, MobilePlatformAndroid} {
		tr := &mockMobileMediaBridgeTransport{}
		mob := NewMobileCompanionAppForPlatform(plat, "", "")
		if _, err := mob.WireNativeTransport(ctx, tr); err != nil {
			t.Fatalf("WireNativeTransport(%s): %v", plat, err)
		}

		tt := ui.NewTester(mob.View, 393, 852)
		tt.Frame()

		// 1. Tap Photo in mobile Chat -> invokes OS photos.recentScreenshots bridge & stages @photo mention!
		if err := tt.Click("Photo"); err != nil {
			t.Fatalf("click Photo (%s): %v", plat, err)
		}
		tt.Frame()
		mentions := mob.Transcript.Composer.Snapshot().Mentions
		if len(mentions) != 1 || mentions[0].Kind != "photo" || mentions[0].Target != "shot-42" {
			t.Fatalf("expected @photo:shot-42 mention from native bridge (%s), got %+v", plat, mentions)
		}

		// 2. Tap Voice -> Attach Voice in mobile Chat -> invokes OS voice.latestTranscript bridge & populates chat draft!
		if err := tt.Click("Voice"); err != nil {
			t.Fatalf("click Voice (%s): %v", plat, err)
		}
		tt.Frame()
		if err := tt.Click("Attach Voice"); err != nil {
			t.Fatalf("click Attach Voice (%s): %v", plat, err)
		}
		tt.Frame()
		mob.mu.Lock()
		draft := mob.chatDraft
		status := mob.LastStatusLine
		mob.mu.Unlock()
		if draft != "Review PR #1 tonight" || !tt.HasText("Voice transcript (en-US · 1800ms)") {
			t.Fatalf("expected native voice transcript in chat draft (%s), got draft=%q status=%q", plat, draft, status)
		}

		// 3. Open Model & Reasoning sheet -> switch to Marlin Pro -> updates context window to 1M and status line!
		if err := tt.Click("Marlin Core"); err != nil {
			t.Fatalf("click Marlin Core (%s): %v", plat, err)
		}
		tt.Frame()
		if err := tt.Click("Marlin Pro"); err != nil {
			t.Fatalf("click Marlin Pro (%s): %v", plat, err)
		}
		tt.Frame()
		if gotMax := mob.Transcript.ContextUsage().MaxTokens; gotMax != 1_000_000 {
			t.Fatalf("expected context window updated to 1M on mobile model switch (%s), got %d", plat, gotMax)
		}
	}
}
