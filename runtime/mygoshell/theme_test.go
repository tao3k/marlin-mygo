// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import "testing"

func TestBuiltinThemesPassWCAGContrastAndEmit34Tokens(t *testing.T) {
	presets := BuiltinThemes()
	if len(presets) < 5 {
		t.Fatalf("expected at least 5 curated themes, got %d", len(presets))
	}
	for _, p := range presets {
		dt := DeriveTheme(p.ID, p.Name, p.Base)
		if ratio := ContrastRatioRGB(dt.Ink, dt.Canvas); ratio < 7.0 {
			t.Errorf("theme %s Ink/Canvas contrast %.2f < 7.0", p.ID, ratio)
		}
		if ratio := ContrastRatioRGB(dt.Ink, dt.Surface); ratio < 6.0 {
			t.Errorf("theme %s Ink/Surface contrast %.2f < 6.0", p.ID, ratio)
		}
		if ratio := ContrastRatioRGB(dt.InkOnAccent, dt.Accent); ratio < 4.5 {
			t.Errorf("theme %s InkOnAccent/Accent contrast %.2f < 4.5", p.ID, ratio)
		}
		tokens := DerivedThemeTokensMap(dt)
		if len(tokens) != 34 {
			t.Errorf("theme %s expected 34 derived CSS tokens, got %d", p.ID, len(tokens))
		}
		if tokens["--marlin-appearance"] != string(p.Appearance) {
			t.Errorf("theme %s appearance mismatch: got %q want %q", p.ID, tokens["--marlin-appearance"], p.Appearance)
		}
	}

	// Deterministic theme cycling wraps cleanly.
	cur := presets[0].ID
	for i := 0; i < len(presets); i++ {
		cur = NextThemePreset(cur).ID
	}
	if cur != presets[0].ID {
		t.Fatalf("expected NextThemePreset cycle to wrap back to %q, got %q", presets[0].ID, cur)
	}
}
