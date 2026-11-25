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

// MobilePlatform selects authentic iOS (HIG) or Android (Material 3) phone chrome,
// safe-area geometry, and platform-native capture channels.
type MobilePlatform string

const (
	MobilePlatformIOS     MobilePlatform = "ios"
	MobilePlatformAndroid MobilePlatform = "android"
)

// MobileDeviceSpec defines logical viewport dimensions and safe-area geometry
// for authentic phone proportions on real devices and desktop previews.
type MobileDeviceSpec struct {
	ID              string
	Name            string
	Platform        MobilePlatform
	LogicalWidth    int
	LogicalHeight   int
	CornerRadius    float32
	CardRadius      float32
	StatusBarHeight float32
	HomeBarWidth    float32
	HomeBarHeight   float32
}

// BuiltinMobileDeviceSpec returns canonical logical phone viewport specs.
func BuiltinMobileDeviceSpec(platform MobilePlatform, deviceID string) MobileDeviceSpec {
	switch strings.ToLower(strings.TrimSpace(deviceID)) {
	case "iphone-se":
		return MobileDeviceSpec{
			ID:              "iphone-se",
			Name:            "iPhone SE",
			Platform:        MobilePlatformIOS,
			LogicalWidth:    375,
			LogicalHeight:   667,
			CornerRadius:    0,
			CardRadius:      14,
			StatusBarHeight: 24,
			HomeBarWidth:    120,
			HomeBarHeight:   4,
		}
	case "pixel9", "android":
		return MobileDeviceSpec{
			ID:              "pixel9",
			Name:            "Pixel 9",
			Platform:        MobilePlatformAndroid,
			LogicalWidth:    412,
			LogicalHeight:   915,
			CornerRadius:    28,
			CardRadius:      16,
			StatusBarHeight: 34,
			HomeBarWidth:    108,
			HomeBarHeight:   4,
		}
	default:
		if platform == MobilePlatformAndroid {
			return BuiltinMobileDeviceSpec(MobilePlatformAndroid, "pixel9")
		}
		return MobileDeviceSpec{
			ID:              "iphone16",
			Name:            "iPhone 16 Pro",
			Platform:        MobilePlatformIOS,
			LogicalWidth:    393,
			LogicalHeight:   852,
			CornerRadius:    36,
			CardRadius:      16,
			StatusBarHeight: 44,
			HomeBarWidth:    134,
			HomeBarHeight:   5,
		}
	}
}

// MobileTab identifies one bottom-navigation view inside MobileCompanionApp.
type MobileTab string

const (
	MobileTabChat    MobileTab = "chat"
	MobileTabGates   MobileTab = "gates"
	MobileTabCapture MobileTab = "capture"
	MobileTabPacks   MobileTab = "packs"
)

// MobileMessage represents one touch-friendly chat bubble inside the mobile
// Chat tab (replacing desktop-density transcript chrome on phones).
type MobileMessage struct {
	FromUser bool
	Sender   string
	Text     string
	Meta     string
}

// MobileDeclarativePack represents an App-Store-2.5.2-compliant declarative
// OTA pack (zero downloaded executable JS/WASM code) synced from desktop/cloud.
type MobileDeclarativePack struct {
	PluginID string
	Version  string
	Summary  string
	Runtime  schema.PanelRuntimeKind
	Active   bool
}

// MobileCompanionApp renders Marlin's authentic phone-first Companion UI for
// iOS (Apple HIG: Dynamic Island, Large Title nav, Tab Bar, Home Indicator)
// and Android (Material 3: punch-hole status bar, Top App Bar, Pill Nav Bar,
// Gesture Bar), backed by MyGo's native GPU primitives and DerivedTheme tokens.
type MobileCompanionApp struct {
	TargetLabel     string
	Platform        MobilePlatform
	Device          MobileDeviceSpec
	Theme           schema.UITheme
	Transcript      *TranscriptController
	OnSendPrompt    func(prompt string)
	OnCancelTurn    func()
	OnGateDecision  func(gateID string, approve bool)
	OnQuickCapture  func(channel, text string)
	OnActivatePack  func(pluginID string) error
	LastStatusLine  string
	mu              sync.Mutex
	themePresetID   string
	activeTab       MobileTab
	messages        []MobileMessage
	chatDraft       string
	pendingGates    []PendingGateView
	packs           []MobileDeclarativePack
	captureDraft    string
	captureChannel  string
	policyDenialMsg string
}

