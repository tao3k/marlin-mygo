// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/device"
	"github.com/tao3k/marlin-mygo/runtime/device/nativebridge"
)

// MobileBridgeSummary caches live introspected capabilities from an attached device.Bridge.
type MobileBridgeSummary struct {
	TargetKind  schema.TargetKind
	Descriptors []device.CapabilityDescriptor
}

// AttachDeviceBridge binds a live platform device.Bridge (iOS Swift or Android Kotlin)
// directly onto MobileCompanionApp so Gates, Capture, Voice, and Packs interact with
// real native OS capabilities and one-shot GateReceipt enforcement.
func (m *MobileCompanionApp) AttachDeviceBridge(br *device.Bridge) {
	if br == nil {
		return
	}
	descs, _ := br.SnapshotDescriptors(context.Background())
	m.mu.Lock()
	m.DeviceBridge = br
	m.bridgeDescriptors = descs
	tc := m.Transcript
	if m.OnPickPhoto == nil {
		m.OnPickPhoto = m.pickRecentScreenshotFromBridge
	}
	m.mu.Unlock()

	if tc != nil {
		if tc.OnStopVoice == nil {
			tc.OnStopVoice = m.completeChatVoiceFromBridge
		}
		if tc.OnModelChanged == nil {
			tc.OnModelChanged = m.syncMobileModelSelection
		}
	}
}

// WireNativeTransport registers all 5 mobile protocol families onto a fresh device.Bridge
// for the app's active platform and attaches it to MobileCompanionApp in one call.
func (m *MobileCompanionApp) WireNativeTransport(ctx context.Context, transport nativebridge.NativeCallTransport) (*device.Bridge, error) {
	m.mu.Lock()
	plat := m.Platform
	m.mu.Unlock()

	kind := schema.TargetMobileIOS
	if plat == MobilePlatformAndroid {
		kind = schema.TargetMobileAndroid
	}
	br := device.NewBridge(kind)
	if err := nativebridge.RegisterAllProtocols(ctx, br, transport); err != nil {
		return nil, err
	}
	m.AttachDeviceBridge(br)
	return br, nil
}

func (m *MobileCompanionApp) dispatchApprovedGateToBridge(gate PendingGateView) {
	m.mu.Lock()
	br := m.DeviceBridge
	plat := m.Platform
	m.mu.Unlock()
	if br == nil {
		return
	}
	capID := schema.CapabilityID(gate.ToolName)
	if capID == "" {
		if plat == MobilePlatformAndroid {
			capID = device.CapAndroidNotifySend
		} else {
			capID = device.CapIOSNotifySend
		}
	}
	args, _ := json.Marshal(device.NotificationRequest{
		Title: "Marlin Approved",
		Body:  gate.Prompt,
	})
	receiptID := "rcpt-mob-" + gate.GateID
	res, err := br.Dispatch(context.Background(), device.InvocationRequest{
		InvocationID:  "inv-mob-" + gate.GateID,
		CapabilityID:  capID,
		GateReceiptID: receiptID,
		Args:          args,
	})
	m.mu.Lock()
	if err != nil {
		m.LastStatusLine = fmt.Sprintf("Bridge gate dispatch error: %v", err)
	} else if res.OK {
		m.LastStatusLine = fmt.Sprintf("Delivered via %s (receipt %s)", capID, receiptID)
	}
	m.mu.Unlock()
}

func (m *MobileCompanionApp) drainNativeCaptureQueue() {
	m.mu.Lock()
	br := m.DeviceBridge
	plat := m.Platform
	m.mu.Unlock()
	if br == nil {
		m.mu.Lock()
		m.LastStatusLine = "No native device bridge attached"
		m.mu.Unlock()
		return
	}
	capID := device.CapIOSShareDrain
	if plat == MobilePlatformAndroid {
		capID = device.CapAndroidShareDrain
	}
	res, err := br.Dispatch(context.Background(), device.InvocationRequest{
		InvocationID: "inv-mob-drain",
		CapabilityID: capID,
	})
	if err != nil {
		m.mu.Lock()
		m.LastStatusLine = fmt.Sprintf("Drain error: %v", err)
		m.mu.Unlock()
		return
	}
	var items []device.CapturePayload
	_ = json.Unmarshal(res.Result, &items)
	m.mu.Lock()
	if len(items) == 0 {
		m.LastStatusLine = "Native capture queue empty (0 items)"
	} else {
		first := strings.TrimSpace(items[0].Text)
		if first == "" {
			first = strings.TrimSpace(items[0].OCRText)
		}
		if first != "" {
			m.captureDraft = first
		}
		m.LastStatusLine = fmt.Sprintf("Drained %d native capture(s) from %s", len(items), items[0].SourceChannel)
	}
	m.mu.Unlock()
}

