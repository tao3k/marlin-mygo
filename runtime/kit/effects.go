// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package kit

import (
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/loading"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"
	"github.com/egoist/mygo/ui/shader"
)

// Kage GPU fragment shader rendering an Apple Intelligence-inspired drifting
// luminous glass orb (compiles natively to Metal, Direct3D 11, OpenGL, WebGL 2).
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
	t := Time * 0.9
	a := 0.5 + 0.5*sin(uv.x*2.4 + t + cos(uv.y*2.1 - t*0.7))
	b := 0.5 + 0.5*cos(uv.y*2.6 - t*1.1 + sin(uv.x*1.9 + t*0.5))
	col := mix(Primary.rgb, Secondary.rgb, a)
	col = mix(col, Tertiary.rgb, b*0.65)
	// Specular glass highlight near top-left + Fresnel rim glow.
	spec := smoothstep(0.62, 0.0, length(uv - vec2(-0.32, -0.34))) * 0.45
	rim := smoothstep(0.55, 0.98, r) * 0.35
	col = col + vec3(spec + rim)
	edge := 1.0 - smoothstep(0.92, 1.0, r)
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

// ThinkingOrb paints a GPU shader-driven drifting liquid glass AI sphere
// (inspired by Apple Intelligence) that accelerates gently when active.
func ThinkingOrb(c *ui.Context, key string, size float32, active, motionOn bool, pal Palette) ui.Element {
	if size <= 0 {
		size = 76
	}
	sh := getOrbShader()
	el := ui.Box(c.Key(key)).Width(size).Height(size).Radius(RadiusPill).
		Shadow(0, 8, 22, -4, WithAlpha(pal.Accent, 0.42))
	if sh == nil {
		return el.LinearGradient(ui.LinearGradient{
			From:  pal.Info,
			To:    pal.Accent,
			Angle: 135,
			Oklab: true,
		})
	}
	t := float32(0.8)
	if motionOn {
		elapsed := float32(time.Since(orbStart).Seconds())
		if active {
			t = elapsed * 2.4
		} else {
			t = elapsed * 0.55
		}
	}
	return el.Draw(func(p *ui.Painter, box ui.Rect) {
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

// SparkGauge renders an iOS / Material 3 Expressive capsule gauge with SF Symbol,
// overflow-safe Display label, tabular Mono metric, and Oklab gradient pill fill
// smoothly spring-animated when motionOn is true.
func SparkGauge(
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
	row := ui.ButtonBase(c.Key(key)).Label(label).
		Height(36).Padding(0, 8).Radius(10).Gap(10).Clip().
		AlignItems(ui.Center).Justify(ui.Start)
	if row.Hovered() {
		row.Background(WithAlpha(pal.Hover, 0.55))
	}

	pct := targetPct
	if motionOn {
		pct = row.Spring("gauge", targetPct)
		if pct < 0.06 {
			pct = 0.06
		}
		if pct > 1 {
			pct = 1
		}
	}

	row.Children(func() {
		Symbol(c, iconName, 14, tone)
		if label != "" {
			Line(c, label, TextSpec{Font: FontDisplay, Size: 12, Weight: 600, Color: pal.Muted}).Width(56)
		}
		ui.Row(c).Grow(1).MinWidth(0).Height(11).Radius(RadiusPill).
			Background(WithAlpha(pal.Bar, 0.72)).Clip().Children(func() {
			ui.Box(c).Grow(pct).Height(11).Radius(RadiusPill).
				LinearGradient(ui.LinearGradient{
					From:  tone,
					To:    Lighten(tone, 0.25),
					Angle: 90,
					Oklab: true,
				})
			if rem := 1 - pct; rem > 0.01 {
				ui.Box(c).Grow(rem).Height(11)
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