// NewMobileCompanionApp initializes an iOS-default MobileCompanionApp.
func NewMobileCompanionApp(targetLabel string) *MobileCompanionApp {
	return NewMobileCompanionAppForPlatform(MobilePlatformIOS, "iphone16", targetLabel)
}

// NewMobileCompanionAppForPlatform constructs a platform-tailored iOS or Android
// companion shell with authentic logical dimensions, safe areas, and native channels.
func NewMobileCompanionAppForPlatform(platform MobilePlatform, deviceID, targetLabel string) *MobileCompanionApp {
	spec := BuiltinMobileDeviceSpec(platform, deviceID)
	if targetLabel == "" {
		if spec.Platform == MobilePlatformAndroid {
			targetLabel = "Pixel 9 · Connected to Desktop Host"
		} else {
			targetLabel = "iPhone 16 Pro · Connected to Desktop Host"
		}
	}
	defaultChannel := "ios.share-extension"
	greeting := "Synced with Desktop Studio over mTLS. Tap a quick action or ask about today's agenda."
	if spec.Platform == MobilePlatformAndroid {
		defaultChannel = "android.share-intent"
		greeting = "Connected via mTLS gRPC. MediaStore OCR, Share Intent, and Tier-0 Gates are ready."
	}
	preset := ThemeByID("midnight")
	return &MobileCompanionApp{
		TargetLabel:    targetLabel,
		Platform:       spec.Platform,
		Device:         spec,
		Theme:          preset.Base,
		themePresetID:  preset.ID,
		Transcript:     NewTranscriptController(),
		activeTab:      MobileTabChat,
		captureChannel: defaultChannel,
		messages: []MobileMessage{
			{FromUser: false, Sender: "Marlin", Text: greeting, Meta: "mTLS · live"},
		},
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

// AppendMobileMessage appends a chat bubble to the phone Chat feed (and mirrors
// to TranscriptController for shared telemetry).
func (m *MobileCompanionApp) AppendMobileMessage(fromUser bool, sender, text, meta string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	m.mu.Lock()
	m.messages = append(m.messages, MobileMessage{
		FromUser: fromUser,
		Sender:   sender,
		Text:     trimmed,
		Meta:     meta,
	})
	if len(m.messages) > 64 {
		m.messages = m.messages[len(m.messages)-64:]
	}
	m.mu.Unlock()
	if m.Transcript != nil {
		if fromUser {
			m.Transcript.AppendUser(trimmed)
		} else {
			m.Transcript.AppendThinking(sender, trimmed, false)
		}
	}
}

// SetThemePreset applies one of Marlin's 5 curated themes to the mobile shell.
func (m *MobileCompanionApp) SetThemePreset(id string) {
	preset := ThemeByID(id)
	m.mu.Lock()
	m.themePresetID = preset.ID
	m.Theme = preset.Base
	m.mu.Unlock()
}

// CycleTheme advances to the next curated theme preset.
func (m *MobileCompanionApp) CycleTheme() ThemePreset {
	m.mu.Lock()
	next := NextThemePreset(m.themePresetID)
	m.themePresetID = next.ID
	m.Theme = next.Base
	m.mu.Unlock()
	return next
}

// SwitchPlatform toggles between iOS (HIG) and Android (Material 3) live preview.
func (m *MobileCompanionApp) SwitchPlatform(platform MobilePlatform) {
	spec := BuiltinMobileDeviceSpec(platform, "")
	m.mu.Lock()
	m.Platform = spec.Platform
	m.Device = spec
	if spec.Platform == MobilePlatformAndroid {
		m.captureChannel = "android.share-intent"
	} else {
		m.captureChannel = "ios.share-extension"
	}
	m.LastStatusLine = fmt.Sprintf("Platform: %s (%dx%d)", spec.Name, spec.LogicalWidth, spec.LogicalHeight)
	m.mu.Unlock()
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

func (m *MobileCompanionApp) platformChannels(plat MobilePlatform) []struct{ id, label string } {
	if plat == MobilePlatformAndroid {
		return []struct{ id, label string }{
			{"android.share-intent", "Share Intent"},
			{"android.mediastore-ocr", "Photo OCR"},
			{"android.quick-tile", "Quick Tile"},
			{"android.voice-memo", "Voice"},
		}
	}
	return []struct{ id, label string }{
		{"ios.share-extension", "Share Ext"},
		{"ios.photokit-ocr", "Photo OCR"},
		{"ios.backtap-shortcut", "Back-Tap"},
		{"ios.voice-memo", "Voice"},
	}
}

func mobileTabHeading(tab MobileTab, plat MobilePlatform) string {
	switch tab {
	case MobileTabGates:
		return "Approvals"
	case MobileTabCapture:
		return "Quick Capture"
	case MobileTabPacks:
		if plat == MobilePlatformAndroid {
			return "Android Settings & Packs"
		}
		return "Settings & Packs"
	default:
		return "Marlin Mobile"
	}
}

// View renders the authentic phone-first iOS or Android layout onto MyGo's GPU UI.
func (m *MobileCompanionApp) View(c *ui.Context) {
	m.mu.Lock()
	preset := ThemeByID(m.themePresetID)
	theme := m.Theme
	plat := m.Platform
	dev := m.Device
	tab := m.activeTab
	msgs := append([]MobileMessage(nil), m.messages...)
	chatDraft := m.chatDraft
	gates := append([]PendingGateView(nil), m.pendingGates...)
	packs := append([]MobileDeclarativePack(nil), m.packs...)
	draft := m.captureDraft
	channel := m.captureChannel
	denial := m.policyDenialMsg
	status := m.LastStatusLine
	m.mu.Unlock()

	t := ApplyMarlinTheme(c, theme)
	dt := DeriveTheme(preset.ID, preset.Name, theme)

	var (
		nextTab          = tab
		nextChannel      = channel
		decideGateID     string
		decideApprove    bool
		submitPrompt     string
		submitCapture    string
		activatePackID   string
		selectThemeID    string
		cycleThemeNow    bool
		togglePlatformTo MobilePlatform
	)

	ui.Column(c.Key("mobile-screen")).Fill().MinWidth(0).MinHeight(0).
		AlignItems(ui.Stretch).Background(toColor(dt.Canvas)).Clip().Children(func() {
		// 1. Authentic OS Status Bar + Safe Area (iOS Dynamic Island vs Android Punch-Hole).
		m.renderStatusBar(c, dt, plat, dev)

		// 2. Authentic Top Navigation Bar (iOS Large Title vs Android Material 3 Top App Bar).
		m.renderNavigationBar(c, t, dt, plat, tab, preset.Name, len(gates), &cycleThemeNow, &togglePlatformTo)

		// 3. Scroll-safe Active Tab Body with generous 16pt mobile side margins.
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(12, 16, 10, 16).Gap(12).AlignItems(ui.Stretch).Clip().Children(func() {
			switch tab {
			case MobileTabChat:
				m.renderChatTab(c, t, dt, dev, msgs, &chatDraft, &submitPrompt)

			case MobileTabGates:
				m.renderGatesTab(c, t, dt, dev, gates, &decideGateID, &decideApprove)

			case MobileTabCapture:
				m.renderCaptureTab(c, t, dt, dev, plat, channel, &nextChannel, &draft, &submitCapture)

			case MobileTabPacks:
				m.renderPacksTab(c, t, dt, dev, plat, preset.ID, packs, denial, &selectThemeID, &activatePackID)
			}
			if status != "" {
				ui.Text(c, status).FontSize(11).TextColor(toColor(dt.InkMuted))
			}
		})

		// 4. Authentic Bottom Navigation (iOS Tab Bar + Home Indicator vs Android M3 Nav + Gesture Bar).
		m.renderBottomBar(c, t, dt, plat, dev, tab, len(gates), &nextTab)
	})

	// Commit state transitions atomically outside layout closures.
	m.mu.Lock()
	m.activeTab = nextTab
	m.chatDraft = chatDraft
	m.captureDraft = draft
	m.captureChannel = nextChannel
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

	if cycleThemeNow {
		m.CycleTheme()
	}
	if selectThemeID != "" {
		m.SetThemePreset(selectThemeID)
	}
	if togglePlatformTo != "" {
		m.SwitchPlatform(togglePlatformTo)
	}
	if submitPrompt != "" {
		m.AppendMobileMessage(true, "You", submitPrompt, "sent")
		if m.OnSendPrompt != nil {
			m.OnSendPrompt(submitPrompt)
		} else {
			m.AppendMobileMessage(false, "Marlin", "Queued on remote desktop host: "+submitPrompt, "synced")
		}
	}
	if decideGateID != "" && m.OnGateDecision != nil {
		m.OnGateDecision(decideGateID, decideApprove)
	}
	if submitCapture != "" && m.OnQuickCapture != nil {
		m.OnQuickCapture(nextChannel, submitCapture)
	}
	if activatePackID != "" {
		_ = m.TryActivatePack(activatePackID)
	}
}

func (m *MobileCompanionApp) renderStatusBar(c *ui.Context, dt DerivedTheme, plat MobilePlatform, dev MobileDeviceSpec) {
	ui.Row(c).Height(dev.StatusBarHeight).Padding(6, 20, 4, 20).AlignItems(ui.Center).
		Background(toColor(dt.Surface)).Children(func() {
		ui.Text(c, "9:41").FontSize(12).Bold().TextColor(toColor(dt.Ink))
		ui.Spacer(c)
		if plat == MobilePlatformIOS {
			// Dynamic Island silhouette.
			ui.Row(c).Width(92).Height(22).Radius(11).
				Background(ui.RGB(8, 10, 14)).Border(1, toColor(dt.BorderSubtle)).Children(func() {})
		} else {
			// Android punch-hole camera cutout.
			ui.Row(c).Width(14).Height(14).Radius(7).
				Background(ui.RGB(8, 10, 14)).Border(1, toColor(dt.BorderSubtle)).Children(func() {})
		}
		ui.Spacer(c)
		ui.Text(c, "5G · 98%").FontSize(11).Bold().TextColor(toColor(dt.InkMuted))
	})
}

func (m *MobileCompanionApp) renderNavigationBar(
	c *ui.Context,
	t *ui.Theme,
	dt DerivedTheme,
	plat MobilePlatform,
	tab MobileTab,
	themeName string,
	gateCount int,
	cycleThemeNow *bool,
	togglePlatformTo *MobilePlatform,
) {
	heading := mobileTabHeading(tab, plat)
	titleSize := float32(22)
	if plat == MobilePlatformAndroid {
		titleSize = 19
	}
	ui.Column(c).Padding(6, 16, 10, 16).Gap(4).AlignItems(ui.Stretch).
		Background(toColor(dt.Surface)).BorderWidth(0, 0, 1, 0).BorderColor(toColor(dt.BorderSubtle)).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Text(c, heading).FontSize(titleSize).Bold().TextColor(toColor(dt.Ink))
			ui.Spacer(c)
			if gateCount > 0 {
				ui.Textf(c, "%d GATE", gateCount).FontSize(11).Bold().TextColor(toColor(dt.Warning))
			}
			platLbl := "iOS"
			nextPlat := MobilePlatformAndroid
			if plat == MobilePlatformAndroid {
				platLbl = "Android"
				nextPlat = MobilePlatformIOS
			}
			if ui.Button(c.Key("mob-plat"), platLbl).Clicked() {
				*togglePlatformTo = nextPlat
			}
			if ui.Button(c.Key("mob-theme"), themeName).Clicked() {
				*cycleThemeNow = true
			}
		})
		ui.Text(c, m.TargetLabel).FontSize(11).TextColor(t.TextMuted)
	})
}

