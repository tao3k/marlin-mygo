// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !wasip1

package gouir

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// PerfCase declares a deterministic latency and heap-allocation performance
// budget for a Go plugin hot path (state updates, UIElement builders, DrawList
// emitters). On budget failure, AssertPerfCase emits a machine-readable
// [MARLIN_PERF_VIOLATION] JSON block with concrete remediation guidance for
// both human developers and AI coding agents.
type PerfCase struct {
	Name        string
	MaxP50      time.Duration
	MaxP95      time.Duration // optional; defaults to 3 * MaxP50 when MaxP50 > 0
	MaxAllocs   float64       // negative disables allocation check; 0 enforces strict zero-alloc
	Warmup      int           // defaults to 20
	Iterations  int           // defaults to 200
	Remediation string
	Run         func()
}

// PerfCaseResult captures measured latency, allocations, and pass/fail status.
type PerfCaseResult struct {
	Name        string  `json:"name"`
	MeasuredUs  float64 `json:"measuredUs"`
	P95Us       float64 `json:"p95Us"`
	BudgetUs    float64 `json:"budgetUs"`
	P95BudgetUs float64 `json:"p95BudgetUs"`
	AllocsPerOp float64 `json:"allocsPerOp"`
	MaxAllocs   float64 `json:"maxAllocs"`
	Iterations  int     `json:"iterations"`
	Passed      bool    `json:"passed"`
	Remediation string  `json:"remediation,omitempty"`
}

// MeasurePerfCase executes c with warmup + N timed runs and optional allocation
// counting, returning a structured PerfCaseResult without failing a test directly.
func MeasurePerfCase(c PerfCase) PerfCaseResult {
	warmup := c.Warmup
	if warmup <= 0 {
		warmup = 20
	}
	iters := c.Iterations
	if iters < 5 {
		iters = 200
	}
	for i := 0; i < warmup; i++ {
		c.Run()
	}

	samples := make([]time.Duration, iters)
	for i := 0; i < iters; i++ {
		t0 := time.Now()
		c.Run()
		samples[i] = time.Since(t0)
	}
	slices.Sort(samples)
	p50 := samples[len(samples)/2]
	p95Idx := (len(samples) * 95) / 100
	if p95Idx >= len(samples) {
		p95Idx = len(samples) - 1
	}
	p95 := samples[p95Idx]

	var allocs float64
	if c.MaxAllocs >= 0 {
		allocs = testing.AllocsPerRun(50, c.Run)
	}

	maxP95 := c.MaxP95
	if maxP95 <= 0 && c.MaxP50 > 0 {
		maxP95 = c.MaxP50 * 3
	}

	passed := true
	if c.MaxP50 > 0 && p50 > c.MaxP50 {
		passed = false
	}
	if maxP95 > 0 && p95 > maxP95 {
		passed = false
	}
	if c.MaxAllocs >= 0 && allocs > c.MaxAllocs {
		passed = false
	}

	res := PerfCaseResult{
		Name:        c.Name,
		MeasuredUs:  float64(p50.Nanoseconds()) / 1000.0,
		P95Us:       float64(p95.Nanoseconds()) / 1000.0,
		BudgetUs:    float64(c.MaxP50.Nanoseconds()) / 1000.0,
		P95BudgetUs: float64(maxP95.Nanoseconds()) / 1000.0,
		AllocsPerOp: allocs,
		MaxAllocs:   c.MaxAllocs,
		Iterations:  iters,
		Passed:      passed,
	}
	if !passed {
		res.Remediation = c.Remediation
	}
	return res
}

// AssertPerfCase acts as a Go test macro enforcing latency and allocation
// ceilings on a plugin hot path, emitting a [MARLIN_PERF_VIOLATION] JSON
// payload on failure so coding agents can diagnose and fix regressions.
func AssertPerfCase(t testing.TB, c PerfCase) PerfCaseResult {
	t.Helper()
	res := MeasurePerfCase(c)
	if !res.Passed {
		raw, _ := json.Marshal(res)
		t.Fatalf("[MARLIN_PERF_VIOLATION] %s", string(raw))
	}
	return res
}

// CountUITreeStats returns total node count and maximum nesting depth of root.
func CountUITreeStats(root schema.UIElement) (nodes, maxDepth int) {
	var walk func(el schema.UIElement, depth int)
	walk = func(el schema.UIElement, depth int) {
		nodes++
		if depth > maxDepth {
			maxDepth = depth
		}
		for _, ch := range el.Children {
			walk(ch, depth+1)
		}
	}
	walk(root, 1)
	return nodes, maxDepth
}

// AssertUITreeBudget enforces both MAR-UI-R03 safety bounds and compact plugin
// UI-IR tree limits (nodes <= maxNodes, depth <= maxDepth).
func AssertUITreeBudget(t testing.TB, root schema.UIElement, maxNodes, maxDepth int) (nodes, depth int) {
	t.Helper()
	if err := root.ValidateBounds(schema.MaxUITreeNodes, schema.MaxUITreeDepth); err != nil {
		t.Fatalf("[MARLIN_PERF_VIOLATION] UIElement violated MAR-UI-R03 hard bounds: %v", err)
	}
	nodes, depth = CountUITreeStats(root)
	if (maxNodes > 0 && nodes > maxNodes) || (maxDepth > 0 && depth > maxDepth) {
		diag := map[string]any{
			"name":        "ui_tree.compactness",
			"nodes":       nodes,
			"maxNodes":    maxNodes,
			"depth":       depth,
			"maxDepth":    maxDepth,
			"remediation": "Flatten nested Rows/Columns or virtualize long item lists to keep UI-IR trees compact.",
		}
		raw, _ := json.Marshal(diag)
		t.Fatalf("[MARLIN_PERF_VIOLATION] %s", string(raw))
	}
	return nodes, depth
}
