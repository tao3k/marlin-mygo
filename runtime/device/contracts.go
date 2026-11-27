// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package device

import (
	"context"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// Canonical device.* capability identifiers across desktop and mobile targets.
const (
	CapIOSPhotosRecent        schema.CapabilityID = "device.ios.photos.recent"
	CapIOSOCR                 schema.CapabilityID = "device.ios.ocr"
	CapIOSVoiceTranscribe     schema.CapabilityID = "device.ios.voice.transcribe"
	CapIOSShareDrain          schema.CapabilityID = "device.ios.share.drain"
	CapIOSContactsSearch      schema.CapabilityID = "device.ios.contacts.search"
	CapIOSNotifySend          schema.CapabilityID = "device.ios.notify.send"
	CapAndroidPhotosRecent    schema.CapabilityID = "device.android.photos.recent"
	CapAndroidOCR             schema.CapabilityID = "device.android.ocr"
	CapAndroidVoiceTranscribe schema.CapabilityID = "device.android.voice.transcribe"
	CapAndroidShareDrain      schema.CapabilityID = "device.android.share.drain"
	CapAndroidContactsSearch  schema.CapabilityID = "device.android.contacts.search"
	CapAndroidNotifySend      schema.CapabilityID = "device.android.notify.send"
	CapDesktopClipboardRead   schema.CapabilityID = "device.desktop.clipboard.read"
	CapDesktopNotifySend      schema.CapabilityID = "device.desktop.notify.send"
)

// PhotoItem is a bounded metadata record returned by PhotosProtocol.
type PhotoItem struct {
	LocalID       string `json:"localId"`
	CreatedUnixMS int64  `json:"createdUnixMs"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	IsScreenshot  bool   `json:"isScreenshot"`
	MIMEType      string `json:"mimeType"`
}

// PhotosProtocol abstracts PhotoKit + Vision OCR (platform.ios) and MediaStore (platform.android).
type PhotosProtocol interface {
	PermissionState(ctx context.Context) (OsPermissionState, error)
	RecentScreenshots(ctx context.Context, limit int) ([]PhotoItem, error)
	ExtractTextOCR(ctx context.Context, localID string) (string, error)
}

// VoiceTranscript represents a bounded on-device speech recognition snippet.
type VoiceTranscript struct {
	TranscriptID string `json:"transcriptId"`
	Locale       string `json:"locale"`
	Text         string `json:"text"`
	DurationMS   int64  `json:"durationMs"`
}

// VoiceCaptureProtocol abstracts on-device Speech recognition (platform.ios / platform.desktop).
type VoiceCaptureProtocol interface {
	PermissionState(ctx context.Context) (OsPermissionState, error)
	LatestTranscript(ctx context.Context) (VoiceTranscript, error)
}

// ContactSummary is a minimal, PII-bounded contact match record.
type ContactSummary struct {
	ContactID   string `json:"contactId"`
	DisplayName string `json:"displayName"`
	Handle      string `json:"handle,omitempty"`
}

// ContactsProtocol abstracts bounded contact lookup on personal assistant targets.
type ContactsProtocol interface {
	PermissionState(ctx context.Context) (OsPermissionState, error)
	SearchContacts(ctx context.Context, query string, limit int) ([]ContactSummary, error)
}

// NotificationRequest requests a user-visible OS notification (mutating -> GateReceipt required).
type NotificationRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// NotificationProtocol delivers OS notifications once authorized by a Rust GateReceipt.
type NotificationProtocol interface {
	PermissionState(ctx context.Context) (OsPermissionState, error)
	SendNotification(ctx context.Context, req NotificationRequest) error
}

// ClipboardProtocol reads bounded plain-text clipboard snapshots on desktop targets.
type ClipboardProtocol interface {
	PermissionState(ctx context.Context) (OsPermissionState, error)
	ReadText(ctx context.Context) (string, error)
}

// CapturePayload is emitted by proactive user triggers (iOS Back-Tap Shortcut,
// Share Extension AppGroup queue, Voice Memo, Android Share Intent, Desktop).
type CapturePayload struct {
	SourceChannel string            `json:"sourceChannel"`
	Text          string            `json:"text,omitempty"`
	OCRText       string            `json:"ocrText,omitempty"`
	ImageBytes    []byte            `json:"imageBytes,omitempty"`
	MIMEType      string            `json:"mimeType,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// CaptureProtocol receives user-initiated captures and forwards them to
// marlin.device.v1.DeviceBridgeService.IngestCapture.
type CaptureProtocol interface {
	DrainPendingCaptures(ctx context.Context) ([]CapturePayload, error)
}

// CaptureSink forwards drained captures to marlin.device.v1.DeviceBridgeService.IngestCapture.
type CaptureSink interface {
	IngestCapture(ctx context.Context, deviceID, sessionID string, payload CapturePayload, idempotencyKey string) error
}
