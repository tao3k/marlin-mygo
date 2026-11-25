// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// Pre-parsed multimodal attachment & search vector icons (zero per-frame alloc).
var (
	svgIconPaperclip = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m21.44 11.05-9.19 9.19a6 6 0 0 1-8.49-8.49l8.57-8.57A4 4 0 1 1 18 8.84l-8.59 8.57a2 2 0 0 1-2.83-2.83l8.49-8.48"/></svg>`,
	))
	svgIconImage = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2" ry="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/></svg>`,
	))
	svgIconFileText = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/></svg>`,
	))
	svgIconMic = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z"/><path d="M19 10v2a7 7 0 0 1-14 0v-2"/><line x1="12" y1="19" x2="12" y2="23"/><line x1="8" y1="23" x2="16" y2="23"/></svg>`,
	))
	svgIconSearch = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>`,
	))
	svgIconClipboard = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2"/><rect x="8" y="2" width="8" height="4" rx="1" ry="1"/></svg>`,
	))
	svgIconEye = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>`,
	))
)

// SessionSearchHit represents one thin search hit returned by the Rust backend
// search engine for O(log N) TranscriptController.JumpToRow navigation.
type SessionSearchHit struct {
	Row     int
	Snippet string
	Score   float32
}

// SessionSummaryView is a secret-free, pure UI projection of one durable Rust
// AppServer session displayed in the collapsible left sidebar.
type SessionSummaryView struct {
	SessionID     string
	Title         string
	TurnCount     uint64
	RecoveryState string
	Pinned        bool
}

// AttachmentCardChip renders one multimodal attachment (image, PDF, audio, or
// code/text) with kind-specific icon, resolution/line metadata, byte size,
// short SHA-256 CAS digest, upload progress, click-to-preview Lightbox trigger,
// and optional Remove button.
func AttachmentCardChip(
	c *ui.Context,
	dt DerivedTheme,
	att composer.AttachmentRef,
	removable bool,
	onPreview func(composer.AttachmentRef),
	onRemove func(id string),
) {
	icon, kindLabel, tone := attachmentVisualMeta(att.Kind, dt)

	metaParts := []string{composer.FormatByteSize(att.ByteSize)}
	if att.Kind == composer.AttachmentImage && att.Width > 0 && att.Height > 0 {
		metaParts = append(metaParts, fmt.Sprintf("%dx%d", att.Width, att.Height))
	} else if att.LineCount > 0 {
		metaParts = append(metaParts, fmt.Sprintf("%d lines", att.LineCount))
	}
	if short := att.ShortDigest(); short != "" {
		metaParts = append(metaParts, "#"+short)
	}

	ui.Row(c.Key("att-card-"+att.ID)).Padding(6, 10).Gap(8).Radius(10).AlignItems(ui.Center).
		Background(toColor(dt.SurfaceRecessed)).
		Border(1, rgbaFromRGB(tone, 0.32)).Children(func() {
		bodyBtn := ui.ButtonBase(c.Key("att-inspect-"+att.ID)).Label("Inspect "+att.Name).
			Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Padding(5).Radius(8).Background(rgbaFromRGB(tone, 0.14)).Children(func() {
				ui.Icon(c, icon).Size(14, 14).TextColor(toColor(tone))
			})
			ui.Column(c).Gap(1).Children(func() {
				ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
					ui.Text(c, att.Name).FontSize(11.5).FontWeight(700).TextColor(toColor(dt.Ink))
					AppleBadgePill(c, kindLabel, tone, dt)
					switch att.Status {
					case composer.StatusUploading:
						AppleBadgePill(c, fmt.Sprintf("UPLOADING %d%%", int(att.Progress*100)), dt.Warning, dt)
					case composer.StatusFailed:
						AppleBadgePill(c, "FAILED", dt.Danger, dt)
					case composer.StatusUploaded:
						AppleBadgePill(c, "CAS SYNCED", dt.Success, dt)
					}
				})
				ui.Text(c, strings.Join(metaParts, " · ")).FontSize(10).TextColor(toColor(dt.InkMuted))
			})
		})
		if bodyBtn.Clicked() && onPreview != nil {
			onPreview(att)
		}
		if removable && onRemove != nil {
			if ApplePillButton(c, "att-rm-"+att.ID, "Remove", BtnGlass, dt, svgIconXMark, true).Clicked() {
				onRemove(att.ID)
			}
		}
	})
}

// RenderAttachmentPreviewModal paints a Tier-0 read-only Attachment Preview
// Lightbox above workspace panes, showing fail-closed magic verification
// metadata, content-addressed CAS URI, image resolution card, or numbered
// source-code preview with 1-click Copy Snippet.
func RenderAttachmentPreviewModal(
	c *ui.Context,
	dt DerivedTheme,
	att composer.AttachmentRef,
	onCopy func(string),
	onClose func(),
) {
	icon, kindLabel, tone := attachmentVisualMeta(att.Kind, dt)
	ui.Column(c.Key("att-preview-backdrop")).Absolute().Top(0).Right(0).Bottom(0).Left(0).ZIndex(960).
		Background(ui.RGBA(8, 10, 16, 0.68)).
		AlignItems(ui.Center).Justify(ui.Center).Padding(24).Children(func() {
		ui.Column(c.Key("att-preview-modal")).Width(620).MaxWidth(680).Padding(16).Gap(12).Radius(16).
			Background(toColor(dt.SurfaceElevated)).
			Border(1, rgbaFromRGB(tone, 0.45)).
			Shadow(0, 18, 40, -8, ambientShadowColor(dt)).
			AlignItems(ui.Stretch).Children(func() {
			// Header
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Padding(6).Radius(8).Background(rgbaFromRGB(tone, 0.16)).Children(func() {
					ui.Icon(c, icon).Size(16, 16).TextColor(toColor(tone))
				})
				ui.Column(c).Gap(2).Grow(1).Children(func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, att.Name).FontSize(14).FontWeight(700).TextColor(toColor(dt.Ink))
						AppleBadgePill(c, kindLabel, tone, dt)
						AppleBadgePill(c, "MAGIC VERIFIED · FAIL-CLOSED", dt.Success, dt)
					})
					ui.Textf(c, "MIME: %s · Size: %s · CAS: %s",
						att.MIMEType, composer.FormatByteSize(att.ByteSize), att.ArtifactURI).
						FontSize(11).TextColor(toColor(dt.InkMuted))
				})
				if ApplePillButton(c, "att-preview-close", "Close Preview", BtnGlass, dt, svgIconXMark, true).Clicked() && onClose != nil {
					onClose()
				}
			})

			// Body preview by multimodal kind
			switch att.Kind {
			case composer.AttachmentImage:
				ui.Column(c).Padding(16).Gap(6).Radius(12).AlignItems(ui.Center).
					Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
					ui.Icon(c, svgIconImage).Size(32, 32).TextColor(toColor(dt.Accent))
					ui.Textf(c, "Verified Raster Image · %d x %d px", att.Width, att.Height).
						FontSize(13).FontWeight(700).TextColor(toColor(dt.Ink))
					ui.Textf(c, "SHA-256: %s", att.SHA256Hex).FontSize(10.5).TextColor(toColor(dt.InkMuted))
				})
			case composer.AttachmentCode, composer.AttachmentText:
				ui.Column(c).Padding(10, 12).Gap(6).Radius(12).
					Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
						ui.Textf(c, "SOURCE PREVIEW · %d lines", att.LineCount).
							FontSize(10.5).FontWeight(700).TextColor(toColor(dt.InkMuted))
						ui.Spacer(c)
						if onCopy != nil && att.PreviewText != "" {
							if ApplePillButton(c, "att-preview-copy", "Copy Snippet", BtnTinted, dt, svgIconClipboard, true).Clicked() {
								onCopy(att.PreviewText)
							}
						}
					})
					lines := strings.Split(att.PreviewText, "\n")
					limit := min(len(lines), 16)
					for i := 0; i < limit; i++ {
						ui.Textf(c, "%2d | %s", i+1, lines[i]).FontSize(11.5).LineHeight(1.35).TextColor(toColor(dt.Ink))
					}
				})
			case composer.AttachmentPDF:
				ui.Column(c).Padding(14).Gap(6).Radius(12).
					Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, svgIconFileText).Size(20, 20).TextColor(toColor(dt.Warning))
						ui.Text(c, "Verified %PDF- Document Artifact").FontSize(13).FontWeight(700).TextColor(toColor(dt.Ink))
					})
					ui.Text(c, "Staged for content-addressed Rust AppServer ingestion without frontend PDF script execution.").
						FontSize(11.5).TextColor(toColor(dt.InkSecondary))
				})
			case composer.AttachmentAudio:
				ui.Column(c).Padding(14).Gap(6).Radius(12).
					Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, svgIconMic).Size(20, 20).TextColor(toColor(dt.Success))
						ui.Textf(c, "Verified Audio Clip (%s)", att.MIMEType).FontSize(13).FontWeight(700).TextColor(toColor(dt.Ink))
					})
					ui.Text(c, "Ready for Rust multimodal audio transcription / reasoning pipeline.").
						FontSize(11.5).TextColor(toColor(dt.InkSecondary))
				})
			}

			// Footer status
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Textf(c, "Status: %s", strings.ToUpper(string(att.Status))).
					FontSize(11).FontWeight(600).TextColor(toColor(dt.InkSecondary))
				if att.ErrorMessage != "" {
					ui.Text(c, att.ErrorMessage).FontSize(11).TextColor(toColor(dt.Danger))
				}
				ui.Spacer(c)
				ui.Text(c, "Press ESC to close").FontSize(10.5).TextColor(toColor(dt.InkMuted))
			})
		})
	})
}

// ContextMentionChip renders one structured `@` context pill above the composer.
func ContextMentionChip(c *ui.Context, dt DerivedTheme, m composer.ContextMention, onRemove func(key string)) {
	lbl := fmt.Sprintf("@%s:%s", m.Kind, m.Label)
	ui.Row(c.Key("mention-"+m.Key())).Padding(4, 8).Gap(6).Radius(999).AlignItems(ui.Center).
		Background(rgbaFromRGB(dt.Info, 0.14)).Border(1, rgbaFromRGB(dt.Info, 0.35)).Children(func() {
		ui.Text(c, lbl).FontSize(11).FontWeight(600).TextColor(toColor(dt.Info))
		if onRemove != nil {
			if ApplePillButton(c, "mention-rm-"+m.Key(), "x", BtnGlass, dt, nil, true).Clicked() {
				onRemove(m.Key())
			}
		}
	})
}

// RenderSlashCommandPopup paints the inline `/` autocomplete dropdown above the composer.
func RenderSlashCommandPopup(
	c *ui.Context,
	dt DerivedTheme,
	matches []composer.SlashCommand,
	onSelect func(composer.SlashCommand),
) {
	if len(matches) == 0 {
		return
	}
	ui.Column(c.Key("slash-popup")).Padding(8).Gap(4).Radius(12).
		Background(toColor(dt.SurfaceElevated)).
		Border(1, glassBorderColor(dt)).
		Shadow(0, 6, 16, -4, ambientShadowColor(dt)).
		AlignItems(ui.Stretch).Children(func() {
		ui.Text(c, "SLASH COMMANDS").FontSize(10).FontWeight(700).TextColor(toColor(dt.InkMuted))
		limit := min(len(matches), 6)
		for i := 0; i < limit; i++ {
			cmd := matches[i]
			ui.Row(c.Key("slash-item-"+cmd.Name)).Gap(8).AlignItems(ui.Center).Children(func() {
				if ApplePillButton(c, "slash-btn-"+cmd.Name, cmd.Name, BtnTinted, dt, nil, true).Clicked() && onSelect != nil {
					onSelect(cmd)
				}
				ui.Text(c, cmd.Description).FontSize(11.5).TextColor(toColor(dt.InkSecondary))
			})
		}
	})
}

// RenderMentionPopup paints the inline `@` context mention dropdown above the composer.
func RenderMentionPopup(
	c *ui.Context,
	dt DerivedTheme,
	candidates []composer.ContextMention,
	onSelect func(composer.ContextMention),
) {
	if len(candidates) == 0 {
		return
	}
	ui.Column(c.Key("mention-popup")).Padding(8).Gap(4).Radius(12).
		Background(toColor(dt.SurfaceElevated)).
		Border(1, glassBorderColor(dt)).
		Shadow(0, 6, 16, -4, ambientShadowColor(dt)).
		AlignItems(ui.Stretch).Children(func() {
		ui.Text(c, "ATTACH CONTEXT (@)").FontSize(10).FontWeight(700).TextColor(toColor(dt.InkMuted))
		limit := min(len(candidates), 6)
		for i := 0; i < limit; i++ {
			cand := candidates[i]
			lbl := fmt.Sprintf("@%s:%s", cand.Kind, cand.Label)
			ui.Row(c.Key("mention-cand-"+cand.Key())).Gap(8).AlignItems(ui.Center).Children(func() {
				if ApplePillButton(c, "mention-pick-"+cand.Key(), lbl, BtnGlass, dt, nil, true).Clicked() && onSelect != nil {
					onSelect(cand)
				}
				ui.Text(c, cand.Target).FontSize(11).TextColor(toColor(dt.InkMuted))
			})
		}
	})
}

// RenderSessionSearchBar paints the thin Cmd+F Find-in-Session bar backed by
// Rust backend search hits and O(log N) TranscriptController.JumpToRow.
func RenderSessionSearchBar(
	c *ui.Context,
	dt DerivedTheme,
	query *string,
	hits []SessionSearchHit,
	activeIdx int,
	onPrev func(),
	onNext func(),
	onClose func(),
) {
	ui.Column(c.Key("session-search-bar")).Padding(8, 10).Gap(6).Radius(12).
		Background(toColor(dt.SurfaceElevated)).
		Border(1, rgbaFromRGB(dt.Accent, 0.35)).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, svgIconSearch).Size(14, 14).TextColor(toColor(dt.Accent))
			ui.TextInputBase(c.Key("session-search-input"), query).
				Placeholder("Find in session (Rust backend search)...").
				FontSize(12.5).TextColor(toColor(dt.Ink)).Grow(1).AutoFocus()

			counter := "0 matches"
			if len(hits) > 0 {
				counter = fmt.Sprintf("%d / %d", activeIdx+1, len(hits))
			}
			AppleBadgePill(c, counter, dt.Info, dt)

			if len(hits) > 0 {
				if ApplePillButton(c, "search-prev", "Prev", BtnGlass, dt, nil, true).Clicked() && onPrev != nil {
					onPrev()
				}
				if ApplePillButton(c, "search-next", "Next", BtnTinted, dt, nil, true).Clicked() && onNext != nil {
					onNext()
				}
			}
			if ApplePillButton(c, "search-close", "Close", BtnGlass, dt, svgIconXMark, true).Clicked() && onClose != nil {
				onClose()
			}
		})
		if len(hits) > 0 && activeIdx >= 0 && activeIdx < len(hits) && hits[activeIdx].Snippet != "" {
			ui.Textf(c, "Row #%d · %s", hits[activeIdx].Row+1, hits[activeIdx].Snippet).
				FontSize(11).TextColor(toColor(dt.InkSecondary))
		}
	})
}

func attachmentVisualMeta(kind composer.AttachmentKind, dt DerivedTheme) (*ui.SVG, string, schema.RGB) {
	switch kind {
	case composer.AttachmentImage:
		return svgIconImage, "IMAGE", dt.Accent
	case composer.AttachmentPDF:
		return svgIconFileText, "PDF", dt.Warning
	case composer.AttachmentAudio:
		return svgIconMic, "AUDIO", dt.Success
	case composer.AttachmentCode:
		return svgIconFileText, "CODE", dt.Info
	default:
		return svgIconPaperclip, "TEXT", dt.InkMuted
	}
}
