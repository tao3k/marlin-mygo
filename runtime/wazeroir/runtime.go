// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package wazeroir implements marlin-mygo's zero-cgo WASM-IR plugin runtime
// using github.com/tetratelabs/wazero with a SHA-256 keyed AOT CompiledModule
// cache, hard per-plugin linear memory ceilings, bidirectional marlin_host_v1
// scoped actions, and zero-JSON binary DrawList rendering.
package wazeroir

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

// EngineConfig configures ModuleEngine's AOT compilation cache.
type EngineConfig struct {
	// CompilationCacheDir stores SHA-256 keyed native AOT machine code on disk
	// via wazero.NewCompilationCacheWithDir so desktop restarts and lazy wakes
	// skip full Go WASI AOT recompilation (~350ms -> ~8ms).
	// When empty and DisableDiskCache is false, DefaultCompilationCacheDir() is used.
	CompilationCacheDir string
	// DisableDiskCache forces an ephemeral in-memory compilation cache only.
	DisableDiskCache bool
}

// DefaultCompilationCacheDir resolves the persistent disk directory used for
// wazero AOT compilation caching (honors MARLIN_WAZERO_CACHE_DIR when set).
func DefaultCompilationCacheDir() string {
	if env := strings.TrimSpace(os.Getenv("MARLIN_WAZERO_CACHE_DIR")); env != "" {
		return env
	}
	if base, err := os.UserCacheDir(); err == nil && base != "" {
		return filepath.Join(base, "marlin-mygo", "wazero-aot-v1")
	}
	return filepath.Join(os.TempDir(), "marlin-mygo-wazero-aot-v1")
}

// ModuleEngine maintains a single wazero compilation cache (backed by a
// persistent disk AOT cache when available), the authoritative marlin_host_v1
// module, and an unforgeable api.Module -> *ScopedProxy table (the WASM
// equivalent of WebView CallerPage isolation).
type ModuleEngine struct {
	rt        wazero.Runtime
	compCache wazero.CompilationCache
	cacheDir  string
	mu        sync.RWMutex
	cache     map[[32]byte]wazero.CompiledModule
	proxies   map[api.Module]*pluginhost.ScopedProxy
}

func NewModuleEngine(ctx context.Context) (*ModuleEngine, error) {
	return NewModuleEngineWithConfig(ctx, EngineConfig{})
}

// NewModuleEngineWithConfig initializes a zero-cgo wazero runtime with hard
// linear-memory ceilings and optional disk-persistent AOT compilation caching.
func NewModuleEngineWithConfig(ctx context.Context, ecfg EngineConfig) (*ModuleEngine, error) {
	cfg := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(pluginhost.WasmDefaultMemoryPages).
		WithCloseOnContextDone(true)

	var (
		compCache wazero.CompilationCache
		usedDir   string
	)
	if !ecfg.DisableDiskCache {
		dir := strings.TrimSpace(ecfg.CompilationCacheDir)
		if dir == "" {
			dir = DefaultCompilationCacheDir()
		}
		if err := os.MkdirAll(dir, 0o755); err == nil {
			if cc, ccErr := wazero.NewCompilationCacheWithDir(dir); ccErr == nil {
				compCache = cc
				usedDir = dir
				cfg = cfg.WithCompilationCache(cc)
			}
		}
	}

	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		if compCache != nil {
			_ = compCache.Close(ctx)
		}
		return nil, fmt.Errorf("wazeroir: init wasi: %w", err)
	}
	eng := &ModuleEngine{
		rt:        rt,
		compCache: compCache,
		cacheDir:  usedDir,
		cache:     make(map[[32]byte]wazero.CompiledModule),
		proxies:   make(map[api.Module]*pluginhost.ScopedProxy),
	}
	if err := eng.registerHostModule(ctx); err != nil {
		_ = eng.Close(ctx)
		return nil, err
	}
	return eng, nil
}

// CompilationCacheDir returns the active persistent disk AOT cache directory,
// or empty string when running with an ephemeral in-memory cache only.
func (e *ModuleEngine) CompilationCacheDir() string {
	return e.cacheDir
}

