// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package kit

import "github.com/egoist/mygo/ui"

// RailItem describes one navigation or action entry inside EdgeRail.
type RailItem struct {
	ID      string
	Icon    string
	Label   string
	Tone    ui.Color
	Active  bool
	OnClick func()
}

// EdgeRailConfig configures a floating auto-hide left-edge glass sidebar that
// stays tucked away as a whisper-thin translucent edge indicator and glides
// smoothly into view on left-edge hover (or when pinned) using 120Hz spring
// physics, disappearing automatically when the pointer leaves.
//
// Downstream apps and custom sidebar widgets can supply Primary/Utility icon
// lists or a full CustomContent builder for richer multi-section sidebars.
type EdgeRailConfig struct {
	Key           string
	Width         float32 // defaults to 58 DIPs for a sleek icon dock
	Primary       []RailItem
	Utility       []RailItem
	CustomContent func(c *ui.Context) // optional custom sidebar body
	Pinned        bool
	OnToggle      func()
	Palette       Palette
	Surface       SurfaceOptions
}

// EdgeRail renders a floating auto-hide left-edge glass sidebar over the host
// viewport without causing layout reflow.
func EdgeRail(c *ui.Context, cfg EdgeRailConfig) {
	key := cfg.Key
	if key == "" {
		key = "kit-edge-rail"
	}
	pal := cfg.Palette
	panelW := cfg.Width
	if panelW <= 0 {
		panelW = 58
	}

	// Slim hover sensor along the left edge (16px wide when hidden, panelW+28 when
	// revealed) so the pointer glides smoothly from the screen edge onto controls
	// and auto-hides the moment the pointer leaves.
	sensor := ui.Box(c.Key(key + "-sensor")).Absolute().Left(0).Top(0).Bottom(0).ZIndex(30)
	revealed := cfg.Pinned || sensor.Hovered()
	if revealed {
		sensor.Width(panelW + 28)
	} else {
		sensor.Width(16)
	}

	// Smooth 120Hz spring slide and opacity interpolation.
	targetX := -(panelW + 18)
	targetOpacity := float32(0)
	handleOpacity := float32(0.75)
	if revealed {
		targetX = 10
		targetOpacity = 1
		handleOpacity = 0
	}
	slideX := targetX
	panelAlpha := targetOpacity
	if cfg.Surface.Motion {
		slideX = sensor.Spring("rail-x", targetX)
		panelAlpha = sensor.Spring("rail-a", targetOpacity)
	}

	sensor.Children(func() {
		// Resting whisper-thin edge indicator pill centered vertically along the left edge.
		if handleOpacity > 0.05 {
			ui.Column(c).Absolute().Left(3).Top(0).Bottom(0).Width(8).
				Justify(ui.Center).AlignItems(ui.Center).PassThrough().IgnoreInput(true).Children(func() {
				ui.Box(c.Key(key+"-pill")).Width(4).Height(56).Radius(RadiusPill).
					Background(WithAlpha(pal.Info, 0.52)).
					Shadow(0, 0, 12, 0, WithAlpha(pal.Info, 0.45))
			})
		}

		// Floating frosted glass island rail (visible while revealed or animating out).
		if revealed || panelAlpha > 0.04 {
			panel := ui.Column(c.Key(key+"-panel")).Absolute().Left(0).Top(12).Bottom(12).Width(panelW).
				Radius(20).Padding(10, 0).Gap(8).AlignItems(ui.Center).Justify(ui.SpaceBetween).
				Translate(slideX, 0).Opacity(panelAlpha).Clip()

			if cfg.Surface.Glass {
				baseOp := ResolveGlassOpacity(cfg.Surface, nil)
				topA := min(baseOp+0.20, 0.88)
				botA := min(baseOp+0.10, 0.80)
				topCol := WithAlpha(Lighten(pal.Surface, 0.10), topA)
				botCol := WithAlpha(pal.Surface, botA)
				if pal.IsLight {
					topCol = WithAlpha(ui.RGB(255, 255, 255), min(topA+0.12, 0.92))
					botCol = WithAlpha(pal.Surface, min(botA+0.10, 0.86))
				}
				panel.LinearGradient(ui.LinearGradient{From: topCol, To: botCol, Angle: 165, Oklab: true}).
					BorderWidth(1).BorderLinearGradient(ui.LinearGradient{
					From:  WithAlpha(ui.RGB(255, 255, 255), 0.34),
					To:    WithAlpha(ui.RGB(255, 255, 255), 0.06),
					Angle: 150,
					Oklab: true,
				}).
					InsetShadow(0, 1, 0, 0, WithAlpha(ui.RGB(255, 255, 255), 0.30)).
					Shadow(6, 14, 34, -4, WithAlpha(ui.RGB(0, 0, 0), 0.48))
			} else {
				panel.Background(pal.Surface).
					BorderWidth(1).BorderColor(pal.Border).
					Shadow(4, 12, 26, -6, WithAlpha(ui.RGB(0, 0, 0), 0.40))
			}

			panel.Children(func() {
				// Top pin toggle button.
				if cfg.OnToggle != nil {
					pinTone := pal.Muted
					pinLabel := "Pin Left Rail"
					if cfg.Pinned {
						pinTone = pal.Info
						pinLabel = "Unpin Left Rail (Auto-Hide)"
					}
					pinBtn := ActionControl(ui.ButtonBase(c.Key(key+"-pin")).Label(pinLabel).
						Width(38).Height(34).Radius(11).Justify(ui.Center).AlignItems(ui.Center))
					pinBtn.Tooltip(pinLabel)
					if cfg.Pinned {
						pinBtn.Background(WithAlpha(pal.Info, 0.18))
					} else if pinBtn.Hovered() {
						pinBtn.Background(WithAlpha(pal.Hover, 0.72))
					}
					pinBtn.Children(func() {
						Symbol(c, "sidebar", 16, pinTone)
					})
					if pinBtn.Clicked() {
						cfg.OnToggle()
					}
				}

				if cfg.CustomContent != nil {
					cfg.CustomContent(c)
					return
				}

				// Primary workspace navigation icons.
				ui.Column(c.Key(key+"-primary")).Gap(7).AlignItems(ui.Center).Children(func() {
					for _, item := range cfg.Primary {
						renderRailButton(c, key+"-p-"+item.ID, item, pal, cfg.Surface.Motion)
					}
				})

				// Bottom utility controls.
				ui.Column(c.Key(key+"-utility")).Gap(7).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Width(26).Height(1).Background(WithAlpha(pal.Muted, 0.22))
					for _, item := range cfg.Utility {
						renderRailButton(c, key+"-u-"+item.ID, item, pal, cfg.Surface.Motion)
					}
				})
			})
		}
	})
}

