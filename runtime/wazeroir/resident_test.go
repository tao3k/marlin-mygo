// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package wazeroir

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestResidentReactorRendersRepeatedlyInWarmMemory(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go binary not in PATH")
	}
	wasmOut := filepath.Join(t.TempDir(), "reactor.wasm")
	build := exec.Command(goBin, "build", "-buildmode=c-shared", "-o", wasmOut, "../../examples/plugins/go-graph-inspector-reactor")
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("failed to compile wasip1 reactor plugin: %v\n%s", err, string(out))
	}
	wasmBytes, err := os.ReadFile(wasmOut)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	eng, err := NewModuleEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close(ctx)

	inst, err := eng.InstantiateResident(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("InstantiateResident failed: %v", err)
	}
	defer inst.Close(ctx)

	for wantSeq := 1; wantSeq <= 5; wantSeq++ {
		tree, err := inst.Render(ctx)
		if err != nil {
			t.Fatalf("frame %d Render failed: %v", wantSeq, err)
		}
		if tree.Kind != schema.ElemPanel || !strings.Contains(tree.Title, "Resident Reactor") {
			t.Fatalf("frame %d unexpected root: %+v", wantSeq, tree)
		}
		if len(tree.Children) == 0 || len(tree.Children[0].Children) == 0 {
			t.Fatalf("frame %d missing rows/text children: %+v", wantSeq, tree)
		}
		spans := tree.Children[0].Children[0].Spans
		if len(spans) < 2 || !strings.Contains(spans[1].Text, "#"+string(rune('0'+wantSeq))) {
			t.Fatalf("frame %d expected monotonic counter in spans, got %+v", wantSeq, spans)
		}
	}
}
