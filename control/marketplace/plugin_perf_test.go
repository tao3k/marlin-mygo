// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tao3k/marlin-mygo/control/marketplace"
)

func TestBenchPluginDirPassesAllThreePersonalPlanningExamples(t *testing.T) {
	ctx := context.Background()
	examplesRoot := filepath.Join("..", "..", "examples", "plugins")
	cfg := marketplace.PluginPerfConfig{
		Iterations:   40,
		LatencyScale: 4.0, // accounts for Go -race detector instrumentation (~4x) on 3-CPU CI runners
	}

	// 1. All 3 React 19 AI Workflow WebView plugins (real `bun build` + React 19 + `src/perf.ts` macro suites).
	for _, tc := range []struct {
		dir        string
		wantMetric string
	}{
		{"ts-org-agenda-planner", "ts.selectAgendaCards_500"},
		{"react-ai-memory-inbox", "ts.filterMemoryCards_500"},
		{"react-ai-workflow-cockpit", "ts.summarizeWorkflowRun_200"},
	} {
		tsRep, err := marketplace.BenchPluginDir(ctx, filepath.Join(examplesRoot, tc.dir), cfg)
		if err != nil || !tsRep.Passed {
			t.Fatalf("BenchPluginDir(%s) failed: err=%v rep=%+v", tc.dir, err, tsRep)
		}
		found := false
		for _, m := range tsRep.Metrics {
			if m.Name == tc.wantMetric && m.Passed {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: expected %s metric from src/perf.ts, got %+v", tc.dir, tc.wantMetric, tsRep.Metrics)
		}
	}

	// 2. Go WASI Reactor plugin with real `GOOS=wasip1` compile + `wazero` UI-IR & DrawList microbenchmarks.
	wasmRep, err := marketplace.BenchPluginDir(ctx, filepath.Join(examplesRoot, "go-focus-clock-reactor"), cfg)
	if err != nil || !wasmRep.Passed {
		t.Fatalf("BenchPluginDir(go-focus-clock-reactor) failed: err=%v rep=%+v", err, wasmRep)
	}
	var sawRender, sawDrawList bool
	for _, m := range wasmRep.Metrics {
		if m.Name == "wasm.render_ui_ir_p50" && m.Passed {
			sawRender = true
		}
		if m.Name == "wasm.drawlist_p50" && m.Passed {
			sawDrawList = true
		}
	}
	if !sawRender || !sawDrawList {
		t.Fatalf("expected both UI-IR and DrawList perf metrics, got %+v", wasmRep.Metrics)
	}

	// 3. Declarative zero-code planner pack.
	declRep, err := marketplace.BenchPluginDir(ctx, filepath.Join(examplesRoot, "declarative-daily-planner-pack"), cfg)
	if err != nil || !declRep.Passed {
		t.Fatalf("BenchPluginDir(declarative-daily-planner-pack) failed: err=%v rep=%+v", err, declRep)
	}
}

func TestBenchPluginDirNegativeControlsEmitActionableAgentRemediations(t *testing.T) {
	ctx := context.Background()

	// Negative Control 1: Sluggish TS plugin violating its src/perf.ts budget.
	slowTS := t.TempDir()
	_ = os.WriteFile(filepath.Join(slowTS, "marlin-plugin.toml"), []byte(`
id = "acme.slow-board"
version = "0.1.0"
contract_major = 1
[contributions.p]
kind = "ui-panel"
[contributions.p.panel]
runtime = "webview-untrusted"
slot_id = "workbench.main"
entry_path = "index.html"
`), 0o644)
	_ = os.WriteFile(filepath.Join(slowTS, "index.html"), []byte(`<!doctype html><html><head><meta http-equiv="Content-Security-Policy" content="default-src 'self'; script-src 'self'"/></head><body><script type="module" src="./src/main.ts"></script></body></html>`), 0o644)
	_ = os.MkdirAll(filepath.Join(slowTS, "src"), 0o755)
	_ = os.WriteFile(filepath.Join(slowTS, "src", "main.ts"), []byte(`export const ok = true;`), 0o644)
	_ = os.WriteFile(filepath.Join(slowTS, "src", "perf.ts"), []byte(`
import { definePluginPerfSuite, runPluginPerfCLI } from "@tao3k/marlin-panel-sdk";
const suite = definePluginPerfSuite({
  pluginId: "acme.slow-board",
  cases: [{
    name: "quadratic_filter",
    budgetUs: 0.001,
    p95BudgetUs: 0.002,
    iterations: 15,
    remediation: "Replace O(N^2) nested filter loop with an indexed Map lookup",
    run: () => {
      let s = 0;
      for (let i = 0; i < 4000; i++) s += (i * 31) % 7;
      if (s < 0) throw new Error("unreachable");
    },
  }],
});
if (import.meta.main) runPluginPerfCLI(suite);
`), 0o644)

	repTS, err := marketplace.BenchPluginDir(ctx, slowTS, marketplace.PluginPerfConfig{Iterations: 15, LatencyScale: 1.0})
	if !errors.Is(err, marketplace.ErrPerfBudgetExceeded) || repTS == nil || repTS.Passed {
		t.Fatalf("expected slow TS plugin to fail ErrPerfBudgetExceeded, got err=%v rep=%+v", err, repTS)
	}
	joinedHints := strings.Join(repTS.Hints, " | ")
	if !strings.Contains(joinedHints, "indexed Map lookup") {
		t.Fatalf("expected actionable remediation hint in TS perf report, got hints=%v", repTS.Hints)
	}

	// Negative Control 2: Bloated declarative pack exceeding tight byte ceiling.
	bigDecl := t.TempDir()
	_ = os.WriteFile(filepath.Join(bigDecl, "marlin-plugin.toml"), []byte(`
id = "acme.bloated-pack"
version = "0.1.0"
contract_major = 1
[contributions.p]
kind = "theme-pack"
[contributions.p.panel]
runtime = "declarative"
slot_id = "workbench.inspector"
entry_path = "theme.toml"
`), 0o644)
	_ = os.WriteFile(filepath.Join(bigDecl, "theme.toml"), []byte(strings.Repeat("# pad\n", 400)), 0o644)

	repDecl, err := marketplace.BenchPluginDir(ctx, bigDecl, marketplace.PluginPerfConfig{
		Iterations:          20,
		MaxDeclarativeBytes: 512,
	})
	if !errors.Is(err, marketplace.ErrPerfBudgetExceeded) || repDecl == nil || repDecl.Passed {
		t.Fatalf("expected bloated declarative pack to fail ErrPerfBudgetExceeded, got err=%v rep=%+v", err, repDecl)
	}
	if len(repDecl.Hints) == 0 {
		t.Fatalf("expected non-empty remediation hints for bloated declarative pack")
	}

	// Negative Control 3: Go WASI reactor exceeding an intentionally strict render ceiling.
	wasmDir := filepath.Join("..", "..", "examples", "plugins", "go-focus-clock-reactor")
	repWasm, err := marketplace.BenchPluginDir(ctx, wasmDir, marketplace.PluginPerfConfig{
		Iterations:      20,
		MaxWasmRenderUs: 0.001, // impossible 1ns ceiling guarantees deterministic failure
	})
	if !errors.Is(err, marketplace.ErrPerfBudgetExceeded) || repWasm == nil || repWasm.Passed {
		t.Fatalf("expected strict WASM render budget to fail ErrPerfBudgetExceeded, got err=%v rep=%+v", err, repWasm)
	}
	if !strings.Contains(strings.Join(repWasm.Hints, " | "), "marlin_ui_render") {
		t.Fatalf("expected marlin_ui_render remediation hint, got %v", repWasm.Hints)
	}
}
