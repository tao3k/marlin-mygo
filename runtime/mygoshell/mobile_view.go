// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/device"
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
	FromUser    bool
	Sender      string
	Text        string
	Meta        string
	Attachments []composer.AttachmentRef
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
	Windows         *shell.WindowTree
	Auth            *AuthController
	OnSendPrompt    func(prompt string)
	OnCancelTurn    func()
	OnGateDecision  func(gateID string, approve bool)
	OnQuickCapture    func(channel, text string)
	OnActivatePack    func(pluginID string) error
	OnPickPhoto       func() // iOS PHPicker / Android Photo Picker
	OnTakePhoto       func() // iOS Camera / Android CameraX
	OnPickDocument    func() // iOS UIDocumentPicker / Android SAF
	DeviceBridge      *device.Bridge
	LastStatusLine    string
	mu                sync.Mutex
	authOpen          bool
	bridgeDescriptors []device.CapabilityDescriptor
	themePresetID     string
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
	m.ApplyCustomTheme(ThemeByID(id))
}

// ApplyCustomTheme applies any Official Marketplace ThemePreset to the mobile shell.
func (m *MobileCompanionApp) ApplyCustomTheme(preset ThemePreset) {
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
	bridgeDescs := append([]device.CapabilityDescriptor(nil), m.bridgeDescriptors...)
	hasBridge := m.DeviceBridge != nil
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
		drainQueueNow    bool
		pullVoiceNow     bool
		activatePackID   string
		selectThemeID    string
		cycleThemeNow    bool
		togglePlatformTo MobilePlatform
		pickPhotoNow     bool
		takePhotoNow     bool
		pickDocNow       bool
		removeAttID      string
		openPreviewAtt   *composer.AttachmentRef
		closePreviewNow  bool
		submittedMobTurn *composer.TurnSubmission
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
				m.renderChatTab(c, t, dt, dev, msgs, &chatDraft, &submitPrompt, &submittedMobTurn, &pickPhotoNow, &takePhotoNow, &pickDocNow, &removeAttID, &openPreviewAtt)

			case MobileTabGates:
				m.renderGatesTab(c, t, dt, dev, gates, &decideGateID, &decideApprove)

			case MobileTabCapture:
				m.renderCaptureTab(c, t, dt, dev, plat, channel, hasBridge, &nextChannel, &draft, &submitCapture, &drainQueueNow, &pullVoiceNow)

			case MobileTabPacks:
				m.renderPacksTab(c, t, dt, dev, plat, preset.ID, packs, bridgeDescs, denial, &selectThemeID, &activatePackID)
			}
			if status != "" {
				ui.Text(c, status).FontSize(11).TextColor(toColor(dt.InkMuted))
			}
		})

		// 4. Authentic Bottom Navigation (iOS Tab Bar + Home Indicator vs Android M3 Nav + Gesture Bar).
		m.renderBottomBar(c, t, dt, plat, dev, tab, len(gates), &nextTab)
	})

	if m.Auth != nil {
		ui.Modal(c, &m.authOpen, func() {
			m.Auth.Render(c, t, dt, func() {
				m.authOpen = false
			})
		})
	}
	if m.Transcript != nil {
		if att, open := m.Transcript.PreviewedAttachment(); open {
			RenderMobileAttachmentSheet(c, dt, att, m.Transcript.CachedThumbnail(att.SHA256Hex), m.Transcript.OnCopyCode, func() {
				closePreviewNow = true
			})
		}
	}

	// Commit state transitions atomically outside layout closures.
	m.mu.Lock()
	m.activeTab = nextTab
	m.chatDraft = chatDraft
	m.captureDraft = draft
	m.captureChannel = nextChannel
	var decidedGate PendingGateView
	if decideGateID != "" {
		filtered := m.pendingGates[:0]
		for _, g := range m.pendingGates {
			if g.GateID != decideGateID {
				filtered = append(filtered, g)
			} else {
				decidedGate = g
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
	if openPreviewAtt != nil && m.Transcript != nil {
		m.Transcript.OpenAttachmentPreview(*openPreviewAtt)
	}
	if closePreviewNow && m.Transcript != nil {
		m.Transcript.CloseAttachmentPreview()
	}
	if removeAttID != "" && m.Transcript != nil && m.Transcript.Composer != nil {
		m.Transcript.Composer.RemoveAttachment(removeAttID)
	}
	if pickPhotoNow && m.OnPickPhoto != nil {
		m.OnPickPhoto()
	}
	if takePhotoNow && m.OnTakePhoto != nil {
		m.OnTakePhoto()
	}
	if pickDocNow && m.OnPickDocument != nil {
		m.OnPickDocument()
	}
	if submittedMobTurn != nil {
		refs := make([]composer.AttachmentRef, 0, len(submittedMobTurn.Attachments))
		for _, att := range submittedMobTurn.Attachments {
			refs = append(refs, att.Ref)
		}
		m.mu.Lock()
		m.messages = append(m.messages, MobileMessage{
			FromUser:    true,
			Sender:      "You",
			Text:        submittedMobTurn.Prompt,
			Meta:        fmt.Sprintf("sent · %d attachment(s)", len(refs)),
			Attachments: refs,
		})
		m.mu.Unlock()
		if m.Transcript != nil && m.Transcript.OnSubmitTurn != nil {
			m.Transcript.OnSubmitTurn(*submittedMobTurn)
		} else if m.OnSendPrompt != nil && strings.TrimSpace(submittedMobTurn.Prompt) != "" {
			m.OnSendPrompt(submittedMobTurn.Prompt)
		}
	} else if submitPrompt != "" {
		m.AppendMobileMessage(true, "You", submitPrompt, "sent")
		if m.OnSendPrompt != nil {
			m.OnSendPrompt(submitPrompt)
		} else {
			m.AppendMobileMessage(false, "Marlin", "Queued on remote desktop host: "+submitPrompt, "synced")
		}
	}
	if decideGateID != "" {
		if decideApprove {
			m.dispatchApprovedGateToBridge(decidedGate)
		}
		if m.OnGateDecision != nil {
			m.OnGateDecision(decideGateID, decideApprove)
		}
	}
	if drainQueueNow {
		m.drainNativeCaptureQueue()
	}
	if pullVoiceNow {
		m.pullLatestNativeVoice()
	}
	if submitCapture != "" && m.OnQuickCapture != nil {
		m.OnQuickCapture(nextChannel, submitCapture)
	}
	if activatePackID != "" {
		_ = m.TryActivatePack(activatePackID)
	}
}

func (m *MobileCompanionApp) renderStatusBar(c *ui.Context, dt DerivedTheme, plat MobilePlatform, dev MobileDeviceSpec) {
	ui.Row(c).Height(dev.StatusBarHeight).Padding(8, 22, 4, 22).AlignItems(ui.Center).
		Background(toColor(dt.Canvas)).Children(func() {
		ui.Text(c, "9:41").FontSize(13).FontWeight(600).TextColor(toColor(dt.Ink))
		ui.Spacer(c)
		if plat == MobilePlatformIOS {
			// Authentic iPhone Dynamic Island pill with subtle lens reflection.
			ui.Row(c).Width(98).Height(26).Radius(13).Padding(0, 10).AlignItems(ui.Center).Justify(ui.End).
				Background(ui.RGB(5, 6, 9)).
				Border(1, ui.RGBA(255, 255, 255, 0.08)).
				Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, 0.30)).Children(func() {
				ui.Box(c).Size(8, 8).Radius(4).Background(ui.RGB(18, 24, 38)).
					Border(1, ui.RGBA(96, 165, 250, 0.25))
			})
		} else {
			// Authentic Android Pixel punch-hole camera cutout.
			ui.Row(c).Width(16).Height(16).Radius(8).AlignItems(ui.Center).Justify(ui.Center).
				Background(ui.RGB(6, 8, 12)).Border(1, ui.RGBA(255, 255, 255, 0.10)).Children(func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(ui.RGB(20, 28, 44))
			})
		}
		ui.Spacer(c)
		ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "5G").FontSize(11).FontWeight(600).TextColor(toColor(dt.Ink))
			ui.Row(c).Padding(1, 5).Radius(4).Background(rgbaFromRGB(dt.Success, 0.20)).
				Border(1, rgbaFromRGB(dt.Success, 0.35)).Children(func() {
				ui.Text(c, "98%").FontSize(10).FontWeight(700).TextColor(toColor(dt.Success))
			})
		})
	})
}

