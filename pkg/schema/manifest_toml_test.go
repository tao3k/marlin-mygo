// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package schema_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestParseCanonicalExampleManifests(t *testing.T) {
	cases := []struct {
		rel      string
		wantID   schema.PluginID
		wantSlot schema.SlotID
		wantRT   schema.PanelRuntimeKind
	}{
		{
			rel:      filepath.Join("..", "..", "examples", "plugins", "go-graph-inspector-reactor", "marlin-plugin.toml"),
			wantID:   "tao3k.graph-inspector",
			wantSlot: "workbench.inspector",
			wantRT:   schema.RuntimeWasmUIIR,
		},
		{
			rel:      filepath.Join("..", "..", "examples", "plugins", "go-focus-clock-reactor", "marlin-plugin.toml"),
			wantID:   "tao3k.focus-clock-reactor",
			wantSlot: "workbench.inspector",
			wantRT:   schema.RuntimeWasmUIIR,
		},
		{
			rel:      filepath.Join("..", "..", "examples", "plugins", "ts-org-kanban", "marlin-plugin.toml"),
			wantID:   "tao3k.org-kanban",
			wantSlot: "workbench.main",
			wantRT:   schema.RuntimeWebViewUntrusted,
		},
		{
			rel:      filepath.Join("..", "..", "examples", "plugins", "ts-session-radar", "marlin-plugin.toml"),
			wantID:   "tao3k.session-radar",
			wantSlot: "workbench.main",
			wantRT:   schema.RuntimeWebViewUntrusted,
		},
		{
			rel:      filepath.Join("..", "..", "examples", "plugins", "ts-org-agenda-planner", "marlin-plugin.toml"),
			wantID:   "tao3k.org-agenda-planner",
			wantSlot: "workbench.main",
			wantRT:   schema.RuntimeWebViewUntrusted,
		},
		{
			rel:      filepath.Join("..", "..", "examples", "plugins", "ios-personal-overlay", "marlin-plugin.toml"),
			wantID:   "tao3k.ios-personal-capture",
			wantSlot: "workbench.main",
			wantRT:   schema.RuntimeDeclarative,
		},
		{
			rel:      filepath.Join("..", "..", "examples", "plugins", "declarative-daily-planner-pack", "marlin-plugin.toml"),
			wantID:   "tao3k.daily-planner-pack",
			wantSlot: "workbench.inspector",
			wantRT:   schema.RuntimeDeclarative,
		},
	}

	for _, tc := range cases {
		raw, err := os.ReadFile(tc.rel)
		if err != nil {
			t.Fatalf("read %s: %v", tc.rel, err)
		}
		m, err := schema.ParsePluginManifestTOML(raw)
		if err != nil {
			t.Fatalf("ParsePluginManifestTOML(%s) failed: %v", tc.rel, err)
		}
		if m.ID != tc.wantID {
			t.Fatalf("%s: got ID %q, want %q", tc.rel, m.ID, tc.wantID)
		}
		foundPanel := false
		for _, c := range m.Contributions {
			if c.Kind == "ui-panel" && c.Panel != nil {
				foundPanel = true
				if c.Panel.SlotID != tc.wantSlot || c.Panel.Runtime != tc.wantRT {
					t.Fatalf("%s: got slot=%q rt=%q, want slot=%q rt=%q",
						tc.rel, c.Panel.SlotID, c.Panel.Runtime, tc.wantSlot, tc.wantRT)
				}
			}
		}
		if !foundPanel {
			t.Fatalf("%s: expected at least one ui-panel contribution", tc.rel)
		}
	}
}

func TestParseManifestRejectsRestrictedSlotAndEnforcesTargetPolicy(t *testing.T) {
	evil := []byte(`
id = "evil.gate"
version = "0.1.0"
contract_major = 1

[contributions.hijack]
kind = "ui-panel"

[contributions.hijack.panel]
runtime = "webview-untrusted"
slot_id = "approval.gate"
cardinality = "single"
`)
	if _, err := schema.ParsePluginManifestTOML(evil); err == nil {
		t.Fatal("expected approval.gate manifest to be rejected")
	}

	if !schema.RuntimeAllowedOnTarget(schema.RuntimeDeclarative, schema.TargetMobileIOS) {
		t.Fatal("declarative must be allowed on mobile-ios")
	}
	if schema.RuntimeAllowedOnTarget(schema.RuntimeWebViewUntrusted, schema.TargetMobileIOS) {
		t.Fatal("webview-untrusted must be rejected on mobile-ios")
	}
	if schema.RuntimeAllowedOnTarget(schema.RuntimeWasmUIIR, schema.TargetMobileIOS) {
		t.Fatal("wasm-ui-ir must be rejected on mobile-ios")
	}
	if !schema.RuntimeAllowedOnTarget(schema.RuntimeWasmUIIR, schema.TargetDesktopMacOS) {
		t.Fatal("wasm-ui-ir must be allowed on desktop-macos")
	}
}

func FuzzParsePluginManifestTOML(f *testing.F) {
	f.Add([]byte(`id = "tao3k.demo"
version = "0.1.0"
contract_major = 1
[contributions.p]
kind = "ui-panel"
[contributions.p.panel]
runtime = "declarative"
slot_id = "workbench.main"
cardinality = "single"
read_scopes = ["a.b"]
declared_actions = ["c.d"]
`))
	f.Add([]byte(`id = ""`))
	f.Add([]byte(`[bad.section.depth.four]`))
	f.Add([]byte("id = \"tao3k.multi\"\nversion = \"0.1.0\"\ncontract_major = 1\n[contributions.p]\nkind = \"ui-panel\"\n[contributions.p.panel]\nruntime = \"declarative\"\nslot_id = \"workbench.main\"\ndeclared_actions = [\n  \"org.capture.plan\",\n  \"org.clock.in\",\n]\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := schema.ParsePluginManifestTOML(data)
		if err == nil {
			if m == nil || m.ID == "" {
				t.Fatalf("nil or empty manifest accepted without error")
			}
			for _, c := range m.Contributions {
				if c.Panel != nil && schema.IsRestrictedSlot(c.Panel.SlotID) {
					t.Fatalf("restricted slot %q leaked past parser", c.Panel.SlotID)
				}
			}
		}
	})
}
