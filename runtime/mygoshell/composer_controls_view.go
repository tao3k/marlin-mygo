// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/composer"
)

// RenderVoiceRecordingBar renders the live Tier-0 voice recording bar inside
// the composer capsule with pulsing REC badge, 24-bar GPU waveform meter,
// elapsed timer, Cancel, and Stop/Attach Voice trigger.
func RenderVoiceRecordingBar(
	c *ui.Context,
	dt DerivedTheme,
	snap composer.VoiceSnapshot,
	onCancel func(),
	onStop func(),
) {
	tone := dt.Danger
	statusBadge := "REC"
	if snap.Phase == composer.VoiceProcessing {
		tone = dt.Warning
		statusBadge = "PROCESSING"
	}
	ui.Column(c.Key("composer-voice-bar")).Padding(8, 12).Gap(6).Radius(14).AlignItems(ui.Stretch).
		Background(rgbaFromRGB(tone, 0.12)).
		Border(1, rgbaFromRGB(tone, 0.40)).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(8, 8).Radius(4).Background(toColor(tone))
			AppleBadgePill(c, statusBadge, tone, dt)
			ui.Text(c, snap.FormatElapsed()).FontSize(12).FontWeight(700).TextColor(toColor(dt.Ink))

			// Live 24-bar GPU audio level meter (responsive width, zero overflow on 375px phones).
			ui.Row(c).Height(22).Gap(2).AlignItems(ui.Center).Grow(1).Clip().Children(func() {
				for i := 0; i < 24; i++ {
					lvl := float32(0.15)
					if i < len(snap.Levels) {
						lvl = snap.Levels[i]
					}
					h := 4 + lvl*18
					ui.Box(c).Size(3, h).Radius(1.5).Background(rgbaFromRGB(tone, 0.85))
				}
			})
		})

		ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
			if ApplePillButton(c, "voice-cancel", "Cancel Voice", BtnGlass, dt, svgIconXMark, true).Clicked() && onCancel != nil {
				onCancel()
			}
			if snap.Phase == composer.VoiceRecording {
				if ApplePillButton(c, "voice-stop", "Attach Voice", BtnPrimary, dt, svgIconCheck, true).Clicked() && onStop != nil {
					onStop()
				}
			}
		})
	})
}

// RenderModelPickerPopup renders the searchable Model & Reasoning Effort picker
// popover directly above the composer capsule so users can switch any backend
// model and compute budget (Fast / Balanced / Deep) in 1 click.
func RenderModelPickerPopup(
	c *ui.Context,
	dt DerivedTheme,
	snap composer.ComposerSnapshot,
	query *string,
	matches []composer.ModelOption,
	onSelectModel func(model composer.ModelOption),
	onSelectEffort func(effort composer.ReasoningEffort),
	onClose func(),
) {
	ui.Column(c.Key("model-picker-popup")).Padding(10, 12).Gap(8).Radius(14).
		Background(toColor(dt.SurfaceElevated)).
		Border(1, rgbaFromRGB(dt.Accent, 0.40)).
		Shadow(0, 10, 24, -6, ambientShadowColor(dt)).
		AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "MODEL & REASONING EFFORT").FontSize(10.5).FontWeight(700).LetterSpacing(0.4).TextColor(toColor(dt.InkMuted))
			ui.Spacer(c)
			if ApplePillButton(c, "model-picker-close", "Close", BtnGlass, dt, svgIconXMark, true).Clicked() && onClose != nil {
				onClose()
			}
		})

		// Search filter when backend advertises multiple models.
		if len(snap.Models) > 2 {
			ui.Row(c).Padding(4, 10).Radius(10).
				Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
				ui.TextInputBase(c.Key("model-picker-filter"), query).
					Placeholder("Filter models (e.g. fast, pro, vision)...").
					FontSize(12).TextColor(toColor(dt.Ink)).Grow(1)
			})
		}

		// Model option list with capability pills.
		ui.Column(c).Gap(4).AlignItems(ui.Stretch).Children(func() {
			if len(matches) == 0 {
				ui.Text(c, "No models match filter.").FontSize(12).TextColor(toColor(dt.InkMuted))
			}
			for _, m := range matches {
				active := m.ID == snap.ActiveModel.ID
				bg := toColor(dt.SurfaceRecessed)
				bdr := glassBorderColor(dt)
				if active {
					bg = rgbaFromRGB(dt.Accent, 0.14)
					bdr = rgbaFromRGB(dt.Accent, 0.45)
				}
				btn := ui.ButtonBase(c.Key("model-opt-"+m.ID)).Label(m.Label).
					Padding(7, 10).Radius(10).AlignItems(ui.Center).Gap(6).
					Background(bg).Border(1, bdr)
				btn.Children(func() {
					if active {
						ui.Text(c, "●").FontSize(11).FontWeight(700).TextColor(toColor(dt.Accent))
					}
					ui.Text(c, m.Label).FontSize(12.5).FontWeight(700).TextColor(toColor(dt.Ink))
					if m.Provider != "" {
						ui.Text(c, m.Provider).FontSize(10.5).TextColor(toColor(dt.InkMuted))
					}
					ui.Spacer(c)
					if m.ContextLabel != "" {
						AppleBadgePill(c, m.ContextLabel, dt.InkMuted, dt)
					}
					if m.SupportsVision {
						AppleBadgePill(c, "VISION", dt.Info, dt)
					}
					if m.SupportsAudio {
						AppleBadgePill(c, "AUDIO", dt.Success, dt)
					}
					if m.SupportsThink {
						AppleBadgePill(c, "THINK", dt.Accent, dt)
					}
				})
				if btn.Clicked() && onSelectModel != nil {
					onSelectModel(m)
				}
			}
		})

		// Reasoning Effort selector.
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "Reasoning Effort").FontSize(11.5).FontWeight(600).TextColor(toColor(dt.InkSecondary))
			ui.Spacer(c)
			curEff := strings.ToLower(string(snap.ReasoningEffort))
			if curEff == "" {
				curEff = string(composer.EffortBalanced)
			}
			if chosen := AppleSegmentedControl(c, "model-effort-seg", []SegmentedItem{
				{ID: string(composer.EffortFast), Label: "Fast"},
				{ID: string(composer.EffortBalanced), Label: "Balanced"},
				{ID: string(composer.EffortDeep), Label: "Deep"},
			}, curEff, dt, true); chosen != "" && onSelectEffort != nil {
				onSelectEffort(composer.ReasoningEffort(chosen))
			}
		})
	})
}

