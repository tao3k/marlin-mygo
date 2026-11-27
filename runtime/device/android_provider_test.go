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

type fakeContacts struct {
	perm device.OsPermissionState
}

func (f *fakeContacts) PermissionState(context.Context) (device.OsPermissionState, error) {
	return f.perm, nil
}

func (f *fakeContacts) SearchContacts(_ context.Context, query string, limit int) ([]device.ContactSummary, error) {
	return []device.ContactSummary{{ContactID: "c-1", DisplayName: "Ada Lovelace", Handle: "ada@tao3k.org"}}, nil
}

type fakeClipboard struct{}

func (fakeClipboard) PermissionState(context.Context) (device.OsPermissionState, error) {
	return device.PermissionGranted, nil
}
func (fakeClipboard) ReadText(context.Context) (string, error) { return "clipboard-text", nil }

func TestAndroidPersonalProviderAndGatedNotifications(t *testing.T) {
	ctx := context.Background()
	notify := &fakeNotify{perm: device.PermissionGranted}
	photos := &fakeIOSPhotos{perm: device.PermissionGranted}
	capture := &fakeIOSCapture{items: []device.CapturePayload{
		{SourceChannel: "android.share-intent", Text: "Saved clip"},
	}}
	contacts := &fakeContacts{perm: device.PermissionGranted}

	bridge := device.NewBridge(schema.TargetMobileAndroid)
	if err := device.RegisterPlatformProtocols(ctx, bridge, device.ProtocolBundle{
		Photos:   photos,
		Voice:    fakeIOSVoice{},
		Capture:  capture,
		Contacts: contacts,
		Notify:   notify,
	}); err != nil {
		t.Fatalf("RegisterPlatformProtocols(android) failed: %v", err)
	}

	descs, err := bridge.SnapshotDescriptors(ctx)
	if err != nil || len(descs) != 6 {
		t.Fatalf("expected 6 android descriptors, got %d err=%v", len(descs), err)
	}

	// Read-only contacts search succeeds without GateReceipt.
	res, err := bridge.Dispatch(ctx, device.InvocationRequest{
		InvocationID: "and-contacts-1",
		CapabilityID: device.CapAndroidContactsSearch,
		Args:         json.RawMessage(`{"query":"Ada","limit":5}`),
	})
	if err != nil || !res.OK {
		t.Fatalf("expected contacts search success, got res=%+v err=%v", res, err)
	}

	// Mutating notification enforces one-shot GateReceipt.
	if _, err := bridge.Dispatch(ctx, device.InvocationRequest{
		InvocationID: "and-notify-missing",
		CapabilityID: device.CapAndroidNotifySend,
		Args:         json.RawMessage(`{"title":"Marlin","body":"Blocked"}`),
	}); !errors.Is(err, device.ErrGateReceiptMissing) {
		t.Fatalf("expected ErrGateReceiptMissing, got %v", err)
	}

	if res, err := bridge.Dispatch(ctx, device.InvocationRequest{
		InvocationID:  "and-notify-ok",
		CapabilityID:  device.CapAndroidNotifySend,
		GateReceiptID: "rcpt-and-1",
		Args:          json.RawMessage(`{"title":"Marlin","body":"Approved"}`),
	}); err != nil || !res.OK {
		t.Fatalf("expected gated notify success, got res=%+v err=%v", res, err)
	}

	if _, err := bridge.Dispatch(ctx, device.InvocationRequest{
		InvocationID:  "and-notify-replay",
		CapabilityID:  device.CapAndroidNotifySend,
		GateReceiptID: "rcpt-and-1",
		Args:          json.RawMessage(`{"title":"Marlin","body":"Replay"}`),
	}); !errors.Is(err, device.ErrGateReceiptReplayed) {
		t.Fatalf("expected ErrGateReceiptReplayed, got %v", err)
	}

	// Revoking OS permission immediately denies read-only Android OCR.
	photos.perm = device.PermissionDenied
	if _, err := bridge.Dispatch(ctx, device.InvocationRequest{
		InvocationID: "and-ocr-denied",
		CapabilityID: device.CapAndroidOCR,
		Args:         json.RawMessage(`{"localId":"ph-1"}`),
	}); !errors.Is(err, device.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got %v", err)
	}
}

func TestRegisterPlatformProtocolsRoutesAllTargets(t *testing.T) {
	ctx := context.Background()
	bundle := device.ProtocolBundle{
		Photos:    &fakeIOSPhotos{perm: device.PermissionGranted},
		Voice:     fakeIOSVoice{},
		Capture:   &fakeIOSCapture{},
		Contacts:  &fakeContacts{perm: device.PermissionGranted},
		Notify:    &fakeNotify{perm: device.PermissionGranted},
		Clipboard: fakeClipboard{},
	}

	for _, tc := range []struct {
		kind      schema.TargetKind
		wantCount int
		probeCap  schema.CapabilityID
	}{
		{schema.TargetMobileIOS, 6, device.CapIOSContactsSearch},
		{schema.TargetMobileAndroid, 6, device.CapAndroidContactsSearch},
		{schema.TargetDesktopMacOS, 2, device.CapDesktopClipboardRead},
	} {
		br := device.NewBridge(tc.kind)
		if err := device.RegisterPlatformProtocols(ctx, br, bundle); err != nil {
			t.Fatalf("RegisterPlatformProtocols(%s): %v", tc.kind, err)
		}
		descs, err := br.SnapshotDescriptors(ctx)
		if err != nil || len(descs) != tc.wantCount {
			t.Fatalf("target %s: expected %d descriptors, got %d err=%v", tc.kind, tc.wantCount, len(descs), err)
		}
		res, err := br.Dispatch(ctx, device.InvocationRequest{
			InvocationID: "probe-" + string(tc.kind),
			CapabilityID: tc.probeCap,
		})
		if err != nil || !res.OK {
			t.Fatalf("target %s probe %s failed: res=%+v err=%v", tc.kind, tc.probeCap, res, err)
		}
	}
}
