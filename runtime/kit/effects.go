// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package kit

import (
	"math"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/loading"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"
	"github.com/egoist/mygo/ui/shader"
)

// Kage GPU fragment shader rendering an Apple Intelligence-inspired drifting
// luminous liquid-glass orb (compiles natively to Metal, Direct3D 11, OpenGL, WebGL 2).
const orbShaderSrc = `package main

var Time float
var Primary vec4
var Secondary vec4
var Tertiary vec4

func Fragment(dst vec4, pos vec2) vec4 {
	size := rectSize()
	uv := (pos / max(size, vec2(1, 1))) * 2.0 - vec2(1.0, 1.0)
	r := length(uv)
	if r > 1.0 {
		return vec4(0)
	}
	// Spherical glass refraction normal for authentic 3D volume.
	z := sqrt(max(0.0, 1.0 - r*r))
	t := Time * 0.85
	w1 := 0.5 + 0.5*sin(uv.x*2.6 + t + cos(uv.y*2.3 - t*0.75))
	w2 := 0.5 + 0.5*cos(uv.y*2.8 - t*1.05 + sin(uv.x*2.1 + t*0.55))
	w3 := 0.5 + 0.5*sin((uv.x-uv.y)*2.2 + t*0.9)
	col := mix(Primary.rgb, Secondary.rgb, w1)
	col = mix(col, Tertiary.rgb, w2*0.58)
	col = mix(col*0.78, col*1.12, w3*z)
	// Crisp top-left glass specular crescent + soft lower bounce + luminous Fresnel rim.
	spec := smoothstep(0.56, 0.0, length(uv - vec2(-0.34, -0.36))) * 0.58
	bounce := smoothstep(0.75, 0.0, length(uv - vec2(0.32, 0.44))) * 0.22
	rim := pow(smoothstep(0.45, 0.99, r), 1.8) * 0.52
	col = col + vec3(spec) + Primary.rgb*bounce + mix(Primary.rgb, vec3(1.0), 0.5)*rim
	edge := 1.0 - smoothstep(0.93, 1.0, r)
	return vec4(col*edge, edge)
}
`

var (
	orbOnce   sync.Once
	orbShader *shader.Shader
	orbStart  = time.Now()
)

func getOrbShader() *shader.Shader {
	orbOnce.Do(func() {
		if s, err := shader.Compile(orbShaderSrc); err == nil {
			orbShader = s
		}
	})
	return orbShader
}

// ThinkingOrb paints a flagship 3D-lit Metal/GPU Kage shader liquid-glass AI
// sphere surrounded by a luminous ambient halo.
func ThinkingOrb(c *ui.Context, key string, size float32, active, motionOn bool, pal Palette) ui.Element {
	if size <= 0 {
		size = 96
	}
	sh := getOrbShader()
	el := ui.Box(c.Key(key)).Width(size).Height(size).Radius(RadiusPill).
		BorderWidth(1).BorderColor(WithAlpha(ui.RGB(255, 255, 255), 0.22)).
		Shadow(0, 12, 32, -4, WithAlpha(pal.Accent, 0.56))
	if sh == nil {
		return el.LinearGradient(ui.LinearGradient{
			From:  pal.Info,
			To:    pal.Accent,
			Angle: 135,
			Oklab: true,
		})
	}
	t := float32(1.1)
	if motionOn {
		elapsed := float32(time.Since(orbStart).Seconds())
		if active {
			t = elapsed * 2.5
		} else {
			t = elapsed * 0.65
		}
	}
	return el.Draw(func(p *ui.Painter, box ui.Rect) {
		if !p.Accelerated() {
			p.FillGradient(box, ui.LinearGradient{
				From:  pal.Info,
				To:    pal.Accent,
				Angle: 135,
				Oklab: true,
			}, size/2)
			return
		}
		sh.Draw(p, box, shader.Options{
			Radius: size / 2,
			Uniforms: map[string]any{
				"Time":      t,
				"Primary":   pal.Info,
				"Secondary": pal.Accent,
				"Tertiary":  pal.Success,
			},
		})
	})
}

