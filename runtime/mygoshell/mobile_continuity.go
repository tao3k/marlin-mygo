// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/resilience"
)

const (
	mobileChatDraftScope    = "mobile:chat"
	mobileCaptureDraftScope = "mobile:capture"
)

// SetChatDraft updates the phone Chat composer draft and persists it into
// Transcript.Resilience so OS background eviction or power loss never loses input.
func (m *MobileCompanionApp) SetChatDraft(text string) {
	m.mu.Lock()
	changed := m.chatDraft != text
	m.chatDraft = text
	m.chatDraftRev++
	tc := m.Transcript
	m.mu.Unlock()
	if tc != nil && tc.Resilience != nil {
		tc.Resilience.SetDraft(mobileChatDraftScope, text)
		if changed && tc.OnDraftChanged != nil {
			tc.OnDraftChanged(mobileChatDraftScope, text)
		}
	}
}

// ChatDraft returns the current phone Chat composer draft.
func (m *MobileCompanionApp) ChatDraft() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.chatDraft
}

// RestoreDraftsFromResilience restores persisted mobile Chat & Capture drafts
// after a cold restart or OS background kill.
func (m *MobileCompanionApp) RestoreDraftsFromResilience() (chatRestored, captureRestored string) {
	m.mu.Lock()
	tc := m.Transcript
	m.mu.Unlock()
	if tc == nil || tc.Resilience == nil {
		return "", ""
	}
	chatRestored = tc.Resilience.Draft(mobileChatDraftScope)
	captureRestored = tc.Resilience.Draft(mobileCaptureDraftScope)
	m.mu.Lock()
	if chatRestored != "" {
		m.chatDraft = chatRestored
		m.chatDraftRev++
	}
	if captureRestored != "" {
		m.captureDraft = captureRestored
		m.captureDraftRev++
	}
	m.mu.Unlock()
	return chatRestored, captureRestored
}

func renderMobileContinuityBanner(
	c *ui.Context,
	dt DerivedTheme,
	snap resilience.ResilienceSnapshot,
	retryNow *bool,
) {
	banner := snap.BannerText()
	if banner == "" {
		return
	}
	tone := dt.Warning
	if snap.State == resilience.ConnOffline {
		tone = dt.Danger
	} else if snap.State == resilience.ConnReplaying {
		tone = dt.Info
	}
	ui.Column(c.Key("mob-continuity-banner")).Padding(7, 10).Gap(5).Radius(12).
		Background(rgbaFromRGB(tone, 0.12)).Border(1, rgbaFromRGB(tone, 0.36)).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			AppleBadgePill(c, string(snap.State), tone, dt)
			ui.Spacer(c)
			if len(snap.PendingOutbox) > 0 {
				if ApplePillButton(c, "mob-retry-outbox", "Retry", BtnTinted, dt, nil, true).Clicked() {
					*retryNow = true
				}
			}
		})
		ui.Text(c, banner).FontSize(11.5).FontWeight(600).TextColor(toColor(dt.Ink))
	})
}
