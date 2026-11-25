// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package mygoshell binds marlin-mygo's transport-agnostic shell, UI IR, and
// pluginhost contracts onto github.com/egoist/mygo's native GPU UI and
// isolated Untrusted WebView primitives.
package mygoshell

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func toColor(c schema.RGB) ui.Color {
	return ui.RGB(c.R, c.G, c.B)
}

func adjustRGB(c schema.RGB, delta int) ui.Color {
	clamp := func(v int) uint8 {
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v)
	}
	return ui.RGB(clamp(int(c.R)+delta), clamp(int(c.G)+delta), clamp(int(c.B)+delta))
}

// StudioDarkPalette returns a refined macOS Obsidian-Graphite + Indigo palette
// engineered for high legibility, calm contrast, and hairline borders.
func StudioDarkPalette() schema.UITheme {
	return schema.UITheme{
		Canvas:  schema.RGB{R: 15, G: 17, B: 23},    // #0f1117 deep obsidian canvas
		Bar:     schema.RGB{R: 22, G: 26, B: 35},    // #161a23 elevated surface
		Ink:     schema.RGB{R: 235, G: 239, B: 247}, // #ebeff7 crisp primary text
		Accent:  schema.RGB{R: 124, G: 106, B: 247}, // #7c6af7 refined Linear indigo
		Info:    schema.RGB{R: 96, G: 165, B: 250},  // #60a5fa soft sky blue
		Success: schema.RGB{R: 74, G: 222, B: 128},  // #4ade80 clean emerald
		Warning: schema.RGB{R: 251, G: 191, B: 36},  // #fbbf24 warm amber
		Muted:   schema.RGB{R: 139, G: 148, B: 166}, // #8b94a6 balanced muted slate
	}
}

// StudioLightPalette returns a crisp macOS Paper-Snow + Indigo light theme
// with WCAG-compliant contrast ratios and hairline neutral borders.
func StudioLightPalette() schema.UITheme {
	return schema.UITheme{
		Canvas:  schema.RGB{R: 247, G: 248, B: 251}, // #f7f8fb paper canvas
		Bar:     schema.RGB{R: 255, G: 255, B: 255}, // #ffffff elevated surface
		Ink:     schema.RGB{R: 22, G: 26, B: 36},    // #161a24 crisp primary ink
		Accent:  schema.RGB{R: 99, G: 82, B: 232},   // #6352e8 rich indigo accent
		Info:    schema.RGB{R: 37, G: 99, B: 235},   // #2563eb clear cobalt
		Success: schema.RGB{R: 22, G: 163, B: 74},   // #16a34a clean emerald
		Warning: schema.RGB{R: 217, G: 119, B: 6},   // #d97706 warm amber
		Muted:   schema.RGB{R: 100, G: 110, B: 128}, // #646e80 balanced slate
	}
}

// ThemeTokensMap serializes a semantic UITheme into CSS custom property tokens
// (`--marlin-*`) delivered over ScopedEvent("theme.live") to WebView plugins.
func ThemeTokensMap(mt schema.UITheme) map[string]string {
	hex := func(c schema.RGB) string {
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	}
	return map[string]string{
		"--marlin-canvas":  hex(mt.Canvas),
		"--marlin-bar":     hex(mt.Bar),
		"--marlin-ink":     hex(mt.Ink),
		"--marlin-accent":  hex(mt.Accent),
		"--marlin-info":    hex(mt.Info),
		"--marlin-success": hex(mt.Success),
		"--marlin-warning": hex(mt.Warning),
		"--marlin-muted":   hex(mt.Muted),
	}
}

// ApplyMarlinTheme projects Marlin's 8-color semantic palette onto a cohesive,
// harmonious MyGo ui.Theme (including derived hover, pressed, border, radius,
// and contrast-checked AccentText so every GPU control matches the surface).
func ApplyMarlinTheme(c *ui.Context, mt schema.UITheme) *ui.Theme {
	isLight := int(mt.Canvas.R)+int(mt.Canvas.G)+int(mt.Canvas.B) > 384
	var th *ui.Theme
	if isLight {
		th = ui.LightTheme()
	} else {
		th = ui.DarkTheme()
	}
	th.Background = toColor(mt.Canvas)
	th.Surface = toColor(mt.Bar)
	if isLight {
		th.SurfaceHover = adjustRGB(mt.Bar, -10)
		th.SurfacePressed = adjustRGB(mt.Bar, -18)
		th.Border = adjustRGB(mt.Canvas, -22)
		th.InactiveAccent = adjustRGB(mt.Canvas, -28)
	} else {
		th.SurfaceHover = adjustRGB(mt.Bar, 11)
		th.SurfacePressed = adjustRGB(mt.Bar, 18)
		th.Border = adjustRGB(mt.Bar, 22)
		th.InactiveAccent = adjustRGB(mt.Bar, 26)
	}
	th.Text = toColor(mt.Ink)
	th.TextMuted = toColor(mt.Muted)
	th.Accent = toColor(mt.Accent)
	th.AccentHover = adjustRGB(mt.Accent, 14)
	th.AccentPressed = adjustRGB(mt.Accent, -14)
	th.AccentText = ui.RGB(255, 255, 255)
	th.Success = toColor(mt.Success)
	th.Warning = toColor(mt.Warning)
	th.Focus = toColor(mt.Accent)
	th.Radius = 8
	th.Spacing = 4
	th.FontSize = 13
	c.SetTheme(th)
	return th
}

// ToneColor maps a semantic UITone from Scheme UI IR onto the active Marlin palette.
func ToneColor(mt schema.UITheme, tone schema.UITone) ui.Color {
	switch tone {
	case schema.ToneCanvas:
		return toColor(mt.Canvas)
	case schema.ToneAccent:
		return toColor(mt.Accent)
	case schema.ToneInfo:
		return toColor(mt.Info)
	case schema.ToneSuccess:
		return toColor(mt.Success)
	case schema.ToneWarning:
		return toColor(mt.Warning)
	case schema.ToneMuted:
		return toColor(mt.Muted)
	default:
		return toColor(mt.Ink)
	}
}
