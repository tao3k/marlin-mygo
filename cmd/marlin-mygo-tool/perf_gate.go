// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// PerfBudget defines a hard latency and allocation ceiling for one benchmark.
// LatencyScale may relax MaxNs on noisy virtualized CI runners, whereas
// MaxBytes and MaxAllocs are NEVER relaxed so zero-alloc invariants stay strict.
type PerfBudget struct {
	Name      string
	MaxNs     float64
	MaxBytes  int64 // < 0 disables byte ceiling
	MaxAllocs int64 // < 0 disables alloc ceiling; 0 enforces strict zero-alloc
}

// BenchSample holds parsed go test -bench -benchmem measurements for one benchmark.
type BenchSample struct {
	Name        string
	Iterations  int64
	NsPerOp     float64
	BytesPerOp  int64
	AllocsPerOp int64
}

// PerfResult records the evaluation of one benchmark against its PerfBudget.
type PerfResult struct {
	Budget          PerfBudget
	Sample          BenchSample
	EffectiveMaxNs  float64
	Missing         bool
	LatencyFailed   bool
	BytesFailed     bool
	AllocsFailed    bool
	HeadroomPercent float64
}

func (r PerfResult) Passed() bool {
	return !r.Missing && !r.LatencyFailed && !r.BytesFailed && !r.AllocsFailed
}

// Tightened strict budgets calibrated for sub-millisecond 240Hz / 4.16ms CPU
// headroom (2x-50x tighter than the original 120Hz draft budgets) with hard
// zero-allocation enforcement across all 7 Plane-1 engine core hot paths.
func activePerfBudgets() []PerfBudget {
	// On macOS, MyGo shapes text via CoreText (off the Go heap -> 17 allocs/frame).
	// On Linux (CGO_ENABLED=0), MyGo uses pure-Go go-text/typesetting (~85 shaper allocs).
	transcriptMaxBytes := int64(1_536)
	transcriptMaxAllocs := int64(24)
	if runtime.GOOS != "darwin" {
		transcriptMaxBytes = 4_096
		transcriptMaxAllocs = 120
	}
	return []PerfBudget{
		{Name: "BenchmarkArenaCompile_1000Nodes", MaxNs: 450_000, MaxBytes: 655_360, MaxAllocs: 24},
		{Name: "BenchmarkArenaIncrementalDiff_1LeafOf1000", MaxNs: 500, MaxBytes: 0, MaxAllocs: 0},
		{Name: "BenchmarkArenaRestyleOnly_1000Nodes", MaxNs: 50, MaxBytes: 0, MaxAllocs: 0},
		{Name: "BenchmarkTopicBusLockFreeFanOut_16Plugins_Coalesced", MaxNs: 1_500, MaxBytes: 0, MaxAllocs: 0},
		{Name: "BenchmarkKeyTrieLookup_3KeyChord", MaxNs: 75, MaxBytes: 0, MaxAllocs: 0},
		{Name: "BenchmarkFuzzyRankCommands_1000", MaxNs: 130_000, MaxBytes: 0, MaxAllocs: 0},
		{Name: "BenchmarkVirtualHeightIndex_100kRows_FindRowAtY", MaxNs: 200, MaxBytes: 0, MaxAllocs: 0},
		{Name: "BenchmarkVirtualHeightIndex_100kRows_FoldUpdate", MaxNs: 15, MaxBytes: 0, MaxAllocs: 0},
		{Name: "BenchmarkWazeroIR_ZeroCopyDrawList_64Cmds", MaxNs: 75_000, MaxBytes: 512, MaxAllocs: 6},
		{Name: "BenchmarkWazeroIR_ResidentReactorRender", MaxNs: 300_000, MaxBytes: 24_576, MaxAllocs: 18},
		{Name: "BenchmarkWazeroIR_CachedExecute", MaxNs: 25_000_000, MaxBytes: -1, MaxAllocs: -1},
		{Name: "BenchmarkGRPC_LoopbackScopedPanelAction", MaxNs: 250_000, MaxBytes: 20_480, MaxAllocs: 220},
		{Name: "BenchmarkMyGoShell_HeadlessFrame", MaxNs: 90_000, MaxBytes: 2_560, MaxAllocs: 24},
		{Name: "BenchmarkMyGoShell_100kRowVirtualizedTranscriptFrame", MaxNs: 250_000, MaxBytes: transcriptMaxBytes, MaxAllocs: transcriptMaxAllocs},
		{Name: "BenchmarkLazyLoader_100Plugins_HookDispatch_ZeroAlloc", MaxNs: 60, MaxBytes: 0, MaxAllocs: 0},
		{Name: "BenchmarkLazyLoader_100Plugins_IdleReapCheck_O1", MaxNs: 60, MaxBytes: 0, MaxAllocs: 0},
	}
}

