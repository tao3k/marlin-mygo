// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build wasip1

// Reactor build of go-graph-inspector compiled with:
//   GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o panel.wasm ./examples/plugins/go-graph-inspector-reactor
//
// The Go runtime initializes ONCE at plugin Activate() via WASI _initialize;
// subsequent renders invoke marlin_ui_render in resident linear memory.
package main

import (
	"fmt"
	"unsafe"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	gouir "github.com/tao3k/marlin-mygo/sdk/go-ui-ir"
)

var (
	frameSeq  uint64
	renderBuf []byte
)

//go:wasmexport marlin_ui_render
func marlinUIRender() uint64 {
	frameSeq++
	tree := gouir.Panel(
		"Graph Trajectory (Resident Reactor)",
		schema.ToneInfo,
		gouir.Rows(
			gouir.Text(
				gouir.Span("run-01 ", schema.ToneAccent),
				gouir.Span(fmt.Sprintf("frame #%d verified", frameSeq), schema.ToneSuccess),
			),
			gouir.Text(
				gouir.Span("• plan_org_context  ", schema.ToneNormal),
				gouir.Span("[receipt: sha256:9a4f]", schema.ToneMuted),
			),
		),
	)
	raw, err := gouir.Encode(tree)
	if err != nil {
		return 0
	}
	renderBuf = raw
	ptr := uint32(uintptr(unsafe.Pointer(&renderBuf[0])))
	return (uint64(ptr) << 32) | uint64(len(renderBuf))
}

func main() {}