// RingGauge renders an iOS Control Center / Apple Watch style GPU vector
// circular activity ring (clockwise from 12 o'clock with rounded stroke caps,
// Oklab gradient sweep, centered tabular readout, and crisp bottom label).
func RingGauge(
	c *ui.Context,
	key, iconName, label, valueText string,
	targetPct float32,
	tone ui.Color,
	pal Palette,
	motionOn bool,
	onClick func(),
) ui.Element {
	if targetPct < 0.06 {
		targetPct = 0.06
	}
	if targetPct > 1 {
		targetPct = 1
	}
	tile := ActionControl(ui.ButtonBase(c.Key(key)).Label(label).
		Grow(1).MinWidth(0).MinHeight(0).Padding(8, 6).Radius(14).Clip().
		Justify(ui.Center).AlignItems(ui.Center))
	if valueText != "" {
		tile.Tooltip(label + " · " + valueText)
	}
	if tile.Hovered() {
		tile.Background(WithAlpha(pal.Hover, 0.55))
	}

	pct := targetPct
	if motionOn {
		pct = tile.Spring("ring-pct", targetPct)
		if pct < 0.06 {
			pct = 0.06
		}
		if pct > 1 {
			pct = 1
		}
	}

	const ringSize float32 = 58
	const strokeW float32 = 6.5

	tile.Children(func() {
		SafeCol(c, key+"-in", 6).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ring := ui.Row(c.Key(key+"-ring")).Width(ringSize).Height(ringSize).
				Justify(ui.Center).AlignItems(ui.Center).
				Draw(func(p *ui.Painter, box ui.Rect) {
					cx := box.X + box.W/2
					cy := box.Y + box.H/2
					rad := min(box.W, box.H)/2 - strokeW/2 - 1
					if rad <= 2 {
						return
					}
					var track ui.Path
					track.Circle(cx, cy, rad)
					p.StrokePath(&track, strokeW, WithAlpha(tone, 0.18))

					// Build smooth clockwise arc starting at 12 o'clock (-pi/2).
					const steps = 48
					n := int(math.Round(float64(steps) * float64(pct)))
					if n < 2 {
						n = 2
					}
					var arc ui.Path
					arc.Cap(ui.RoundCap)
					for i := 0; i <= n; i++ {
						ang := -math.Pi/2 + (2*math.Pi*float64(pct))*float64(i)/float64(n)
						x := cx + rad*float32(math.Cos(ang))
						y := cy + rad*float32(math.Sin(ang))
						if i == 0 {
							arc.MoveTo(x, y)
						} else {
							arc.LineTo(x, y)
						}
					}
					p.StrokePathGradient(&arc, strokeW, ui.LinearGradient{
						From:  Lighten(tone, 0.24),
						To:    tone,
						Angle: 135,
						Oklab: true,
					})
				})
			ring.Children(func() {
				if valueText != "" {
					MonoStat(c, valueText, 12.5, pal.Ink)
				} else if iconName != "" {
					Symbol(c, iconName, 16, tone)
				}
			})
			Line(c, label, TextSpec{Font: FontDisplay, Size: 11.5, Weight: 600, Color: pal.Muted})
		})
	})
	if tile.Clicked() && onClick != nil {
		onClick()
	}
	return tile
}