func (m *MobileCompanionApp) renderNavigationBar(
	c *ui.Context,
	_ *ui.Theme,
	dt DerivedTheme,
	plat MobilePlatform,
	tab MobileTab,
	themeName string,
	gateCount int,
	cycleThemeNow *bool,
	togglePlatformTo *MobilePlatform,
) {
	heading := mobileTabHeading(tab, plat)
	titleSize := float32(26)
	if plat == MobilePlatformAndroid {
		titleSize = 22
	}
	ui.Column(c).Padding(4, 16, 10, 16).Gap(8).AlignItems(ui.Stretch).
		Background(toColor(dt.Canvas)).BorderWidth(0, 0, 1, 0).BorderColor(glassBorderColor(dt)).Children(func() {
		// Row 1: Platform Segmented Control (iOS | Android always both visible!) + Theme Swatch Pill.
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			chosen := AppleSegmentedControl(c, "mob-plat", []SegmentedItem{
				{ID: string(MobilePlatformIOS), Label: "iOS"},
				{ID: string(MobilePlatformAndroid), Label: "Android"},
			}, string(plat), dt, true)
			if chosen != "" && MobilePlatform(chosen) != plat {
				*togglePlatformTo = MobilePlatform(chosen)
			}
			ui.Spacer(c)
			if gateCount > 0 {
				AppleBadgePill(c, fmt.Sprintf("%d GATE", gateCount), dt.Warning, dt)
			}
			if m.Auth != nil {
				authVar := BtnGlass
				if m.Auth.IsSignedIn() {
					authVar = BtnTinted
				}
				if ApplePillButton(c, "mob-auth", m.Auth.TopBarLabel(), authVar, dt, svgIconShield, true).Clicked() {
					m.authOpen = true
				}
			}
			if AppleSwatchPill(c, "mob-theme", themeName, dt.Accent, false, dt).Clicked() {
				*cycleThemeNow = true
			}
		})

		// Row 2: Authentic Apple Large Title (or Material 3 Top App Bar Title) + live connection dot.
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, heading).FontSize(titleSize).FontWeight(700).LetterSpacing(-0.4).TextColor(toColor(dt.Ink))
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(toColor(dt.Success))
				ui.Text(c, m.TargetLabel).FontSize(11.5).FontWeight(500).TextColor(toColor(dt.InkMuted))
			})
		})
	})
}

