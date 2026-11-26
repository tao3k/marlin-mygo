// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package style

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"
)

// APCALc computes the APCA-W3 (0.0.98G-4g) perceptual lightness contrast Lc
// between text color fg and background color bg, returning signed Lc in
// approximately [-108, +106]. Positive values indicate dark text on light bg;
// negative values indicate light text on dark bg.
func APCALc(fg, bg ui.Color) float64 {
	yTxt := apcaLuminance(fg)
	yBg := apcaLuminance(bg)

	// Soft clamp near black.
	const blkThrs = 0.022
	const blkClmp = 1.414
	if yTxt < blkThrs {
		yTxt += math.Pow(blkThrs-yTxt, blkClmp)
	}
	if yBg < blkThrs {
		yBg += math.Pow(blkThrs-yBg, blkClmp)
	}
	if math.Abs(yBg-yTxt) < 0.0005 {
		return 0
	}

	const (
		normBG     = 0.56
		normTXT    = 0.57
		revBG      = 0.65
		revTXT     = 0.62
		scaleBoW   = 1.14
		scaleWoB   = 1.14
		loClip     = 0.1
		loBoWOff   = 0.027
		loWoBOff   = 0.027
	)
	var sapc float64
	if yBg > yTxt {
		sapc = (math.Pow(yBg, normBG) - math.Pow(yTxt, normTXT)) * scaleBoW
		if sapc < loClip {
			return 0
		}
		return (sapc - loBoWOff) * 100.0
	}
	sapc = (math.Pow(yBg, revBG) - math.Pow(yTxt, revTXT)) * scaleWoB
	if sapc > -loClip {
		return 0
	}
	return (sapc + loWoBOff) * 100.0
}

func apcaLuminance(c ui.Color) float64 {
	r := math.Pow(float64(c.R)/255.0, 2.4)
	g := math.Pow(float64(c.G)/255.0, 2.4)
	b := math.Pow(float64(c.B)/255.0, 2.4)
	return 0.2126729*r + 0.7151522*g + 0.0721750*b
}

// AuditPaletteContrast checks primary ink and muted ink contrast against bg
// using APCA perceptual Lc thresholds (stricter on translucent frosted glass
// where dynamic wallpaper diffusion lowers effective contrast).
func AuditPaletteContrast(fg, muted, bg ui.Color, onGlass bool) []string {
	minBody := 60.0
	minMuted := 42.0
	if onGlass {
		minBody = 68.0
		minMuted = 45.0
	}
	var warnings []string
	if lc := math.Abs(APCALc(fg, bg)); lc < minBody {
		warnings = append(warnings, fmt.Sprintf("APCA contrast warning: primary ink Lc=%.1f below recommended %.0f (glass=%v)", lc, minBody, onGlass))
	}
	if lc := math.Abs(APCALc(muted, bg)); lc < minMuted {
		warnings = append(warnings, fmt.Sprintf("APCA contrast warning: muted ink Lc=%.1f below recommended %.0f (glass=%v)", lc, minMuted, onGlass))
	}
	return warnings
}
