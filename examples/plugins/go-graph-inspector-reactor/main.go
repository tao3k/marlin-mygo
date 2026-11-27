// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build wasip1

// Reactor build of go-graph-inspector compiled with:
//
//	GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -buildmode=c-shared -o panel.wasm ./examples/plugins/go-graph-inspector-reactor
//
// Demonstrates the complete zero-unsafe Go WASM-IR Reactor ABI:
//   - marlin_alloc: delegates to gouir.AllocBuffer
//   - marlin_ui_on_event: delegates to gouir.DispatchEvent + gouir.EmitAction
//   - marlin_ui_render: returns gouir.RenderResult with interactive GPU ActionButton
//   - marlin_ui_render_drawlist: returns gouir.DrawListResult (zero-JSON binary GPU cmds)
package main

import (
	"encoding/json"
	"fmt"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	gouir "github.com/tao3k/marlin-mygo/sdk/go-ui-ir"
)

var (
	frameSeq     uint64
	focusedNode  = "plan_org_context"
	lastAdmitted bool
	drawCmds     [64]schema.DrawCmd
)

type graphEventPayload struct {
	NodeID       string `json:"nodeId"`
	TriggerProbe bool   `json:"triggerProbe"`
}

//go:wasmexport marlin_alloc
func marlinAlloc(size uint32) uint32 {
	return gouir.AllocBuffer(size)
}

//go:wasmexport marlin_ui_on_event
func marlinUIOnEvent(ptr, size uint32) int32 {
	return gouir.DispatchEvent(ptr, size, func(ev gouir.Event) error {
		var p graphEventPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return err
		}
		if p.NodeID != "" {
			focusedNode = p.NodeID
		}
		if p.TriggerProbe {
			// Adversarial probe: attempt undeclared action; host ScopedProxy MUST deny.
			lastAdmitted = gouir.EmitAction("shell.exec", []byte(`{"cmd":"whoami"}`), "probe-"+ev.EventID)
			return nil
		}
		args, _ := json.Marshal(map[string]string{"nodeId": focusedNode})
		lastAdmitted = gouir.EmitAction("graph.node.inspect", args, "inspect-"+ev.EventID)
		return nil
	})
}

//go:wasmexport marlin_ui_render
func marlinUIRender() uint64 {
	frameSeq++
	status := "idle"
	if lastAdmitted {
		status = "action-admitted"
	}
	argsJSON := fmt.Sprintf(`{"nodeId":%q}`, focusedNode)
	return gouir.RenderResult(gouir.Panel(
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
			gouir.ActionButton("Inspect "+focusedNode, "graph.node.inspect", argsJSON, schema.ToneAccent),
		),
	))
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
			Extra1: uint32(i),
		}
	}
	return gouir.DrawListResult(drawCmds[:])
}

func main() {}
