// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package style_test

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/runtime/style"
)

func TestW3CDesignTokensJSONAndOKLCHCascade(t *testing.T) {
	baseCSS := `:root { --bg: #1e222a; --accent: #88c0d0; --radius-card: 18px; }
.bento-card { background: var(--bg); border-radius: var(--radius-card); }`
	contract := style.DefaultContract("bento-card")
	base, err := style.Parse(baseCSS, contract)
	if err != nil {
		t.Fatalf("parse base CSS: %v", err)
	}

	tokensJSON := `{
		"color": {
			"$type": "color",
			"bg": { "$value": "oklch(0.24 0.02 255)" },
			"brand": { "$value": "oklch(0.75 0.14 215 / 0.9)" },
			"accent": { "$value": "{color.brand}" }
		},
		"radius": {
			"card": { "$type": "dimension", "$value": "22px" }
		}
	}`
	tokSheet, err := style.ParseDesignTokensJSON(tokensJSON, contract)
	if err != nil {
		t.Fatalf("ParseDesignTokensJSON failed: %v", err)
	}

	merged := base.Cascade(tokSheet)
	bg := merged.VarColor("--bg", ui.RGB(0, 0, 0))
	if bg == ui.RGB(0x1e, 0x22, 0x2a) || bg == ui.RGB(0, 0, 0) {
		t.Fatalf("expected oklch --bg to override base hex, got %+v", bg)
	}
	acc := merged.VarColor("--accent", ui.RGB(0, 0, 0))
	if acc.A < 220 || acc.A > 235 {
		t.Fatalf("expected oklch alpha ~0.9 (230), got %+v", acc)
	}
	if rad := merged.VarPx("--radius-card", 0); rad != 22 {
		t.Fatalf("expected radius-card=22px from tokens.json, got %g", rad)
	}

	// Cycle detection must fail closed.
	cyclic := `{"color": {"a": {"$type": "color", "$value": "{color.b}"}, "b": {"$type": "color", "$value": "{color.a}"}}}`
	if _, err := style.ParseDesignTokensJSON(cyclic, contract); err == nil {
		t.Fatalf("expected cyclic token alias to fail closed")
	}

	// Invalid color must fail closed.
	badCol := `{"color": {"bg": {"$type": "color", "$value": "url(https://evil.invalid/x.css)"}}}`
	if _, err := style.ParseDesignTokensJSON(badCol, contract); err == nil {
		t.Fatalf("expected malicious color value to fail closed")
	}
}
