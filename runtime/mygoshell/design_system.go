// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// Shared logical corner radii for crisp native desktop chrome (inspired by
// Sheprd / Linear / Ghostty: tight architectural geometry rather than giant pills).
const (
	RadiusPanel   float32 = 12
	RadiusControl float32 = 8
	RadiusSmall   float32 = 4
)

// AppleButtonVariant selects a clean, theme-token-driven button style.
type AppleButtonVariant uint8

const (
	BtnPrimary AppleButtonVariant = iota
	BtnTinted
	BtnGlass
	BtnDestructive
	BtnSuccess
)

// SegmentedItem describes one option inside an Apple-style segmented control.
type SegmentedItem struct {
	ID    string
	Label string
}

// Pre-parsed SF-Symbols-inspired crisp 24x24 vector icons (parsed once at init; zero per-frame alloc).
var (
	svgIconChat = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/></svg>`,
	))
	svgIconShield = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/><path d="M9 12l2 2 4-4"/></svg>`,
	))
	svgIconBolt = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/></svg>`,
	))
	svgIconSparkles = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9L12 3z"/><path d="M19 3v4"/><path d="M21 5h-4"/></svg>`,
	))
	svgIconArrowUp = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="19" x2="12" y2="5"/><polyline points="5 12 12 5 19 12"/></svg>`,
	))
	svgIconCheck = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>`,
	))
	svgIconXMark = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>`,
	))
	svgIconSplitRight = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2"/><line x1="12" y1="4" x2="12" y2="20"/></svg>`,
	))
	svgIconSplitBelow = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2"/><line x1="3" y1="12" x2="21" y2="12"/></svg>`,
	))
	svgIconSwap = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="17 2 21 6 17 10"/><path d="M3 6h18"/><polyline points="7 22 3 18 7 14"/><path d="M21 18H3"/></svg>`,
	))
	svgIconExpand = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 3 21 3 21 9"/><polyline points="9 21 3 21 3 15"/><line x1="21" y1="3" x2="14" y2="10"/><line x1="3" y1="21" x2="10" y2="14"/></svg>`,
	))
	svgIconUndo = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="9 14 4 9 9 4"/><path d="M20 20v-7a4 4 0 0 0-4-4H4"/></svg>`,
	))
	svgIconRedo = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 14 20 9 15 4"/><path d="M4 20v-7a4 4 0 0 1 4-4h12"/></svg>`,
	))
)

func rgbaFromRGB(c schema.RGB, alpha float32) ui.Color {
	return ui.RGBA(c.R, c.G, c.B, alpha)
}

func glassBorderColor(dt DerivedTheme) ui.Color {
	if dt.Appearance == AppearanceLight {
		return ui.RGBA(15, 23, 42, 0.08)
	}
	return ui.RGBA(255, 255, 255, 0.11)
}

func glassTopSpecular(dt DerivedTheme) ui.Color {
	if dt.Appearance == AppearanceLight {
		return ui.RGBA(255, 255, 255, 0.85)
	}
	return ui.RGBA(255, 255, 255, 0.13)
}

func ambientShadowColor(dt DerivedTheme) ui.Color {
	if dt.Appearance == AppearanceLight {
		return ui.RGBA(15, 23, 42, 0.08)
	}
	return ui.RGBA(0, 0, 0, 0.34)
}

// ApplePillButton renders a crisp, restrained, theme-token-driven control button
// with architectural 8px corners, flat surfaces, and quiet 1px hairline borders.
func ApplePillButton(
	c *ui.Context,
	key string,
	label string,
	variant AppleButtonVariant,
	dt DerivedTheme,
	icon *ui.SVG,
	compact bool,
) ui.Element {
	padY := float32(7)
	padX := float32(12)
	fontSize := float32(12.5)
	radius := RadiusControl
	if compact {
		padY = 4
		padX = 9
		fontSize = 11.5
		radius = 6
	}

	btn := ui.ButtonBase(c.Key(key)).Label(label).Padding(padY, padX).Radius(radius).
		AlignItems(ui.Center).Justify(ui.Center).Gap(6)

	var textColor ui.Color
	switch variant {
	case BtnPrimary:
		fill := toColor(dt.Accent)
		if btn.Hovered() {
			fill = toColor(dt.AccentHover)
		}
		textColor = toColor(dt.InkOnAccent)
		btn.Background(fill).Border(1, rgbaFromRGB(dt.Accent, 0.35))

	case BtnTinted:
		alpha := float32(0.12)
		if dt.Appearance == AppearanceLight {
			alpha = 0.09
		}
		if btn.Hovered() {
			alpha += 0.06
		}
		textColor = toColor(dt.Ink)
		btn.Background(rgbaFromRGB(dt.Accent, alpha)).
			Border(1, rgbaFromRGB(dt.Accent, 0.28))

	case BtnDestructive:
		alpha := float32(0.12)
		if dt.Appearance == AppearanceLight {
			alpha = 0.09
		}
		textColor = toColor(dt.Danger)
		btn.Background(rgbaFromRGB(dt.Danger, alpha)).
			Border(1, rgbaFromRGB(dt.Danger, 0.28))

	case BtnSuccess:
		alpha := float32(0.12)
		if dt.Appearance == AppearanceLight {
			alpha = 0.09
		}
		textColor = toColor(dt.Success)
		btn.Background(rgbaFromRGB(dt.Success, alpha)).
			Border(1, rgbaFromRGB(dt.Success, 0.28))

	default: // BtnGlass — clean flat neutral control surface with quiet 1px hairline border
		textColor = toColor(dt.Ink)
		fill := toColor(dt.SurfaceElevated)
		borderCol := toColor(dt.Border)
		if btn.Hovered() {
			fill = toColor(dt.SurfaceHover)
			borderCol = toColor(dt.BorderStrong)
		} else if dt.Appearance == AppearanceLight {
			fill = ui.RGB(255, 255, 255)
		}
		btn.Background(fill).Border(1, borderCol)
	}

	btn.Children(func() {
		if icon != nil {
			sz := fontSize + 1
			ui.Icon(c, icon).Size(sz, sz).TextColor(textColor)
		}
		ui.Text(c, label).FontSize(fontSize).FontWeight(500).TextColor(textColor).NoWrap()
	})
	return btn
}

