// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestShowcaseLayoutDesktopWideAndCompactNarrowViewports(t *testing.T) {
	targets := transport.NewTargetRegistry()
	_ = targets.Register(&testTarget{meta: transport.TargetMeta{
		ID: "local-mac", DisplayName: "Local Sidecar", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})

	slots := shell.NewSlotRegistry()
	var clickedClockAction string
	mainRend := NewUIElementRenderer("tao3k.org-agenda-planner", false, slots, func() schema.UIElement {
		return schema.UIElement{
			Kind:  schema.ElemPanel,
			Title: "Org Agenda (React 19 Showcase Slot)",
			Tone:  schema.ToneInfo,
			Children: []schema.UIElement{
				{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "React 19 Active", Tone: schema.ToneSuccess}}},
			},
		}
	})
	inspRend := NewUIElementRenderer("tao3k.focus-clock-reactor", false, slots, func() schema.UIElement {
		return schema.UIElement{
			Kind:  schema.ElemPanel,
			Title: "Focus Clock & Org Agenda (GPU Reactor)",
			Tone:  schema.ToneAccent,
			Children: []schema.UIElement{
				{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "25:00 RUNNING", Tone: schema.ToneSuccess}}},
				{Kind: schema.ElemButton, Title: "Clock In 25m", Action: "org.clock.in", ActionArgs: `{"durationSec":1500}`, Tone: schema.ToneAccent},
			},
		}
	})
	inspRend.OnAction = func(action, _ string) {
		clickedClockAction = action
	}

	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.main", AllowUntrustedPlugin: true,
	}, mainRend)
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.inspector", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.inspector", AllowUntrustedPlugin: true,
	}, inspRend)

	wb := shell.NewWorkbenchController(slots)
	if err := wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation:  1,
		ProfileName: "desktop-showcase",
		Theme:       schema.DefaultUITheme(),
		FrameTree: schema.UIElement{
			Kind: schema.ElemRows,
			Children: []schema.UIElement{
				{Kind: schema.ElemSlot, SlotID: "workbench.main"},
				{Kind: schema.ElemSlot, SlotID: "workbench.inspector"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	app := NewWorkbenchApp(targets, slots, wb)
	// Showcase starts with Review Gate armed in top bar, NOT blocking the main screen.
	app.SetPendingGate(PendingGateView{
		GateID:   "gate-showcase-1",
		ToolName: "device.ios.notify.send",
		Prompt:   "Approve AI Workflow dispatch?",
	}, false)

	activePlugin := "tao3k.org-agenda-planner"
	app.QuickPlugins = []QuickPluginEntry{
		{PluginID: "tao3k.org-agenda-planner", Label: "Org Agenda (React 19)"},
		{PluginID: "tao3k.ai-memory-inbox", Label: "AI Memory Inbox (React 19)"},
		{PluginID: "tao3k.ai-workflow-cockpit", Label: "AI Workflow Cockpit (React 19)"},
	}
	app.ActiveQuickPluginID = func() string { return activePlugin }
	app.OnQuickPluginSelect = func(id string) { activePlugin = id }

	var approvedGate string
	app.OnGateDecision = func(gateID string, approve bool) {
		if approve {
			approvedGate = gateID
		}
	}

	// 1. Wide desktop viewport (1240x820): both slots visible, gate armed but NOT open.
	wide := ui.NewTester(app.View, 1240, 820)
	if !wide.HasText("React 19 Active") || !wide.HasText("25:00 RUNNING") {
		t.Fatalf("expected both main and inspector slots visible at 1240x820, got %q", wide.Texts())
	}
	if wide.HasText("Approve Once") {
		t.Fatalf("expected SetPendingGate(..., false) to keep gate modal closed on startup")
	}
	if !wide.HasText("AI WORKFLOW PLUGINS") || !wide.HasText("AI Memory Inbox (React 19)") {
		t.Fatalf("expected sidebar AI WORKFLOW PLUGINS quick switcher, got %q", wide.Texts())
	}

	// Click sidebar quick-switcher button -> hot-swaps active plugin ID.
	if err := wide.Click("AI Memory Inbox (React 19)"); err != nil {
		t.Fatalf("failed to click AI Memory Inbox (React 19) quick-switcher button: %v", err)
	}
	if activePlugin != "tao3k.ai-memory-inbox" {
		t.Fatalf("expected quick-switcher to select tao3k.ai-memory-inbox, got %q", activePlugin)
	}

	// Click native GPU ActionButton inside Go WASI inspector slot.
	if err := wide.Click("Clock In 25m"); err != nil {
		t.Fatalf("failed to click native GPU Clock In 25m button: %v", err)
	}
	if clickedClockAction != "org.clock.in" {
		t.Fatalf("expected org.clock.in action from GPU button, got %q", clickedClockAction)
	}

	// Open Tier-0 Gate via Review Gate button and approve once.
	if err := wide.Click("Review Gate"); err != nil {
		t.Fatalf("failed to click Review Gate top-bar button: %v", err)
	}
	wide.Frame()
	if !wide.HasText("Approve Once") {
		t.Fatal("expected Tier-0 Approval Gate modal to float above slots after clicking Review Gate")
	}
	if err := wide.Click("Approve Once"); err != nil {
		t.Fatalf("failed to click Approve Once: %v", err)
	}
	if approvedGate != "gate-showcase-1" {
		t.Fatalf("expected gate-showcase-1 approval callback, got %q", approvedGate)
	}

	// 2. Compact / narrow viewport (680x440): renders cleanly without panics or missing controls.
	narrow := ui.NewTester(app.View, 680, 440)
	if !narrow.HasText("React 19 Active") || !narrow.HasText("AI Workflow Cockpit (React 19)") {
		t.Fatalf("expected compact 680x440 viewport to render main slot and quick switcher, got %q", narrow.Texts())
	}
}
