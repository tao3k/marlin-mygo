// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Reference Go WASM-IR plugin compiled with `GOOS=wasip1 GOARCH=wasm`.
// Emits bounded schema.UIElement trees rendered directly on the host's native
// Metal / Direct3D 11 / OpenGL GPU pipeline inside wazero (zero WebView).
package main

import (
	"fmt"
	"os"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	gouir "github.com/tao3k/marlin-mygo/sdk/go-ui-ir"
)

func buildInspectorTree() schema.UIElement {
	return gouir.Panel(
		"Graph Trajectory (Go WASM-IR)",
		schema.ToneInfo,
		gouir.Rows(
			gouir.Text(
				gouir.Span("run-01 ", schema.ToneAccent),
				gouir.Span("3/3 nodes verified", schema.ToneSuccess),
			),
			gouir.Text(
				gouir.Span("• plan_org_context   ", schema.ToneNormal),
				gouir.Span("[receipt: sha256:9a4f...]", schema.ToneMuted),
			),
			gouir.Text(
				gouir.Span("• tool_gate_review   ", schema.ToneNormal),
				gouir.Span("[approved-once]", schema.ToneSuccess),
			),
			gouir.Text(
				gouir.Span("• commit_org_record  ", schema.ToneNormal),
				gouir.Span("[committed]", schema.ToneInfo),
			),
		),
	)
}

func main() {
	raw, err := gouir.Encode(buildInspectorTree())
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode ui ir: %v\n", err)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(raw)
}
