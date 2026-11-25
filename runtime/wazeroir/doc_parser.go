// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package wazeroir

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	"github.com/tao3k/marlin-mygo/engine/richtext"
)

const (
	defaultDocParserTimeout = 500 * time.Millisecond
	maxDocParserOutputBytes = 2 << 20 // 2 MiB fail-closed ceiling
)

// WasmDocumentParser executes an external WASI Reactor document parser module
// (such as a wasm32-wasip1 build of tao3k/orgize or a Markdown WASM module)
// inside wazero's AOT-cached sandbox and implements richtext.Parser.
//
// Required WASM reactor exports (CGO_ENABLED=0 linear-memory ABI):
//   - alloc(size uint32) -> ptr uint32
//   - parse_document(fmtPtr, fmtLen, srcPtr, srcLen uint32) -> packed uint64 ((outPtr << 32) | outLen)
// Optional export:
//   - dealloc(ptr, size uint32)
//   - _initialize() (invoked once on load if exported)
//
// Output bytes are decoded via richtext.DecodeDocumentAutoJSON so the WASM
// module may emit either richtext.DocumentEnvelope, orgize sectionIndexJson, or
// orgize orgElementsJson with fail-closed bounds and XSS link filtering.
type WasmDocumentParser struct {
	mu       sync.Mutex
	eng      *ModuleEngine
	compiled wazero.CompiledModule
	timeout  time.Duration
	limits   richtext.AdmissionLimits
}

// WasmDocumentParserConfig tunes the WASI document parser execution sandbox.
type WasmDocumentParserConfig struct {
	Engine  *ModuleEngine
	Timeout time.Duration
	Limits  richtext.AdmissionLimits
}

// NewWasmDocumentParser compiles (or hits the persistent AOT cache for) a WASI
// document parser module once for low-latency repeated ParseDocument calls.
func NewWasmDocumentParser(ctx context.Context, wasmBytes []byte, cfg WasmDocumentParserConfig) (*WasmDocumentParser, error) {
	if len(wasmBytes) == 0 {
		return nil, errors.New("wazeroir: empty document parser WASM bytes")
	}
	eng := cfg.Engine
	if eng == nil {
		var err error
		eng, err = NewModuleEngineWithConfig(ctx, EngineConfig{DisableDiskCache: true})
		if err != nil {
			return nil, err
		}
	}
	compiled, err := eng.CompileOrGet(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("wazeroir: compile document parser WASM: %w", err)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultDocParserTimeout
	}
	lim := cfg.Limits
	if lim.MaxPayloadBytes <= 0 {
		lim = richtext.DefaultAdmissionLimits()
	}
	return &WasmDocumentParser{
		eng:      eng,
		compiled: compiled,
		timeout:  timeout,
		limits:   lim,
	}, nil
}

// ParseDocument implements richtext.Parser using a fresh timeout-guarded WASI
// instance with strict memory bounds so malformed inputs or infinite loops
// never stall Marlin's 120Hz GPU UI thread.
func (p *WasmDocumentParser) ParseDocument(ctx context.Context, format richtext.Format, docID, source string) ([]richtext.Block, error) {
	if p == nil || p.compiled == nil {
		return nil, errors.New("wazeroir: nil WasmDocumentParser")
	}
	if len(source) > p.limits.MaxPayloadBytes {
		return nil, fmt.Errorf("%w: input %d > %d", richtext.ErrIRTooLarge, len(source), p.limits.MaxPayloadBytes)
	}

	callCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	p.mu.Lock()
	defer p.mu.Unlock()

	modCfg := wazero.NewModuleConfig().
		WithName("").
		WithStartFunctions()
	mod, err := p.eng.rt.InstantiateModule(callCtx, p.compiled, modCfg)
	if err != nil {
		return nil, fmt.Errorf("wazeroir: instantiate doc parser module: %w", err)
	}
	defer mod.Close(context.Background())

	if initFn := mod.ExportedFunction("_initialize"); initFn != nil {
		if _, err := initFn.Call(callCtx); err != nil {
			return nil, fmt.Errorf("wazeroir: doc parser _initialize failed: %w", err)
		}
	}
	allocFn := mod.ExportedFunction("alloc")
	parseFn := mod.ExportedFunction("parse_document")
	if allocFn == nil || parseFn == nil {
		return nil, errors.New("wazeroir: doc parser WASM must export alloc(u32)->u32 and parse_document(u32,u32,u32,u32)->u64")
	}
	deallocFn := mod.ExportedFunction("dealloc")
	mem := mod.Memory()
	if mem == nil {
		return nil, errors.New("wazeroir: doc parser WASM exports no linear memory")
	}

	fmtBytes := []byte(format)
	srcBytes := []byte(source)

	fmtPtr, err := wasmAllocWrite(callCtx, allocFn, mem, fmtBytes)
	if err != nil {
		return nil, err
	}
	if deallocFn != nil && len(fmtBytes) > 0 {
		defer func() { _, _ = deallocFn.Call(context.Background(), uint64(fmtPtr), uint64(len(fmtBytes))) }()
	}

	srcPtr, err := wasmAllocWrite(callCtx, allocFn, mem, srcBytes)
	if err != nil {
		return nil, err
	}
	if deallocFn != nil && len(srcBytes) > 0 {
		defer func() { _, _ = deallocFn.Call(context.Background(), uint64(srcPtr), uint64(len(srcBytes))) }()
	}

	res, err := parseFn.Call(callCtx, uint64(fmtPtr), uint64(len(fmtBytes)), uint64(srcPtr), uint64(len(srcBytes)))
	if err != nil {
		return nil, fmt.Errorf("wazeroir: parse_document call failed: %w", err)
	}
	if len(res) != 1 {
		return nil, fmt.Errorf("wazeroir: parse_document returned %d values, expected packed u64", len(res))
	}
	packed := res[0]
	outPtr := uint32(packed >> 32)
	outLen := uint32(packed & 0xFFFFFFFF)
	if outLen == 0 {
		return nil, nil
	}
	if int(outLen) > maxDocParserOutputBytes || int(outLen) > p.limits.MaxPayloadBytes {
		return nil, fmt.Errorf("%w: WASM output %d bytes exceeds ceiling", richtext.ErrIRTooLarge, outLen)
	}
	rawJSON, ok := mem.Read(outPtr, outLen)
	if !ok {
		return nil, fmt.Errorf("wazeroir: parse_document output slice [%d:+%d] out of linear memory bounds", outPtr, outLen)
	}
	// Copy out of WASM linear memory before closing runtime.
	copied := make([]byte, len(rawJSON))
	copy(copied, rawJSON)
	return richtext.DecodeDocumentAutoJSONWithLimits(docID, copied, p.limits)
}

func wasmAllocWrite(ctx context.Context, allocFn api.Function, mem api.Memory, data []byte) (uint32, error) {
	if len(data) == 0 {
		return 0, nil
	}
	res, err := allocFn.Call(ctx, uint64(len(data)))
	if err != nil || len(res) != 1 {
		return 0, fmt.Errorf("wazeroir: alloc(%d) failed: %w", len(data), err)
	}
	ptr := uint32(res[0])
	if !mem.Write(ptr, data) {
		return 0, fmt.Errorf("wazeroir: memory write [%d:+%d] out of bounds", ptr, len(data))
	}
	return ptr, nil
}
