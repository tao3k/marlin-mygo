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

// Flagship dual typography tokens: native OS system-ui (macOS SF Pro / iOS /
// Android Roboto) for crisp Control Center widgets, and tabular SF Mono / Menlo
// for clocks, code, and Emacs buffers.
const (
	FontDisplay = "system-ui"
	FontMono    = "SF Mono, Menlo, monospace"

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

// ConcentricRadius computes a harmonious inner corner radius matching industrial
// design concentric geometry (inner = max(outer - padding, 6)).
func ConcentricRadius(outer, pad float32) float32 {
	r := outer - pad
	if r < 6 {
		return 6
	}
	return r
}

// Eyebrow renders a crisp uppercase Control Center section eyebrow with
// refined positive optical letter-spacing.
func Eyebrow(c *ui.Context, text string, col ui.Color) ui.Element {
	return Line(c, text, TextSpec{Font: FontDisplay, Size: 10.5, Weight: 700, Color: col}).
		LetterSpacing(0.85)
}

// Headline renders a primary widget title in native SF Pro / system-ui with
// tight optical Display tracking.
func Headline(c *ui.Context, text string, size float32, col ui.Color) ui.Element {
	if size <= 0 {
		size = 15
	}
	el := Line(c, text, TextSpec{Font: FontDisplay, Size: size, Weight: 600, Color: col})
	if size >= 16 {
		el.LetterSpacing(-0.35)
	} else {
		el.LetterSpacing(-0.15)
	}
	return el
}

// DisplayFigure renders oversized hero numerals (clocks, primary KPIs) in
// native system-ui with tight display tracking and OpenType tabular numerals
// (tnum) so live ticking digits never jitter horizontally.
func DisplayFigure(c *ui.Context, text string, size float32, col ui.Color) ui.Element {
	if size <= 0 {
		size = 36
	}
	return Line(c, text, TextSpec{Font: FontDisplay, Size: size, Weight: 700, Color: col}).
		LetterSpacing(-1.1).FontFeatures("tnum")
}

// Caption renders a muted supporting line in clean native system-ui.
func Caption(c *ui.Context, text string, col ui.Color) ui.Element {
	return Line(c, text, TextSpec{Font: FontDisplay, Size: 11.5, Weight: 500, Color: col})
}

// MonoStat renders crisp OpenType tabular monospace figures (ratios, metrics,
// timestamps, shortcut badges) guaranteed never to jitter on update.
func MonoStat(c *ui.Context, text string, size float32, col ui.Color) ui.Element {
	if size <= 0 {
		size = 15
	}
	return Line(c, text, TextSpec{Font: FontMono, Size: size, Weight: 600, Color: col}).
		FontFeatures("tnum")
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

// ActionControl marks a non-text interactive control (toggle switch, tab pill,
// launcher tile, toolbar action) with:
//   - focus isolation (KeepFocus + no stray focus ring so Space never hijacks modal KeyMaps),
//   - native pointing-hand cursor (CursorPointer),
//   - tactile Linear/iOS physical press detent (scale 0.975 while pressed).
func ActionControl(el ui.Element) ui.Element {
	el.KeepFocus().FocusRing(false).Cursor(ui.CursorPointer)
	if el.Pressed() {
		el.Scale(0.975, 0.975)
	}
	return el
}

// ModalShortcut checks whether (mods, key) was pressed inside a modal keymap
// rooted at scope. Unmodified single-key bindings (except Escape) yield
// automatically whenever keyboard focus is in a text editor (c.FocusTakesText),
// while claiming the key on scope prevents any focused descendant ButtonBase
// from hijacking Space/keys before the host keymap runs.
func ModalShortcut(c *ui.Context, scope ui.Element, mods ui.Modifiers, key ui.Key) bool {
	if c == nil {
		return false
	}
	if mods == 0 && key != ui.KeyEscape && c.FocusTakesText() {
		return false
	}
	scoped := false
	if scope != (ui.Element{}) {
		scoped = scope.Shortcut(mods, key)
	}
	global := c.Shortcut(mods, key)
	return scoped || global
}

// ResolveRadius resolves a CSS variable radius from sheet with fallback.
func ResolveRadius(sheet *style.Sheet, varName string, fallback float32) float32 {
	if sheet == nil {
		return fallback
	}
	return sheet.VarPx(varName, fallback)
}
