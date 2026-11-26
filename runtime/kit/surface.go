// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package kit

import (
	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/runtime/style"
)

// DefaultGlassOpacity is the tuned VisionOS / Ghostty card translucency over
// hardware OS frosted vibrancy (clear desktop bleed-through with crisp contrast).
const DefaultGlassOpacity float32 = 0.34

// SurfaceOptions configures hot-pluggable GPU surface rendering (Frosted Glass
// Islands vs crisp Matte, glass card opacity in [0.10, 0.90], and 120Hz Spring Motion).
type SurfaceOptions struct {
	Glass        bool
	Motion       bool
	GlassOpacity float32 // 0 defaults to DefaultGlassOpacity (0.34); can also be set via CSS/tokens --glass-opacity
}

// ResolveGlassOpacity resolves the effective card glass opacity from opts,
// governed stylesheet/token variable `--glass-opacity`, or DefaultGlassOpacity.
func ResolveGlassOpacity(opts SurfaceOptions, sheet *style.Sheet) float32 {
	op := opts.GlassOpacity
	if op <= 0 && sheet != nil {
		op = sheet.VarPx("--glass-opacity", 0)
	}
	if op <= 0 {
		op = DefaultGlassOpacity
	}
	if op < 0.08 {
		op = 0.08
	}
	if op > 0.92 {
		op = 0.92
	}
	return op
}

// ApplyWindowTransparency configures c.Root() for genuine OS-level frosted
// glass translucency whenever glassEnabled is true and the host window has
// Vibrancy active (macOS NSVisualEffectView / Windows Acrylic).
// Returns true when OS desktop vibrancy is actively shining through.
func ApplyWindowTransparency(c *ui.Context, glassEnabled bool) bool {
	if c == nil {
		return false
	}
	if glassEnabled && c.Vibrancy() {
		c.Root().Background(ui.Transparent)
		return true
	}
	return false
}

// ChromeBarBg returns a seamless frosted-glass bar tint when glassEnabled and
// OS window vibrancy are active, or solid otherwise.
func ChromeBarBg(c *ui.Context, solid ui.Color, glassEnabled bool, alpha float32) ui.Color {
	if glassEnabled && c != nil && c.Vibrancy() {
		return WithAlpha(solid, alpha)
	}
	return solid
}

// ViewportBg returns a readable frosted-glass viewport tint when glassEnabled
// and OS window vibrancy are active, or solid otherwise.
func ViewportBg(c *ui.Context, solid ui.Color, glassEnabled bool, alpha float32) ui.Color {
	if glassEnabled && c != nil && c.Vibrancy() {
		return WithAlpha(solid, alpha)
	}
	return solid
}

// RenderGlassAtmosphere paints subtle Oklab aurora accents behind floating
// widget islands. When OS window vibrancy is active, no opaque veil is drawn
// so the hardware-blurred desktop shines cleanly between floating islands; in
// non-vibrant windows (Linux/Web/headless), a deep twilight gradient provides
// rich depth behind the surfaces.
func RenderGlassAtmosphere(c *ui.Context, pal Palette, opts SurfaceOptions) {
	if !opts.Glass {
		return
	}
	vibrant := c != nil && c.Vibrancy()
	if !vibrant {
		topCol := MixColor(pal.Canvas, pal.Info, 0.20)
		botCol := MixColor(pal.Bar, pal.Accent, 0.16)
		ui.Box(c.Key("kit-atm-base")).Absolute().Fill().ZIndex(-3).PassThrough().IgnoreInput(true).
			LinearGradient(ui.LinearGradient{From: topCol, To: botCol, Angle: 140, Oklab: true})
	}

	orbA := float32(0.09)
	orbB := float32(0.07)
	if !vibrant {
		orbA = 0.22
		orbB = 0.18
	}
	ui.Box(c.Key("kit-atm-orb1")).Absolute().Fill().ZIndex(-2).PassThrough().IgnoreInput(true).
		RadialGradient(ui.RadialGradient{
			Center: ui.Point{X: 0.12, Y: 0.14},
			Oklab:  true,
			Stops: []ui.GradientStop{
				{Color: WithAlpha(pal.Info, orbA), At: 0},
				{Color: ui.Transparent, At: 0.54},
			},
		})
	ui.Box(c.Key("kit-atm-orb2")).Absolute().Fill().ZIndex(-1).PassThrough().IgnoreInput(true).
		RadialGradient(ui.RadialGradient{
			Center: ui.Point{X: 0.88, Y: 0.86},
			Oklab:  true,
			Stops: []ui.GradientStop{
				{Color: WithAlpha(pal.Accent, orbB), At: 0},
				{Color: ui.Transparent, At: 0.58},
			},
		})
}

