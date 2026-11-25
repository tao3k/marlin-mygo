// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package device

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

type fakeProvider struct {
	descs []CapabilityDescriptor
}

func (p *fakeProvider) Family() string { return "ios" }
func (p *fakeProvider) Descriptors(context.Context) ([]CapabilityDescriptor, error) {
	return p.descs, nil
}
func (p *fakeProvider) Invoke(_ context.Context, req InvocationRequest) (InvocationResult, error) {
	return InvocationResult{InvocationID: req.InvocationID, OK: true, Result: json.RawMessage(`{"ok":true}`)}, nil
}

func TestDeviceBridgeEnforcesPermissionsAndGateReceipt(t *testing.T) {
	ctx := context.Background()
	br := NewBridge(schema.TargetMobileIOS)
	err := br.RegisterProvider(ctx, &fakeProvider{
		descs: []CapabilityDescriptor{
			{CapabilityID: "ios.photos.recent", ProtocolFamily: "photos", Permission: PermissionGranted, ReadOnly: true},
			{CapabilityID: "ios.calendar.create", ProtocolFamily: "pim", Permission: PermissionGranted, ReadOnly: false},
			{CapabilityID: "ios.contacts.search", ProtocolFamily: "pim", Permission: PermissionDenied, ReadOnly: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if res, err := br.Dispatch(ctx, InvocationRequest{InvocationID: "1", CapabilityID: "ios.photos.recent"}); err != nil || !res.OK {
		t.Fatalf("expected read-only granted capability to succeed, got res=%+v err=%v", res, err)
	}
	if _, err := br.Dispatch(ctx, InvocationRequest{InvocationID: "2", CapabilityID: "ios.contacts.search"}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got %v", err)
	}
	if _, err := br.Dispatch(ctx, InvocationRequest{InvocationID: "3", CapabilityID: "ios.calendar.create"}); !errors.Is(err, ErrGateReceiptMissing) {
		t.Fatalf("expected ErrGateReceiptMissing on mutating call without receipt, got %v", err)
	}
	if res, err := br.Dispatch(ctx, InvocationRequest{InvocationID: "4", CapabilityID: "ios.calendar.create", GateReceiptID: "rcpt-gate-9"}); err != nil || !res.OK {
		t.Fatalf("expected mutating call with receipt to succeed, got res=%+v err=%v", res, err)
	}
	if _, err := br.Dispatch(ctx, InvocationRequest{InvocationID: "5", CapabilityID: "ios.calendar.create", GateReceiptID: "rcpt-gate-9"}); !errors.Is(err, ErrGateReceiptReplayed) {
		t.Fatalf("expected ErrGateReceiptReplayed on second use of one-shot receipt, got %v", err)
	}
}

func TestDeviceBridgeConsumedReceiptWindowStaysBounded(t *testing.T) {
	br := NewBridge(schema.TargetDesktopMacOS)
	prov := &fakeProvider{descs: []CapabilityDescriptor{{
		CapabilityID: "ios.photos.export", ProtocolFamily: "platform.ios",
		Permission: PermissionGranted, ReadOnly: false,
	}}}
	if err := br.RegisterProvider(context.Background(), prov); err != nil {
		t.Fatal(err)
	}

	const total = maxTrackedReceipts + 256
	for i := 0; i < total; i++ {
		if _, err := br.Dispatch(context.Background(), InvocationRequest{
			InvocationID:  fmt.Sprintf("inv-%d", i),
			CapabilityID:  "ios.photos.export",
			GateReceiptID: fmt.Sprintf("rcpt-%d", i),
		}); err != nil {
			t.Fatalf("dispatch %d failed: %v", i, err)
		}
	}
	if got := len(br.consumedReceipts); got != maxTrackedReceipts {
		t.Fatalf("expected receipt window bounded at %d, got %d", maxTrackedReceipts, got)
	}
	if got := len(br.receiptOrder); got != maxTrackedReceipts {
		t.Fatalf("expected FIFO window bounded at %d, got %d", maxTrackedReceipts, got)
	}

	// A receipt inside the window must still be rejected as replayed.
	if _, err := br.Dispatch(context.Background(), InvocationRequest{
		InvocationID: "replay", CapabilityID: "ios.photos.export",
		GateReceiptID: fmt.Sprintf("rcpt-%d", total-1),
	}); !errors.Is(err, ErrGateReceiptReplayed) {
		t.Fatalf("expected ErrGateReceiptReplayed inside window, got %v", err)
	}
}
