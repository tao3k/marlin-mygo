// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package device

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// IOSPersonalProvider exposes platform.ios capabilities (Photos/OCR, Voice,
// Share Extension capture queue, Contacts, and GateReceipt-protected Notifications)
// under the uniform device.ios.* namespace.
type IOSPersonalProvider struct {
	Photos   PhotosProtocol
	Voice    VoiceCaptureProtocol
	Capture  CaptureProtocol
	Contacts ContactsProtocol
	Notify   NotificationProtocol
}

func (p *IOSPersonalProvider) Family() string { return "platform.ios" }

func (p *IOSPersonalProvider) Descriptors(ctx context.Context) ([]CapabilityDescriptor, error) {
	var out []CapabilityDescriptor
	if p.Photos != nil {
		perm, err := p.Photos.PermissionState(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out,
			CapabilityDescriptor{
				CapabilityID:   schema.CapabilityID(CapIOSPhotosRecent),
				ProtocolFamily: "platform.ios.photos",
				Permission:     perm,
				InputSchemaID:  "marlin.device.photos.recent.req.v1",
				OutputSchemaID: "marlin.device.photos.recent.resp.v1",
				ReadOnly:       true,
			},
			CapabilityDescriptor{
				CapabilityID:   schema.CapabilityID(CapIOSOCR),
				ProtocolFamily: "platform.ios.vision",
				Permission:     perm,
				InputSchemaID:  "marlin.device.ocr.req.v1",
				OutputSchemaID: "marlin.device.ocr.resp.v1",
				ReadOnly:       true,
			},
		)
	}
	if p.Voice != nil {
		perm, err := p.Voice.PermissionState(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, CapabilityDescriptor{
			CapabilityID:   schema.CapabilityID(CapIOSVoiceTranscribe),
			ProtocolFamily: "platform.ios.speech",
			Permission:     perm,
			InputSchemaID:  "marlin.device.voice.req.v1",
			OutputSchemaID: "marlin.device.voice.resp.v1",
			ReadOnly:       true,
		})
	}
	if p.Capture != nil {
		out = append(out, CapabilityDescriptor{
			CapabilityID:   schema.CapabilityID(CapIOSShareDrain),
			ProtocolFamily: "platform.ios.share",
			Permission:     PermissionGranted,
			InputSchemaID:  "marlin.device.capture.drain.req.v1",
			OutputSchemaID: "marlin.device.capture.drain.resp.v1",
			ReadOnly:       true,
		})
	}
	if p.Contacts != nil {
		perm, err := p.Contacts.PermissionState(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, CapabilityDescriptor{
			CapabilityID:   schema.CapabilityID(CapIOSContactsSearch),
			ProtocolFamily: "platform.ios.contacts",
			Permission:     perm,
			InputSchemaID:  "marlin.device.contacts.search.req.v1",
			OutputSchemaID: "marlin.device.contacts.search.resp.v1",
			ReadOnly:       true,
		})
	}
	if p.Notify != nil {
		perm, err := p.Notify.PermissionState(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, CapabilityDescriptor{
			CapabilityID:   schema.CapabilityID(CapIOSNotifySend),
			ProtocolFamily: "platform.ios.notifications",
			Permission:     perm,
			InputSchemaID:  "marlin.device.notify.req.v1",
			OutputSchemaID: "marlin.device.notify.resp.v1",
			ReadOnly:       false, // mutating -> Bridge enforces one-shot Rust GateReceiptID
		})
	}
	return out, nil
}

func (p *IOSPersonalProvider) Invoke(ctx context.Context, req InvocationRequest) (InvocationResult, error) {
	switch req.CapabilityID {
	case CapIOSPhotosRecent:
		if p.Photos == nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: ErrCapabilityUnknown.Error()}, ErrCapabilityUnknown
		}
		var args struct {
			Limit int `json:"limit"`
		}
		if len(req.Args) > 0 {
			_ = json.Unmarshal(req.Args, &args)
		}
		if args.Limit <= 0 || args.Limit > 50 {
			args.Limit = 10
		}
		items, err := p.Photos.RecentScreenshots(ctx, args.Limit)
		if err != nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: err.Error()}, err
		}
		raw, _ := json.Marshal(items)
		return InvocationResult{InvocationID: req.InvocationID, OK: true, Result: raw}, nil

	case CapIOSOCR:
		if p.Photos == nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: ErrCapabilityUnknown.Error()}, ErrCapabilityUnknown
		}
		var args struct {
			LocalID string `json:"localId"`
		}
		if err := json.Unmarshal(req.Args, &args); err != nil || args.LocalID == "" {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: "localId is required"}, fmt.Errorf("device: ocr requires localId")
		}
		text, err := p.Photos.ExtractTextOCR(ctx, args.LocalID)
		if err != nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: err.Error()}, err
		}
		raw, _ := json.Marshal(map[string]string{"text": text})
		return InvocationResult{InvocationID: req.InvocationID, OK: true, Result: raw}, nil

	case CapIOSVoiceTranscribe:
		if p.Voice == nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: ErrCapabilityUnknown.Error()}, ErrCapabilityUnknown
		}
		tr, err := p.Voice.LatestTranscript(ctx)
		if err != nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: err.Error()}, err
		}
		raw, _ := json.Marshal(tr)
		return InvocationResult{InvocationID: req.InvocationID, OK: true, Result: raw}, nil

	case CapIOSShareDrain:
		if p.Capture == nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: ErrCapabilityUnknown.Error()}, ErrCapabilityUnknown
		}
		items, err := p.Capture.DrainPendingCaptures(ctx)
		if err != nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: err.Error()}, err
		}
		raw, _ := json.Marshal(items)
		return InvocationResult{InvocationID: req.InvocationID, OK: true, Result: raw}, nil

	case CapIOSContactsSearch:
		if p.Contacts == nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: ErrCapabilityUnknown.Error()}, ErrCapabilityUnknown
		}
		var args struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		_ = json.Unmarshal(req.Args, &args)
		if args.Limit <= 0 || args.Limit > 25 {
			args.Limit = 10
		}
		items, err := p.Contacts.SearchContacts(ctx, args.Query, args.Limit)
		if err != nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: err.Error()}, err
		}
		raw, _ := json.Marshal(items)
		return InvocationResult{InvocationID: req.InvocationID, OK: true, Result: raw}, nil

	case CapIOSNotifySend:
		if p.Notify == nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: ErrCapabilityUnknown.Error()}, ErrCapabilityUnknown
		}
		var args NotificationRequest
		if err := json.Unmarshal(req.Args, &args); err != nil || args.Title == "" {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: "notification title required"}, fmt.Errorf("device: notification title required")
		}
		if err := p.Notify.SendNotification(ctx, args); err != nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: err.Error()}, err
		}
		return InvocationResult{InvocationID: req.InvocationID, OK: true, Result: json.RawMessage(`{"sent":true}`)}, nil

	default:
		return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: ErrCapabilityUnknown.Error()}, ErrCapabilityUnknown
	}
}

