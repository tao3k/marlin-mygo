// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package device

import "context"

// Platform-neutral native module interface contracts designed for
// `mygo native add swift,kotlin,rust`. Implementations live in per-target
// packages so iOS (Swift PhotoKit/Contacts/AppIntents/Vision), Android
// (Kotlin MediaStore/Contacts/Intents), and Desktop targets stay isolated.

// PhotoItem is a bounded metadata record returned by PhotosProtocol.
type PhotoItem struct {
	LocalID       string `json:"localId"`
	CreatedUnixMS int64  `json:"createdUnixMs"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	IsScreenshot  bool   `json:"isScreenshot"`
	MIMEType      string `json:"mimeType"`
}

// PhotosProtocol abstracts iOS PhotoKit + Vision OCR and Android MediaStore.
type PhotosProtocol interface {
	PermissionState(ctx context.Context) (OsPermissionState, error)
	RecentScreenshots(ctx context.Context, limit int) ([]PhotoItem, error)
	ExtractTextOCR(ctx context.Context, localID string) (string, error)
}

// CapturePayload is emitted by proactive user triggers (iOS Back-Tap Shortcut
// AppIntent, iOS Share Extension AppGroup queue, Android Share Intent, Desktop).
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
