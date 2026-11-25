// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package nativebridge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/device"
)

type stubNativeTransport struct {
	sentNotifications []device.NotificationRequest
}

func (s *stubNativeTransport) Call(_ context.Context, method string, argsJSON []byte) ([]byte, error) {
	switch method {
	case "permissionState":
		return []byte(`{"state":"granted"}`), nil
	case "photos.recentScreenshots":
		return []byte(`{"items":[{"localId":"ph-1","isScreenshot":true,"width":1170,"height":2532,"mimeType":"image/png"}]}`), nil
	case "vision.extractOCR":
		return []byte(`{"text":"Meeting at 14:00"}`), nil
	case "notify.send":
		var req device.NotificationRequest
		if err := json.Unmarshal(argsJSON, &req); err != nil {
			return nil, err
		}
		s.sentNotifications = append(s.sentNotifications, req)
		return []byte(`{"ok":true}`), nil
	case "capture.drain":
		return []byte(`{"captures":[{"sourceChannel":"ios.share-extension","text":"Draft Q4 roadmap"}]}`), nil
	default:
		return nil, ErrNativeBridgeUnavailable
	}
}

func TestNativeBridgeAdapterIntegratesWithDeviceBridgeAndGateReceipts(t *testing.T) {
	ctx := context.Background()
	tr := &stubNativeTransport{}
	adapter := &Adapter{Transport: tr, Platform: "ios"}

	br := device.NewBridge(schema.TargetMobileIOS)
	if err := br.RegisterProvider(ctx, &device.IOSPersonalProvider{
		Photos:  adapter,
		Notify:  adapter,
		Capture: adapter,
	}); err != nil {
		t.Fatal(err)
	}

	// Read-only OCR succeeds without gate receipt.
	res, err := br.Dispatch(ctx, device.InvocationRequest{
		InvocationID: "inv-ocr",
		CapabilityID: device.CapIOSOCR,
		Args:         json.RawMessage(`{"localId":"ph-1"}`),
	})
	if err != nil || !res.OK {
		t.Fatalf("expected OCR success, got res=%+v err=%v", res, err)
	}

	// Mutating notification MUST require one-shot GateReceipt.
	if _, err := br.Dispatch(ctx, device.InvocationRequest{
		InvocationID: "inv-notify-denied",
		CapabilityID: device.CapIOSNotifySend,
		Args:         json.RawMessage(`{"title":"Marlin","body":"Test"}`),
	}); !errors.Is(err, device.ErrGateReceiptMissing) {
		t.Fatalf("expected ErrGateReceiptMissing, got %v", err)
	}

	if res, err := br.Dispatch(ctx, device.InvocationRequest{
		InvocationID:  "inv-notify-ok",
		CapabilityID:  device.CapIOSNotifySend,
		GateReceiptID: "rcpt-ios-1",
		Args:          json.RawMessage(`{"title":"Marlin","body":"Approved"}`),
	}); err != nil || !res.OK {
		t.Fatalf("expected gated notify success, got res=%+v err=%v", res, err)
	}
	if len(tr.sentNotifications) != 1 || tr.sentNotifications[0].Body != "Approved" {
		t.Fatalf("expected native transport to receive notification, got %+v", tr.sentNotifications)
	}
}