// RenderMobileModelSheet renders a thumb-friendly iOS/Android bottom-sheet
// Model & Reasoning Effort picker with native grabber bar and 1-tap switching.
func RenderMobileModelSheet(
	c *ui.Context,
	dt DerivedTheme,
	snap composer.ComposerSnapshot,
	onSelectModel func(model composer.ModelOption),
	onSelectEffort func(effort composer.ReasoningEffort),
	onClose func(),
) {
	ui.Column(c.Key("mob-model-sheet-backdrop")).Absolute().Top(0).Right(0).Bottom(0).Left(0).ZIndex(960).
		Background(ui.RGBA(8, 10, 16, 0.62)).
		AlignItems(ui.Stretch).Justify(ui.End).Children(func() {
		ui.Column(c.Key("mob-model-sheet")).Padding(12, 16, 22, 16).Gap(10).Radius(22, 22, 0, 0).
			Background(toColor(dt.SurfaceElevated)).
			BorderWidth(1, 1, 0, 1).BorderColor(rgbaFromRGB(dt.Accent, 0.40)).
			AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Justify(ui.Center).Children(func() {
				ui.Box(c).Size(36, 4).Radius(2).Background(toColor(dt.BorderStrong))
			})
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, "Model & Reasoning").FontSize(15).FontWeight(700).TextColor(toColor(dt.Ink))
				ui.Spacer(c)
				if ApplePillButton(c, "mob-model-done", "Done", BtnTinted, dt, nil, true).Clicked() && onClose != nil {
					onClose()
				}
			})
			for _, m := range snap.Models {
				active := m.ID == snap.ActiveModel.ID
				bg := toColor(dt.SurfaceRecessed)
				bdr := glassBorderColor(dt)
				if active {
					bg = rgbaFromRGB(dt.Accent, 0.14)
					bdr = rgbaFromRGB(dt.Accent, 0.45)
				}
				btn := ui.ButtonBase(c.Key("mob-model-"+m.ID)).Label(m.Label).
					Padding(10, 12).Radius(12).AlignItems(ui.Center).Gap(8).
					Background(bg).Border(1, bdr)
				btn.Children(func() {
					ui.Text(c, m.Label).FontSize(13.5).FontWeight(700).TextColor(toColor(dt.Ink))
					ui.Spacer(c)
					if m.SupportsVision {
						AppleBadgePill(c, "VISION", dt.Info, dt)
					}
					if m.SupportsAudio {
						AppleBadgePill(c, "AUDIO", dt.Success, dt)
					}
					if m.SupportsThink {
						AppleBadgePill(c, "THINK", dt.Accent, dt)
					}
				})
				if btn.Clicked() && onSelectModel != nil {
					onSelectModel(m)
				}
			}
			curEff := strings.ToLower(string(snap.ReasoningEffort))
			if curEff == "" {
				curEff = string(composer.EffortBalanced)
			}
			if chosen := AppleSegmentedControl(c, "mob-effort-seg", []SegmentedItem{
				{ID: string(composer.EffortFast), Label: "Fast"},
				{ID: string(composer.EffortBalanced), Label: "Balanced"},
				{ID: string(composer.EffortDeep), Label: "Deep"},
			}, curEff, dt, true); chosen != "" && onSelectEffort != nil {
				onSelectEffort(composer.ReasoningEffort(chosen))
			}
		})
	})
}