func (m *MobileCompanionApp) renderChatTab(
	c *ui.Context,
	_ *ui.Theme,
	dt DerivedTheme,
	_ MobileDeviceSpec,
	msgs []MobileMessage,
	chatDraft *string,
	submitPrompt *string,
	submittedTurn **composer.TurnSubmission,
	pickPhotoNow *bool,
	takePhotoNow *bool,
	pickDocNow *bool,
	removeAttID *string,
	openPreviewAtt **composer.AttachmentRef,
) {
	// Message feed with authentic iMessage / Apple Intelligence asymmetric bubbles.
	ui.Column(c).Grow(1).MinHeight(0).Gap(12).AlignItems(ui.Stretch).Children(func() {
		start := 0
		if len(msgs) > 8 {
			start = len(msgs) - 8
		}
		for i := start; i < len(msgs); i++ {
			msg := msgs[i]
			if msg.FromUser {
				ui.Row(c.Key(fmt.Sprintf("mbub-%d", i))).Justify(ui.End).Children(func() {
					topCol := toColor(mixRGB(dt.Accent, schema.RGB{R: 255, G: 255, B: 255}, 0.10))
					ui.Column(c).MaxWidth(286).Padding(11, 15).Gap(4).Radius(20, 20, 6, 20).
						Gradient(topCol, toColor(dt.Accent), 180).
						InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.24)).
						Shadow(0, 6, 18, -4, rgbaFromRGB(dt.Accent, 0.36)).Children(func() {
						if msg.Text != "" {
							ui.Text(c, msg.Text).FontSize(14).LineHeight(1.36).TextColor(toColor(dt.InkOnAccent))
						}
						if len(msg.Attachments) > 0 {
							for _, att := range msg.Attachments {
								var thumb ui.ImageSource
								if m.Transcript != nil {
									thumb = m.Transcript.CachedThumbnail(att.SHA256Hex)
								}
								AttachmentCardChip(c, dt, att, thumb, false, func(ref composer.AttachmentRef) {
									cp := ref
									*openPreviewAtt = &cp
								}, nil)
							}
						}
						if msg.Meta != "" {
							ui.Text(c, msg.Meta).FontSize(10).FontWeight(500).TextColor(ui.RGBA(dt.InkOnAccent.R, dt.InkOnAccent.G, dt.InkOnAccent.B, 0.78))
						}
					})
				})
			} else {
				ui.Row(c.Key(fmt.Sprintf("mbub-%d", i))).Justify(ui.Start).Children(func() {
					bubbleBg := toColor(dt.SurfaceElevated)
					if dt.Appearance == AppearanceLight {
						bubbleBg = ui.RGB(255, 255, 255)
					}
					ui.Column(c).MaxWidth(306).Padding(12, 15).Gap(4).Radius(20, 20, 20, 6).
						Background(bubbleBg).
						Border(1, glassBorderColor(dt)).
						InsetShadow(0, 1, 0, 0, glassTopSpecular(dt)).
						Shadow(0, 4, 14, -4, ambientShadowColor(dt)).Children(func() {
						if msg.Sender != "" {
							ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
								ui.Icon(c, svgIconSparkles).Size(12, 12).TextColor(toColor(dt.Accent))
								ui.Text(c, msg.Sender).FontSize(11.5).FontWeight(700).TextColor(toColor(dt.Accent))
							})
						}
						ui.Text(c, msg.Text).FontSize(14).LineHeight(1.38).TextColor(toColor(dt.Ink))
						if msg.Meta != "" {
							ui.Text(c, msg.Meta).FontSize(10).FontWeight(500).TextColor(toColor(dt.InkMuted))
						}
					})
				})
			}
		}
		ui.Spacer(c)

		// Shared multimodal attachment strip above the phone composer pill.
		var stagedAtts []composer.AttachmentRef
		if m.Transcript != nil && m.Transcript.Composer != nil {
			stagedAtts = m.Transcript.Composer.Snapshot().Attachments
		}
		if len(stagedAtts) > 0 {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				for _, att := range stagedAtts {
					AttachmentCardChip(c, dt, att, m.Transcript.CachedThumbnail(att.SHA256Hex), true, func(ref composer.AttachmentRef) {
						cp := ref
						*openPreviewAtt = &cp
					}, func(id string) { *removeAttID = id })
				}
			})
		}

		// Native mobile media capture hooks (Photo / Camera / File) + quick-suggestion capsules.
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			if m.OnPickPhoto != nil {
				if ApplePillButton(c, "mob-att-photo", "Photo", BtnGlass, dt, svgIconImage, true).Clicked() {
					*pickPhotoNow = true
				}
			}
			if m.OnTakePhoto != nil {
				if ApplePillButton(c, "mob-att-camera", "Camera", BtnGlass, dt, svgIconEye, true).Clicked() {
					*takePhotoNow = true
				}
			}
			if m.OnPickDocument != nil {
				if ApplePillButton(c, "mob-att-doc", "File", BtnGlass, dt, svgIconPaperclip, true).Clicked() {
					*pickDocNow = true
				}
			}
			for _, chip := range []struct{ key, label, prompt string }{
				{"chip-agenda", "Today's Agenda", "Summarize today's Org Agenda and next time block."},
				{"chip-gates", "Check Gates", "List pending Tier-0 approval gates across devices."},
			} {
				if ApplePillButton(c, chip.key, chip.label, BtnTinted, dt, svgIconSparkles, true).Clicked() {
					*submitPrompt = chip.prompt
				}
			}
		})

		// Floating Apple Liquid Glass capsule composer with borderless input + circular ↑ send button.
		ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(6, 8, 6, 16).Radius(26).
			Material(AppleLiquidGlassSurface(dt, false, false)).
			Shadow(0, 8, 22, -6, ambientShadowColor(dt)).Children(func() {
			ui.TextInputBase(c.Key("mob-chat-input"), chatDraft).
				Placeholder("Message Marlin...").
				FontSize(14).TextColor(toColor(dt.Ink)).Grow(1).Padding(6, 2)
			if AppleCircleIconButton(c, "mob-chat-send", "Send", svgIconArrowUp, 34, dt).Clicked() {
				if len(stagedAtts) > 0 && m.Transcript != nil && m.Transcript.Composer != nil {
					if sub, ok := m.Transcript.Composer.BuildAndResetSubmission(*chatDraft); ok {
						cp := sub
						*submittedTurn = &cp
						*chatDraft = ""
					}
				} else if trimmed := strings.TrimSpace(*chatDraft); trimmed != "" {
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
	_ MobileDeviceSpec,
	gates []PendingGateView,
	decideGateID *string,
	decideApprove *bool,
) {
	ui.Text(c, "PENDING APPROVAL GATES").FontSize(11).FontWeight(700).LetterSpacing(0.6).TextColor(toColor(dt.InkMuted))
	if len(gates) == 0 {
		AppleGlassCard(c, "mgates-empty", dt, 22, 22, 6, func() {
			ui.Row(c).Justify(ui.Center).Children(func() {
				ui.Icon(c, svgIconShield).Size(28, 28).TextColor(toColor(dt.Success))
			})
			ui.Row(c).Justify(ui.Center).Children(func() {
				ui.Text(c, "All Clear").FontSize(17).FontWeight(700).TextColor(toColor(dt.Ink))
			})
			ui.Row(c).Justify(ui.Center).Children(func() {
				ui.Text(c, "Zero pending Tier-0 approval gates.").FontSize(13).TextColor(toColor(dt.InkMuted))
			})
		})
		return
	}
	for _, g := range gates {
		gate := g
		AppleGlassCard(c, "mgate-"+gate.GateID, dt, 22, 16, 10, func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				AppleBadgePill(c, "TIER-0 GATE", dt.Warning, dt)
				ui.Text(c, gate.ToolName).FontSize(12).FontWeight(600).TextColor(toColor(dt.InkMuted))
			})
			ui.Text(c, gate.Prompt).FontSize(16).FontWeight(700).LineHeight(1.3).TextColor(toColor(dt.Ink))
			if gate.Description != "" {
				ui.Text(c, gate.Description).FontSize(13).LineHeight(1.35).TextColor(toColor(dt.InkSecondary))
			}
			ui.Row(c).Gap(10).Padding(4, 0, 0, 0).Children(func() {
				if ApplePillButton(c, "mob-rej-"+gate.GateID, "Reject", BtnDestructive, dt, svgIconXMark, false).Grow(1).Clicked() {
					*decideGateID = gate.GateID
					*decideApprove = false
				}
				if ApplePillButton(c, "mob-app-"+gate.GateID, "Approve Once", BtnPrimary, dt, svgIconCheck, false).Grow(1).Clicked() {
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
	_ MobileDeviceSpec,
	plat MobilePlatform,
	channel string,
	hasBridge bool,
	nextChannel *string,
	draft *string,
	submitCapture *string,
	drainQueueNow *bool,
	pullVoiceNow *bool,
) {
	ui.Text(c, "NATIVE CAPTURE CHANNEL").FontSize(11).FontWeight(700).LetterSpacing(0.6).TextColor(toColor(dt.InkMuted))
	ui.Row(c).Gap(6).Children(func() {
		for _, ch := range m.platformChannels(plat) {
			variant := BtnGlass
			if ch.id == channel {
				variant = BtnPrimary
			}
			if ApplePillButton(c, "mch-"+ch.id, ch.label, variant, dt, nil, true).Clicked() {
				*nextChannel = ch.id
			}
		}
	})

	AppleGlassCard(c, "mcap-card", dt, 22, 16, 12, func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, svgIconBolt).Size(14, 14).TextColor(toColor(dt.Accent))
			ui.Text(c, "Route: "+channel).FontSize(12).FontWeight(600).TextColor(toColor(dt.Accent))
		})
		ui.Row(c).Padding(10, 14).Radius(16).
			Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
			ui.TextInputBase(c.Key("mob-cap-input"), draft).
				Placeholder("Voice note, screenshot OCR, or Org task...").
				FontSize(14).TextColor(toColor(dt.Ink)).Grow(1)
		})
		if ApplePillButton(c, "mob-cap-send", "Capture to Inbox", BtnPrimary, dt, svgIconBolt, false).Clicked() {
			if trimmed := strings.TrimSpace(*draft); trimmed != "" {
				*submitCapture = trimmed
				*draft = ""
			}
		}
		if hasBridge {
			ui.Row(c).Gap(8).Children(func() {
				if ApplePillButton(c, "mob-cap-drain", "Drain Native Queue", BtnTinted, dt, nil, true).Grow(1).Clicked() {
					*drainQueueNow = true
				}
				if ApplePillButton(c, "mob-cap-voice", "Pull Voice", BtnGlass, dt, nil, true).Grow(1).Clicked() {
					*pullVoiceNow = true
				}
			})
		}
	})
}

