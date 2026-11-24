// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package wazeroir

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestExecuteCompiledGoWasmIRPlugin(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go binary not in PATH")
	}
	wasmOut := filepath.Join(t.TempDir(), "graph-inspector.wasm")
	build := exec.Command(goBin, "build", "-o", wasmOut, "../../examples/plugins/go-graph-inspector")
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("failed to compile wasip1 Go UI-IR plugin: %v\n%s", err, string(out))
	}

	wasmBytes, err := os.ReadFile(wasmOut)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := ExecuteOnce(context.Background(), wasmBytes)
	if err != nil {
		t.Fatalf("wazero execution failed: %v", err)
	}
	if tree.Kind != schema.ElemPanel || tree.Title != "Graph Trajectory (Go WASM-IR)" {
		t.Fatalf("unexpected admitted UI IR tree: %+v", tree)
	}
	if len(tree.Children) != 1 || tree.Children[0].Kind != schema.ElemRows {
		t.Fatalf("expected rows child inside panel, got %+v", tree.Children)
	}
}