func (m *MobileCompanionApp) renderChatTab(
	c *ui.Context,
	_ *ui.Theme,
	dt DerivedTheme,
	dev MobileDeviceSpec,
	msgs []MobileMessage,
	chatDraft *string,
	submitPrompt *string,
) {
	// Message feed with authentic mobile left/right bubbles.
	ui.Column(c).Grow(1).MinHeight(0).Gap(10).AlignItems(ui.Stretch).Children(func() {
		start := 0
		if len(msgs) > 8 {
			start = len(msgs) - 8
		}
		for i := start; i < len(msgs); i++ {
			msg := msgs[i]
			if msg.FromUser {
				ui.Row(c.Key(fmt.Sprintf("mbub-%d", i))).Justify(ui.End).Children(func() {
					ui.Column(c).MaxWidth(285).Padding(10, 14).Gap(3).Radius(dev.CardRadius).
						Background(toColor(dt.Accent)).Children(func() {
						ui.Text(c, msg.Text).FontSize(14).TextColor(toColor(dt.InkOnAccent))
						if msg.Meta != "" {
							ui.Text(c, msg.Meta).FontSize(10).TextColor(toColor(dt.InkOnAccent))
						}
					})
				})
			} else {
				ui.Row(c.Key(fmt.Sprintf("mbub-%d", i))).Justify(ui.Start).Children(func() {
					ui.Column(c).MaxWidth(300).Padding(10, 14).Gap(3).Radius(dev.CardRadius).
						Background(toColor(dt.SurfaceElevated)).Border(1, toColor(dt.BorderSubtle)).Children(func() {
						if msg.Sender != "" {
							ui.Text(c, msg.Sender).FontSize(11).Bold().TextColor(toColor(dt.Accent))
						}
						ui.Text(c, msg.Text).FontSize(14).TextColor(toColor(dt.Ink))
						if msg.Meta != "" {
							ui.Text(c, msg.Meta).FontSize(10).TextColor(toColor(dt.InkMuted))
						}
					})
				})
			}
		}
		ui.Spacer(c)

		// One-tap mobile prompt chips right above the thumb-zone composer.
		ui.Row(c).Gap(6).Children(func() {
			for _, chip := range []struct{ key, label, prompt string }{
				{"chip-agenda", "Today's Agenda", "Summarize today's Org Agenda and next time block."},
				{"chip-gates", "Check Gates", "List pending Tier-0 approval gates across devices."},
			} {
				if ui.Button(c.Key(chip.key), chip.label).Clicked() {
					*submitPrompt = chip.prompt
				}
			}
		})

		// Thumb-zone mobile composer bar.
		ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(8, 10).Radius(dev.CardRadius).
			Background(toColor(dt.Surface)).Border(1, toColor(dt.Border)).Children(func() {
			ui.TextInput(c.Key("mob-chat-input"), chatDraft).Placeholder("Message Marlin...").Grow(1)
			if ui.PrimaryButton(c.Key("mob-chat-send"), "Send").Clicked() {
				if trimmed := strings.TrimSpace(*chatDraft); trimmed != "" {
					*submitPrompt = trimmed
					*chatDraft = ""
				}
			}
		})
	})
}

