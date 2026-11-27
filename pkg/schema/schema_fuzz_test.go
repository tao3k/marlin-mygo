// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package schema

import (
	"strings"
	"testing"
)

func refValidDotID(s string) bool {
	if len(s) == 0 || len(s) > MaxIdentifierBytes {
		return false
	}
	for _, seg := range strings.Split(s, ".") {
		if len(seg) == 0 {
			return false
		}
		if !isASCIIAlphaNum(seg[0]) {
			return false
		}
		for i := 0; i < len(seg); i++ {
			b := seg[i]
			if !isASCIIAlphaNum(b) && b != '-' && b != '_' {
				return false
			}
		}
	}
	return true
}

func FuzzValidateDotIdentifier(f *testing.F) {
	for _, seed := range []string{
		"tao3k.org-kanban",
		"workbench.main",
		"",
		".leading",
		"trailing.",
		"a..b",
		"-bad.start",
		"ok.with_under-dash.123",
		strings.Repeat("a", MaxIdentifierBytes),
		strings.Repeat("a", MaxIdentifierBytes+1),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		err := ValidateDotIdentifier(s)
		want := refValidDotID(s)
		if (err == nil) != want {
			t.Fatalf("ValidateDotIdentifier(%q): err=%v wantValid=%v", s, err, want)
		}
		_, perr := ParsePluginID(s)
		wantPlugin := want && strings.Contains(s, ".")
		if (perr == nil) != wantPlugin {
			t.Fatalf("ParsePluginID(%q): err=%v wantPlugin=%v", s, perr, wantPlugin)
		}
	})
}

func FuzzParseSubmissionID(f *testing.F) {
	for _, seed := range []string{"sub_001-a", "", "bad.dot", "bad/slash", strings.Repeat("x", MaxIdentifierBytes+1)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_, err := ParseSubmissionID(s)
		want := len(s) > 0 && len(s) <= MaxIdentifierBytes
		for i := 0; want && i < len(s); i++ {
			b := s[i]
			if !isASCIIAlphaNum(b) && b != '-' && b != '_' {
				want = false
			}
		}
		if (err == nil) != want {
			t.Fatalf("ParseSubmissionID(%q): err=%v wantValid=%v", s, err, want)
		}
	})
}