// ApplyCardSurface styles any Bento widget island with flagship VisionOS /
// macOS Sonoma frosted translucency (top-lit Oklab glass sheen + crisp 1px top
// specular catchlight + directional crystal hairline rim + deep floating
// ambient drop shadow) or velvety EWW Nord Matte elevation, plus optional
// 120Hz Spring hover lift.
func ApplyCardSurface(
	el ui.Element,
	classes []string,
	hovered bool,
	pal Palette,
	sheet *style.Sheet,
	opts SurfaceOptions,
) {
	st := style.StateNone
	if hovered {
		st |= style.StateHover
	}
	cs := sheet.Resolve(classes, st)
	radius := RadiusCard
	if cs.HasBorderRadius {
		radius = cs.BorderRadius
	}
	el.Radius(radius)

	if opts.Glass {
		baseOp := ResolveGlassOpacity(opts, sheet)
		topA := min(baseOp+0.08, 0.92)
		botA := max(baseOp-0.06, 0.06)
		if hovered {
			topA = min(topA+0.10, 0.95)
			botA = min(botA+0.10, 0.90)
		}
		if pal.IsLight {
			topA = min(topA+0.14, 0.94)
			botA = min(botA+0.10, 0.88)
		}
		topCol := WithAlpha(Lighten(pal.Surface, 0.10), topA)
		botCol := WithAlpha(pal.Surface, botA)
		if pal.IsLight {
			topCol = WithAlpha(ui.RGB(255, 255, 255), topA)
			botCol = WithAlpha(pal.Surface, botA)
		}
		el.LinearGradient(ui.LinearGradient{
			From:  topCol,
			To:    botCol,
			Angle: 155,
			Oklab: true,
		})

		// Directional VisionOS / macOS crystal hairline rim + single crisp drop shadow
		// (keeping GPU shadow batches lean for sub-8ms 120Hz tab/dashboard switching).
		rimTop := WithAlpha(ui.RGB(255, 255, 255), 0.24)
		rimBot := WithAlpha(ui.RGB(255, 255, 255), 0.06)
		if hovered {
			rimTop = WithAlpha(pal.Info, 0.65)
			rimBot = WithAlpha(pal.Accent, 0.24)
		}
		el.BorderWidth(1).BorderLinearGradient(ui.LinearGradient{
			From:  rimTop,
			To:    rimBot,
			Angle: 145,
			Oklab: true,
		})
		if hovered && opts.Motion {
			el.Shadow(0, 16, 36, -8, WithAlpha(ui.RGB(0, 0, 0), 0.50))
		} else {
			el.Shadow(0, 10, 26, -8, WithAlpha(ui.RGB(0, 0, 0), 0.36))
		}

		// Cursor-reactive VisionOS / Linear specular spotlight sheen on GPU hover.
		if px, py, over := el.PointerPosition(); over {
			spotColor := WithAlpha(ui.RGB(255, 255, 255), 0.11)
			if pal.IsLight {
				spotColor = WithAlpha(pal.Info, 0.12)
			}
			el.Draw(func(p *ui.Painter, box ui.Rect) {
				if box.W <= 0 || box.H <= 0 {
					return
				}
				p.FillRadialGradient(box, ui.RadialGradient{
					Center: ui.Point{X: px / box.W, Y: py / box.H},
					Oklab:  true,
					Stops: []ui.GradientStop{
						{Color: spotColor, At: 0},
						{Color: ui.Transparent, At: 0.58},
					},
				}, radius)
			})
		}
	} else {
		bg := pal.Surface
		if hovered {
			bg = pal.Hover
		}
		el.Background(bg)
		cs.ApplyToElement(el)
		el.BorderWidth(1).BorderColor(WithAlpha(ui.RGB(255, 255, 255), 0.10))
		el.Shadow(0, 8, 22, -8, WithAlpha(ui.RGB(0, 0, 0), 0.32))
	}

	if opts.Motion {
		targetLift := float32(0)
		if hovered {
			targetLift = -2.5
		}
		lift := el.Spring("kit-lift", targetLift)
		if lift != 0 {
			el.Translate(0, lift)
		}
	}
}
