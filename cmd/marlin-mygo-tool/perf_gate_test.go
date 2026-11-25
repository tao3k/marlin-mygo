// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package main

import (
	"strings"
	"testing"
)

func TestPerfGateParserAndNegativeControls(t *testing.T) {
	raw := `
goos: darwin
goarch: arm64
BenchmarkArenaIncrementalDiff_1LeafOf1000-12   3090831   301.2 ns/op   0 B/op   0 allocs/op
BenchmarkKeyTrieLookup_3KeyChord-12           16931715    60.17 ns/op  0 B/op   0 allocs/op
`
	samples := parseBenchmarkOutput(raw)
	if len(samples) != 2 {
		t.Fatalf("expected 2 parsed benchmarks, got %d", len(samples))
	}

	budgets := []PerfBudget{
		{Name: "BenchmarkArenaIncrementalDiff_1LeafOf1000", MaxNs: 1_000, MaxBytes: 0, MaxAllocs: 0},
		{Name: "BenchmarkKeyTrieLookup_3KeyChord", MaxNs: 100, MaxBytes: 0, MaxAllocs: 0},
	}
	if _, err := evaluatePerfGate(samples, budgets, 1.0); err != nil {
		t.Fatalf("expected valid samples to pass strict gate, got %v", err)
	}

	// Negative control 1: 1 unexpected heap alloc on a 0-alloc path MUST fail even with -latency-scale=4.0!
	regressedAlloc := map[string]BenchSample{
		"BenchmarkArenaIncrementalDiff_1LeafOf1000": {Name: "BenchmarkArenaIncrementalDiff_1LeafOf1000", NsPerOp: 200, BytesPerOp: 16, AllocsPerOp: 1},
		"BenchmarkKeyTrieLookup_3KeyChord":          samples["BenchmarkKeyTrieLookup_3KeyChord"],
	}
	if _, err := evaluatePerfGate(regressedAlloc, budgets, 4.0); err == nil || !strings.Contains(err.Error(), "allocs 1/op > budget 0/op") {
		t.Fatalf("expected zero-alloc violation to fail even with 4x latency scale, got %v", err)
	}

	// Negative control 2: latency regression MUST fail.
	regressedLatency := map[string]BenchSample{
		"BenchmarkArenaIncrementalDiff_1LeafOf1000": {Name: "BenchmarkArenaIncrementalDiff_1LeafOf1000", NsPerOp: 1500, BytesPerOp: 0, AllocsPerOp: 0},
		"BenchmarkKeyTrieLookup_3KeyChord":          samples["BenchmarkKeyTrieLookup_3KeyChord"],
	}
	if _, err := evaluatePerfGate(regressedLatency, budgets, 1.0); err == nil || !strings.Contains(err.Error(), "latency") {
		t.Fatalf("expected latency violation to fail, got %v", err)
	}

	// Negative control 3: missing benchmark MUST fail.
	if _, err := evaluatePerfGate(map[string]BenchSample{}, budgets, 1.0); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected missing benchmark to fail, got %v", err)
	}
}