func (m *MobileCompanionApp) renderGatesTab(
	c *ui.Context,
	_ *ui.Theme,
	dt DerivedTheme,
	dev MobileDeviceSpec,
	gates []PendingGateView,
	decideGateID *string,
	decideApprove *bool,
) {
	ui.Text(c, "PENDING APPROVAL GATES").FontSize(11).Bold().TextColor(toColor(dt.InkMuted))
	if len(gates) == 0 {
		ui.Column(c).Padding(18).Gap(6).Radius(dev.CardRadius).AlignItems(ui.Center).
			Background(toColor(dt.Surface)).Border(1, toColor(dt.BorderSubtle)).Children(func() {
			ui.Text(c, "All clear").FontSize(16).Bold().TextColor(toColor(dt.Success))
			ui.Text(c, "Zero pending Tier-0 approval gates.").FontSize(13).TextColor(toColor(dt.InkMuted))
		})
		return
	}
	for _, g := range gates {
		gate := g
		ui.Column(c.Key("mgate-"+gate.GateID)).Padding(14).Gap(8).Radius(dev.CardRadius).
			Background(toColor(dt.Surface)).Border(1, toColor(dt.Warning)).Children(func() {
			ui.Text(c, gate.ToolName).Bold().FontSize(13).TextColor(toColor(dt.Warning))
			ui.Text(c, gate.Prompt).FontSize(15).Bold().TextColor(toColor(dt.Ink))
			if gate.Description != "" {
				ui.Text(c, gate.Description).FontSize(12).TextColor(toColor(dt.InkMuted))
			}
			ui.Row(c).Gap(10).Children(func() {
				if ui.Button(c.Key("mob-rej-"+gate.GateID), "Reject").Grow(1).Clicked() {
					*decideGateID = gate.GateID
					*decideApprove = false
				}
				if ui.PrimaryButton(c.Key("mob-app-"+gate.GateID), "Approve Once").Grow(1).Clicked() {
					*decideGateID = gate.GateID
					*decideApprove = true
				}
			})
		})
	}
}

