// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package kit provides Marlin MyGo's framework-level, overflow-proof native GPU
// UI design system (Metal, Direct3D 11, OpenGL, WebGL 2, and CPU fallback).
//
// Downstream Marlin applications (Desktop, Mobile, Web WASM, Agent Room, and
// user Bento widgets) build on kit's bounded containers, dual Display/Mono
// typography, iOS 26 Liquid Glass + M3 Expressive surface materials, native
// SF Symbols (with crisp SVG fallbacks), GPU fragment shaders, and chart
// sparklines so text overflow and rough unstyled controls are eliminated at the
// framework layer.
package kit

import (
	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/runtime/style"
)

// Flagship dual typography tokens: clean system Display Sans for widgets/cards,
// tabular Monospace for live clocks, code, and Emacs buffers.
const (
	FontDisplay = "SF Pro Display, -apple-system, Inter, system-ui, sans-serif"
	FontMono    = "SF Mono, Menlo, JetBrains Mono, monospace"

	RadiusCard    float32 = 20
	RadiusInner   float32 = 12
	RadiusControl float32 = 10
	RadiusSmall   float32 = 6
	RadiusPill    float32 = 999
)

// Palette supplies semantic surface & accent colors into kit primitives.
type Palette struct {
	IsLight bool
	Canvas  ui.Color
	Bar     ui.Color
	Surface ui.Color
	Hover   ui.Color
	Border  ui.Color
	Ink     ui.Color
	Muted   ui.Color
	Accent  ui.Color
	Info    ui.Color
	Success ui.Color
	Warning ui.Color
	Danger  ui.Color
}

// ToneColor maps a semantic tone name ("accent", "info", "success", "warning",
// "danger", "muted") to the corresponding Palette color.
func (p Palette) ToneColor(tone string) ui.Color {
	switch tone {
	case "accent":
		return p.Accent
	case "info":
		return p.Info
	case "success":
		return p.Success
	case "warning":
		return p.Warning
	case "danger", "error":
		return p.Danger
	case "muted":
		return p.Muted
	default:
		return p.Ink
	}
}

// TextSpec configures one framework-bounded text label.
type TextSpec struct {
	Font   string
	Size   float32
	Weight int
	Color  ui.Color
}

// SafeRow creates a horizontal flex container guaranteed never to overflow its
// parent bounds (MinWidth(0), MinHeight(0), Clip()).
func SafeRow(c *ui.Context, key string, gap float32) ui.Element {
	el := ui.Row(c)
	if key != "" {
		el = ui.Row(c.Key(key))
	}
	return el.MinWidth(0).MinHeight(0).Gap(gap).Clip()
}

// SafeCol creates a vertical flex container guaranteed never to overflow its
// parent bounds (MinWidth(0), MinHeight(0), Clip()).
func SafeCol(c *ui.Context, key string, gap float32) ui.Element {
	el := ui.Column(c)
	if key != "" {
		el = ui.Column(c.Key(key))
	}
	return el.MinWidth(0).MinHeight(0).Gap(gap).Clip()
}

// Line renders a single-line label with framework-enforced overflow safety
// (MinWidth(0) + Clip + Ellipsis + NoWrap + MaxLines(1)).
func Line(c *ui.Context, text string, sp TextSpec) ui.Element {
	font := sp.Font
	if font == "" {
		font = FontDisplay
	}
	size := sp.Size
	if size <= 0 {
		size = 13
	}
	weight := sp.Weight
	if weight <= 0 {
		weight = 500
	}
	return ui.Text(c, text).Font(font).FontSize(size).FontWeight(weight).
		TextColor(sp.Color).MinWidth(0).Clip().Ellipsis("...").NoWrap().MaxLines(1)
}

// FlexLine renders a growing single-line label inside a flex row with
// framework-enforced ellipsis truncation so it never collides with sibling buttons.
func FlexLine(c *ui.Context, text string, sp TextSpec) ui.Element {
	return Line(c, text, sp).Grow(1)
}

// Eyebrow renders a crisp iOS / Material 3 widget eyebrow label.
func Eyebrow(c *ui.Context, text string, col ui.Color) ui.Element {
	return Line(c, text, TextSpec{Font: FontDisplay, Size: 11, Weight: 700, Color: col})
}

// Headline renders a primary widget title in clean Display Sans.
func Headline(c *ui.Context, text string, size float32, col ui.Color) ui.Element {
	if size <= 0 {
		size = 15
	}
	return Line(c, text, TextSpec{Font: FontDisplay, Size: size, Weight: 700, Color: col})
}

// Caption renders a muted supporting line in clean Display Sans.
func Caption(c *ui.Context, text string, col ui.Color) ui.Element {
	return Line(c, text, TextSpec{Font: FontDisplay, Size: 11.5, Weight: 500, Color: col})
}

// MonoStat renders crisp tabular monospace figures (clocks, ratios, metrics).
func MonoStat(c *ui.Context, text string, size float32, col ui.Color) ui.Element {
	if size <= 0 {
		size = 16
	}
	return Line(c, text, TextSpec{Font: FontMono, Size: size, Weight: 700, Color: col})
}

// WithAlpha returns c with float32 alpha in [0, 1].
func WithAlpha(c ui.Color, alpha float32) ui.Color {
	return ui.RGBA(c.R, c.G, c.B, alpha)
}

// Lighten blends c toward white by t in [0, 1].
func Lighten(c ui.Color, t float32) ui.Color {
	return MixColor(c, ui.RGB(255, 255, 255), t)
}

// MixColor linearly interpolates between a and b.
func MixColor(a, b ui.Color, t float32) ui.Color {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	inv := 1 - t
	return ui.RGB(
		uint8(float32(a.R)*inv+float32(b.R)*t),
		uint8(float32(a.G)*inv+float32(b.G)*t),
		uint8(float32(a.B)*inv+float32(b.B)*t),
	)
}

// ResolveRadius resolves a CSS variable radius from sheet with fallback.
func ResolveRadius(sheet *style.Sheet, varName string, fallback float32) float32 {
	if sheet == nil {
		return fallback
	}
	return sheet.VarPx(varName, fallback)
}