func (e *ModuleEngine) registerHostModule(ctx context.Context) error {
	_, err := e.rt.NewHostModuleBuilder(pluginhost.WasmHostModule).
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(e.hostEmitAction),
			[]api.ValueType{
				api.ValueTypeI32, api.ValueTypeI32,
				api.ValueTypeI32, api.ValueTypeI32,
				api.ValueTypeI32, api.ValueTypeI32,
			},
			[]api.ValueType{api.ValueTypeI32},
		).
		Export(pluginhost.WasmHostEmitAction).
		Instantiate(ctx)
	if err != nil {
		return fmt.Errorf("wazeroir: instantiate %s host module: %w", pluginhost.WasmHostModule, err)
	}
	return nil
}

// hostEmitAction enforces module-identity -> ScopedProxy binding, payload caps,
// manifest DeclaredActions allowlists, and atomic revocation.
// Return codes: 0 = admitted, -1 = unbound/revoked, -2 = invalid memory/bounds, -3 = denied.
func (e *ModuleEngine) hostEmitAction(ctx context.Context, mod api.Module, stack []uint64) {
	actPtr, actLen := uint32(stack[0]), uint32(stack[1])
	argsPtr, argsLen := uint32(stack[2]), uint32(stack[3])
	idempPtr, idempLen := uint32(stack[4]), uint32(stack[5])

	e.mu.RLock()
	proxy := e.proxies[mod]
	e.mu.RUnlock()
	if proxy == nil {
		stack[0] = api.EncodeI32(-1)
		return
	}
	if actLen == 0 || actLen > 256 || argsLen > pluginhost.MaxActionBodyBytes || idempLen > 256 {
		stack[0] = api.EncodeI32(-2)
		return
	}
	mem := mod.Memory()
	if mem == nil {
		stack[0] = api.EncodeI32(-2)
		return
	}
	actBytes, ok := mem.Read(actPtr, actLen)
	if !ok {
		stack[0] = api.EncodeI32(-2)
		return
	}
	var argsRaw json.RawMessage
	if argsLen > 0 {
		ab, ok := mem.Read(argsPtr, argsLen)
		if !ok {
			stack[0] = api.EncodeI32(-2)
			return
		}
		argsRaw = append(json.RawMessage(nil), ab...)
	}
	var idemp string
	if idempLen > 0 {
		ib, ok := mem.Read(idempPtr, idempLen)
		if !ok {
			stack[0] = api.EncodeI32(-2)
			return
		}
		idemp = string(ib)
	}
	receipt, err := proxy.SubmitAction(ctx, string(actBytes), argsRaw, idemp)
	if err != nil || !receipt.Admitted {
		stack[0] = api.EncodeI32(-3)
		return
	}
	stack[0] = 0
}

func (e *ModuleEngine) bindProxy(mod api.Module, proxy *pluginhost.ScopedProxy) {
	if proxy == nil || mod == nil {
		return
	}
	e.mu.Lock()
	e.proxies[mod] = proxy
	e.mu.Unlock()
}

