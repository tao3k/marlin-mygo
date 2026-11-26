// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package widgets_test

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/runtime/kit"
	"github.com/tao3k/marlin-mygo/runtime/style"
	"github.com/tao3k/marlin-mygo/runtime/widgets"
)

func TestExtendedSidebarJSONPrimitivesAndVisibleWhen(t *testing.T) {
	raw := `{
		"schemaVersion": "marlin.ui.sidebar.v1",
		"width": 260,
		"body": {
			"id": "sb-scroll",
			"kind": "scroll",
			"children": [
				{
					"id": "sec-flow",
					"kind": "section",
					"title": "WORKFLOW CONTROLS",
					"children": [
						{ "id": "act-step", "kind": "action-tile", "icon": "bolt", "title": "Step Flow", "onClick": "island:workflow-graph:step" },
						{ "id": "cond-shown", "kind": "text", "value": "Online Banner", "visibleWhen": "agent.status==Online" },
						{ "id": "cond-hidden", "kind": "text", "value": "Hidden Banner", "visibleWhen": "agent.status==Offline" }
					]
				},
				{ "id": "div-1", "kind": "divider" },
				{ "id": "mini-flow", "kind": "web-island", "islandId": "workflow-graph", "borderless": true, "height": 120 }
			]
		}
	}`
	spec, err := widgets.ParseSidebarJSON(raw)
	if err != nil {
		t.Fatalf("ParseSidebarJSON failed: %v", err)
	}

	sig := widgets.NewSignals()
	sig.Set("agent.status", "Online")
	var lastAct string
	rt := &widgets.Runtime{
		Modules:  widgets.DefaultModules(),
		OnAction: func(a string) { lastAct = a },
	}
	pal := kit.Palette{
		Canvas: ui.Hex("#1e222a"), Bar: ui.Hex("#191d24"), Surface: ui.Hex("#2b303c"),
		Hover: ui.Hex("#353c4b"), Border: ui.Hex("#3d4556"), Ink: ui.Hex("#e5e9f0"),
		Muted: ui.Hex("#8c96a8"), Accent: ui.Hex("#d48ab8"), Info: ui.Hex("#88c0d0"),
		Success: ui.Hex("#a3be8c"), Warning: ui.Hex("#ebcb8b"), Danger: ui.Hex("#e06c75"),
	}
	sh, _ := style.Parse("", style.DefaultContract())

	tt := ui.NewTester(func(c *ui.Context) {
		widgets.Render(c, spec.Body, sig, pal, sh, rt)
	}, 300, 480)

	if !tt.HasText("WORKFLOW CONTROLS") || !tt.HasText("Online Banner") || tt.HasText("Hidden Banner") {
		t.Fatalf("expected visibleWhen filtering and section header rendered")
	}
	if err := tt.Click("Step Flow"); err != nil {
		t.Fatalf("click Step Flow: %v", err)
	}
	if lastAct != "island:workflow-graph:step" {
		t.Fatalf("expected island action dispatched, got %q", lastAct)
	}
	// Clicking section header collapses its children interactively.
	if err := tt.Click("WORKFLOW CONTROLS"); err != nil {
		t.Fatalf("collapse section: %v", err)
	}
	tt.Frame()
	if tt.HasText("Online Banner") {
		t.Fatalf("expected section children hidden after collapse")
	}
}
