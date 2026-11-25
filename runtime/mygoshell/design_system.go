// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// AppleButtonVariant selects an Apple HIG / Liquid Glass or Material 3 pill button style.
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

// ApplePillButton renders a modern Apple HIG / Liquid Glass capsule button with
// subtle specular depth, zero harsh desktop borders, and clean typography.
func ApplePillButton(
	c *ui.Context,
	key string,
	label string,
	variant AppleButtonVariant,
	dt DerivedTheme,
	icon *ui.SVG,
	compact bool,
) ui.Element {
	padY := float32(9)
	padX := float32(15)
	fontSize := float32(13)
	if compact {
		padY = 6
		padX = 11
		fontSize = 12
	}

	btn := ui.ButtonBase(c.Key(key)).Label(label).Padding(padY, padX).Radius(999).
		AlignItems(ui.Center).Justify(ui.Center).Gap(6)

	var textColor ui.Color
	switch variant {
	case BtnPrimary:
		topCol := toColor(mixRGB(dt.Accent, schema.RGB{R: 255, G: 255, B: 255}, 0.12))
		botCol := toColor(dt.Accent)
		textColor = toColor(dt.InkOnAccent)
		btn.Gradient(topCol, botCol, 180).
			InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.28))
		if !compact {
			btn.Shadow(0, 3, 10, -2, rgbaFromRGB(dt.Accent, 0.34))
		}

	case BtnTinted:
		alpha := float32(0.15)
		if dt.Appearance == AppearanceLight {
			alpha = 0.11
		}
		textColor = toColor(dt.Accent)
		if dt.Appearance == AppearanceDark {
			textColor = toColor(mixRGB(dt.Accent, schema.RGB{R: 255, G: 255, B: 255}, 0.30))
		}
		btn.Background(rgbaFromRGB(dt.Accent, alpha)).
			Border(1, rgbaFromRGB(dt.Accent, 0.22))

	case BtnDestructive:
		alpha := float32(0.15)
		if dt.Appearance == AppearanceLight {
			alpha = 0.10
		}
		textColor = toColor(dt.Danger)
		btn.Background(rgbaFromRGB(dt.Danger, alpha)).
			Border(1, rgbaFromRGB(dt.Danger, 0.24))

	case BtnSuccess:
		alpha := float32(0.15)
		if dt.Appearance == AppearanceLight {
			alpha = 0.11
		}
		textColor = toColor(dt.Success)
		btn.Background(rgbaFromRGB(dt.Success, alpha)).
			Border(1, rgbaFromRGB(dt.Success, 0.24))

	default: // BtnGlass — 120Hz specular frosted capsule with crisp hover lift
		textColor = toColor(dt.Ink)
		fill := toColor(dt.SurfaceElevated)
		if btn.Hovered() {
			fill = toColor(dt.SurfaceHover)
		} else if dt.Appearance == AppearanceLight {
			fill = ui.RGBA(255, 255, 255, 0.92)
		}
		borderCol := glassBorderColor(dt)
		if btn.Hovered() {
			borderCol = rgbaFromRGB(dt.Accent, 0.36)
		}
		btn.Background(fill).
			Border(1, borderCol).
			InsetShadow(0, 1, 0, 0, glassTopSpecular(dt))
		if !compact {
			btn.Shadow(0, 2, 6, -1, ambientShadowColor(dt))
		}
	}

	btn.Children(func() {
		if icon != nil {
			sz := fontSize + 1
			ui.Icon(c, icon).Size(sz, sz).TextColor(textColor)
		}
		ui.Text(c, label).FontSize(fontSize).FontWeight(600).TextColor(textColor).NoWrap()
	})
	return btn
}

// AppleSwatchPill renders a sleek Apple Glass theme pill with a circular color dot.
func AppleSwatchPill(
	c *ui.Context,
	key string,
	label string,
	swatch schema.RGB,
	active bool,
	dt DerivedTheme,
) ui.Element {
	btn := ui.ButtonBase(c.Key(key)).Label(label).Padding(6, 11).Radius(999).
		AlignItems(ui.Center).Justify(ui.Center).Gap(6)
	var textColor ui.Color
	if active {
		topCol := toColor(mixRGB(dt.Accent, schema.RGB{R: 255, G: 255, B: 255}, 0.10))
		btn.Gradient(topCol, toColor(dt.Accent), 180).
			InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.25))
		textColor = toColor(dt.InkOnAccent)
	} else {
		fill := toColor(dt.SurfaceElevated)
		if dt.Appearance == AppearanceLight {
			fill = ui.RGBA(255, 255, 255, 0.92)
		}
		btn.Background(fill).
			Border(1, glassBorderColor(dt)).
			InsetShadow(0, 1, 0, 0, glassTopSpecular(dt))
		textColor = toColor(dt.Ink)
	}
	btn.Children(func() {
		ui.Box(c).Size(8, 8).Radius(4).Background(toColor(swatch)).
			Border(1, ui.RGBA(255, 255, 255, 0.35))
		ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(textColor).NoWrap()
	})
	return btn
}