func renderRailButton(c *ui.Context, key string, item RailItem, pal Palette, motionOn bool) {
	tone := item.Tone
	if tone == (ui.Color{}) {
		tone = pal.Info
	}
	btn := ActionControl(ui.ButtonBase(c.Key(key)).Label(item.Label).
		Width(40).Height(40).Radius(13).Justify(ui.Center).AlignItems(ui.Center))
	if item.Label != "" {
		btn.Tooltip(item.Label)
	}
	iconCol := tone
	if item.Active {
		iconCol = ui.RGB(255, 255, 255)
		btn.LinearGradient(ui.LinearGradient{
			From:  Lighten(tone, 0.22),
			To:    tone,
			Angle: 142,
			Oklab: true,
		}).
			BorderWidth(1).BorderColor(WithAlpha(ui.RGB(255, 255, 255), 0.26)).
			InsetShadow(0, 1, 1, 0, WithAlpha(ui.RGB(255, 255, 255), 0.36)).
			Shadow(0, 6, 16, -3, WithAlpha(tone, 0.55))
	} else if btn.Hovered() {
		btn.Background(WithAlpha(tone, 0.20))
	}
	if motionOn {
		target := float32(1.0)
		if btn.Hovered() {
			target = 1.09
		}
		if sc := btn.Spring("rail-pop", target); sc != 1.0 {
			btn.Scale(sc, sc)
		}
	}
	btn.Children(func() {
		if item.Active {
			ui.Box(c).Absolute().Left(-7).Top(10).Bottom(10).Width(3).Radius(RadiusPill).Background(tone)
		}
		Symbol(c, item.Icon, 18, iconCol)
	})
	if btn.Clicked() && item.OnClick != nil {
		item.OnClick()
	}
}