func runPerfGate(args []string) error {
	fs := flag.NewFlagSet("perf-gate", flag.ContinueOnError)
	benchTime := fs.String("benchtime", "300ms", "duration passed to go test -benchtime")
	latencyScale := fs.Float64("latency-scale", 1.0, "multiplier applied ONLY to latency ceilings (e.g. 4.0 on virtualized CI runners; alloc/byte ceilings stay strict)")
	skipTS := fs.Bool("skip-ts", false, "skip Bun TypeScript Panel SDK benchmark gate")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *latencyScale < 1.0 {
		return fmt.Errorf("-latency-scale must be >= 1.0, got %.2f", *latencyScale)
	}

	fmt.Printf("Host environment: %s/%s (%d CPUs), %s\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version())
	fmt.Printf("[1/2] Running Go engine performance gate (benchtime=%s, latency-scale=%.1fx, alloc-ceilings=STRICT)...\n", *benchTime, *latencyScale)
	cmd := exec.Command("go", "test", "-run=^$", "-bench=.", "-benchmem", "-benchtime="+*benchTime, "./bench/...")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprint(os.Stderr, buf.String())
		return fmt.Errorf("go bench execution failed: %w", err)
	}

	samples := parseBenchmarkOutput(buf.String())
	results, gateErr := evaluatePerfGate(samples, activePerfBudgets(), *latencyScale)
	printPerfReport(results)
	if gateErr != nil {
		return gateErr
	}

	if !*skipTS {
		if bunBin, err := exec.LookPath("bun"); err == nil {
			fmt.Printf("[2/2] Running TypeScript Panel SDK performance gate (latency-scale=%.1fx)...\n", *latencyScale)
			tsCmd := exec.Command(bunBin, "bench/ts/panel_bench.ts")
			tsCmd.Env = append(os.Environ(), fmt.Sprintf("MARLIN_PERF_LATENCY_SCALE=%.2f", *latencyScale))
			tsCmd.Stdout = os.Stdout
			tsCmd.Stderr = os.Stderr
			if err := tsCmd.Run(); err != nil {
				return fmt.Errorf("TypeScript benchmark gate failed: %w", err)
			}
		} else {
			fmt.Println("[2/2] bun not found in PATH; skipping TypeScript benchmark gate (pass -skip-ts to silence)")
		}
	}

	fmt.Println("Marlin MyGo performance gate PASSED (all latency, byte, and zero-alloc ceilings satisfied).")
	return nil
}

func parseBenchmarkOutput(raw string) map[string]BenchSample {
	out := make(map[string]BenchSample)
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "Benchmark") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		name := fields[0]
		if idx := strings.LastIndexByte(name, '-'); idx > 0 {
			if _, err := strconv.Atoi(name[idx+1:]); err == nil {
				name = name[:idx]
			}
		}
		iters, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		sample := BenchSample{Name: name, Iterations: iters}
		for i := 2; i+1 < len(fields); i += 2 {
			valStr, unit := fields[i], fields[i+1]
			switch unit {
			case "ns/op":
				if v, err := strconv.ParseFloat(valStr, 64); err == nil {
					sample.NsPerOp = v
				}
			case "B/op":
				if v, err := strconv.ParseInt(valStr, 10, 64); err == nil {
					sample.BytesPerOp = v
				}
			case "allocs/op":
				if v, err := strconv.ParseInt(valStr, 10, 64); err == nil {
					sample.AllocsPerOp = v
				}
			}
		}
		out[name] = sample
	}
	return out
}

