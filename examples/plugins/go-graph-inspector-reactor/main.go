// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build wasip1

// Reactor build of go-graph-inspector compiled with:
//   GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o panel.wasm ./examples/plugins/go-graph-inspector-reactor
//
// Demonstrates the complete bidirectional zero-cgo Go WASM-IR Reactor ABI:
//   - marlin_alloc: host-to-guest linear memory allocator
//   - marlin_ui_on_event: receives host-scoped events and calls marlin_host_v1.emit_action
//   - marlin_ui_render: emits declarative schema.UIElement trees
//   - marlin_ui_render_drawlist: emits zero-JSON binary GPU DrawList commands
package main

import (
	"encoding/json"
	"fmt"
	"unsafe"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	gouir "github.com/tao3k/marlin-mygo/sdk/go-ui-ir"
)

var (
	frameSeq     uint64
	focusedNode  = "plan_org_context"
	lastAdmitted bool
	inBuf        []byte
	renderBuf    []byte
	drawCmds     [64]schema.DrawCmd
	drawBuf      []byte
)

type incomingEvent struct {
	EventID string `json:"eventId"`
	Topic   string `json:"topic"`
	Payload struct {
		NodeID       string `json:"nodeId"`
		TriggerProbe bool   `json:"triggerProbe"`
	} `json:"payload"`
}

//go:wasmexport marlin_alloc
func marlinAlloc(size uint32) uint32 {
	if size == 0 || size > 64*1024 {
		return 0
	}
	if uint32(cap(inBuf)) < size {
		inBuf = make([]byte, size)
	} else {
		inBuf = inBuf[:size]
	}
	return uint32(uintptr(unsafe.Pointer(&inBuf[0])))
}

//go:wasmexport marlin_ui_on_event
func marlinUIOnEvent(ptr, size uint32) int32 {
	if size == 0 || size > uint32(len(inBuf)) {
		return -1
	}
	var ev incomingEvent
	if err := json.Unmarshal(inBuf[:size], &ev); err != nil {
		return -2
	}
	if ev.Payload.NodeID != "" {
		focusedNode = ev.Payload.NodeID
	}
	if ev.Payload.TriggerProbe {
		// Adversarial probe: attempt undeclared action; host ScopedProxy MUST deny.
		lastAdmitted = gouir.EmitAction("shell.exec", []byte(`{"cmd":"whoami"}`), "probe-"+ev.EventID)
		return 0
	}
	args, _ := json.Marshal(map[string]string{"nodeId": focusedNode})
	lastAdmitted = gouir.EmitAction("graph.node.inspect", args, "inspect-"+ev.EventID)
	return 0
}

//go:wasmexport marlin_ui_render
func marlinUIRender() uint64 {
	frameSeq++
	status := "idle"
	if lastAdmitted {
		status = "action-admitted"
	}
	tree := gouir.Panel(
		"Graph Trajectory (Resident Reactor)",
		schema.ToneInfo,
		gouir.Rows(
			gouir.Text(
				gouir.Span("run-01 ", schema.ToneAccent),
				gouir.Span(fmt.Sprintf("frame #%d verified (%s)", frameSeq, status), schema.ToneSuccess),
			),
			gouir.Text(
				gouir.Span("• "+focusedNode+"  ", schema.ToneNormal),
				gouir.Span("[receipt: sha256:9a4f]", schema.ToneMuted),
			),
		),
	)
	raw, err := gouir.Encode(tree)
	if err != nil {
		return 0
	}
	renderBuf = raw
	return gouir.PackBytes(renderBuf)
}

//go:wasmexport marlin_ui_render_drawlist
func marlinUIRenderDrawList() uint64 {
	frameSeq++
	for i := range drawCmds {
		drawCmds[i] = schema.DrawCmd{
			Op:     schema.DrawOpFillRect,
			Radius: 4,
			X:      float32(i%8) * 28,
			Y:      float32(i/8) * 18,
			W:      22,
			H:      12,
			RGBA:   0x51afefFF,
			Extra0: uint32(frameSeq),
		}
	}
	enc, err := schema.EncodeDrawList(drawCmds[:], drawBuf[:0])
	if err != nil {
		return 0
	}
	drawBuf = enc
	return gouir.PackBytes(drawBuf)
}

func main() {}
