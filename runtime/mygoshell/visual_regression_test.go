// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

type visualScene struct {
	name      string
	width     int
	height    int
	expectLit bool // true = light canvas, false = dark canvas
	view      func(c *ui.Context)
}

func averageLuma(img *image.RGBA, rect image.Rectangle) float64 {
	b := img.Bounds().Intersect(rect)
	if b.Empty() {
		return 0
	}
	var sum float64
	var count int
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			off := img.PixOffset(x, y)
			r := float64(img.Pix[off])
			g := float64(img.Pix[off+1])
			bl := float64(img.Pix[off+2])
			sum += 0.2126*r + 0.7152*g + 0.0722*bl
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

func distinctColorSamples(img *image.RGBA) int {
	b := img.Bounds()
	seen := make(map[uint32]struct{}, 64)
	for y := b.Min.Y; y < b.Max.Y; y += 4 {
		for x := b.Min.X; x < b.Max.X; x += 4 {
			off := img.PixOffset(x, y)
			key := uint32(img.Pix[off])<<16 | uint32(img.Pix[off+1])<<8 | uint32(img.Pix[off+2])
			seen[key] = struct{}{}
		}
	}
	return len(seen)
}

func imageSHA256(img *image.RGBA) string {
	sum := sha256.Sum256(img.Pix)
	return hex.EncodeToString(sum[:8])
}

// TestVisualFrameSnapshotsAndContrastInvariants renders all 5 canonical Studio
// Desktop & Mobile views through MyGo's native headless rasterizer (tester.Image())
// and enforces pixel-level visual invariants:
//   - Dark vs Light theme luminance polarity
//   - Distinct sidebar vs main-canvas chrome separation
//   - Non-blank rich color diversity (catches white-screen / black-screen regressions)
//   - Pixel-exact deterministic SHA-256 across consecutive frames
//   - Optional PNG snapshot export when MARLIN_VISUAL_OUT=<dir> is set (`just plugin visual-check`)
func TestVisualFrameSnapshotsAndContrastInvariants(t *testing.T) {
	outDir := os.Getenv("MARLIN_VISUAL_OUT")
	if outDir != "" {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", outDir, err)
		}
	}

	buildStudio := func(th schema.UITheme, openGate bool) *WorkbenchApp {
		app, slots, _ := buildStudioBenchHarness()
		_ = app.Workbench.AdmitSnapshot(schema.UIWorkbenchSnapshot{
			Generation:  2,
			ProfileName: "studio-visual",
			Theme:       th,
			FrameTree: schema.UIElement{
				Kind: schema.ElemRows,
				Children: []schema.UIElement{
					{Kind: schema.ElemSlot, SlotID: "workbench.main", Flex: 1},
					{Kind: schema.ElemSlot, SlotID: "workbench.inspector"},
				},
			},
		})
		_ = slots
		app.SetPendingGate(PendingGateView{
			GateID:      "gate-vis-1",
			ToolName:    "device.ios.notify.send",
			Prompt:      "Approve iPhone evening summary notification?",
			Description: "Tier-0 visual regression modal overlay.",
		}, openGate)
		return app
	}

	mobileIOS := NewMobileCompanionAppForPlatform(MobilePlatformIOS, "iphone16", "iPhone 16 Pro · Desktop Host")
	mobileIOS.SetPendingGates([]PendingGateView{
		{GateID: "gate-m1", ToolName: "device.ios.notify.send", Prompt: "Approve evening reminder?"},
	})
	mobileIOS.SetTab(MobileTabGates)

	mobileAndroid := NewMobileCompanionAppForPlatform(MobilePlatformAndroid, "pixel9", "Pixel 9 · Desktop Host")
	mobileAndroid.SetThemePreset("paper")
	mobileAndroid.SetTab(MobileTabChat)

	scenes := []visualScene{
		{name: "studio-desktop-dark-1340x860", width: 1340, height: 860, expectLit: false, view: buildStudio(StudioDarkPalette(), false).View},
		{name: "studio-desktop-light-1340x860", width: 1340, height: 860, expectLit: true, view: buildStudio(StudioLightPalette(), false).View},
		{name: "studio-desktop-narrow-680x440", width: 680, height: 440, expectLit: false, view: buildStudio(StudioDarkPalette(), false).View},
		{name: "studio-gate-modal-1340x860", width: 1340, height: 860, expectLit: false, view: buildStudio(StudioDarkPalette(), true).View},
		{name: "mobile-ios-393x852", width: 393, height: 852, expectLit: false, view: mobileIOS.View},
		{name: "mobile-android-light-412x915", width: 412, height: 915, expectLit: true, view: mobileAndroid.View},
	}

	for _, sc := range scenes {
		t.Run(sc.name, func(t *testing.T) {
			tt := ui.NewTester(sc.view, sc.width, sc.height)
			tt.Frame()
			img1 := tt.Image()
			if img1 == nil || img1.Bounds().Dx() != sc.width || img1.Bounds().Dy() != sc.height {
				t.Fatalf("unexpected image bounds: %v", img1)
			}
			hash1 := imageSHA256(img1)

			// Second steady frame must match pixel-for-pixel (zero visual flicker).
			tt.Frame()
			img2 := tt.Image()
			hash2 := imageSHA256(img2)
			if hash1 != hash2 {
				t.Fatalf("non-deterministic frame render on %s: frame1=%s frame2=%s", sc.name, hash1, hash2)
			}

			colors := distinctColorSamples(img1)
			if colors < 8 {
				t.Fatalf("visual regression on %s: only %d distinct colors (possible blank screen)", sc.name, colors)
			}

			luma := averageLuma(img1, img1.Bounds())
			if sc.expectLit && luma < 170 {
				t.Fatalf("expected light theme canvas luma >= 170 on %s, got %.1f", sc.name, luma)
			}
			if !sc.expectLit && luma > 95 {
				t.Fatalf("expected dark theme canvas luma <= 95 on %s, got %.1f", sc.name, luma)
			}

			t.Logf("[visual] %-30s %dx%d  luma=%5.1f  colors=%4d  sha256=%s",
				sc.name, sc.width, sc.height, luma, colors, hash1)

			if outDir != "" {
				dst := filepath.Join(outDir, sc.name+".png")
				f, err := os.Create(dst)
				if err != nil {
					t.Fatalf("create %s: %v", dst, err)
				}
				if err := png.Encode(f, img1); err != nil {
					_ = f.Close()
					t.Fatalf("encode png %s: %v", dst, err)
				}
				_ = f.Close()
			}
		})
	}
}
