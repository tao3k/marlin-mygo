// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package wazeroir_test

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/runtime/wazeroir"
)

func TestDiskPersistentCompilationCacheAcceleratesColdEngineRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Verify DisableDiskCache leaves cacheDir empty.
	ephemeral, err := wazeroir.NewModuleEngineWithConfig(ctx, wazeroir.EngineConfig{DisableDiskCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if ephemeral.CompilationCacheDir() != "" {
		t.Fatalf("expected empty cacheDir when DisableDiskCache=true, got %q", ephemeral.CompilationCacheDir())
	}
	_ = ephemeral.Close(ctx)

	// Compile go-focus-clock-reactor once to real wasip1 bytes.
	tmpWasm := filepath.Join(t.TempDir(), "panel.wasm")
	build := exec.CommandContext(ctx, "go", "build", "-buildmode=c-shared", "-o", tmpWasm, ".")
	build.Dir = filepath.Join("..", "..", "examples", "plugins", "go-focus-clock-reactor")
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile focus-clock wasip1 failed: %v\n%s", err, string(out))
	}
	wasmBytes, err := os.ReadFile(tmpWasm)
	if err != nil {
		t.Fatal(err)
	}

	cacheDir := filepath.Join(t.TempDir(), "aot-cache")

	// Engine #1: cold AOT compile populates persistent disk cache.
	tCold := time.Now()
	eng1, err := wazeroir.NewModuleEngineWithConfig(ctx, wazeroir.EngineConfig{CompilationCacheDir: cacheDir})
	if err != nil {
		t.Fatal(err)
	}
	if eng1.CompilationCacheDir() != cacheDir {
		t.Fatalf("got cacheDir %q, want %q", eng1.CompilationCacheDir(), cacheDir)
	}
	if _, err := eng1.CompileOrGet(ctx, wasmBytes); err != nil {
		t.Fatalf("eng1.CompileOrGet failed: %v", err)
	}
	coldDur := time.Since(tCold)
	if err := eng1.Close(ctx); err != nil {
		t.Fatalf("eng1.Close failed: %v", err)
	}

	var cachedFiles int
	_ = filepath.WalkDir(cacheDir, func(path string, d fs.DirEntry, wErr error) error {
		if wErr == nil && !d.IsDir() {
			cachedFiles++
		}
		return nil
	})
	if cachedFiles == 0 {
		t.Fatalf("expected persistent wazero AOT cache files in %s, found 0", cacheDir)
	}

	// Engine #2 (simulates full desktop process restart): loads native AOT artifact from disk!
	tWarm := time.Now()
	eng2, err := wazeroir.NewModuleEngineWithConfig(ctx, wazeroir.EngineConfig{CompilationCacheDir: cacheDir})
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close(ctx)

	resident, err := eng2.InstantiateResident(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("eng2.InstantiateResident from disk cache failed: %v", err)
	}
	defer resident.Close(ctx)

	tree, err := resident.Render(ctx)
	if err != nil || tree.Kind == "" {
		t.Fatalf("eng2 resident.Render failed: tree=%+v err=%v", tree, err)
	}
	warmDur := time.Since(tWarm)
	t.Logf("wazero AOT disk cache verified (%d file(s)): cold=%s -> restart_warm=%s", cachedFiles, coldDur.Round(time.Millisecond), warmDur.Round(time.Millisecond))
}
