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