func (e *ModuleEngine) unbindProxy(mod api.Module) {
	if mod == nil {
		return
	}
	e.mu.Lock()
	delete(e.proxies, mod)
	e.mu.Unlock()
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

// ExecuteCached instantiates a pre-compiled WASI _start module in a fresh isolated
// sandbox (no FS, no network, bounded memory), captures stdout UI IR, and
// validates MAR-UI-R03 tree bounds before returning.
func (e *ModuleEngine) ExecuteCached(ctx context.Context, wasmBytes []byte) (schema.UIElement, error) {
	compiled, err := e.CompileOrGet(ctx, wasmBytes)
	if err != nil {
		return schema.UIElement{}, err
	}
	var stdout, stderr bytes.Buffer
	modCfg := wazero.NewModuleConfig().
		WithName("").
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
	rtErr := e.rt.Close(ctx)
	var ccErr error
	if e.compCache != nil {
		ccErr = e.compCache.Close(ctx)
	}
	if rtErr != nil {
		return rtErr
	}
	return ccErr
}

// Runtime implements pluginhost.RuntimeFactory for schema.RuntimeWasmUIIR.
// If panel.wasm exports marlin_ui_render, it automatically runs as a warm
// ResidentInstance with live bidirectional OnEvent + emit_action support.
type Runtime struct {
	slots  *shell.SlotRegistry
	engine *ModuleEngine
}

func NewRuntime(slots *shell.SlotRegistry, engine *ModuleEngine) *Runtime {
	return &Runtime{slots: slots, engine: engine}
}

func (r *Runtime) Kind() schema.PanelRuntimeKind { return schema.RuntimeWasmUIIR }

func (r *Runtime) Instantiate(ctx context.Context, scope pluginhost.ScopedScope, proxy *pluginhost.ScopedProxy) (pluginhost.Instance, error) {
	if scope.Assets == nil {
		return nil, fmt.Errorf("wazeroir: plugin %q has no asset filesystem", scope.PluginID)
	}
	entry := scope.EntryPath
	if entry == "" {
		entry = "panel.wasm"
	}
	wasmBytes, err := fs.ReadFile(scope.Assets, entry)
	if err != nil {
		return nil, fmt.Errorf("wazeroir: read %s for %q: %w", entry, scope.PluginID, err)
	}

	eng := r.engine
	ownedEngine := false
	if eng == nil {
		eng, err = NewModuleEngine(ctx)
		if err != nil {
			return nil, err
		}
		ownedEngine = true
	}

	compiled, err := eng.CompileOrGet(ctx, wasmBytes)
	if err != nil {
		if ownedEngine {
			_ = eng.Close(ctx)
		}
		return nil, err
	}

	// Prefer resident WASI Reactor mode whenever marlin_ui_render is exported.
	if _, isReactor := compiled.ExportedFunctions()[pluginhost.WasmExportRender]; isReactor {
		resident, err := eng.InstantiateResidentWithProxy(ctx, wasmBytes, proxy)
		if err != nil {
			if ownedEngine {
				_ = eng.Close(ctx)
			}
			return nil, err
		}
		tree, err := resident.Render(ctx)
		if err != nil {
			_ = resident.Close(ctx)
			if ownedEngine {
				_ = eng.Close(ctx)
			}
			return nil, err
		}
		inst := &wasmInstance{
			scope:       scope,
			resident:    resident,
			ownedEngine: ownedEngine,
			eng:         eng,
			tree:        tree,
		}
		rend := mygoshell.NewUIElementRenderer(string(scope.PluginID), false, r.slots, inst.currentTree)
		if proxy != nil {
			var actionSeq atomic.Uint64
			rend.OnAction = func(action, argsJSON string) {
				var raw json.RawMessage
				if argsJSON != "" {
					raw = json.RawMessage(argsJSON)
				}
				idemp := fmt.Sprintf("wasm-ui-%s-%d", scope.PluginID, actionSeq.Add(1))
				_, _ = proxy.SubmitAction(context.Background(), action, raw, idemp)
			}
		}
		inst.renderer = rend
		return inst, nil
	}

	tree, err := eng.ExecuteCached(ctx, wasmBytes)
	if ownedEngine {
		_ = eng.Close(ctx)
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
	scope       pluginhost.ScopedScope
	renderer    shell.SlotRenderer
	resident    *ResidentInstance
	ownedEngine bool
	eng         *ModuleEngine

	mu   sync.RWMutex
	tree schema.UIElement
}

func (w *wasmInstance) Scope() pluginhost.ScopedScope { return w.scope }
func (w *wasmInstance) Renderer() shell.SlotRenderer  { return w.renderer }

func (w *wasmInstance) DeliverEvent(ev pluginhost.ScopedEvent) error {
	if w.resident == nil {
		return nil
	}
	ctx := context.Background()
	if err := w.resident.OnEvent(ctx, ev); err != nil {
		return err
	}
	tree, err := w.resident.Render(ctx)
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.tree = tree
	w.mu.Unlock()
	return nil
}

func (w *wasmInstance) Close() error {
	var err error
	if w.resident != nil {
		err = w.resident.Close(context.Background())
		w.resident = nil
	}
	if w.ownedEngine && w.eng != nil {
		_ = w.eng.Close(context.Background())
		w.eng = nil
	}
	return err
}

func (w *wasmInstance) currentTree() schema.UIElement {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.tree
}

// RenderDrawList exposes the mounted reactor's zero-JSON binary GPU DrawList path.
func (w *wasmInstance) RenderDrawList(ctx context.Context, dst []schema.DrawCmd) ([]schema.DrawCmd, error) {
	if w.resident == nil {
		return nil, fmt.Errorf("wazeroir: plugin %q is not a resident reactor", w.scope.PluginID)
	}
	return w.resident.RenderDrawList(ctx, dst)
}
