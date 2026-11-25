// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package gouir

import (
	"testing"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

func TestGoUIIRBuilderRoundTripAdmission(t *testing.T) {
	tree := Panel(
		"Graph Trajectory",
		schema.ToneAccent,
		Rows(
			Text(Span("node-01: ", schema.ToneMuted), Span("admitted", schema.ToneSuccess)),
			Slot("inspector.details"),
		),
	)
	raw, err := Encode(tree)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	admitted, err := pluginhost.DecodeAndAdmitWasmUITree(raw)
	if err != nil {
		t.Fatalf("host admission failed: %v", err)
	}
	if admitted.Kind != schema.ElemPanel || admitted.Title != "Graph Trajectory" {
		t.Fatalf("unexpected decoded tree: %+v", admitted)
	}
}

func TestRenderResultAndDrawListResultHostHelpers(t *testing.T) {
	tree := Panel("Lazy Demo", schema.ToneInfo, Text(Span("ok", schema.ToneSuccess)))
	if n := RenderResult(tree); n == 0 {
		t.Fatalf("expected non-zero RenderResult for valid UIElement tree")
	}
	if n := RenderResult(schema.UIElement{}); n != 0 {
		t.Fatalf("expected 0 RenderResult for invalid empty UIElement, got %d", n)
	}
	cmds := []schema.DrawCmd{
		{Op: schema.DrawOpFillRect, X: 0, Y: 0, W: 100, H: 40, RGBA: 0x282c34ff},
		{Op: schema.DrawOpBadge, X: 8, Y: 12, W: 64, H: 18, RGBA: 0x98c379ff},
	}
	if n := DrawListResult(cmds); n == 0 {
		t.Fatalf("expected non-zero DrawListResult for valid DrawCmd slice")
	}

	btn := ActionButton("Inspect", "graph.node.inspect", `{"nodeId":"n1"}`, schema.ToneAccent)
	if btn.Kind != schema.ElemButton || btn.Action != "graph.node.inspect" {
		t.Fatalf("unexpected ActionButton: %+v", btn)
	}
	if AllocBuffer(128) != 128 || AllocBuffer(0) != 0 {
		t.Fatal("unexpected AllocBuffer bounds behavior")
	}
	var gotTopic string
	rc := DispatchEventBytes([]byte(`{"eventId":"ev-1","topic":"graph.nodes","payload":{"count":3}}`), func(ev Event) error {
		gotTopic = ev.Topic
		return nil
	})
	if rc != 0 || gotTopic != "graph.nodes" {
		t.Fatalf("expected DispatchEventBytes rc=0 topic=graph.nodes, got rc=%d topic=%q", rc, gotTopic)
	}
}