// DesktopProvider exposes platform.desktop capabilities under device.desktop.*.
type DesktopProvider struct {
	Clipboard ClipboardProtocol
	Notify    NotificationProtocol
}

func (p *DesktopProvider) Family() string { return "platform.desktop" }

func (p *DesktopProvider) Descriptors(ctx context.Context) ([]CapabilityDescriptor, error) {
	var out []CapabilityDescriptor
	if p.Clipboard != nil {
		perm, err := p.Clipboard.PermissionState(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, CapabilityDescriptor{
			CapabilityID:   schema.CapabilityID(CapDesktopClipboardRead),
			ProtocolFamily: "platform.desktop.clipboard",
			Permission:     perm,
			InputSchemaID:  "marlin.device.clipboard.req.v1",
			OutputSchemaID: "marlin.device.clipboard.resp.v1",
			ReadOnly:       true,
		})
	}
	if p.Notify != nil {
		perm, err := p.Notify.PermissionState(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, CapabilityDescriptor{
			CapabilityID:   schema.CapabilityID(CapDesktopNotifySend),
			ProtocolFamily: "platform.desktop.notifications",
			Permission:     perm,
			InputSchemaID:  "marlin.device.notify.req.v1",
			OutputSchemaID: "marlin.device.notify.resp.v1",
			ReadOnly:       false,
		})
	}
	return out, nil
}

func (p *DesktopProvider) Invoke(ctx context.Context, req InvocationRequest) (InvocationResult, error) {
	switch req.CapabilityID {
	case CapDesktopClipboardRead:
		if p.Clipboard == nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: ErrCapabilityUnknown.Error()}, ErrCapabilityUnknown
		}
		text, err := p.Clipboard.ReadText(ctx)
		if err != nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: err.Error()}, err
		}
		raw, _ := json.Marshal(map[string]string{"text": text})
		return InvocationResult{InvocationID: req.InvocationID, OK: true, Result: raw}, nil

	case CapDesktopNotifySend:
		if p.Notify == nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: ErrCapabilityUnknown.Error()}, ErrCapabilityUnknown
		}
		var args NotificationRequest
		if err := json.Unmarshal(req.Args, &args); err != nil || args.Title == "" {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: "notification title required"}, fmt.Errorf("device: notification title required")
		}
		if err := p.Notify.SendNotification(ctx, args); err != nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: err.Error()}, err
		}
		return InvocationResult{InvocationID: req.InvocationID, OK: true, Result: json.RawMessage(`{"sent":true}`)}, nil

	default:
		return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: ErrCapabilityUnknown.Error()}, ErrCapabilityUnknown
	}
}

// SyncCaptures drains pending proactive captures (iOS Share Sheet / Back-Tap /
// Voice Memo / Desktop) and delivers each payload idempotently to sink.
func (b *Bridge) SyncCaptures(
	ctx context.Context,
	deviceID string,
	sessionID string,
	proto CaptureProtocol,
	sink CaptureSink,
) (int, error) {
	if proto == nil || sink == nil {
		return 0, fmt.Errorf("device: capture protocol and sink are required")
	}
	items, err := proto.DrainPendingCaptures(ctx)
	if err != nil {
		return 0, err
	}
	delivered := 0
	for _, item := range items {
		key := deterministicCaptureKey(deviceID, sessionID, item)
		if err := sink.IngestCapture(ctx, deviceID, sessionID, item, key); err != nil {
			return delivered, err
		}
		delivered++
	}
	return delivered, nil
}

func deterministicCaptureKey(deviceID, sessionID string, payload CapturePayload) string {
	h := sha256.New()
	_, _ = h.Write([]byte(deviceID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(sessionID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(payload.SourceChannel))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(payload.Text))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(payload.OCRText))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(payload.ImageBytes)
	sum := h.Sum(nil)
	return "cap-" + hex.EncodeToString(sum[:8])
}
