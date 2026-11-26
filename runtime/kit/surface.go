// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package kit

import (
	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/runtime/style"
)

// SurfaceOptions configures hot-pluggable GPU surface rendering (Liquid Glass
// vs crisp Matte, and 120Hz Spring Motion).
type SurfaceOptions struct {
	Glass  bool
	Motion bool
}

// ApplyWindowTransparency configures c.Root() for genuine OS-level frosted
// glass translucency whenever glassEnabled is true and the host window has
// Vibrancy active (macOS NSVisualEffectView / Liquid Glass, Windows Acrylic).
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

// RenderGlassAtmosphere paints whisper-light Oklab aurora accents behind
// Liquid Glass widgets. When OS window vibrancy is active, alpha stays ultra-low
// so the user's actual desktop wallpaper refracts clearly through the glass;
// in non-vibrant windows (Linux/Web/headless), a richer twilight backdrop gives
// the Liquid Glass shader lush depth to refract.
func RenderGlassAtmosphere(c *ui.Context, pal Palette, opts SurfaceOptions) {
	if !opts.Glass {
		return
	}
	vibrant := c != nil && c.Vibrancy()
	if !vibrant {
		topCol := MixColor(pal.Canvas, pal.Info, 0.22)
		botCol := MixColor(pal.Bar, pal.Accent, 0.18)
		ui.Box(c.Key("kit-atm-base")).Absolute().Fill().ZIndex(-3).PassThrough().IgnoreInput(true).
			LinearGradient(ui.LinearGradient{From: topCol, To: botCol, Angle: 138, Oklab: true})
	} else {
		// Ultra-sheer tint over real macOS desktop vibrancy for legibility without hiding wallpaper.
		ui.Box(c.Key("kit-atm-veil")).Absolute().Fill().ZIndex(-3).PassThrough().IgnoreInput(true).
			Background(WithAlpha(pal.Canvas, 0.16))
	}

	orbA := float32(0.14)
	orbB := float32(0.12)
	if !vibrant {
		orbA = 0.24
		orbB = 0.22
	}
	ui.Box(c.Key("kit-atm-orb1")).Absolute().Fill().ZIndex(-2).PassThrough().IgnoreInput(true).
		RadialGradient(ui.RadialGradient{
			Center: ui.Point{X: 0.14, Y: 0.16},
			Oklab:  true,
			Stops: []ui.GradientStop{
				{Color: WithAlpha(pal.Info, orbA), At: 0},
				{Color: ui.Transparent, At: 0.58},
			},
		})
	ui.Box(c.Key("kit-atm-orb2")).Absolute().Fill().ZIndex(-1).PassThrough().IgnoreInput(true).
		RadialGradient(ui.RadialGradient{
			Center: ui.Point{X: 0.86, Y: 0.82},
			Oklab:  true,
			Stops: []ui.GradientStop{
				{Color: WithAlpha(pal.Accent, orbB), At: 0},
				{Color: ui.Transparent, At: 0.62},
			},
		})
}

// ApplyCardSurface styles any widget card with flagship iOS 26 Liquid Glass
// (clear glass shader + directional top-left specular BorderLinearGradient rim
// + inner top highlight + deep ambient shadow) or crisp M3 Expressive Matte
// elevation, plus optional 120Hz Spring hover lift.
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
		tintAlpha := float32(0.18)
		if hovered {
			tintAlpha = 0.28
		}
		if pal.IsLight {
			tintAlpha = 0.34
		}
		el.Material(glass.Glass{
			Style:       glass.Clear,
			Tint:        WithAlpha(pal.Surface, tintAlpha),
			Interactive: true,
			Clarity:     0.64,
		})
		// Directional iOS 26 crystal specular rim: bright top-left light fading diagonally.
		rimTop := WithAlpha(ui.RGB(255, 255, 255), 0.36)
		rimBot := WithAlpha(ui.RGB(255, 255, 255), 0.06)
		if hovered {
			rimTop = WithAlpha(pal.Info, 0.68)
			rimBot = WithAlpha(pal.Accent, 0.24)
		}
		el.BorderWidth(1).BorderLinearGradient(ui.LinearGradient{
			From:  rimTop,
			To:    rimBot,
			Angle: 145,
			Oklab: true,
		})
		el.InsetShadow(0, 1, 1.5, 0, WithAlpha(ui.RGB(255, 255, 255), 0.22))
		if hovered && opts.Motion {
			el.Shadow(0, 16, 36, -8, WithAlpha(ui.RGB(0, 0, 0), 0.38))
		} else {
			el.Shadow(0, 10, 26, -8, WithAlpha(ui.RGB(0, 0, 0), 0.26))
		}
	} else {
		bg := pal.Surface
		if hovered {
			bg = pal.Hover
		}
		el.Background(bg)
		cs.ApplyToElement(el)
		el.BorderWidth(1).BorderLinearGradient(ui.LinearGradient{
			From:  WithAlpha(ui.RGB(255, 255, 255), 0.10),
			To:    WithAlpha(ui.RGB(255, 255, 255), 0.02),
			Angle: 180,
		})
		el.Shadow(0, 8, 20, -8, WithAlpha(ui.RGB(0, 0, 0), 0.24))
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
