// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// MobileTab identifies one bottom-navigation view inside MobileCompanionApp.
type MobileTab string

const (
	MobileTabChat    MobileTab = "chat"
	MobileTabGates   MobileTab = "gates"
	MobileTabCapture MobileTab = "capture"
	MobileTabPacks   MobileTab = "packs"
)

// MobileDeclarativePack represents an App-Store-2.5.2-compliant declarative
// OTA pack (zero downloaded executable JS/WASM code) synced from desktop/cloud.
type MobileDeclarativePack struct {
	PluginID string
	Version  string
	Summary  string
	Runtime  schema.PanelRuntimeKind
	Active   bool
}

// MobileCompanionApp renders Marlin's compact 390x844 mobile-first companion
// UI (iOS / Android) built on MyGo's native GPU primitives:
//   - Chat: Fenwick-virtualized turn transcript + composer
//   - Gates: One-tap remote Tier-0 Approval Gate review (host.gate)
//   - Capture: Quick text/voice/share capture routed to device.IngestCapture
//   - Packs: Declarative-only OTA theme/keymap packs (rejects executable runtimes)
type MobileCompanionApp struct {
	TargetLabel     string
	Theme           schema.UITheme
	Transcript      *TranscriptController
	OnSendPrompt    func(prompt string)
	OnCancelTurn    func()
	OnGateDecision  func(gateID string, approve bool)
	OnQuickCapture  func(channel, text string)
	OnActivatePack  func(pluginID string) error
	LastStatusLine  string
	mu              sync.Mutex
	activeTab       MobileTab
	pendingGates    []PendingGateView
	packs           []MobileDeclarativePack
	captureDraft    string
	captureChannel  string
	policyDenialMsg string
}

func NewMobileCompanionApp(targetLabel string) *MobileCompanionApp {
	if targetLabel == "" {
		targetLabel = "iPhone Personal -> Desktop Agent"
	}
	return &MobileCompanionApp{
		TargetLabel:    targetLabel,
		Theme:          StudioDarkPalette(),
		Transcript:     NewTranscriptController(),
		activeTab:      MobileTabChat,
		captureChannel: "mobile.quick-capture",
	}
}

func (m *MobileCompanionApp) SetTab(tab MobileTab) {
	m.mu.Lock()
	m.activeTab = tab
	m.mu.Unlock()
}

func (m *MobileCompanionApp) ActiveTab() MobileTab {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeTab
}

func (m *MobileCompanionApp) SetPendingGates(gates []PendingGateView) {
	m.mu.Lock()
	m.pendingGates = append([]PendingGateView(nil), gates...)
	m.mu.Unlock()
}

// SetPacks registers OTA packs while enforcing App Store 2.5.2: any non-declarative
// runtime (webview-untrusted / wasm-ui-ir) is visibly blocked and denied.
func (m *MobileCompanionApp) SetPacks(packs []MobileDeclarativePack) {
	m.mu.Lock()
	m.packs = append([]MobileDeclarativePack(nil), packs...)
	m.mu.Unlock()
}

// TryActivatePack enforces mobile App Store 2.5.2 compliance before activating.
func (m *MobileCompanionApp) TryActivatePack(pluginID string) error {
	m.mu.Lock()
	var found *MobileDeclarativePack
	for i := range m.packs {
		if m.packs[i].PluginID == pluginID {
			cp := m.packs[i]
			found = &cp
			break
		}
	}
	if found == nil {
		m.mu.Unlock()
		return fmt.Errorf("mobile: pack %q not found", pluginID)
	}
	if found.Runtime != schema.RuntimeDeclarative {
		msg := fmt.Sprintf("Blocked %s (%s): mobile OTA allows declarative packs only (App Store 2.5.2)", found.PluginID, found.Runtime)
		m.policyDenialMsg = msg
		m.mu.Unlock()
		return fmt.Errorf("%s", msg)
	}
	m.policyDenialMsg = ""
	for i := range m.packs {
		m.packs[i].Active = m.packs[i].PluginID == pluginID
	}
	m.LastStatusLine = "Applied declarative OTA pack: " + pluginID
	cb := m.OnActivatePack
	m.mu.Unlock()
	if cb != nil {
		return cb(pluginID)
	}
	return nil
}