func (m *MobileCompanionApp) renderCaptureTab(
	c *ui.Context,
	_ *ui.Theme,
	dt DerivedTheme,
	dev MobileDeviceSpec,
	plat MobilePlatform,
	channel string,
	nextChannel *string,
	draft *string,
	submitCapture *string,
) {
	ui.Text(c, "CAPTURE CHANNEL").FontSize(11).Bold().TextColor(toColor(dt.InkMuted))
	ui.Row(c).Gap(6).Children(func() {
		for _, ch := range m.platformChannels(plat) {
			if ch.id == channel {
				if ui.PrimaryButton(c.Key("mch-"+ch.id), ch.label).Clicked() {
					*nextChannel = ch.id
				}
			} else {
				if ui.Button(c.Key("mch-"+ch.id), ch.label).Clicked() {
					*nextChannel = ch.id
				}
			}
		}
	})

	ui.Column(c).Padding(14).Gap(10).Radius(dev.CardRadius).
		Background(toColor(dt.Surface)).Border(1, toColor(dt.Border)).Children(func() {
		ui.Text(c, "Route: "+channel).FontSize(12).TextColor(toColor(dt.Accent))
		ui.TextInput(c.Key("mob-cap-input"), draft).Placeholder("Voice note, screenshot OCR, or Org task...")
		if ui.PrimaryButton(c.Key("mob-cap-send"), "Capture to Inbox").Clicked() {
			if trimmed := strings.TrimSpace(*draft); trimmed != "" {
				*submitCapture = trimmed
				*draft = ""
			}
		}
	})
}

