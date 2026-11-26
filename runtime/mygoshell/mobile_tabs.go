// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/device"
)

func (m *MobileCompanionApp) renderChatTab(
	c *ui.Context,
	_ *ui.Theme,
	dt DerivedTheme,
	_ MobileDeviceSpec,
	msgs []MobileMessage,
	chatDraft *string,
	chatDraftRev uint64,
	submitPrompt *string,
	submittedTurn **composer.TurnSubmission,
	pickPhotoNow *bool,
	takePhotoNow *bool,
	pickDocNow *bool,
	removeAttID *string,
	openPreviewAtt **composer.AttachmentRef,
	startVoiceNow *bool,
	stopVoiceNow *bool,
	cancelVoiceNow *bool,
	openModelSheetNow *bool,
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

		// Shared multimodal attachment strip + live voice bar above the phone composer pill.
		var stagedAtts []composer.AttachmentRef
		activeModelLabel := "Marlin Core"
		var voiceSnap composer.VoiceSnapshot
		if m.Transcript != nil {
			if m.Transcript.Composer != nil {
				cs := m.Transcript.Composer.Snapshot()
				stagedAtts = cs.Attachments
				if cs.ActiveModel.Label != "" {
					activeModelLabel = cs.ActiveModel.Label
				}
			}
			if m.Transcript.Voice != nil {
				voiceSnap = m.Transcript.Voice.Snapshot()
			}
		}
		if voiceSnap.Phase != composer.VoiceIdle {
			RenderVoiceRecordingBar(c, dt, voiceSnap,
				func() { *cancelVoiceNow = true },
				func() { *stopVoiceNow = true },
			)
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

		// Native mobile media capture hooks (Photo / Camera / File / Voice / Model) + quick-suggestion capsules.
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
			if voiceSnap.Phase == composer.VoiceIdle && m.Transcript != nil {
				if ApplePillButton(c, "mob-att-voice", "Voice", BtnGlass, dt, svgIconMic, true).Clicked() {
					*startVoiceNow = true
				}
			}
			if m.Transcript != nil && m.Transcript.Composer != nil {
				if ApplePillButton(c, "mob-att-model", activeModelLabel, BtnGlass, dt, nil, true).Clicked() {
					*openModelSheetNow = true
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
			ui.TextInputBase(c.Key(fmt.Sprintf("mob-chat-input-%d", chatDraftRev)), chatDraft).
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