// View renders the complete mobile companion layout onto MyGo's native GPU UI.
func (m *MobileCompanionApp) View(c *ui.Context) {
	t := ApplyMarlinTheme(c, m.Theme)

	m.mu.Lock()
	tab := m.activeTab
	gates := append([]PendingGateView(nil), m.pendingGates...)
	packs := append([]MobileDeclarativePack(nil), m.packs...)
	draft := m.captureDraft
	channel := m.captureChannel
	denial := m.policyDenialMsg
	status := m.LastStatusLine
	m.mu.Unlock()

	var (
		nextTab        = tab
		decideGateID   string
		decideApprove  bool
		submitCapture  string
		activatePackID string
	)

	ui.Column(c).Fill().AlignItems(ui.Stretch).Background(t.Background).Children(func() {
		// Compact mobile status header.
		ui.Row(c).Padding(10, 14).Gap(8).AlignItems(ui.Center).
			Background(t.Surface).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Text(c, "Marlin Mobile").FontSize(15).Bold().TextColor(t.Text)
				ui.Text(c, m.TargetLabel).FontSize(11).TextColor(t.TextMuted)
			})
			if len(gates) > 0 {
				ui.Textf(c, "%d GATE", len(gates)).FontSize(11).Bold().TextColor(t.Warning)
			}
		})

		// Active tab body.
		ui.Column(c).Grow(1).MinWidth(0).Padding(12).Gap(10).AlignItems(ui.Stretch).Children(func() {
			switch tab {
			case MobileTabChat:
				if m.Transcript != nil {
					m.Transcript.Render(c, t, m.OnSendPrompt, m.OnCancelTurn)
				}

			case MobileTabGates:
				ui.Text(c, "PENDING APPROVAL GATES").FontSize(11).Bold().TextColor(t.TextMuted)
				if len(gates) == 0 {
					ui.Text(c, "All clear — zero pending approval gates.").FontSize(13).TextColor(t.Success)
				}
				for _, g := range gates {
					gate := g
					ui.Column(c).Padding(12).Gap(6).Radius(10).
						Background(t.Surface).Border(1, t.Warning).Children(func() {
						ui.Text(c, gate.ToolName).Bold().FontSize(13).TextColor(t.Warning)
						ui.Text(c, gate.Prompt).FontSize(13).TextColor(t.Text)
						if gate.Description != "" {
							ui.Text(c, gate.Description).FontSize(11).TextColor(t.TextMuted)
						}
						ui.Row(c).Gap(8).Children(func() {
							if ui.Button(c.Key("mob-rej-"+gate.GateID), "Reject").Clicked() {
								decideGateID = gate.GateID
								decideApprove = false
							}
							if ui.PrimaryButton(c.Key("mob-app-"+gate.GateID), "Approve Once").Clicked() {
								decideGateID = gate.GateID
								decideApprove = true
							}
						})
					})
				}

			case MobileTabCapture:
				ui.Text(c, "PROACTIVE CAPTURE -> ORGIZE").FontSize(11).Bold().TextColor(t.TextMuted)
				ui.Column(c).Padding(12).Gap(8).Radius(10).
					Background(t.Surface).Border(1, t.Border).Children(func() {
					ui.Text(c, "Channel: "+channel).FontSize(11).TextColor(t.TextMuted)
					ui.TextInput(c.Key("mob-cap-input"), &draft).Placeholder("Voice note, idea, or Org task...")
					if ui.PrimaryButton(c.Key("mob-cap-send"), "Capture to Inbox").Clicked() {
						if trimmed := strings.TrimSpace(draft); trimmed != "" {
							submitCapture = trimmed
							draft = ""
						}
					}
				})

			case MobileTabPacks:
				ui.Text(c, "DECLARATIVE OTA PACKS (APP STORE 2.5.2 SAFE)").FontSize(11).Bold().TextColor(t.TextMuted)
				if denial != "" {
					ui.Text(c, denial).FontSize(11).Bold().TextColor(t.Warning)
				}
				for _, pk := range packs {
					p := pk
					ui.Column(c).Padding(10).Gap(4).Radius(8).
						Background(t.Surface).Border(1, t.Border).Children(func() {
						ui.Textf(c, "%s v%s [%s]", p.PluginID, p.Version, p.Runtime).Bold().FontSize(12)
						if p.Summary != "" {
							ui.Text(c, p.Summary).FontSize(11).TextColor(t.TextMuted)
						}
						btnLbl := "Apply " + p.PluginID
						if p.Active {
							btnLbl = "Active: " + p.PluginID
						}
						if ui.Button(c.Key("mob-pk-"+p.PluginID), btnLbl).Clicked() {
							activatePackID = p.PluginID
						}
					})
				}
			}
			if status != "" {
				ui.Text(c, status).FontSize(11).TextColor(t.TextMuted)
			}
		})

		// Bottom navigation bar.
		ui.Row(c).Padding(8, 10).Gap(6).AlignItems(ui.Center).
			Background(t.Surface).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
			tabs := []struct {
				id    MobileTab
				label string
			}{
				{MobileTabChat, "Chat"},
				{MobileTabGates, "Gates"},
				{MobileTabCapture, "Capture"},
				{MobileTabPacks, "Packs"},
			}
			for _, tb := range tabs {
				if tab == tb.id {
					if ui.PrimaryButton(c.Key("mtab-"+string(tb.id)), tb.label).Grow(1).Clicked() {
						nextTab = tb.id
					}
				} else {
					if ui.Button(c.Key("mtab-"+string(tb.id)), tb.label).Grow(1).Clicked() {
						nextTab = tb.id
					}
				}
			}
		})
	})

	m.mu.Lock()
	m.activeTab = nextTab
	m.captureDraft = draft
	if decideGateID != "" {
		filtered := m.pendingGates[:0]
		for _, g := range m.pendingGates {
			if g.GateID != decideGateID {
				filtered = append(filtered, g)
			}
		}
		m.pendingGates = filtered
		if decideApprove {
			m.LastStatusLine = "Approved gate " + decideGateID
		} else {
			m.LastStatusLine = "Rejected gate " + decideGateID
		}
	}
	if submitCapture != "" {
		m.LastStatusLine = "Captured: " + submitCapture
	}
	m.mu.Unlock()

	if decideGateID != "" && m.OnGateDecision != nil {
		m.OnGateDecision(decideGateID, decideApprove)
	}
	if submitCapture != "" && m.OnQuickCapture != nil {
		m.OnQuickCapture(channel, submitCapture)
	}
	if activatePackID != "" {
		_ = m.TryActivatePack(activatePackID)
	}
}
