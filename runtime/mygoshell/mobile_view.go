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
	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/promptgate"
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
	TargetLabel       string
	Platform          MobilePlatform
	Device            MobileDeviceSpec
	Theme             schema.UITheme
	Transcript        *TranscriptController
	PromptGate        *PromptGateController
	FeatureHost       *features.Host
	HelmSearch        *HelmMinibufferController
	Windows           *shell.WindowTree
	Auth              *AuthController
	OnSendPrompt      func(prompt string)
	OnCancelTurn      func()
	OnGateDecision    func(gateID string, approve bool)
	OnQuickCapture    func(channel, text string)
	OnActivatePack    func(pluginID string) error
	OnPickPhoto       func() // iOS PHPicker / Android Photo Picker
	OnTakePhoto       func() // iOS Camera / Android CameraX
	OnPickDocument    func() // iOS UIDocumentPicker / Android SAF
	DeviceBridge      *device.Bridge
	LastStatusLine    string
	mu                sync.Mutex
	authOpen          bool
	helmOpen          bool
	activeFeatureID   string
	bridgeDescriptors []device.CapabilityDescriptor
	themePresetID     string
	activeTab         MobileTab
	messages          []MobileMessage
	chatDraft         string
	chatDraftRev      uint64
	pendingGates      []PendingGateView
	packs             []MobileDeclarativePack
	captureDraft      string
	captureDraftRev   uint64
	captureChannel    string
	policyDenialMsg   string
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
		PromptGate:     NewPromptGateController(),
		activeTab:      MobileTabChat,
		captureChannel: defaultChannel,
		messages: []MobileMessage{
			{FromUser: false, Sender: "Marlin", Text: greeting, Meta: "mTLS · live"},
		},
	}
}

// ShowAskPrompt enqueues a Tier-0 Ask question bottom sheet above all phone tabs.
func (m *MobileCompanionApp) ShowAskPrompt(req promptgate.PromptRequest) error {
	if m.PromptGate == nil {
		m.PromptGate = NewPromptGateController()
	}
	req.Kind = promptgate.PromptAsk
	return m.PromptGate.Enqueue(req)
}

// ShowOptionPrompt enqueues a Tier-0 Option picker bottom sheet above all phone tabs.
func (m *MobileCompanionApp) ShowOptionPrompt(req promptgate.PromptRequest) error {
	if m.PromptGate == nil {
		m.PromptGate = NewPromptGateController()
	}
	req.Kind = promptgate.PromptOption
	return m.PromptGate.Enqueue(req)
}

// ShowAuthPrompt enqueues a Tier-0 Authorization gate bottom sheet above all phone tabs.
func (m *MobileCompanionApp) ShowAuthPrompt(req promptgate.PromptRequest) error {
	if m.PromptGate == nil {
		m.PromptGate = NewPromptGateController()
	}
	req.Kind = promptgate.PromptAuth
	return m.PromptGate.Enqueue(req)
}

// OpenFeature opens an opt-in FeatureHost buffer (e.g. "feature:chatroom") on Mobile.
func (m *MobileCompanionApp) OpenFeature(bufferID string) bool {
	if m.FeatureHost == nil || bufferID == "" {
		return false
	}
	if _, ok := m.FeatureHost.Renderer(bufferID); !ok {
		return false
	}
	m.mu.Lock()
	m.activeFeatureID = bufferID
	m.mu.Unlock()
	return true
}

// CloseFeature returns from an open opt-in Feature view back to the phone tabs.
func (m *MobileCompanionApp) CloseFeature() {
	m.mu.Lock()
	m.activeFeatureID = ""
	m.mu.Unlock()
}

