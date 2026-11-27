// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package style_test

import (
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/runtime/style"
)

func TestDTCG2025ColorObjectsAndAPCALc(t *testing.T) {
	raw := `{
		"color": {
			"$type": "color",
			"bg": { "$value": { "colorSpace": "oklch", "components": [0.22, 0.018, 258] } },
			"fg": { "$value": { "colorSpace": "srgb", "components": [0.90, 0.92, 0.95] } },
			"accent": { "$value": { "colorSpace": "oklch", "components": [0.74, 0.12, 330], "alpha": 0.9 } }
		},
		"glass": {
			"opacity": { "$type": "number", "$value": 0.28 }
		}
	}`
	sh, err := style.ParseDesignTokensJSON(raw, style.DefaultContract())
	if err != nil {
		t.Fatalf("ParseDesignTokensJSON DTCG 2025.10 failed: %v", err)
	}
	bg := sh.VarColor("--bg", ui.RGB(0, 0, 0))
	fg := sh.VarColor("--fg", ui.RGB(0, 0, 0))
	if bg == (ui.Color{}) || fg == (ui.Color{}) {
		t.Fatalf("expected resolved DTCG 2025.10 colors, got bg=%+v fg=%+v", bg, fg)
	}
	if op := sh.VarPx("--glass-opacity", 0); math.Abs(float64(op-0.28)) > 0.001 {
		t.Fatalf("expected --glass-opacity=0.28, got %v", op)
	}
	if vars := sh.ExportVars(); len(vars) < 4 {
		t.Fatalf("expected ExportVars populated, got %v", vars)
	}

	// High-contrast light ink on dark bg passes APCA on glass.
	if lc := math.Abs(style.APCALc(fg, bg)); lc < 70 {
		t.Fatalf("expected high APCA Lc >= 70 for crisp dark palette, got %.1f", lc)
	}
	if warns := style.AuditPaletteContrast(fg, ui.Hex("#9ba6b8"), bg, true); len(warns) != 0 {
		t.Fatalf("expected zero APCA warnings for readable palette, got %v", warns)
	}

	// Low-contrast dark-gray-on-dark-gray MUST trigger APCA glass contrast warning.
	lowWarns := style.AuditPaletteContrast(ui.Hex("#3a404c"), ui.Hex("#2e3440"), ui.Hex("#1e222a"), true)
	if len(lowWarns) < 2 || !strings.Contains(lowWarns[0], "APCA contrast warning") {
		t.Fatalf("expected APCA warnings for low-contrast glass palette, got %v", lowWarns)
	}
}