func evaluatePerfGate(samples map[string]BenchSample, budgets []PerfBudget, latencyScale float64) ([]PerfResult, error) {
	results := make([]PerfResult, 0, len(budgets))
	var failures []string
	for _, b := range budgets {
		effMaxNs := b.MaxNs * latencyScale
		res := PerfResult{
			Budget:         b,
			EffectiveMaxNs: effMaxNs,
		}
		sample, ok := samples[b.Name]
		if !ok {
			res.Missing = true
			failures = append(failures, fmt.Sprintf("%s: missing from benchmark output", b.Name))
			results = append(results, res)
			continue
		}
		res.Sample = sample
		if effMaxNs > 0 {
			res.HeadroomPercent = ((effMaxNs - sample.NsPerOp) / effMaxNs) * 100
		}
		if sample.NsPerOp > effMaxNs {
			res.LatencyFailed = true
			failures = append(failures, fmt.Sprintf("%s: latency %.1f ns/op > budget %.1f ns/op", b.Name, sample.NsPerOp, effMaxNs))
		}
		if b.MaxBytes >= 0 && sample.BytesPerOp > b.MaxBytes {
			res.BytesFailed = true
			failures = append(failures, fmt.Sprintf("%s: bytes %d B/op > budget %d B/op", b.Name, sample.BytesPerOp, b.MaxBytes))
		}
		if b.MaxAllocs >= 0 && sample.AllocsPerOp > b.MaxAllocs {
			res.AllocsFailed = true
			failures = append(failures, fmt.Sprintf("%s: allocs %d/op > budget %d/op", b.Name, sample.AllocsPerOp, b.MaxAllocs))
		}
		results = append(results, res)
	}
	if len(failures) > 0 {
		return results, fmt.Errorf("%d performance budget violation(s):\n  - %s", len(failures), strings.Join(failures, "\n  - "))
	}
	return results, nil
}

func printPerfReport(results []PerfResult) {
	fmt.Printf("%-52s %12s %12s %10s %10s %8s\n", "BENCHMARK", "MEASURED", "BUDGET", "ALLOCS", "BYTES", "STATUS")
	fmt.Println(strings.Repeat("-", 109))
	for _, r := range results {
		status := "PASS"
		if !r.Passed() {
			status = "FAIL"
		}
		if r.Missing {
			fmt.Printf("%-52s %12s %12s %10s %10s %8s\n", r.Budget.Name, "MISSING", formatNs(r.EffectiveMaxNs), "-", "-", "FAIL")
			continue
		}
		allocStr := fmt.Sprintf("%d", r.Sample.AllocsPerOp)
		if r.Budget.MaxAllocs >= 0 {
			allocStr = fmt.Sprintf("%d/%d", r.Sample.AllocsPerOp, r.Budget.MaxAllocs)
		}
		byteStr := fmt.Sprintf("%dB", r.Sample.BytesPerOp)
		if r.Budget.MaxBytes >= 0 {
			byteStr = fmt.Sprintf("%d/%dB", r.Sample.BytesPerOp, r.Budget.MaxBytes)
		}
		fmt.Printf("%-52s %12s %12s %10s %10s %8s\n",
			r.Budget.Name,
			formatNs(r.Sample.NsPerOp),
			formatNs(r.EffectiveMaxNs),
			allocStr,
			byteStr,
			status,
		)
	}
}

func formatNs(ns float64) string {
	switch {
	case ns >= 1_000_000:
		return fmt.Sprintf("%.2fms", ns/1_000_000)
	case ns >= 1_000:
		return fmt.Sprintf("%.1fus", ns/1_000)
	default:
		return fmt.Sprintf("%.1fns", ns)
	}
}