// AppleIconPillButton renders a compact shadowless 120Hz vector icon capsule
// (used on Emacs pane modelines so split/swap/zoom/undo controls stay crisp
// with accessible labels for keyboard and headless UI testing).
func AppleIconPillButton(
	c *ui.Context,
	key string,
	accessibleLabel string,
	variant AppleButtonVariant,
	dt DerivedTheme,
	icon *ui.SVG,
) ui.Element {
	btn := ui.ButtonBase(c.Key(key)).Label(accessibleLabel).Padding(5, 7).Radius(8).
		AlignItems(ui.Center).Justify(ui.Center)

	var textColor ui.Color
	switch variant {
	case BtnTinted:
		alpha := float32(0.16)
		if dt.Appearance == AppearanceLight {
			alpha = 0.11
		}
		textColor = toColor(dt.Accent)
		if dt.Appearance == AppearanceDark {
			textColor = toColor(mixRGB(dt.Accent, schema.RGB{R: 255, G: 255, B: 255}, 0.30))
		}
		btn.Background(rgbaFromRGB(dt.Accent, alpha)).Border(1, rgbaFromRGB(dt.Accent, 0.24))
	case BtnDestructive:
		alpha := float32(0.14)
		if dt.Appearance == AppearanceLight {
			alpha = 0.10
		}
		textColor = toColor(dt.Danger)
		btn.Background(rgbaFromRGB(dt.Danger, alpha)).Border(1, rgbaFromRGB(dt.Danger, 0.24))
	default:
		textColor = toColor(dt.InkSecondary)
		fill := toColor(dt.SurfaceElevated)
		if btn.Hovered() {
			fill = toColor(dt.SurfaceHover)
			textColor = toColor(dt.Ink)
		} else if dt.Appearance == AppearanceLight {
			fill = ui.RGBA(255, 255, 255, 0.90)
		}
		borderCol := glassBorderColor(dt)
		if btn.Hovered() {
			borderCol = rgbaFromRGB(dt.Accent, 0.36)
		}
		btn.Background(fill).Border(1, borderCol)
	}

	btn.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).Size(12.5, 12.5).TextColor(textColor)
		} else {
			ui.Text(c, accessibleLabel).FontSize(11).FontWeight(600).TextColor(textColor).NoWrap()
		}
	})
	return btn
}

// AppleSwatchPill renders a crisp, restrained theme selector button with a circular swatch dot.
func AppleSwatchPill(
	c *ui.Context,
	key string,
	label string,
	swatch schema.RGB,
	active bool,
	dt DerivedTheme,
) ui.Element {
	btn := ui.ButtonBase(c.Key(key)).Label(label).Padding(4, 10).Radius(6).
		AlignItems(ui.Center).Justify(ui.Center).Gap(6)
	var textColor ui.Color
	if active {
		btn.Background(toColor(dt.Accent)).Border(1, rgbaFromRGB(dt.Accent, 0.35))
		textColor = toColor(dt.InkOnAccent)
	} else {
		fill := toColor(dt.SurfaceElevated)
		if btn.Hovered() {
			fill = toColor(dt.SurfaceHover)
		} else if dt.Appearance == AppearanceLight {
			fill = ui.RGB(255, 255, 255)
		}
		btn.Background(fill).Border(1, toColor(dt.Border))
		textColor = toColor(dt.Ink)
	}
	btn.Children(func() {
		ui.Box(c).Size(7, 7).Radius(4).Background(toColor(swatch))
		ui.Text(c, label).FontSize(11.5).FontWeight(500).TextColor(textColor).NoWrap()
	})
	return btn
}

