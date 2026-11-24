// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package wazeroir

import (
	"context"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

// ResidentInstance keeps a Go WASI reactor (-buildmode=c-shared + //go:wasmexport)
// warm in linear memory across frames so Render() never reboots the guest Go runtime.
type ResidentInstance struct {
	mu       sync.Mutex
	mod      api.Module
	renderFn api.Function
}

// InstantiateResident boots a compiled WASI reactor module once (running _initialize)
// and resolves its exported marlin_ui_render entrypoint for sub-millisecond frame calls.
func (e *ModuleEngine) InstantiateResident(ctx context.Context, wasmBytes []byte) (*ResidentInstance, error) {
	compiled, err := e.CompileOrGet(ctx, wasmBytes)
	if err != nil {
		return nil, err
	}
	modCfg := wazero.NewModuleConfig().
		WithName("").
		WithStartFunctions("_initialize")
	mod, err := e.rt.InstantiateModule(ctx, compiled, modCfg)
	if err != nil {
		return nil, fmt.Errorf("wazeroir: init reactor module: %w", err)
	}
	fn := mod.ExportedFunction(pluginhost.WasmExportRender)
	if fn == nil {
		_ = mod.Close(ctx)
		return nil, fmt.Errorf("wazeroir: reactor missing export %q", pluginhost.WasmExportRender)
	}
	return &ResidentInstance{mod: mod, renderFn: fn}, nil
}

// Render invokes the guest's marlin_ui_render() in resident memory, reads the
// returned (ptr<<32 | len) slice, and validates MAR-UI-R03 tree bounds.
func (r *ResidentInstance) Render(ctx context.Context) (schema.UIElement, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, err := r.renderFn.Call(ctx)
	if err != nil {
		return schema.UIElement{}, fmt.Errorf("wazeroir: reactor render failed: %w", err)
	}
	if len(res) == 0 || res[0] == 0 {
		return schema.UIElement{}, fmt.Errorf("wazeroir: reactor returned empty slice")
	}
	packed := res[0]
	ptr := uint32(packed >> 32)
	size := uint32(packed & 0xffffffff)
	if size == 0 || size > 256*1024 {
		return schema.UIElement{}, fmt.Errorf("wazeroir: reactor payload size %d out of bounds", size)
	}
	memBytes, ok := r.mod.Memory().Read(ptr, size)
	if !ok {
		return schema.UIElement{}, fmt.Errorf("wazeroir: memory read [%d:%d] out of range", ptr, ptr+size)
	}
	return pluginhost.DecodeAndAdmitWasmUITree(memBytes)
}

func (r *ResidentInstance) Close(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mod == nil {
		return nil
	}
	err := r.mod.Close(ctx)
	r.mod = nil
	return err
}