// AmbientArtwork renders a rounded-square layered Oklab GPU mesh artwork
// surface with drifting luminous bokeh highlights, diagonal glass specular
// catchlight, and centered Retina SF Symbol overlay.
func AmbientArtwork(c *ui.Context, key string, size, radius float32, active, motionOn bool, pal Palette) ui.Element {
	if size <= 0 {
		size = 104
	}
	if radius <= 0 {
		radius = 14
	}
	angle := float32(135)
	if motionOn {
		speed := int64(45)
		if active {
			speed = 14
		}
		angle = float32(int(time.Since(orbStart).Milliseconds()/speed) % 360)
	}
	el := ui.Row(c.Key(key)).Width(size).Height(size).Radius(radius).Clip().
		Justify(ui.Center).AlignItems(ui.Center).
		LinearGradient(ui.LinearGradient{
			From:  MixColor(pal.Bar, pal.Info, 0.52),
			To:    MixColor(pal.Bar, pal.Accent, 0.68),
			Angle: angle,
			Oklab: true,
		}).
		BorderWidth(1).BorderColor(WithAlpha(ui.RGB(255, 255, 255), 0.24)).
		InsetShadow(0, 1, 1.5, 0, WithAlpha(ui.RGB(255, 255, 255), 0.36)).
		Shadow(0, 10, 26, -6, WithAlpha(pal.Accent, 0.45))

	el.Children(func() {
		// Luminous drifting bokeh orbs + top-left diagonal glass sheen (GPU-native).
		ui.Box(c).Absolute().Fill().PassThrough().IgnoreInput(true).RadialGradient(ui.RadialGradient{
			Center: ui.Point{X: 0.26, Y: 0.28},
			Oklab:  true,
			Stops: []ui.GradientStop{
				{Color: WithAlpha(pal.Info, 0.56), At: 0},
				{Color: ui.Transparent, At: 0.72},
			},
		})
		ui.Box(c).Absolute().Fill().PassThrough().IgnoreInput(true).RadialGradient(ui.RadialGradient{
			Center: ui.Point{X: 0.78, Y: 0.76},
			Oklab:  true,
			Stops: []ui.GradientStop{
				{Color: WithAlpha(pal.Warning, 0.44), At: 0},
				{Color: ui.Transparent, At: 0.68},
			},
		})
		ui.Box(c).Absolute().Fill().PassThrough().IgnoreInput(true).LinearGradient(ui.LinearGradient{
			From:  WithAlpha(ui.RGB(255, 255, 255), 0.26),
			To:    ui.Transparent,
			Angle: 140,
		})
		Symbol(c, "sparkles", size*0.32, WithAlpha(ui.RGB(255, 255, 255), 0.94))
	})
	return el
}

// ActiveEdgeGlow overlays a non-interactive Apple Intelligence flowing conic
// gradient glow around an active/streaming card without blocking pointer input.
func ActiveEdgeGlow(c *ui.Context, key string, radius float32, active bool, pal Palette) {
	if !active {
		return
	}
	angle := float32(int(time.Since(orbStart).Milliseconds()/16) % 360)
	grad := ui.ConicGradient{
		Angle: angle,
		Stops: []ui.GradientStop{
			{Color: pal.Info, At: 0.0},
			{Color: pal.Accent, At: 0.35},
			{Color: pal.Danger, At: 0.68},
			{Color: pal.Info, At: 1.0},
		},
	}
	ui.Box(c.Key(key+"-glow")).Absolute().Fill().Radius(radius).
		BorderWidth(2).BorderConicGradient(grad).
		PassThrough().IgnoreInput(true)
}

// LoadingIndicator renders a crisp GPU loading micro-animation from mygo/plugins/loading.
func LoadingIndicator(c *ui.Context, size float32, col ui.Color, paused bool) ui.Element {
	if size <= 0 {
		size = 16
	}
	return loading.Arc(c, loading.Options{
		Size:   size,
		Color:  col,
		Paused: paused,
	})
}

// CapsuleGauge renders a chunky glowing horizontal capsule progress meter
// (20px tinted SF Symbol on the left + 16px inset rounded track + 120Hz spring
// Oklab gradient pill fill with soft colored glow and hover tooltip).
func CapsuleGauge(
	c *ui.Context,
	key, iconName, label, valueText string,
	targetPct float32,
	tone ui.Color,
	pal Palette,
	motionOn bool,
	onClick func(),
) ui.Element {
	if targetPct < 0.12 {
		targetPct = 0.12
	}
	if targetPct > 1 {
		targetPct = 1
	}
	tip := label
	if valueText != "" {
		tip = label + " · " + valueText
	}
	row := ActionControl(ui.ButtonBase(c.Key(key)).Label(label).
		Height(36).Padding(0, 6).Radius(10).Gap(14).Clip().
		AlignItems(ui.Center).Justify(ui.Start))
	if tip != "" {
		row.Tooltip(tip)
	}
	if row.Hovered() {
		row.Background(WithAlpha(pal.Hover, 0.45))
	}

	pct := targetPct
	if motionOn {
		pct = row.Spring("capsule-gauge", targetPct)
		if pct < 0.12 {
			pct = 0.12
		}
		if pct > 1 {
			pct = 1
		}
	}

	row.Children(func() {
		Symbol(c, iconName, 20, tone)
		ui.Row(c).Grow(1).MinWidth(0).Height(16).Radius(RadiusPill).
			Background(WithAlpha(pal.Bar, 0.78)).
			InsetShadow(0, 1, 2, 0, WithAlpha(ui.RGB(0, 0, 0), 0.36)).
			Clip().Children(func() {
			ui.Box(c).Grow(pct).Height(16).Radius(RadiusPill).
				LinearGradient(ui.LinearGradient{
					From:  tone,
					To:    Lighten(tone, 0.24),
					Angle: 90,
					Oklab: true,
				}).
				InsetShadow(0, 1, 1, 0, WithAlpha(ui.RGB(255, 255, 255), 0.32)).
				Shadow(0, 2, 10, 0, WithAlpha(tone, 0.45))
			if rem := 1 - pct; rem > 0.01 {
				ui.Box(c).Grow(rem).Height(16)
			}
		})
	})
	if row.Clicked() && onClick != nil {
		onClick()
	}
	return row
}

