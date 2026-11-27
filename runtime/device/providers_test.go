// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package device_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/device"
)

type fakeIOSPhotos struct {
	perm device.OsPermissionState
}

func (f *fakeIOSPhotos) PermissionState(context.Context) (device.OsPermissionState, error) {
	return f.perm, nil
}
func (f *fakeIOSPhotos) RecentScreenshots(_ context.Context, limit int) ([]device.PhotoItem, error) {
	return []device.PhotoItem{{LocalID: "ph-1", Width: 1179, Height: 2556, IsScreenshot: true, MIMEType: "image/png"}}, nil
}
func (f *fakeIOSPhotos) ExtractTextOCR(_ context.Context, localID string) (string, error) {
	return "OCR(" + localID + "): Flight CA985 Gate B14", nil
}

type fakeIOSVoice struct{}

func (fakeIOSVoice) PermissionState(context.Context) (device.OsPermissionState, error) {
	return device.PermissionGranted, nil
}
func (fakeIOSVoice) LatestTranscript(context.Context) (device.VoiceTranscript, error) {
	return device.VoiceTranscript{TranscriptID: "vt-1", Locale: "zh-Hans", Text: "Add release checklist to Org inbox", DurationMS: 2400}, nil
}

type fakeIOSCapture struct{ items []device.CapturePayload }

func (f *fakeIOSCapture) DrainPendingCaptures(context.Context) ([]device.CapturePayload, error) {
	out := f.items
	f.items = nil
	return out, nil
}

type fakeNotify struct {
	perm device.OsPermissionState
	sent []device.NotificationRequest
}

func (f *fakeNotify) PermissionState(context.Context) (device.OsPermissionState, error) {
	return f.perm, nil
}
func (f *fakeNotify) SendNotification(_ context.Context, req device.NotificationRequest) error {
	f.sent = append(f.sent, req)
	return nil
}

type recordingSink struct {
	keys []string
}

func (s *recordingSink) IngestCapture(_ context.Context, _, _ string, _ device.CapturePayload, key string) error {
	s.keys = append(s.keys, key)
	return nil
}

func TestIOSPersonalProviderAndGatedNotifications(t *testing.T) {
	ctx := context.Background()
	notify := &fakeNotify{perm: device.PermissionGranted}
	photos := &fakeIOSPhotos{perm: device.PermissionLimited}
	capture := &fakeIOSCapture{items: []device.CapturePayload{
		{SourceChannel: "device.ios.share.drain", Text: "Saved paper link", OCRText: "ArXiv 2604.12345"},
	}}

	provider := &device.IOSPersonalProvider{
		Photos:  photos,
		Voice:   fakeIOSVoice{},
		Capture: capture,
		Notify:  notify,
	}

	bridge := device.NewBridge(schema.TargetMobileIOS)
	if err := bridge.RegisterProvider(ctx, provider); err != nil {
		t.Fatalf("RegisterProvider failed: %v", err)
	}

	// 1. Read-only OCR works under PermissionLimited without GateReceipt.
	ocrRes, err := bridge.Dispatch(ctx, device.InvocationRequest{
		InvocationID: "inv-ocr-1",
		SessionID:    "sess-ios",
		CapabilityID: device.CapIOSOCR,
		Args:         json.RawMessage(`{"localId":"ph-1"}`),
	})
	if err != nil || !ocrRes.OK {
		t.Fatalf("expected OCR success, got res=%+v err=%v", ocrRes, err)
	}

	// 2. Read-only Voice capture transcription works cleanly.
	voiceRes, err := bridge.Dispatch(ctx, device.InvocationRequest{
		InvocationID: "inv-voice-1",
		SessionID:    "sess-ios",
		CapabilityID: device.CapIOSVoiceTranscribe,
	})
	if err != nil || !voiceRes.OK {
		t.Fatalf("expected voice transcribe success, got res=%+v err=%v", voiceRes, err)
	}

	// 3. Mutating device.ios.notify.send MUST fail without Rust GateReceipt.
	if _, err := bridge.Dispatch(ctx, device.InvocationRequest{
		InvocationID: "inv-notify-no-rcpt",
		SessionID:    "sess-ios",
		CapabilityID: device.CapIOSNotifySend,
		Args:         json.RawMessage(`{"title":"Gate Ready","body":"Approve deploy"}`),
	}); !errors.Is(err, device.ErrGateReceiptMissing) {
		t.Fatalf("expected ErrGateReceiptMissing, got %v", err)
	}

	// 4. With valid one-shot GateReceipt -> succeeds once, replay MUST fail!
	if _, err := bridge.Dispatch(ctx, device.InvocationRequest{
		InvocationID:  "inv-notify-ok",
		SessionID:     "sess-ios",
		CapabilityID:  device.CapIOSNotifySend,
		GateReceiptID: "rcpt-gate-ios-42",
		Args:          json.RawMessage(`{"title":"Gate Ready","body":"Approve deploy"}`),
	}); err != nil {
		t.Fatalf("expected gated notify to succeed, got %v", err)
	}
	if _, err := bridge.Dispatch(ctx, device.InvocationRequest{
		InvocationID:  "inv-notify-replay",
		SessionID:     "sess-ios",
		CapabilityID:  device.CapIOSNotifySend,
		GateReceiptID: "rcpt-gate-ios-42",
		Args:          json.RawMessage(`{"title":"Replay","body":"Must fail"}`),
	}); !errors.Is(err, device.ErrGateReceiptReplayed) {
		t.Fatalf("expected ErrGateReceiptReplayed, got %v", err)
	}
	if len(notify.sent) != 1 {
		t.Fatalf("expected exactly 1 notification sent, got %d", len(notify.sent))
	}

	// 5. OS permission revoked dynamically -> read-only photos immediately denied.
	photos.perm = device.PermissionDenied
	if _, err := bridge.Dispatch(ctx, device.InvocationRequest{
		InvocationID: "inv-ocr-denied",
		SessionID:    "sess-ios",
		CapabilityID: device.CapIOSOCR,
		Args:         json.RawMessage(`{"localId":"ph-1"}`),
	}); !errors.Is(err, device.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied after OS revoke, got %v", err)
	}

	// 6. SyncCaptures drains Share Extension queue with deterministic idempotency keys.
	sink := &recordingSink{}
	n, err := bridge.SyncCaptures(ctx, "iphone-16", "sess-ios", capture, sink)
	if err != nil || n != 1 || len(sink.keys) != 1 {
		t.Fatalf("expected 1 synced capture, got n=%d keys=%v err=%v", n, sink.keys, err)
	}
}
