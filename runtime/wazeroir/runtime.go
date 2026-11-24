// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package wazeroir implements marlin-mygo's zero-cgo WASM-IR plugin runtime
// using github.com/tetratelabs/wazero with a SHA-256 keyed AOT CompiledModule
// cache and hard per-plugin linear memory ceilings.
package wazeroir

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

// ModuleEngine maintains a single wazero compilation cache and pre-compiled
// AOT modules keyed by SHA-256 digest so hot-reloading or re-evaluating Go
// WASM-IR plugins avoids repeating costly WebAssembly compilation.
type ModuleEngine struct {
	rt    wazero.Runtime
	mu    sync.RWMutex
	cache map[[32]byte]wazero.CompiledModule
}

func NewModuleEngine(ctx context.Context) (*ModuleEngine, error) {
	cfg := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(pluginhost.WasmDefaultMemoryPages).
		WithCloseOnContextDone(true)
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("wazeroir: init wasi: %w", err)
	}
	return &ModuleEngine{
		rt:    rt,
		cache: make(map[[32]byte]wazero.CompiledModule),
	}, nil
}

// CompileOrGet returns a cached AOT-compiled module for wasmBytes, compiling once per digest.
func (e *ModuleEngine) CompileOrGet(ctx context.Context, wasmBytes []byte) (wazero.CompiledModule, error) {
	sum := sha256.Sum256(wasmBytes)
	e.mu.RLock()
	mod, ok := e.cache[sum]
	e.mu.RUnlock()
	if ok {
		return mod, nil
	}

	compiled, err := e.rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("wazeroir: compile module: %w", err)
	}
	e.mu.Lock()
	if existing, raced := e.cache[sum]; raced {
		e.mu.Unlock()
		_ = compiled.Close(ctx)
		return existing, nil
	}
	e.cache[sum] = compiled
	e.mu.Unlock()
	return compiled, nil
}

// ExecuteCached instantiates a pre-compiled WASI module in a fresh isolated
// sandbox (no FS, no network, bounded memory), captures stdout UI IR, and
// validates MAR-UI-R03 tree bounds before returning.
func (e *ModuleEngine) ExecuteCached(ctx context.Context, wasmBytes []byte) (schema.UIElement, error) {
	compiled, err := e.CompileOrGet(ctx, wasmBytes)
	if err != nil {
		return schema.UIElement{}, err
	}
	var stdout, stderr bytes.Buffer
	modCfg := wazero.NewModuleConfig().
		WithName(""). // anonymous isolated instance
		WithStdout(&stdout).
		WithStderr(&stderr)
	inst, err := e.rt.InstantiateModule(ctx, compiled, modCfg)
	if err != nil {
		return schema.UIElement{}, fmt.Errorf("wazeroir: instantiate cached module: %w (stderr: %s)", err, stderr.String())
	}
	_ = inst.Close(ctx)
	return pluginhost.DecodeAndAdmitWasmUITree(stdout.Bytes())
}

func (e *ModuleEngine) Close(ctx context.Context) error {
	return e.rt.Close(ctx)
}

// Runtime implements pluginhost.RuntimeFactory for schema.RuntimeWasmUIIR.
type Runtime struct {
	slots  *shell.SlotRegistry
	engine *ModuleEngine
}

func NewRuntime(slots *shell.SlotRegistry, engine *ModuleEngine) *Runtime {
	return &Runtime{slots: slots, engine: engine}
}

func (r *Runtime) Kind() schema.PanelRuntimeKind { return schema.RuntimeWasmUIIR }

func (r *Runtime) Instantiate(ctx context.Context, scope pluginhost.ScopedScope, _ *pluginhost.ScopedProxy) (pluginhost.Instance, error) {
	if scope.Assets == nil {
		return nil, fmt.Errorf("wazeroir: plugin %q has no asset filesystem", scope.PluginID)
	}
	wasmBytes, err := fs.ReadFile(scope.Assets, "panel.wasm")
	if err != nil {
		return nil, fmt.Errorf("wazeroir: read panel.wasm for %q: %w", scope.PluginID, err)
	}
	var tree schema.UIElement
	if r.engine != nil {
		tree, err = r.engine.ExecuteCached(ctx, wasmBytes)
	} else {
		tree, err = ExecuteOnce(ctx, wasmBytes)
	}
	if err != nil {
		return nil, err
	}
	inst := &wasmInstance{
		scope: scope,
		tree:  tree,
	}
	inst.renderer = mygoshell.NewUIElementRenderer(string(scope.PluginID), false, r.slots, inst.currentTree)
	return inst, nil
}

// ExecuteOnce compiles and executes a wasip1 module from scratch (cold baseline).
func ExecuteOnce(ctx context.Context, wasmBytes []byte) (schema.UIElement, error) {
	eng, err := NewModuleEngine(ctx)
	if err != nil {
		return schema.UIElement{}, err
	}
	defer eng.Close(ctx)
	return eng.ExecuteCached(ctx, wasmBytes)
}

type wasmInstance struct {
	scope    pluginhost.ScopedScope
	renderer shell.SlotRenderer

	mu   sync.RWMutex
	tree schema.UIElement
}

func (w *wasmInstance) Scope() pluginhost.ScopedScope         { return w.scope }
func (w *wasmInstance) Renderer() shell.SlotRenderer          { return w.renderer }
func (w *wasmInstance) DeliverEvent(pluginhost.ScopedEvent) error { return nil }
func (w *wasmInstance) Close() error                          { return nil }

func (w *wasmInstance) currentTree() schema.UIElement {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.tree
}
