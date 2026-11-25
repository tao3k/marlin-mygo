// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package wazeroir_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/engine/richtext"
	"github.com/tao3k/marlin-mygo/runtime/wazeroir"
)

func TestWasmDocumentParserEndToEndOrgizeDTOAndTimeoutGuard(t *testing.T) {
	ctx := context.Background()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not in PATH")
	}

	// 1. Compile a tiny real WASI Reactor module implementing the exact
	// `alloc` + `parse_document` linear-memory ABI returning a tao3k/orgize
	// OrgizeSectionIndexResponseDto JSON payload!
	tmpDir := t.TempDir()
	modSrc := `package main

import "unsafe"

var outBuf []byte

//go:wasmexport alloc
func alloc(size uint32) uint32 {
	buf := make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&buf[0])))
}

//go:wasmexport parse_document
func parse_document(fmtPtr, fmtLen, srcPtr, srcLen uint32) uint64 {
	_ = unsafe.Slice((*byte)(unsafe.Pointer(uintptr(fmtPtr))), fmtLen)
	_ = unsafe.Slice((*byte)(unsafe.Pointer(uintptr(srcPtr))), srcLen)
	outBuf = []byte(` + "`" + `{"schemaVersion":1,"records":[{"level":1,"titleText":"Parsed Inside Wazero WASM","todo":"TODO","priority":{"raw":"[#A]","effective":"A","isDefault":false},"tags":["orgize","wazero"],"properties":[{"key":"ENGINE","value":"wasm32-wasip1"}],"planning":{"scheduled":{"kind":"active","raw":"<2026-11-26 Thu>"}},"body":[{"text":"Zero Go-side Org parser!"}],"links":[{"path":"https://orgmode.org","description":"Org"},{"path":"javascript:alert(1)","description":"Blocked"}]}]}` + "`" + `)
	ptr := uint32(uintptr(unsafe.Pointer(&outBuf[0])))
	return (uint64(ptr) << 32) | uint64(len(outBuf))
}

func main() {}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module orgparserwasm\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(modSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	wasmPath := filepath.Join(tmpDir, "org_parser.wasm")
	build := exec.CommandContext(ctx, goBin, "build", "-buildmode=c-shared", "-o", wasmPath, ".")
	build.Dir = tmpDir
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile WASI doc parser failed: %v\n%s", err, string(out))
	}
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}

	parser, err := wazeroir.NewWasmDocumentParser(ctx, wasmBytes, wazeroir.WasmDocumentParserConfig{
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewWasmDocumentParser failed: %v", err)
	}

	blocks, err := parser.ParseDocument(ctx, richtext.FormatOrg, "wasm-doc-1", "* TODO [#A] Source")
	if err != nil {
		t.Fatalf("ParseDocument via wazero failed: %v", err)
	}
	if len(blocks) != 5 {
		t.Fatalf("expected 5 admitted Org blocks from WASM, got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].TodoWord != "TODO" || blocks[0].Priority != "A" || blocks[0].RawText != "Parsed Inside Wazero WASM" {
		t.Fatalf("unexpected WASM Org heading: %+v", blocks[0])
	}
	if blocks[4].Spans[1].Kind != richtext.SpanPlain || blocks[4].Spans[1].URL != "" {
		t.Fatalf("fail-closed XSS check failed on WASM output: %+v", blocks[4].Spans[1])
	}

	// 2. Verify an infinite-loop WASM parser is terminated promptly by Timeout guard!
	// Minimal WebAssembly 1.0 module exporting memory, alloc() -> 0, and parse_document() -> (loop (br 0)):
	hangWasm := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		// Type section (14 bytes): type 0 = (i32)->(i32), type 1 = (i32,i32,i32,i32)->(i64)
		0x01, 0x0e, 0x02,
		0x60, 0x01, 0x7f, 0x01, 0x7f,
		0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7e,
		// Function section (3 bytes): func 0 -> type 0, func 1 -> type 1
		0x03, 0x03, 0x02, 0x00, 0x01,
		// Memory section (3 bytes): 1 page
		0x05, 0x03, 0x01, 0x00, 0x01,
		// Export section (35 bytes): memory, alloc, parse_document
		0x07, 0x23, 0x03,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x05, 'a', 'l', 'l', 'o', 'c', 0x00, 0x00,
		0x0e, 'p', 'a', 'r', 's', 'e', '_', 'd', 'o', 'c', 'u', 'm', 'e', 'n', 't', 0x00, 0x01,
		// Code section (16 bytes): func 0 returns 0; func 1 loops forever
		0x0a, 0x10, 0x02,
		0x04, 0x00, 0x41, 0x00, 0x0b,
		0x09, 0x00, 0x03, 0x40, 0x0c, 0x00, 0x0b, 0x42, 0x00, 0x0b,
	}
	hangParser, err := wazeroir.NewWasmDocumentParser(ctx, hangWasm, wazeroir.WasmDocumentParserConfig{
		Timeout: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("compile hangWasm failed: %v", err)
	}
	start := time.Now()
	if _, err := hangParser.ParseDocument(ctx, richtext.FormatOrg, "hang", "x"); err == nil {
		t.Fatal("expected infinite-loop WASM parser to be killed by context timeout")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("infinite-loop WASM parser took too long to cancel: %v", elapsed)
	}
}
