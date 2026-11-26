// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package style_test

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/tao3k/marlin-mygo/runtime/style"
)

func TestGovernedCSSSubsetParsingCascadeAndFailClosed(t *testing.T) {
	contract := style.DefaultContract("tab", "active", "btn", "match-hit")

	baseCSS := `
:root {
  --bg: #282c34;
  --tab-active-bg: #504945;
  --tab-active-fg: #fbf1c7;
  --gold: #ecbe7b;
}
.tab {
  padding: 3px 10px;
  border-radius: 4px;
  font-size: 13.5px;
}
.tab.active {
  background: var(--tab-active-bg);
  color: var(--tab-active-fg);
  font-weight: bold;
}
.tab:hover {
  background: #343a46;
}
.match-hit {
  color: var(--gold);
  background: rgba(236, 190, 123, 0.18);
  border-radius: 3px;
  font-weight: 700;
}
`
	base, err := style.Parse(baseCSS, contract)
	if err != nil {
		t.Fatalf("Parse base: %v", err)
	}

	stActive := base.Resolve([]string{"tab", "active"}, style.StateNone)
	if !stActive.HasBackground || stActive.Background != ui.RGB(80, 73, 69) {
		t.Fatalf("expected #504945 active tab bg, got %+v", stActive)
	}
	if !stActive.HasFontWeight || stActive.FontWeight != 700 || stActive.BorderRadius != 4 {
		t.Fatalf("unexpected active tab style: %+v", stActive)
	}

	// User override sheet cascades cleanly on top of base Doom theme.
	overrideCSS := `
.tab.active {
  border-radius: 8px;
}
`
	ov, err := style.Parse(overrideCSS, contract)
	if err != nil {
		t.Fatalf("Parse override: %v", err)
	}
	merged := base.Cascade(ov)
	stMerged := merged.Resolve([]string{"tab", "active"}, style.StateNone)
	if stMerged.BorderRadius != 8 || stMerged.Background != ui.RGB(80, 73, 69) {
		t.Fatalf("expected cascaded radius=8 with inherited bg, got %+v", stMerged)
	}

	// Fail-closed governance checks:
	cases := []struct {
		name string
		css  string
		want string
	}{
		{"undeclared class", `.random-hack { color: #fff; }`, "not declared"},
		{"disallowed property", `.btn { position: fixed; }`, "not admitted"},
		{"forbidden @import", `@import "evil.css";`, "at-rules"},
		{"forbidden url()", `.btn { background: url(https://evil.example/x.png); }`, "url()"},
	}
	for _, tc := range cases {
		if _, err := style.Parse(tc.css, contract); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: expected error containing %q, got %v", tc.name, tc.want, err)
		}
	}
}