func (m *MobileCompanionApp) renderPacksTab(
	c *ui.Context,
	_ *ui.Theme,
	dt DerivedTheme,
	_ MobileDeviceSpec,
	plat MobilePlatform,
	activeThemeID string,
	packs []MobileDeclarativePack,
	bridgeDescs []device.CapabilityDescriptor,
	denial string,
	selectThemeID *string,
	activatePackID *string,
) {
	// Native OS Bridge status card (Apple Settings grouped inset card).
	bridgeTitle := "iOS Swift Native Bridge"
	bridgeDetail := "PhotoKit · Vision OCR · Speech · Contacts · UserNotifications · AppGroup"
	if plat == MobilePlatformAndroid {
		bridgeTitle = "Android Kotlin Native Bridge"
		bridgeDetail = "MediaStore · Vision OCR · Speech · ContactsContract · NotificationManager"
	}
	AppleGlassCard(c, "mbridge-card", dt, 20, 14, 6, func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Box(c).Size(7, 7).Radius(3.5).Background(toColor(dt.Success))
			ui.Text(c, bridgeTitle).FontSize(13).FontWeight(700).TextColor(toColor(dt.Ink))
			ui.Spacer(c)
			if len(bridgeDescs) > 0 {
				AppleBadgePill(c, fmt.Sprintf("%d LIVE", len(bridgeDescs)), dt.Success, dt)
			}
		})
		ui.Text(c, bridgeDetail).FontSize(11.5).LineHeight(1.35).TextColor(toColor(dt.InkMuted))
		if len(bridgeDescs) > 0 {
			limit := min(len(bridgeDescs), 6)
			ui.Column(c).Gap(3).Padding(4, 0, 0, 0).Children(func() {
				for i := 0; i < limit; i++ {
					d := bridgeDescs[i]
					mode := "ro"
					if !d.ReadOnly {
						mode = "gated"
					}
					ui.Textf(c, "• %s [%s · %s]", d.CapabilityID, d.Permission, mode).
						FontSize(10.5).TextColor(toColor(dt.InkSecondary))
				}
			})
		}
	})

	// Curated 5-theme swatch pills (two tidy rows so zero horizontal clipping on 393px phone).
	ui.Text(c, "APPEARANCE & THEME").FontSize(10.5).FontWeight(700).LetterSpacing(0.6).TextColor(toColor(dt.InkMuted))
	themes := BuiltinThemes()
	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			for i := 0; i < 3 && i < len(themes); i++ {
				p := themes[i]
				if AppleSwatchPill(c, "mthm-"+p.ID, p.Name, p.Base.Accent, p.ID == activeThemeID, dt).Clicked() {
					*selectThemeID = p.ID
				}
			}
		})
		ui.Row(c).Gap(6).Children(func() {
			for i := 3; i < len(themes); i++ {
				p := themes[i]
				if AppleSwatchPill(c, "mthm-"+p.ID, p.Name, p.Base.Accent, p.ID == activeThemeID, dt).Clicked() {
					*selectThemeID = p.ID
				}
			}
		})
	})

	// Shared Workspace Tabs & 0MB Memory Saver card (synced live with Desktop/Web WindowTree).
	if m.Windows != nil {
		wsnap := m.Windows.Snapshot()
		allTabs := m.Windows.AllTabs()
		ui.Text(c, "WORKSPACE TABS & MEMORY SAVER").FontSize(10.5).FontWeight(700).LetterSpacing(0.6).TextColor(toColor(dt.InkMuted))
		AppleGlassCard(c, "mwintabs-card", dt, 20, 14, 6, func() {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, "Synced Tabs").FontSize(13).FontWeight(700).TextColor(toColor(dt.Ink))
				ui.Spacer(c)
				AppleBadgePill(c, fmt.Sprintf("%d active · %d sleep 0MB", wsnap.ActiveTabs, wsnap.HibernatedTabs), dt.Accent, dt)
			})
			limit := min(len(allTabs), 6)
			for i := 0; i < limit; i++ {
				entry := allTabs[i]
				title := entry.Tab.Buffer.Title
				if title == "" {
					title = entry.Tab.Buffer.BufferID
				}
				glyph := "○"
				tone := dt.InkMuted
				if entry.Tab.State == shell.TabActive {
					glyph = "●"
					tone = dt.Success
				} else if entry.Tab.State == shell.TabHibernated {
					glyph = "☾"
					tone = dt.Info
				}
				if entry.Tab.Pinned {
					glyph = "★"
				}
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Textf(c, "%s %s", glyph, title).FontSize(12).FontWeight(600).TextColor(toColor(dt.Ink))
					AppleBadgePill(c, strings.ToUpper(string(entry.Tab.State)), tone, dt)
					ui.Spacer(c)
					if entry.Tab.State == shell.TabHibernated {
						wakeKey := fmt.Sprintf("mob-tab-wake-%s-%d", entry.WindowID, entry.TabIndex)
						if ApplePillButton(c, wakeKey, "Wake "+title, BtnTinted, dt, nil, true).Clicked() {
							m.Windows.SelectTab(entry.WindowID, entry.TabIndex)
						}
					} else {
						sleepKey := fmt.Sprintf("mob-tab-sleep-%s-%d", entry.WindowID, entry.TabIndex)
						if ApplePillButton(c, sleepKey, "Sleep "+title, BtnGlass, dt, nil, true).Clicked() {
							m.Windows.HibernateTab(entry.WindowID, entry.TabIndex)
						}
					}
				})
			}
		})
	}

	ui.Text(c, "DECLARATIVE OTA PACKS (APP STORE 2.5.2 SAFE)").FontSize(10.5).FontWeight(700).LetterSpacing(0.6).TextColor(toColor(dt.InkMuted))
	if denial != "" {
		ui.Row(c).Padding(10, 12).Radius(14).
			Background(rgbaFromRGB(dt.Warning, 0.14)).Border(1, rgbaFromRGB(dt.Warning, 0.30)).Children(func() {
			ui.Text(c, denial).FontSize(11.5).FontWeight(600).TextColor(toColor(dt.Warning))
		})
	}
	for _, pk := range packs {
		p := pk
		AppleGlassCard(c, "mpk-"+p.PluginID, dt, 20, 14, 6, func() {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, p.PluginID).FontSize(13).FontWeight(700).TextColor(toColor(dt.Ink))
				ui.Spacer(c)
				ui.Textf(c, "v%s", p.Version).FontSize(11).TextColor(toColor(dt.InkMuted))
			})
			if p.Summary != "" {
				ui.Text(c, p.Summary).FontSize(11.5).TextColor(toColor(dt.InkSecondary))
			}
			btnLbl := "Apply " + p.PluginID
			variant := BtnTinted
			if p.Active {
				btnLbl = "Active: " + p.PluginID
				variant = BtnSuccess
			}
			if ApplePillButton(c, "mob-pk-"+p.PluginID, btnLbl, variant, dt, nil, true).Clicked() {
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
	gateLabel := "Gates"
	if gateCount > 0 {
		gateLabel = fmt.Sprintf("Gates (%d)", gateCount)
	}
	tabs := []struct {
		id         MobileTab
		label      string
		accessName string
		icon       *ui.SVG
		badge      int
	}{
		{MobileTabChat, "Chat", "Chat", svgIconChat, 0},
		{MobileTabGates, gateLabel, gateLabel, svgIconShield, gateCount},
		{MobileTabCapture, "Capture", "Capture", svgIconBolt, 0},
		{MobileTabPacks, "Packs", "Packs", svgIconSparkles, 0},
	}

	if plat == MobilePlatformIOS {
		// Authentic iOS 18/26 Floating Liquid Glass Capsule Tab Dock (macOS 26 NSGlassEffectView / GPU shader).
		ui.Column(c).AlignItems(ui.Stretch).Background(toColor(dt.Canvas)).Padding(4, 14, 0, 14).Children(func() {
			ui.Row(c).Padding(5, 6).Gap(4).Radius(28).AlignItems(ui.Center).
				Material(AppleLiquidGlassSurface(dt, true, false)).
				Shadow(0, 10, 26, -6, ambientShadowColor(dt)).Children(func() {
				for _, tb := range tabs {
					active := tab == tb.id
					btnBg := ui.Transparent
					ink := toColor(dt.InkMuted)
					if active {
						btnBg = rgbaFromRGB(dt.Accent, 0.16)
						ink = toColor(dt.Accent)
						if dt.Appearance == AppearanceDark {
							ink = toColor(mixRGB(dt.Accent, schema.RGB{R: 255, G: 255, B: 255}, 0.28))
						}
					}
					btn := ui.ButtonBase(c.Key("mtab-"+string(tb.id))).Label(tb.accessName).
						Grow(1).Padding(7, 4).Radius(22).
						Justify(ui.Center).AlignItems(ui.Center).Background(btnBg)
					btn.Children(func() {
						ui.Column(c).AlignItems(ui.Center).Gap(3).Children(func() {
							ui.Icon(c, tb.icon).Size(18, 18).TextColor(ink)
							lbl := ui.Text(c, tb.label).FontSize(10.5).TextColor(ink).NoWrap()
							if active {
								lbl.FontWeight(700)
							} else {
								lbl.FontWeight(500)
							}
						})
					})
					if btn.Clicked() {
						*nextTab = tb.id
					}
				}
			})
			// Authentic iOS Home Indicator.
			ui.Row(c).Padding(8, 0, 8, 0).Justify(ui.Center).Children(func() {
				ui.Row(c).Width(dev.HomeBarWidth).Height(dev.HomeBarHeight).Radius(dev.HomeBarHeight / 2).
					Background(toColor(dt.BorderStrong)).Children(func() {})
			})
		})
		return
	}

	// Authentic Android Material 3 Navigation Bar with active tonal pill indicator + Gesture Pill.
	barBg := toColor(dt.SurfaceElevated)
	if dt.Appearance == AppearanceLight {
		barBg = ui.RGB(250, 251, 254)
	}
	ui.Column(c).AlignItems(ui.Stretch).Background(barBg).
		BorderWidth(1, 0, 0, 0).BorderColor(glassBorderColor(dt)).Children(func() {
		ui.Row(c).Padding(8, 10, 4, 10).Gap(4).AlignItems(ui.Center).Children(func() {
			for _, tb := range tabs {
				active := tab == tb.id
				ink := toColor(dt.InkMuted)
				if active {
					ink = toColor(dt.Accent)
					if dt.Appearance == AppearanceDark {
						ink = toColor(mixRGB(dt.Accent, schema.RGB{R: 255, G: 255, B: 255}, 0.28))
					}
				}
				btn := ui.ButtonBase(c.Key("mtab-"+string(tb.id))).Label(tb.accessName).
					Grow(1).Padding(4, 4).Radius(16).
					Justify(ui.Center).AlignItems(ui.Center).Background(ui.Transparent)
				btn.Children(func() {
					ui.Column(c).AlignItems(ui.Center).Gap(4).Children(func() {
						pill := ui.Row(c).Width(56).Height(28).Radius(14).AlignItems(ui.Center).Justify(ui.Center)
						if active {
							pill.Background(rgbaFromRGB(dt.Accent, 0.20))
						}
						pill.Children(func() {
							ui.Icon(c, tb.icon).Size(18, 18).TextColor(ink)
						})
						lbl := ui.Text(c, tb.label).FontSize(11).TextColor(ink).NoWrap()
						if active {
							lbl.FontWeight(700)
						} else {
							lbl.FontWeight(500)
						}
					})
				})
				if btn.Clicked() {
					*nextTab = tb.id
				}
			}
		})
		ui.Row(c).Padding(4, 0, 8, 0).Justify(ui.Center).Children(func() {
			ui.Row(c).Width(dev.HomeBarWidth).Height(dev.HomeBarHeight).Radius(dev.HomeBarHeight / 2).
				Background(toColor(dt.BorderStrong)).Children(func() {})
		})
	})
}