// SparkGauge renders an iOS / Material 3 Expressive labeled capsule gauge with
// SF Symbol, Display label, tabular Mono metric, and Oklab gradient pill fill.
func SparkGauge(
	c *ui.Context,
	key, iconName, label, valueText string,
	targetPct float32,
	tone ui.Color,
	pal Palette,
	motionOn bool,
	onClick func(),
) ui.Element {
	if targetPct < 0.08 {
		targetPct = 0.08
	}
	if targetPct > 1 {
		targetPct = 1
	}
	row := ActionControl(ui.ButtonBase(c.Key(key)).Label(label).
		Height(36).Padding(0, 8).Radius(10).Gap(10).Clip().
		AlignItems(ui.Center).Justify(ui.Start))
	if row.Hovered() {
		row.Background(WithAlpha(pal.Hover, 0.55))
	}

	pct := targetPct
	if motionOn {
		pct = row.Spring("gauge", targetPct)
		if pct < 0.08 {
			pct = 0.08
		}
		if pct > 1 {
			pct = 1
		}
	}

	row.Children(func() {
		Symbol(c, iconName, 16, tone)
		if label != "" {
			Line(c, label, TextSpec{Font: FontDisplay, Size: 12, Weight: 600, Color: pal.Muted}).Width(56)
		}
		ui.Row(c).Grow(1).MinWidth(0).Height(12).Radius(RadiusPill).
			Background(WithAlpha(pal.Bar, 0.76)).Clip().Children(func() {
			ui.Box(c).Grow(pct).Height(12).Radius(RadiusPill).
				LinearGradient(ui.LinearGradient{
					From:  tone,
					To:    Lighten(tone, 0.25),
					Angle: 90,
					Oklab: true,
				})
			if rem := 1 - pct; rem > 0.01 {
				ui.Box(c).Grow(rem).Height(12)
			}
		})
		if valueText != "" {
			MonoStat(c, valueText, 11.5, pal.Ink)
		}
	})
	if row.Clicked() && onClick != nil {
		onClick()
	}
	return row
}

// MiniTrendBars renders a compact, frameless GPU activity sparkline using mygo/ui/chart.
func MiniTrendBars(c *ui.Context, key string, values []float64, height float32, tone ui.Color) ui.Element {
	if len(values) == 0 {
		values = []float64{0.35, 0.55, 0.42, 0.78, 0.62, 0.90, 0.72}
	}
	if height <= 0 {
		height = 38
	}
	labels := make([]string, len(values))
	maxVal := 1.0
	for i, v := range values {
		labels[i] = ""
		if v > maxVal {
			maxVal = v
		}
	}
	x := chart.NewBand(labels...).Padding(0.28, 0.06)
	y := chart.NewLinear(0, maxVal)
	return chart.Plot(c.Key(key), chart.Axes{}, func(_ ui.Element) {
		for i, v := range values {
			if v < 0.08*maxVal {
				v = 0.08 * maxVal
			}
			col := WithAlpha(tone, 0.55)
			if i == len(values)-1 {
				col = tone
			}
			chart.Rect(c, x.Band(i), y.Span(0, v)).Background(col).Radius(4)
		}
	}).Height(height).MinWidth(0).Clip()
}