// AppleCircleIconButton renders an iOS/macOS circular icon button (e.g. the
// iMessage / Apple Intelligence circular upward-arrow Send button).
func AppleCircleIconButton(
	c *ui.Context,
	key string,
	accessibleLabel string,
	icon *ui.SVG,
	size float32,
	dt DerivedTheme,
) ui.Element {
	topCol := toColor(mixRGB(dt.Accent, schema.RGB{R: 255, G: 255, B: 255}, 0.14))
	btn := ui.ButtonBase(c.Key(key)).Label(accessibleLabel).
		Size(size, size).Radius(size/2).
		AlignItems(ui.Center).Justify(ui.Center).
		Gradient(topCol, toColor(dt.Accent), 180).
		InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.30)).
		Shadow(0, 3, 10, -2, rgbaFromRGB(dt.Accent, 0.42))
	btn.Children(func() {
		iconSz := size * 0.50
		ui.Icon(c, icon).Size(iconSz, iconSz).TextColor(toColor(dt.InkOnAccent))
	})
	return btn
}

// AppleSegmentedControl renders an authentic Apple UISegmentedControl / macOS
// pill segmented bar with an elevated floating thumb. Returns the clicked item ID.
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
		trackBg = ui.RGBA(15, 23, 42, 0.06)
	}
	pillBg := toColor(dt.SurfaceElevated)
	if dt.Appearance == AppearanceLight {
		pillBg = ui.RGB(255, 255, 255)
	}
	padY := float32(5)
	padX := float32(11)
	fontSize := float32(12)
	if compact {
		padY = 4
		padX = 9
		fontSize = 11.5
	}

	var clickedID string
	ui.Row(c.Key(keyPrefix+"-track")).Padding(3).Gap(2).Radius(999).
		Background(trackBg).Border(1, glassBorderColor(dt)).AlignItems(ui.Center).Children(func() {
		for _, item := range items {
			selected := item.ID == activeID
			seg := ui.ButtonBase(c.Key(keyPrefix+"-"+item.ID)).Label(item.Label).
				Padding(padY, padX).Radius(999).AlignItems(ui.Center).Justify(ui.Center)
			txtCol := toColor(dt.InkMuted)
			weight := 500
			if selected {
				txtCol = toColor(dt.Ink)
				weight = 600
				seg.Background(pillBg).
					Border(1, glassBorderColor(dt)).
					InsetShadow(0, 1, 0, 0, glassTopSpecular(dt)).
					Shadow(0, 2, 6, -1, ambientShadowColor(dt))
			} else {
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
	return clickedID
}

// AppleGlassCard renders a modern 120Hz Apple elevated frosted surface with smooth
// corner radius, specular top highlight, hairline rim, and ambient depth shadow.
func AppleGlassCard(c *ui.Context, key string, dt DerivedTheme, radius float32, padding float32, gap float32, body func()) {
	bg := toColor(dt.SurfaceElevated)
	if dt.Appearance == AppearanceLight {
		bg = ui.RGB(255, 255, 255)
	}
	col := ui.Column(c)
	if key != "" {
		col = ui.Column(c.Key(key))
	}
	col.Padding(padding).Gap(gap).Radius(radius).AlignItems(ui.Stretch).
		Background(bg).
		Border(1, glassBorderColor(dt)).
		InsetShadow(0, 1, 0, 0, glassTopSpecular(dt)).
		Shadow(0, 2, 6, -1, ambientShadowColor(dt)).
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

// AppleBadgePill renders a sleek semantic status pill (e.g. "1 GATE", "mTLS live").
func AppleBadgePill(c *ui.Context, label string, tone schema.RGB, dt DerivedTheme) {
	alpha := float32(0.16)
	if dt.Appearance == AppearanceLight {
		alpha = 0.12
	}
	ui.Row(c).Padding(4, 9).Radius(999).Gap(5).AlignItems(ui.Center).
		Background(rgbaFromRGB(tone, alpha)).
		Border(1, rgbaFromRGB(tone, 0.28)).Children(func() {
		ui.Box(c).Size(6, 6).Radius(3).Background(toColor(tone))
		ui.Text(c, label).FontSize(10.5).FontWeight(700).TextColor(toColor(tone)).NoWrap()
	})
}
