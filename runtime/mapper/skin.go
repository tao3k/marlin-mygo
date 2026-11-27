// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package mapper is Marlin MyGo's Ratatui-equivalent declarative UI mapping
// framework. It projects host-neutral Scheme UI IR (schema.UIWorkbenchSnapshot:
// 13 UIElement primitives, width-aware UIStatusItem bars, UITransient Hydras,
// UIExtensionRegion buffers, UICommandSpec, and UIKeyBindingSpec) onto
// github.com/egoist/mygo/ui while delegating visual aesthetics to an injectable
// Skin owned by the downstream product (e.g., marlin-ui Desktop / Mobile / Web).
package mapper

import (
	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// Skin decouples product aesthetics (typography, radii, spacing, palette)
// from Scheme semantic IR mapping so Desktop (Doom Emacs), Mobile (HIG/M3),
// and Web WASM share one zero-duplication mapper.
type Skin struct {
	Font          string
	FontSizeBody  float32
	FontSizeUI    float32
	FontSizeMeta  float32
	RadiusSmall   float32
	RadiusControl float32
	BarHeight     float32
	RowHeight     float32

	Canvas          ui.Color
	Bar             ui.Color
	SurfaceElevated ui.Color
	SurfaceHover    ui.Color
	Border          ui.Color
	Ink             ui.Color
	InkMuted        ui.Color
	Accent          ui.Color
	Info            ui.Color
	Success         ui.Color
	Warning         ui.Color
	Danger          ui.Color
}

// SkinFromUITheme derives a clean, legible Doom-proportioned Skin directly from
// a Scheme-published schema.UITheme.
func SkinFromUITheme(mt schema.UITheme) Skin {
	isLight := int(mt.Canvas.R)+int(mt.Canvas.G)+int(mt.Canvas.B) > 380
	elev := ui.RGB(clampU8(int(mt.Canvas.R)+8), clampU8(int(mt.Canvas.G)+9), clampU8(int(mt.Canvas.B)+12))
	hover := ui.RGB(clampU8(int(mt.Canvas.R)+14), clampU8(int(mt.Canvas.G)+15), clampU8(int(mt.Canvas.B)+19))
	border := ui.RGB(clampU8(int(mt.Bar.R)+24), clampU8(int(mt.Bar.G)+26), clampU8(int(mt.Bar.B)+32))
	danger := ui.RGB(255, 108, 107)
	if isLight {
		elev = ui.RGB(240, 241, 245)
		hover = ui.RGB(232, 234, 240)
		border = ui.RGB(210, 214, 222)
		danger = ui.RGB(205, 52, 62)
	}
	return Skin{
		Font:            "SF Mono, Menlo, JetBrains Mono, monospace",
		FontSizeBody:    14,
		FontSizeUI:      13.5,
		FontSizeMeta:    12.5,
		RadiusSmall:     4,
		RadiusControl:   6,
		BarHeight:       28,
		RowHeight:       28,
		Canvas:          rgbColor(mt.Canvas),
		Bar:             rgbColor(mt.Bar),
		SurfaceElevated: elev,
		SurfaceHover:    hover,
		Border:          border,
		Ink:             rgbColor(mt.Ink),
		InkMuted:        rgbColor(mt.Muted),
		Accent:          rgbColor(mt.Accent),
		Info:            rgbColor(mt.Info),
		Success:         rgbColor(mt.Success),
		Warning:         rgbColor(mt.Warning),
		Danger:          danger,
	}
}

// ToneColor maps a host-neutral Scheme UITone into the Skin's palette.
func (s Skin) ToneColor(t schema.UITone) ui.Color {
	switch t {
	case schema.ToneCanvas:
		return s.Canvas
	case schema.ToneAccent:
		return s.Accent
	case schema.ToneInfo:
		return s.Info
	case schema.ToneSuccess:
		return s.Success
	case schema.ToneWarning:
		return s.Warning
	case schema.ToneMuted:
		return s.InkMuted
	default:
		return s.Ink
	}
}

// BackgroundColor resolves a Scheme UIBackground into a MyGo ui.Color.
func (s Skin) BackgroundColor(bg schema.UIBackground) (ui.Color, bool) {
	switch bg.Kind {
	case schema.BgCanvas:
		return s.Canvas, true
	case schema.BgBar:
		return s.Bar, true
	case schema.BgTone:
		return s.ToneColor(bg.Tone), true
	default:
		return ui.Transparent, false
	}
}

func rgbColor(c schema.RGB) ui.Color {
	return ui.RGB(c.R, c.G, c.B)
}

func clampU8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}