func (m *MobileCompanionApp) pullLatestNativeVoice() {
	m.mu.Lock()
	br := m.DeviceBridge
	plat := m.Platform
	m.mu.Unlock()
	if br == nil {
		m.mu.Lock()
		m.LastStatusLine = "No native device bridge attached"
		m.mu.Unlock()
		return
	}
	capID := device.CapIOSVoiceTranscribe
	if plat == MobilePlatformAndroid {
		capID = device.CapAndroidVoiceTranscribe
	}
	res, err := br.Dispatch(context.Background(), device.InvocationRequest{
		InvocationID: "inv-mob-voice",
		CapabilityID: capID,
	})
	if err != nil {
		m.mu.Lock()
		m.LastStatusLine = fmt.Sprintf("Voice capture error: %v", err)
		m.mu.Unlock()
		return
	}
	var tr device.VoiceTranscript
	_ = json.Unmarshal(res.Result, &tr)
	m.mu.Lock()
	if strings.TrimSpace(tr.Text) != "" {
		m.captureDraft = tr.Text
		m.LastStatusLine = fmt.Sprintf("Voice transcript (%s · %dms)", tr.Locale, tr.DurationMS)
	} else {
		m.LastStatusLine = "No voice transcript available"
	}
	m.mu.Unlock()
}

// completeChatVoiceFromBridge resolves the active mobile Chat Voice bar by
// pulling the latest OS-native transcript from the attached iOS/Android bridge
// and inserting it cleanly into the Chat composer draft.
func (m *MobileCompanionApp) completeChatVoiceFromBridge() {
	m.mu.Lock()
	br := m.DeviceBridge
	plat := m.Platform
	tc := m.Transcript
	m.mu.Unlock()
	if br == nil {
		if tc != nil && tc.Voice != nil {
			tc.Voice.Cancel()
		}
		return
	}
	capID := device.CapIOSVoiceTranscribe
	if plat == MobilePlatformAndroid {
		capID = device.CapAndroidVoiceTranscribe
	}
	res, err := br.Dispatch(context.Background(), device.InvocationRequest{
		InvocationID: "inv-mob-chat-voice",
		CapabilityID: capID,
	})
	if err != nil {
		if tc != nil && tc.Voice != nil {
			tc.Voice.Fail(err)
		}
		m.mu.Lock()
		m.LastStatusLine = fmt.Sprintf("Voice capture error: %v", err)
		m.mu.Unlock()
		return
	}
	var tr device.VoiceTranscript
	_ = json.Unmarshal(res.Result, &tr)
	text := strings.TrimSpace(tr.Text)
	if tc != nil && tc.Voice != nil {
		tc.Voice.Cancel()
	}
	m.mu.Lock()
	if text != "" {
		if strings.TrimSpace(m.chatDraft) == "" {
			m.chatDraft = text
		} else {
			m.chatDraft = strings.TrimSpace(m.chatDraft) + " " + text
		}
		m.LastStatusLine = fmt.Sprintf("Voice transcript (%s · %dms)", tr.Locale, tr.DurationMS)
	} else {
		m.LastStatusLine = "No voice transcript available"
	}
	m.mu.Unlock()
}

// pickRecentScreenshotFromBridge fetches the latest OS screenshot metadata via
// iOS PHPicker/PhotoKit or Android MediaStore and attaches it as a structured
// `@photo:<localId>` context mention in the mobile Chat composer.
func (m *MobileCompanionApp) pickRecentScreenshotFromBridge() {
	m.mu.Lock()
	br := m.DeviceBridge
	plat := m.Platform
	tc := m.Transcript
	m.mu.Unlock()
	if br == nil {
		return
	}
	capID := device.CapIOSPhotosRecent
	if plat == MobilePlatformAndroid {
		capID = device.CapAndroidPhotosRecent
	}
	res, err := br.Dispatch(context.Background(), device.InvocationRequest{
		InvocationID: "inv-mob-photo",
		CapabilityID: capID,
		Args:         json.RawMessage(`{"limit":5}`),
	})
	if err != nil {
		m.mu.Lock()
		m.LastStatusLine = fmt.Sprintf("Photo picker error: %v", err)
		m.mu.Unlock()
		return
	}
	var items []device.PhotoItem
	_ = json.Unmarshal(res.Result, &items)
	if len(items) == 0 {
		m.mu.Lock()
		m.LastStatusLine = "No recent screenshots available"
		m.mu.Unlock()
		return
	}
	first := items[0]
	label := first.LocalID
	if label == "" {
		label = "screenshot"
	}
	if tc != nil && tc.Composer != nil {
		tc.Composer.AddMention(composer.ContextMention{
			Kind:   "photo",
			Target: first.LocalID,
			Label:  label,
		})
	}
	m.mu.Lock()
	m.LastStatusLine = fmt.Sprintf("Attached screenshot %s (%dx%d)", label, first.Width, first.Height)
	m.mu.Unlock()
}

func (m *MobileCompanionApp) syncMobileModelSelection(opt composer.ModelOption) {
	m.mu.Lock()
	tc := m.Transcript
	m.LastStatusLine = fmt.Sprintf("Model: %s (%s)", opt.Label, opt.ContextLabel)
	m.mu.Unlock()
	if tc != nil {
		if winToks := composer.ParseContextWindowTokens(opt.ContextLabel); winToks > 0 {
			tc.SetContextUsage(tc.ContextUsage().UsedTokens, winToks)
		}
	}
}