func (m *MobileCompanionApp) renderPacksTab(
	c *ui.Context,
	_ *ui.Theme,
	dt DerivedTheme,
	dev MobileDeviceSpec,
	plat MobilePlatform,
	activeThemeID string,
	packs []MobileDeclarativePack,
	denial string,
	selectThemeID *string,
	activatePackID *string,
) {
	// Native OS Bridge status card.
	bridgeSummary := "iOS Swift Bridge · PhotoKit + Vision OCR + App Group ShareExt"
	if plat == MobilePlatformAndroid {
		bridgeSummary = "Android Kotlin Bridge · MediaStore + NotificationChannel + Share Intent"
	}
	ui.Column(c).Padding(12).Gap(4).Radius(dev.CardRadius).
		Background(toColor(dt.Surface)).Border(1, toColor(dt.BorderSubtle)).Children(func() {
		ui.Text(c, "NATIVE OS CAPABILITIES").FontSize(10).Bold().TextColor(toColor(dt.InkMuted))
		ui.Text(c, bridgeSummary).FontSize(12).TextColor(toColor(dt.Ink))
	})

	// Curated 5-theme picker.
	ui.Text(c, "APPEARANCE & THEME").FontSize(10).Bold().TextColor(toColor(dt.InkMuted))
	ui.Row(c).Gap(6).Children(func() {
		for _, p := range BuiltinThemes() {
			if p.ID == activeThemeID {
				if ui.PrimaryButton(c.Key("mthm-"+p.ID), p.Name).Clicked() {
					*selectThemeID = p.ID
				}
			} else {
				if ui.Button(c.Key("mthm-"+p.ID), p.Name).Clicked() {
					*selectThemeID = p.ID
				}
			}
		}
	})

	ui.Text(c, "DECLARATIVE OTA PACKS (APP STORE 2.5.2 SAFE)").FontSize(10).Bold().TextColor(toColor(dt.InkMuted))
	if denial != "" {
		ui.Text(c, denial).FontSize(11).Bold().TextColor(toColor(dt.Warning))
	}
	for _, pk := range packs {
		p := pk
		ui.Column(c.Key("mpk-"+p.PluginID)).Padding(12).Gap(5).Radius(dev.CardRadius).
			Background(toColor(dt.Surface)).Border(1, toColor(dt.Border)).Children(func() {
			ui.Textf(c, "%s v%s [%s]", p.PluginID, p.Version, p.Runtime).Bold().FontSize(13).TextColor(toColor(dt.Ink))
			if p.Summary != "" {
				ui.Text(c, p.Summary).FontSize(11).TextColor(toColor(dt.InkMuted))
			}
			btnLbl := "Apply " + p.PluginID
			if p.Active {
				btnLbl = "Active: " + p.PluginID
			}
			if ui.Button(c.Key("mob-pk-"+p.PluginID), btnLbl).Clicked() {
				*activatePackID = p.PluginID
			}
		})
	}
}

