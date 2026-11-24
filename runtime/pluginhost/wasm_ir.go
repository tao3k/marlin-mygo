// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package pluginhost

import (
	"encoding/json"
	"fmt"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// WasmUIABIVersion identifies the zero-cgo wazero host/guest ABI contract for
// Go/Rust/Zig wasip1 plugins emitting marlin.ui.v1 / schema.UIElement trees
// or high-frequency zero-JSON binary DrawList buffers.
const (
	WasmUIABIVersion         = 1
	WasmDefaultMemoryPages   = 256 // 16 MiB linear memory ceiling per plugin
	WasmMaxEventBytes        = 64 << 10
	WasmExportAlloc          = "marlin_alloc"
	WasmExportOnEvent        = "marlin_ui_on_event"
	WasmExportRender         = "marlin_ui_render"
	WasmExportRenderDrawList = "marlin_ui_render_drawlist"
	WasmHostModule           = "marlin_host_v1"
	WasmHostEmitAction       = "emit_action"
)

// WasmIRGuest abstracts a sandboxed WASM-IR instance (implemented over wazero
// in production or pure-Go fakes in unit tests).
type WasmIRGuest interface {
	OnEvent(eventJSON []byte) error
	Render() ([]byte, error)
	Close() error
}

// DecodeAndAdmitWasmUITree parses a guest-emitted UI IR payload and enforces
// MAR-UI-R03 node and depth bounds before handing it to MyGo's GPU renderer.
func DecodeAndAdmitWasmUITree(raw []byte) (schema.UIElement, error) {
	var elem schema.UIElement
	if err := json.Unmarshal(raw, &elem); err != nil {
		return schema.UIElement{}, fmt.Errorf("pluginhost: invalid wasm UI IR payload: %w", err)
	}
	if err := elem.ValidateBounds(schema.MaxUITreeNodes, schema.MaxUITreeDepth); err != nil {
		return schema.UIElement{}, err
	}
	return elem, nil
}