// AppleCircleIconButton renders a clean circular action icon button (e.g. Send).
func AppleCircleIconButton(
	c *ui.Context,
	key string,
	accessibleLabel string,
	icon *ui.SVG,
	size float32,
	dt DerivedTheme,
) ui.Element {
	btn := ui.ButtonBase(c.Key(key)).Label(accessibleLabel).
		Size(size, size).Radius(size/2).
		AlignItems(ui.Center).Justify(ui.Center).
		Background(toColor(dt.Accent))
	btn.Children(func() {
		iconSz := size * 0.48
		ui.Icon(c, icon).Size(iconSz, iconSz).TextColor(toColor(dt.InkOnAccent))
	})
	return btn
}

// AppleSegmentedControl renders a compact, fit-content segmented control with
// crisp architectural 8px outer / 6px active-tab corners (never stretched across
// full dialog width). Returns the clicked item ID.
func AppleSegmentedControl(
	c *ui.Context,
	keyPrefix string,
	items []SegmentedItem,
	activeID string,
	dt DerivedTheme,
	compact bool,
) string {
	trackBg := toColor(dt.SurfaceRecessed)
	if dt.Appearance == AppearanceLight {
		trackBg = ui.RGBA(15, 23, 42, 0.05)
	}
	pillBg := toColor(dt.SurfaceElevated)
	if dt.Appearance == AppearanceLight {
		pillBg = ui.RGB(255, 255, 255)
	}
	padY := float32(4)
	padX := float32(10)
	fontSize := float32(12)
	if compact {
		padY = 3
		padX = 9
		fontSize = 11.5
	}

	var clickedID string
	ui.Row(c.Key(keyPrefix+"-wrap")).AlignItems(ui.Center).Children(func() {
		ui.Row(c.Key(keyPrefix+"-track")).Padding(2).Gap(2).Radius(RadiusControl).
			Background(trackBg).Border(1, toColor(dt.BorderSubtle)).AlignItems(ui.Center).Children(func() {
			for _, item := range items {
				selected := item.ID == activeID
				seg := ui.ButtonBase(c.Key(keyPrefix+"-"+item.ID)).Label(item.Label).
					Padding(padY, padX).Radius(6).AlignItems(ui.Center).Justify(ui.Center)
				txtCol := toColor(dt.InkMuted)
				weight := 500
				if selected {
					txtCol = toColor(dt.Ink)
					weight = 600
					seg.Background(pillBg).Border(1, toColor(dt.Border))
				} else {
					if seg.Hovered() {
						txtCol = toColor(dt.Ink)
					}
					seg.Background(ui.Transparent)
				}
				seg.Children(func() {
					ui.Text(c, item.Label).FontSize(fontSize).FontWeight(weight).TextColor(txtCol).NoWrap()
				})
				if seg.Clicked() {
					clickedID = item.ID
				}
			}
		})
	})
	return clickedID
}

// AppleGlassCard renders a quiet, flat elevated surface container with crisp
// 1px hairline border and zero noisy drop shadows.
func AppleGlassCard(c *ui.Context, key string, dt DerivedTheme, radius float32, padding float32, gap float32, body func()) {
	bg := toColor(dt.Surface)
	if dt.Appearance == AppearanceLight {
		bg = ui.RGB(255, 255, 255)
	}
	if radius > RadiusPanel {
		radius = RadiusPanel
	}
	col := ui.Column(c)
	if key != "" {
		col = ui.Column(c.Key(key))
	}
	col.Padding(padding).Gap(gap).Radius(radius).AlignItems(ui.Stretch).
		Background(bg).
		Border(1, toColor(dt.Border)).
		Children(body)
}

// AppleLiquidGlassSurface returns a configured MyGo Liquid Glass material
// (using macOS 26 NSGlassEffectView when native=true and supported, or GPU shader everywhere).
func AppleLiquidGlassSurface(dt DerivedTheme, native bool, interactive bool) glass.Glass {
	tint := rgbaFromRGB(dt.SurfaceElevated, 0.64)
	if dt.Appearance == AppearanceLight {
		tint = ui.RGBA(255, 255, 255, 0.76)
	}
	return glass.Glass{
		Style:       glass.Regular,
		Tint:        tint,
		Interactive: interactive,
		Native:      native,
		Clarity:     0.2,
	}
}

// AppleBadgePill renders a quiet, compact 4px semantic inline tag.
func AppleBadgePill(c *ui.Context, label string, tone schema.RGB, dt DerivedTheme) {
	alpha := float32(0.12)
	if dt.Appearance == AppearanceLight {
		alpha = 0.09
	}
	ui.Row(c).Padding(2, 6).Radius(RadiusSmall).AlignItems(ui.Center).
		Background(rgbaFromRGB(tone, alpha)).
		Border(1, rgbaFromRGB(tone, 0.24)).Children(func() {
		ui.Text(c, label).FontSize(10).FontWeight(600).TextColor(toColor(tone)).NoWrap()
	})
}
