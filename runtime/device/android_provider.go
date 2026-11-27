// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package device

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// AndroidPersonalProvider exposes platform.android capabilities (MediaStore
// screenshots/OCR, Voice, Share Intent capture queue, Contacts, and
// GateReceipt-protected Notifications) under the uniform device.android.* namespace.
type AndroidPersonalProvider struct {
	Photos   PhotosProtocol
	Voice    VoiceCaptureProtocol
	Capture  CaptureProtocol
	Contacts ContactsProtocol
	Notify   NotificationProtocol
}

func (p *AndroidPersonalProvider) Family() string { return "platform.android" }

func (p *AndroidPersonalProvider) Descriptors(ctx context.Context) ([]CapabilityDescriptor, error) {
	var out []CapabilityDescriptor
	if p.Photos != nil {
		perm, err := p.Photos.PermissionState(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out,
			CapabilityDescriptor{
				CapabilityID:   schema.CapabilityID(CapAndroidPhotosRecent),
				ProtocolFamily: "platform.android.photos",
				Permission:     perm,
				InputSchemaID:  "marlin.device.photos.recent.req.v1",
				OutputSchemaID: "marlin.device.photos.recent.resp.v1",
				ReadOnly:       true,
			},
			CapabilityDescriptor{
				CapabilityID:   schema.CapabilityID(CapAndroidOCR),
				ProtocolFamily: "platform.android.vision",
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
			CapabilityID:   schema.CapabilityID(CapAndroidVoiceTranscribe),
			ProtocolFamily: "platform.android.speech",
			Permission:     perm,
			InputSchemaID:  "marlin.device.voice.req.v1",
			OutputSchemaID: "marlin.device.voice.resp.v1",
			ReadOnly:       true,
		})
	}
	if p.Capture != nil {
		out = append(out, CapabilityDescriptor{
			CapabilityID:   schema.CapabilityID(CapAndroidShareDrain),
			ProtocolFamily: "platform.android.share",
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
			CapabilityID:   schema.CapabilityID(CapAndroidContactsSearch),
			ProtocolFamily: "platform.android.contacts",
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
			CapabilityID:   schema.CapabilityID(CapAndroidNotifySend),
			ProtocolFamily: "platform.android.notifications",
			Permission:     perm,
			InputSchemaID:  "marlin.device.notify.req.v1",
			OutputSchemaID: "marlin.device.notify.resp.v1",
			ReadOnly:       false, // mutating -> Bridge enforces one-shot Rust GateReceiptID
		})
	}
	return out, nil
}

func (p *AndroidPersonalProvider) Invoke(ctx context.Context, req InvocationRequest) (InvocationResult, error) {
	switch req.CapabilityID {
	case CapAndroidPhotosRecent:
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

	case CapAndroidOCR:
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

	case CapAndroidVoiceTranscribe:
		if p.Voice == nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: ErrCapabilityUnknown.Error()}, ErrCapabilityUnknown
		}
		tr, err := p.Voice.LatestTranscript(ctx)
		if err != nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: err.Error()}, err
		}
		raw, _ := json.Marshal(tr)
		return InvocationResult{InvocationID: req.InvocationID, OK: true, Result: raw}, nil

	case CapAndroidShareDrain:
		if p.Capture == nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: ErrCapabilityUnknown.Error()}, ErrCapabilityUnknown
		}
		items, err := p.Capture.DrainPendingCaptures(ctx)
		if err != nil {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: err.Error()}, err
		}
		raw, _ := json.Marshal(items)
		return InvocationResult{InvocationID: req.InvocationID, OK: true, Result: raw}, nil

	case CapAndroidContactsSearch:
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

	case CapAndroidNotifySend:
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

// ProtocolBundle groups platform-neutral device protocol implementations so a
// single RegisterPlatformProtocols call wires Desktop, iOS, or Android cleanly.
type ProtocolBundle struct {
	Photos    PhotosProtocol
	Voice     VoiceCaptureProtocol
	Capture   CaptureProtocol
	Contacts  ContactsProtocol
	Notify    NotificationProtocol
	Clipboard ClipboardProtocol
}

// RegisterPlatformProtocols registers the appropriate CapabilityProvider for
// bridge.TargetKind() (iOS, Android, or Desktop) from one unified ProtocolBundle.
func RegisterPlatformProtocols(ctx context.Context, bridge *Bridge, bundle ProtocolBundle) error {
	if bridge == nil {
		return fmt.Errorf("device: nil Bridge")
	}
	switch bridge.TargetKind() {
	case schema.TargetMobileIOS:
		return bridge.RegisterProvider(ctx, &IOSPersonalProvider{
			Photos:   bundle.Photos,
			Voice:    bundle.Voice,
			Capture:  bundle.Capture,
			Contacts: bundle.Contacts,
			Notify:   bundle.Notify,
		})
	case schema.TargetMobileAndroid:
		return bridge.RegisterProvider(ctx, &AndroidPersonalProvider{
			Photos:   bundle.Photos,
			Voice:    bundle.Voice,
			Capture:  bundle.Capture,
			Contacts: bundle.Contacts,
			Notify:   bundle.Notify,
		})
	default:
		return bridge.RegisterProvider(ctx, &DesktopProvider{
			Clipboard: bundle.Clipboard,
			Notify:    bundle.Notify,
		})
	}
}
