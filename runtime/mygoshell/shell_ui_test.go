// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

type testTarget struct{ meta transport.TargetMeta }

func (t *testTarget) Meta() transport.TargetMeta  { return t.meta }
func (t *testTarget) Ping(context.Context) error  { return nil }
func (t *testTarget) Close() error                { return nil }

func TestWorkbenchAppHeadlessMultiTargetSlotHotPlugAndGate(t *testing.T) {
	targets := transport.NewTargetRegistry()
	_ = targets.Register(&testTarget{meta: transport.TargetMeta{
		ID: "local", DisplayName: "Local Mac", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})
	_ = targets.Register(&testTarget{meta: transport.TargetMeta{
		ID: "remote-1", DisplayName: "Linux GPU", Mode: transport.ModeRemoteGRPC,
		TargetKind: schema.TargetRemoteHeadless, Phase: transport.PhaseReady,
	}})

	slots := shell.NewSlotRegistry()
	fallback := NewUIElementRenderer("native.main", true, slots, func() schema.UIElement {
		return schema.UIElement{
			Kind:  schema.ElemPanel,
			Title: "Native Fallback Transcript",
			Tone:  schema.ToneInfo,
			Children: []schema.UIElement{
				{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Fallback Active", Tone: schema.ToneSuccess}}},
			},
		}
	})
	if err := slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle,
		FallbackRenderer: "native.main", AllowUntrustedPlugin: true,
	}, fallback); err != nil {
		t.Fatal(err)
	}

	wb := shell.NewWorkbenchController(slots)
	if err := wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation:  1,
		ProfileName: "macos-dev",
		Theme:       schema.DefaultUITheme(),
		FrameTree: schema.UIElement{
			Kind: schema.ElemRows,
			Children: []schema.UIElement{
				{Kind: schema.ElemSlot, SlotID: "workbench.main"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	app := NewWorkbenchApp(targets, slots, wb)
	var approvedGate string
	app.OnGateDecision = func(gateID string, approve bool) {
		if approve {
			approvedGate = gateID
		}
	}

	tt := ui.NewTester(app.View, 1000, 640)
	if !tt.HasText("Fallback Active") {
		t.Fatalf("expected fallback text in initial frame, got %q", tt.Texts())
	}

	// Hot-plug a replacement UI IR plugin into workbench.main.
	pluginRenderer := NewUIElementRenderer("tao3k.graph-inspector", false, slots, func() schema.UIElement {
		return schema.UIElement{
			Kind:  schema.ElemPanel,
			Title: "Plugin View",
			Tone:  schema.ToneAccent,
			Children: []schema.UIElement{
				{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Hot-Plugged Plugin", Tone: schema.ToneAccent}}},
			},
		}
	})
	unmount, err := slots.Mount("workbench.main", "", pluginRenderer)
	if err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Hot-Plugged Plugin") {
		t.Fatalf("expected hot-plugged plugin text, got %q", tt.Texts())
	}

	// Unmount restores fallback immediately.
	unmount()
	tt.Frame()
	if !tt.HasText("Fallback Active") {
		t.Fatalf("expected automatic fallback restoration, got %q", tt.Texts())
	}

	// Trigger Approval Gate overlay and approve.
	app.ShowPendingGate(PendingGateView{
		GateID:   "gate-77",
		ToolName: "ios.calendar.create",
		Prompt:   "Create calendar event?",
	})
	tt.Frame()
	if err := tt.Click("Approve Once"); err != nil {
		t.Fatalf("clicking Approve Once failed: %v", err)
	}
	if approvedGate != "gate-77" {
		t.Fatalf("expected gate-77 approved, got %q", approvedGate)
	}
}
