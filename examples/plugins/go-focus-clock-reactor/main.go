// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build wasip1

// Reactor build of go-focus-clock-reactor compiled with:
//
//	GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -buildmode=c-shared -o panel.wasm ./examples/plugins/go-focus-clock-reactor
//
// Demonstrates a 100% zero-unsafe Go WASM-IR Reactor rendering a live personal
// Focus Alarm, Org clock rollup, next agenda item, and quick-capture log directly
// on MyGo's GPU with 0 MB WebView RAM.
package main

import (
	"encoding/json"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	gouir "github.com/tao3k/marlin-mygo/sdk/go-ui-ir"
)

var (
	clockState = NewFocusClockState()
	drawCmds   [64]schema.DrawCmd
)

//go:wasmexport marlin_alloc
func marlinAlloc(size uint32) uint32 {
	return gouir.AllocBuffer(size)
}

//go:wasmexport marlin_ui_on_event
func marlinUIOnEvent(ptr, size uint32) int32 {
	return gouir.DispatchEvent(ptr, size, func(ev gouir.Event) error {
		completed, err := clockState.ApplyEvent(ev)
		if err != nil {
			return err
		}
		if completed {
			args, _ := json.Marshal(map[string]any{
				"title":           clockState.ActiveTitle,
				"completedBlocks": clockState.CompletedBlocks,
			})
			_ = gouir.EmitAction("org.clock.out", args, "clock-complete-"+ev.EventID)
		}
		return nil
	})
}

//go:wasmexport marlin_ui_render
func marlinUIRender() uint64 {
	return gouir.RenderResult(clockState.BuildUITree())
}

//go:wasmexport marlin_ui_render_drawlist
func marlinUIRenderDrawList() uint64 {
	return gouir.DrawListResult(clockState.BuildDrawList(&drawCmds))
}

func main() {}
