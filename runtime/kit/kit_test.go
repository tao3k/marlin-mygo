// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package kit_test

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/runtime/kit"
)

func TestKitPrimitivesGlassShadersAndSparkGauge(t *testing.T) {
	pal := kit.Palette{
		Canvas:  ui.Hex("#1e222a"),
		Bar:     ui.Hex("#191d24"),
		Surface: ui.Hex("#2b303c"),
		Hover:   ui.Hex("#353c4b"),
		Border:  ui.Hex("#3d4556"),
		Ink:     ui.Hex("#e5e9f0"),
		Muted:   ui.Hex("#8c96a8"),
		Accent:  ui.Hex("#d48ab8"),
		Info:    ui.Hex("#88c0d0"),
		Success: ui.Hex("#a3be8c"),
		Warning: ui.Hex("#ebcb8b"),
		Danger:  ui.Hex("#e06c75"),
	}
	clicked := false
	tt := ui.NewTester(func(c *ui.Context) {
		kit.ApplyWindowTransparency(c, true)
		kit.SafeCol(c, "root", 12).Fill().Padding(16).Children(func() {
			kit.RenderGlassAtmosphere(c, pal, kit.SurfaceOptions{Glass: true, Motion: true})
			card := kit.SafeCol(c, "card", 10).Padding(16)
			kit.ApplyCardSurface(card, nil, false, pal, nil, kit.SurfaceOptions{Glass: true, Motion: false})
			card.Children(func() {
				kit.SafeRow(c, "hdr", 10).AlignItems(ui.Center).Children(func() {
					kit.ThinkingOrb(c, "orb", 40, true, false, pal)
					kit.IconBadge(c, "sparkles", 34, 16, pal.Accent)
					kit.FlexLine(c, "Very long widget title that must clip cleanly without overlapping siblings", kit.TextSpec{
						Font:   kit.FontDisplay,
						Size:   15,
						Weight: 700,
						Color:  pal.Ink,
					})
				})
				kit.SparkGauge(c, "g1", "check", "Tasks", "5/8", 0.625, pal.Success, pal, false, func() {
					clicked = true
				})
				kit.MiniTrendBars(c, "trend", []float64{0.2, 0.5, 0.9, 0.7}, 36, pal.Info)
				kit.ActiveEdgeGlow(c, "card", 20, true, pal)
			})
		})
	}, 640, 420)

	if !tt.HasText("Tasks") || !tt.HasText("5/8") {
		t.Fatalf("expected SparkGauge texts, got %v", tt.Texts())
	}
	if err := tt.Click("Tasks"); err != nil {
		t.Fatalf("click SparkGauge: %v", err)
	}
	tt.Frame()
	if !clicked {
		t.Fatalf("expected SparkGauge click handler to fire")
	}
}
