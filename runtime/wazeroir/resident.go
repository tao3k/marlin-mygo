// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package wazeroir

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

// ResidentInstance keeps a Go WASI reactor (-buildmode=c-shared + //go:wasmexport)
// warm in linear memory across frames so OnEvent(), Render(), and zero-JSON
// RenderDrawList() execute in microseconds without rebooting the guest runtime.
type ResidentInstance struct {
	mu           sync.Mutex
	eng          *ModuleEngine
	mod          api.Module
	allocFn      api.Function
	onEventFn    api.Function
	renderFn     api.Function
	drawListFn   api.Function
}

// InstantiateResident boots a compiled WASI reactor module once (running _initialize)
// without a ScopedProxy (suitable for pure read-only benchmarks and tests).
func (e *ModuleEngine) InstantiateResident(ctx context.Context, wasmBytes []byte) (*ResidentInstance, error) {
	return e.InstantiateResidentWithProxy(ctx, wasmBytes, nil)
}

// InstantiateResidentWithProxy boots a WASI reactor and binds its unforgeable
// api.Module identity to proxy for marlin_host_v1.emit_action authorization.
func (e *ModuleEngine) InstantiateResidentWithProxy(ctx context.Context, wasmBytes []byte, proxy *pluginhost.ScopedProxy) (*ResidentInstance, error) {
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
	e.bindProxy(mod, proxy)
	return &ResidentInstance{
		eng:        e,
		mod:        mod,
		allocFn:    mod.ExportedFunction(pluginhost.WasmExportAlloc),
		onEventFn:  mod.ExportedFunction(pluginhost.WasmExportOnEvent),
		renderFn:   fn,
		drawListFn: mod.ExportedFunction(pluginhost.WasmExportRenderDrawList),
	}, nil
}

// OnEvent serializes a host-scoped event, allocates a guest linear-memory buffer
// via marlin_alloc, and invokes marlin_ui_on_event(ptr, len) inside the reactor.
func (r *ResidentInstance) OnEvent(ctx context.Context, ev pluginhost.ScopedEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mod == nil {
		return fmt.Errorf("wazeroir: reactor instance is closed")
	}
	if r.allocFn == nil || r.onEventFn == nil {
		return fmt.Errorf("wazeroir: reactor does not export %q/%q", pluginhost.WasmExportAlloc, pluginhost.WasmExportOnEvent)
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("wazeroir: marshal event: %w", err)
	}
	if len(raw) == 0 || len(raw) > pluginhost.WasmMaxEventBytes {
		return fmt.Errorf("wazeroir: event size %d exceeds cap %d", len(raw), pluginhost.WasmMaxEventBytes)
	}
	allocRes, err := r.allocFn.Call(ctx, uint64(len(raw)))
	if err != nil || len(allocRes) == 0 || allocRes[0] == 0 {
		return fmt.Errorf("wazeroir: guest marlin_alloc(%d) failed: %w", len(raw), err)
	}
	ptr := uint32(allocRes[0])
	if !r.mod.Memory().Write(ptr, raw) {
		return fmt.Errorf("wazeroir: guest memory write [%d:%d] out of range", ptr, ptr+uint32(len(raw)))
	}
	res, err := r.onEventFn.Call(ctx, uint64(ptr), uint64(len(raw)))
	if err != nil {
		return fmt.Errorf("wazeroir: guest marlin_ui_on_event trapped: %w", err)
	}
	if len(res) > 0 && int32(res[0]) != 0 {
		return fmt.Errorf("wazeroir: guest marlin_ui_on_event returned error code %d", int32(res[0]))
	}
	return nil
}

// Render invokes the guest's marlin_ui_render() in resident memory, reads the
// returned (ptr<<32 | len) slice, and validates MAR-UI-R03 tree bounds.
func (r *ResidentInstance) Render(ctx context.Context) (schema.UIElement, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mod == nil {
		return schema.UIElement{}, fmt.Errorf("wazeroir: reactor instance is closed")
	}
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

// RenderDrawList invokes the optional high-frequency zero-JSON binary GPU
// DrawList entrypoint (marlin_ui_render_drawlist) and decodes directly into
// caller-provided dst[:0] with zero JSON parsing.
func (r *ResidentInstance) RenderDrawList(ctx context.Context, dst []schema.DrawCmd) ([]schema.DrawCmd, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mod == nil {
		return nil, fmt.Errorf("wazeroir: reactor instance is closed")
	}
	if r.drawListFn == nil {
		return nil, fmt.Errorf("wazeroir: reactor missing export %q", pluginhost.WasmExportRenderDrawList)
	}
	res, err := r.drawListFn.Call(ctx)
	if err != nil {
		return nil, fmt.Errorf("wazeroir: reactor drawlist failed: %w", err)
	}
	if len(res) == 0 || res[0] == 0 {
		return nil, fmt.Errorf("wazeroir: reactor returned empty drawlist slice")
	}
	packed := res[0]
	ptr := uint32(packed >> 32)
	size := uint32(packed & 0xffffffff)
	if size < schema.DrawListHeaderBytes || size > schema.MaxDrawListByteBytes {
		return nil, fmt.Errorf("wazeroir: drawlist size %d out of bounds", size)
	}
	memBytes, ok := r.mod.Memory().Read(ptr, size)
	if !ok {
		return nil, fmt.Errorf("wazeroir: drawlist read [%d:%d] out of range", ptr, ptr+size)
	}
	return schema.DecodeDrawList(memBytes, dst)
}

func (r *ResidentInstance) Close(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mod == nil {
		return nil
	}
	if r.eng != nil {
		r.eng.unbindProxy(r.mod)
	}
	err := r.mod.Close(ctx)
	r.mod = nil
	return err
}