func (m *MobileCompanionApp) renderBottomBar(
	c *ui.Context,
	_ *ui.Theme,
	dt DerivedTheme,
	plat MobilePlatform,
	dev MobileDeviceSpec,
	tab MobileTab,
	gateCount int,
	nextTab *MobileTab,
) {
	barBg := toColor(dt.Surface)
	if plat == MobilePlatformAndroid {
		barBg = toColor(dt.SurfaceElevated)
	}
	ui.Column(c).AlignItems(ui.Stretch).Background(barBg).
		BorderWidth(1, 0, 0, 0).BorderColor(toColor(dt.BorderSubtle)).Children(func() {
		ui.Row(c).Padding(8, 12, 6, 12).Gap(6).AlignItems(ui.Center).Children(func() {
			gateLabel := "Gates"
			if gateCount > 0 {
				gateLabel = fmt.Sprintf("Gates (%d)", gateCount)
			}
			tabs := []struct {
				id    MobileTab
				label string
			}{
				{MobileTabChat, "Chat"},
				{MobileTabGates, gateLabel},
				{MobileTabCapture, "Capture"},
				{MobileTabPacks, "Packs"},
			}
			for _, tb := range tabs {
				active := tab == tb.id
				btnBg := ui.Transparent
				txtColor := toColor(dt.InkMuted)
				if active {
					btnBg = ui.RGBA(dt.Accent.R, dt.Accent.G, dt.Accent.B, 0.16)
					txtColor = toColor(dt.Accent)
				}
				btn := ui.ButtonBase(c.Key("mtab-"+string(tb.id))).Grow(1).Padding(8, 6).Radius(12).
					Justify(ui.Center).AlignItems(ui.Center).Background(btnBg)
				btn.Children(func() {
					lbl := ui.Text(c, tb.label).FontSize(12).TextColor(txtColor)
					if active {
						lbl.Bold()
					}
				})
				if btn.Clicked() {
					*nextTab = tb.id
				}
			}
		})
		// iOS Home Indicator or Android Gesture Pill safe area.
		ui.Row(c).Padding(4, 0, 8, 0).Justify(ui.Center).Children(func() {
			ui.Row(c).Width(dev.HomeBarWidth).Height(dev.HomeBarHeight).Radius(dev.HomeBarHeight / 2).
				Background(toColor(dt.BorderStrong)).Children(func() {})
		})
	})
}