// ActiveFeatureID returns the currently open opt-in feature buffer ID on Mobile, if any.
func (m *MobileCompanionApp) ActiveFeatureID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeFeatureID
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
	prevChatDraft := chatDraft
	chatDraftRev := m.chatDraftRev
	gates := append([]PendingGateView(nil), m.pendingGates...)
	packs := append([]MobileDeclarativePack(nil), m.packs...)
	draft := m.captureDraft
	prevCapDraft := draft
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
		openPreviewAtt    *composer.AttachmentRef
		closePreviewNow   bool
		submittedMobTurn  *composer.TurnSubmission
		startVoiceNow     bool
		stopVoiceNow      bool
		cancelVoiceNow    bool
		openModelSheetNow bool
		closeModelSheet   bool
		pickedModelID     string
		pickedEffort      composer.ReasoningEffort
		retryMobOutbox    bool
	)

	ui.Column(c.Key("mobile-screen")).Fill().MinWidth(0).MinHeight(0).
		AlignItems(ui.Stretch).Background(toColor(dt.Canvas)).Clip().Children(func() {
		// 1. Authentic OS Status Bar + Safe Area (iOS Dynamic Island vs Android Punch-Hole).
		m.renderStatusBar(c, dt, plat, dev)

		// 2. Authentic Top Navigation Bar (iOS Large Title vs Android Material 3 Top App Bar).
		m.renderNavigationBar(c, t, dt, plat, tab, preset.Name, len(gates), &cycleThemeNow, &togglePlatformTo)

		// 3. Scroll-safe Active Tab Body with generous 16pt mobile side margins.
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(12, 16, 10, 16).Gap(12).AlignItems(ui.Stretch).Clip().Children(func() {
			if m.Transcript != nil && m.Transcript.Resilience != nil {
				renderMobileContinuityBanner(c, dt, m.Transcript.Resilience.Snapshot(mobileChatDraftScope), &retryMobOutbox)
			}
			switch tab {
			case MobileTabChat:
				m.renderChatTab(c, t, dt, dev, msgs, &chatDraft, chatDraftRev, &submitPrompt, &submittedMobTurn, &pickPhotoNow, &takePhotoNow, &pickDocNow, &removeAttID, &openPreviewAtt, &startVoiceNow, &stopVoiceNow, &cancelVoiceNow, &openModelSheetNow)

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

	// Optional FeatureHost full-height mobile overlay (only rendered when features are compiled & active).
	if featID := m.ActiveFeatureID(); featID != "" && m.FeatureHost != nil {
		if renderFn, ok := m.FeatureHost.Renderer(featID); ok {
			closeFeat := false
			ui.Column(c.Key("mob-feat-overlay-"+featID)).Absolute().Top(0).Right(0).Bottom(0).Left(0).ZIndex(920).
				Background(toColor(dt.Canvas)).Padding(12, 10, 14, 10).Gap(8).AlignItems(ui.Stretch).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					if ApplePillButton(c, "mob-feat-back", "Back", BtnGlass, dt, nil, true).Clicked() {
						closeFeat = true
					}
					for _, fb := range m.FeatureHost.Buffers() {
						buf := fb
						v := BtnGlass
						if buf.BufferID == featID {
							v = BtnTinted
						}
						if ApplePillButton(c, "mob-feat-sw-"+buf.BufferID, buf.Title, v, dt, nil, true).Clicked() {
							m.OpenFeature(buf.BufferID)
						}
					}
				})
				renderFn(c, t)
			})
			if closeFeat {
				m.CloseFeature()
			}
		}
	}

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
		} else if m.Transcript.IsModelPickerOpen() && m.Transcript.Composer != nil {
			RenderMobileModelSheet(c, dt, m.Transcript.Composer.Snapshot(),
				func(mod composer.ModelOption) { pickedModelID = mod.ID },
				func(eff composer.ReasoningEffort) { pickedEffort = eff },
				func() { closeModelSheet = true },
			)
		}
	}
	// Compact Mobile Helm Search drawer (0-overflow on 375-412px phone viewports).
	m.renderMobileHelmSheet(c, dt)

	// Tier-0 Ask / Option / Authorization bottom sheet floats at highest z-order over any active phone tab.
	if m.PromptGate != nil {
		m.PromptGate.RenderMobileSheet(c, dt)
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
	if startVoiceNow && m.Transcript != nil {
		m.Transcript.StartVoiceRecording()
	}
	if stopVoiceNow && m.Transcript != nil {
		m.Transcript.StopVoiceRecording()
	}
	if cancelVoiceNow && m.Transcript != nil {
		m.Transcript.CancelVoiceRecording()
	}
	if openModelSheetNow && m.Transcript != nil {
		m.Transcript.OpenModelPicker()
	}
	if closeModelSheet && m.Transcript != nil {
		m.Transcript.CloseModelPicker()
	}
	if pickedModelID != "" && m.Transcript != nil {
		m.Transcript.SelectModel(pickedModelID)
	}
	if pickedEffort != "" && m.Transcript != nil && m.Transcript.Composer != nil {
		m.Transcript.Composer.SetEffort(pickedEffort)
		m.Transcript.SetDeepThink(pickedEffort == composer.EffortDeep)
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
	if m.Transcript != nil && m.Transcript.Resilience != nil {
		if chatDraft != prevChatDraft {
			m.Transcript.Resilience.SetDraft(mobileChatDraftScope, chatDraft)
			if m.Transcript.OnDraftChanged != nil {
				m.Transcript.OnDraftChanged(mobileChatDraftScope, chatDraft)
			}
		}
		if draft != prevCapDraft {
			m.Transcript.Resilience.SetDraft(mobileCaptureDraftScope, draft)
			if m.Transcript.OnDraftChanged != nil {
				m.Transcript.OnDraftChanged(mobileCaptureDraftScope, draft)
			}
		}
		if retryMobOutbox && m.Transcript.OnRetryOutbox != nil {
			m.Transcript.OnRetryOutbox()
		}
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

		// Row 2: Authentic Apple Large Title (or Material 3 Top App Bar Title) + live connection dot + opt-in Feature pill.
		ui.Column(c).Gap(2).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, heading).FontSize(titleSize).FontWeight(700).LetterSpacing(-0.4).TextColor(toColor(dt.Ink))
				ui.Spacer(c)
				if m.HelmSearch != nil {
					if ApplePillButton(c, "mob-open-helm", "Search", BtnGlass, dt, svgIconSearch, true).Clicked() {
						m.OpenHelmSearch()
					}
				}
				if m.FeatureHost != nil {
					if fbufs := m.FeatureHost.Buffers(); len(fbufs) > 0 {
						if ApplePillButton(c, "mob-open-spaces", "Spaces", BtnTinted, dt, svgIconSparkles, true).Clicked() {
							m.OpenFeature(fbufs[0].BufferID)
						}
					}
				}
			})
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(toColor(dt.Success))
				ui.Text(c, m.TargetLabel).FontSize(11.5).FontWeight(500).TextColor(toColor(dt.InkMuted))
			})
		})
	})
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
