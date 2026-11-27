// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
	"github.com/tao3k/marlin-mygo/runtime/wazeroir"
)

// ErrPerfBudgetExceeded is returned by BenchPluginDir when one or more plugin
// performance budgets fail. The accompanying PluginPerfReport always contains
// structured measured values, budgets, and concrete LLM-actionable remediation hints.
var ErrPerfBudgetExceeded = errors.New("marketplace: plugin performance budget exceeded")

// PluginPerfConfig tunes per-plugin performance budgets executed by BenchPluginDir.
// Zero fields automatically fall back to strict Marlin defaults.
type PluginPerfConfig struct {
	Iterations            int     // warm iterations for microbenchmarks (default 150)
	LatencyScale          float64 // multiplier for slower CI VMs (default 1.0)
	MaxWasmBytes          int64   // default 8 MiB
	MaxWasmWakeMs         float64 // cached AOT -> resident instantiation budget (default 120 ms)
	MaxWasmRenderUs       float64 // p50 budget in us (default 500 us)
	MaxWasmDrawListUs     float64 // p50 budget in us (default 150 us)
	MaxUITreeNodes        int     // compact UI-IR budget (default 256, hard cap 1024)
	MaxUITreeDepth        int     // compact UI-IR depth budget (default 16, hard cap 32)
	MaxWebViewBundleBytes int64   // minified bundle.js budget (default 384 KiB — fits React 19 + UI logic, blocks MB bloat)
	MaxWebViewAssetsBytes int64   // total staged payload budget (default 1 MiB)
	MaxWebViewBuildMs     float64 // bun build budget (default 2500 ms)
	MaxDeclarativeBytes   int64   // total pack size budget (default 256 KiB)
	MaxManifestParseUs    float64 // p50 TOML parse budget (default 250 us)
}

func (c PluginPerfConfig) withDefaults() PluginPerfConfig {
	if c.Iterations < 10 {
		c.Iterations = 150
	}
	if c.LatencyScale <= 0 {
		c.LatencyScale = 1.0
	}
	if c.MaxWasmBytes <= 0 {
		c.MaxWasmBytes = 8 * 1024 * 1024
	}
	if c.MaxWasmWakeMs <= 0 {
		c.MaxWasmWakeMs = 120
	}
	if c.MaxWasmRenderUs <= 0 {
		c.MaxWasmRenderUs = 500
	}
	if c.MaxWasmDrawListUs <= 0 {
		c.MaxWasmDrawListUs = 150
	}
	if c.MaxUITreeNodes <= 0 {
		c.MaxUITreeNodes = 256
	}
	if c.MaxUITreeDepth <= 0 {
		c.MaxUITreeDepth = 16
	}
	if c.MaxWebViewBundleBytes <= 0 {
		c.MaxWebViewBundleBytes = 384 * 1024
	}
	if c.MaxWebViewAssetsBytes <= 0 {
		c.MaxWebViewAssetsBytes = 1024 * 1024
	}
	if c.MaxWebViewBuildMs <= 0 {
		c.MaxWebViewBuildMs = 2500
	}
	if c.MaxDeclarativeBytes <= 0 {
		c.MaxDeclarativeBytes = 256 * 1024
	}
	if c.MaxManifestParseUs <= 0 {
		c.MaxManifestParseUs = 250
	}
	return c
}

// PluginPerfMetric records one measured performance metric, its budget, and
// a concrete optimization hint if the budget was exceeded.
type PluginPerfMetric struct {
	Name        string  `json:"name"`
	Measured    float64 `json:"measured"`
	Budget      float64 `json:"budget"`
	Unit        string  `json:"unit"`
	P95         float64 `json:"p95,omitempty"`
	Passed      bool    `json:"passed"`
	Remediation string  `json:"remediation,omitempty"`
}

// PluginPerfReport is the structured JSON-friendly diagnostic report emitted by
// `marlin-mygo-tool bench-plugin` so both humans and AI coding agents can
// immediately identify and fix plugin performance regressions.
type PluginPerfReport struct {
	PluginID    schema.PluginID         `json:"pluginId"`
	Version     string                  `json:"version"`
	Runtime     schema.PanelRuntimeKind `json:"runtime"`
	SlotID      schema.SlotID           `json:"slotId"`
	Environment string                  `json:"environment"`
	Iterations  int                     `json:"iterations"`
	Scale       float64                 `json:"scale"`
	Passed      bool                    `json:"passed"`
	Metrics     []PluginPerfMetric      `json:"metrics"`
	Hints       []string                `json:"hints,omitempty"`
}

func (r *PluginPerfReport) record(name string, measured, budget float64, unit string, p95 float64, remediation string) {
	passed := measured <= budget
	m := PluginPerfMetric{
		Name:     name,
		Measured: round2(measured),
		Budget:   round2(budget),
		Unit:     unit,
		P95:      round2(p95),
		Passed:   passed,
	}
	if !passed {
		r.Passed = false
		m.Remediation = remediation
		if remediation != "" && !slices.Contains(r.Hints, remediation) {
			r.Hints = append(r.Hints, remediation)
		}
	}
	r.Metrics = append(r.Metrics, m)
}

func (r *PluginPerfReport) addHint(hint string) {
	if hint != "" && !slices.Contains(r.Hints, hint) {
		r.Hints = append(r.Hints, hint)
	}
}

// BenchPluginDir runs automated runtime-specific performance benchmarks and
// budget checks against a plugin source directory.
func BenchPluginDir(ctx context.Context, pluginDir string, cfg PluginPerfConfig) (*PluginPerfReport, error) {
	cfg = cfg.withDefaults()
	absDir, err := filepath.Abs(pluginDir)
	if err != nil {
		return nil, err
	}

	rawManifest, err := os.ReadFile(filepath.Join(absDir, "marlin-plugin.toml"))
	if err != nil {
		return nil, fmt.Errorf("read marlin-plugin.toml: %w", err)
	}
	manifest, err := schema.ParsePluginManifestTOML(rawManifest)
	if err != nil {
		return nil, err
	}

	var panel *schema.PanelContribution
	var cids []schema.ContributionID
	for cid := range manifest.Contributions {
		cids = append(cids, cid)
	}
	slices.Sort(cids)
	for _, cid := range cids {
		if c := manifest.Contributions[cid]; c.Panel != nil {
			panel = c.Panel
			break
		}
	}
	if panel == nil {
		return nil, errors.New("marketplace: manifest declares no [contributions.<id>.panel]")
	}

	rep := &PluginPerfReport{
		PluginID:    manifest.ID,
		Version:     manifest.Version,
		Runtime:     panel.Runtime,
		SlotID:      panel.SlotID,
		Environment: fmt.Sprintf("%s/%s (%d CPUs), %s", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version()),
		Iterations:  cfg.Iterations,
		Scale:       cfg.LatencyScale,
		Passed:      true,
	}

	switch panel.Runtime {
	case schema.RuntimeWasmUIIR:
		if err := benchWasmReactorPlugin(ctx, absDir, cfg, rep); err != nil {
			return rep, err
		}
	case schema.RuntimeWebViewUntrusted:
		if err := benchWebViewPlugin(ctx, absDir, panel, cfg, rep); err != nil {
			return rep, err
		}
	case schema.RuntimeDeclarative:
		if err := benchDeclarativePlugin(absDir, rawManifest, panel, cfg, rep); err != nil {
			return rep, err
		}
	default:
		return rep, fmt.Errorf("marketplace: unsupported runtime %q for bench-plugin", panel.Runtime)
	}

	if !rep.Passed {
		return rep, fmt.Errorf("%w: %s violated performance budget(s)", ErrPerfBudgetExceeded, rep.PluginID)
	}
	return rep, nil
}

func benchWasmReactorPlugin(ctx context.Context, absDir string, cfg PluginPerfConfig, rep *PluginPerfReport) error {
	tmpDir, err := os.MkdirTemp("", "marlin-perf-wasm-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	wasmOut := filepath.Join(tmpDir, "panel.wasm")
	build := exec.CommandContext(ctx, "go", "build", "-buildmode=c-shared", "-o", wasmOut, ".")
	build.Dir = absDir
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("GOOS=wasip1 build failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	wasmBytes, err := os.ReadFile(wasmOut)
	if err != nil {
		return err
	}
	rep.record(
		"wasm.binary_size",
		float64(len(wasmBytes)),
		float64(cfg.MaxWasmBytes),
		"bytes",
		0,
		"Trim heavy stdlib/reflection imports or build with -ldflags='-s -w' to keep WASI plugin binaries compact.",
	)

	eng, err := wazeroir.NewModuleEngine(ctx)
	if err != nil {
		return err
	}
	defer eng.Close(ctx)

	// Pre-warm the SHA-256 keyed AOT compilation cache (performed once on install),
	// then measure actual user-facing lazy-wake instantiation latency.
	compiled, err := eng.CompileOrGet(ctx, wasmBytes)
	if err != nil {
		return err
	}
	tWake := time.Now()
	resident, err := eng.InstantiateResident(ctx, wasmBytes)
	if err != nil {
		return err
	}
	defer resident.Close(ctx)
	wakeMs := float64(time.Since(tWake).Microseconds()) / 1000.0
	rep.record(
		"wasm.wake_instantiate",
		wakeMs,
		cfg.MaxWasmWakeMs*cfg.LatencyScale,
		"ms",
		0,
		"Move heavy lookup-table construction out of package init() so cached lazy waking stays sub-120ms.",
	)

	// Warmup + measure marlin_ui_render latency & UI-IR tree compactness.
	var lastTree schema.UIElement
	for i := 0; i < 15; i++ {
		if lastTree, err = resident.Render(ctx); err != nil {
			return fmt.Errorf("warmup marlin_ui_render: %w", err)
		}
	}
	renderSamples := make([]time.Duration, cfg.Iterations)
	for i := 0; i < cfg.Iterations; i++ {
		t0 := time.Now()
		lastTree, err = resident.Render(ctx)
		if err != nil {
			return fmt.Errorf("marlin_ui_render iter %d: %w", i, err)
		}
		renderSamples[i] = time.Since(t0)
	}
	renderP50, renderP95 := durationPercentilesUs(renderSamples)
	rep.record(
		"wasm.render_ui_ir_p50",
		renderP50,
		cfg.MaxWasmRenderUs*cfg.LatencyScale,
		"us",
		renderP95,
		"Avoid O(N^2) loops and heavy string/JSON allocations inside marlin_ui_render; cache static subtrees or move canvas painting to marlin_ui_render_drawlist.",
	)

	nodes, depth := countUITreeBounds(lastTree)
	rep.record(
		"wasm.ui_tree_nodes",
		float64(nodes),
		float64(cfg.MaxUITreeNodes),
		"nodes",
		0,
		"Virtualize long lists or paginate rows so UIElement trees stay compact (soft budget <=256, MAR-UI-R03 cap 1024).",
	)
	rep.record(
		"wasm.ui_tree_depth",
		float64(depth),
		float64(cfg.MaxUITreeDepth),
		"depth",
		0,
		"Flatten unnecessary nested Rows/Columns inside marlin_ui_render.",
	)

	// Automatically benchmark zero-JSON DrawList if exported by the reactor.
	if _, hasDrawList := compiled.ExportedFunctions()[pluginhost.WasmExportRenderDrawList]; hasDrawList {
		dst := make([]schema.DrawCmd, 0, schema.MaxDrawListCmds)
		var cmds []schema.DrawCmd
		for i := 0; i < 15; i++ {
			if cmds, err = resident.RenderDrawList(ctx, dst[:0]); err != nil {
				return fmt.Errorf("warmup marlin_ui_render_drawlist: %w", err)
			}
		}
		dlSamples := make([]time.Duration, cfg.Iterations)
		for i := 0; i < cfg.Iterations; i++ {
			t0 := time.Now()
			cmds, err = resident.RenderDrawList(ctx, dst[:0])
			if err != nil {
				return fmt.Errorf("marlin_ui_render_drawlist iter %d: %w", i, err)
			}
			dlSamples[i] = time.Since(t0)
		}
		dlP50, dlP95 := durationPercentilesUs(dlSamples)
		rep.record(
			"wasm.drawlist_p50",
			dlP50,
			cfg.MaxWasmDrawListUs*cfg.LatencyScale,
			"us",
			dlP95,
			"Populate a preallocated [64]schema.DrawCmd array with zero heap allocations inside marlin_ui_render_drawlist.",
		)
		rep.record(
			"wasm.drawlist_cmds",
			float64(len(cmds)),
			float64(schema.MaxDrawListCmds),
			"cmds",
			0,
			"Batch adjacent rectangles and keep per-frame DrawCmd count within schema.MaxDrawListCmds.",
		)
	}
	return nil
}

type tsPerfSuiteJSON struct {
	PluginID string `json:"pluginId"`
	Passed   bool   `json:"passed"`
	Cases    []struct {
		Name        string  `json:"name"`
		MeasuredUs  float64 `json:"measuredUs"`
		P95Us       float64 `json:"p95Us"`
		BudgetUs    float64 `json:"budgetUs"`
		P95BudgetUs float64 `json:"p95BudgetUs"`
		Passed      bool    `json:"passed"`
		Remediation string  `json:"remediation"`
	} `json:"cases"`
}

func benchWebViewPlugin(ctx context.Context, absDir string, panel *schema.PanelContribution, cfg PluginPerfConfig, rep *PluginPerfReport) error {
	entry := panel.EntryPath
	if entry == "" {
		entry = "index.html"
	}
	tBuild := time.Now()
	staged, cleanup, bundled, bundleBytes, err := BundleTSWebViewDir(ctx, absDir, entry)
	if err != nil {
		return err
	}
	defer cleanup()
	buildMs := float64(time.Since(tBuild).Microseconds()) / 1000.0

	if bundled {
		rep.record(
			"webview.build_time",
			buildMs,
			cfg.MaxWebViewBuildMs*cfg.LatencyScale,
			"ms",
			0,
			"Simplify TypeScript import graph or remove unused barrel re-exports to keep hot-reload builds fast.",
		)
		rep.record(
			"webview.bundle_size",
			float64(bundleBytes),
			float64(cfg.MaxWebViewBundleBytes),
			"bytes",
			0,
			"Tree-shake heavy npm dependencies and offload Org/heavy parsing to tao3k/orgize-wasm or Rust host projections.",
		)
	}

	var totalAssets int64
	_ = filepath.WalkDir(staged, func(p string, d fs.DirEntry, wErr error) error {
		if wErr == nil && !d.IsDir() {
			if info, iErr := d.Info(); iErr == nil {
				totalAssets += info.Size()
			}
		}
		return nil
	})
	rep.record(
		"webview.total_assets",
		float64(totalAssets),
		float64(cfg.MaxWebViewAssetsBytes),
		"bytes",
		0,
		"Compress images/fonts or strip unused static files from the plugin directory.",
	)

	// Automatically discover and execute TypeScript plugin perf macros (src/perf.ts or perf.ts).
	perfFile := locateTSPluginPerfFile(absDir)
	if perfFile == "" {
		rep.addHint("Add src/perf.ts using definePluginPerfSuite() from @tao3k/marlin-panel-sdk to enforce latency budgets on custom JS/TS hot paths.")
		return nil
	}
	bunBin, err := exec.LookPath("bun")
	if err != nil {
		return fmt.Errorf("%w: cannot execute %s", ErrBunNotFound, perfFile)
	}
	args := []string{"run"}
	var tmpCfgDir string
	if _, nmErr := os.Stat(filepath.Join(absDir, "node_modules", "@tao3k", "marlin-panel-sdk")); nmErr != nil {
		if sdkEntry := locateWorkspacePanelSDK(absDir); sdkEntry != "" {
			if dir, cErr := os.MkdirTemp("", "marlin-perf-tsconfig-*"); cErr == nil {
				tmpCfgDir = dir
				defer os.RemoveAll(tmpCfgDir)
				tsconfigPath := filepath.Join(tmpCfgDir, "tsconfig.json")
				cfgDoc := map[string]any{
					"compilerOptions": map[string]any{
						"moduleResolution": "bundler",
						"target":           "ES2022",
						"jsx":              "react-jsx",
						"paths":            buildWorkspaceTSPaths(sdkEntry),
					},
				}
				if rawCfg, mErr := json.Marshal(cfgDoc); mErr == nil && os.WriteFile(tsconfigPath, rawCfg, 0o644) == nil {
					args = append(args, "--tsconfig-override="+tsconfigPath)
				}
			}
		}
	}
	args = append(
		args,
		perfFile,
		"--json",
		"--scale="+strconv.FormatFloat(cfg.LatencyScale, 'f', 2, 64),
		"--iterations="+strconv.Itoa(cfg.Iterations),
	)
	cmd := exec.CommandContext(ctx, bunBin, args...)
	cmd.Dir = absDir
	out, runErr := cmd.Output()
	if len(out) == 0 && runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			return fmt.Errorf("ts perf suite %s crashed: %w\n%s", filepath.Base(perfFile), runErr, strings.TrimSpace(string(ee.Stderr)))
		}
		return fmt.Errorf("ts perf suite %s failed: %w", filepath.Base(perfFile), runErr)
	}
	var parsed tsPerfSuiteJSON
	if err := json.Unmarshal(out, &parsed); err != nil {
		return fmt.Errorf("decode JSON output from %s: %w (stdout=%q)", filepath.Base(perfFile), err, strings.TrimSpace(string(out)))
	}
	for _, c := range parsed.Cases {
		rem := c.Remediation
		if rem == "" {
			rem = fmt.Sprintf("Optimize TypeScript hot path %q to stay under %.1f us median latency.", c.Name, c.BudgetUs)
		}
		// Record using exact pass/fail computed by the TS perf suite (covers both p50 and p95).
		m := PluginPerfMetric{
			Name:     "ts." + c.Name,
			Measured: round2(c.MeasuredUs),
			Budget:   round2(c.BudgetUs),
			Unit:     "us",
			P95:      round2(c.P95Us),
			Passed:   c.Passed,
		}
		if !c.Passed {
			rep.Passed = false
			m.Remediation = rem
			rep.addHint(rem)
		}
		rep.Metrics = append(rep.Metrics, m)
	}
	return nil
}

func benchDeclarativePlugin(absDir string, rawManifest []byte, panel *schema.PanelContribution, cfg PluginPerfConfig, rep *PluginPerfReport) error {
	var totalBytes int64
	_ = filepath.WalkDir(absDir, func(p string, d fs.DirEntry, wErr error) error {
		if wErr == nil && !d.IsDir() {
			if info, iErr := d.Info(); iErr == nil {
				totalBytes += info.Size()
			}
		}
		return nil
	})
	rep.record(
		"declarative.total_size",
		float64(totalBytes),
		float64(cfg.MaxDeclarativeBytes),
		"bytes",
		0,
		"Keep declarative packs under 256 KiB for instant desktop and mobile OTA sync.",
	)

	if panel.EntryPath != "" {
		if info, err := os.Stat(filepath.Join(absDir, filepath.FromSlash(panel.EntryPath))); err == nil {
			rep.record(
				"declarative.entry_size",
				float64(info.Size()),
				float64(cfg.MaxDeclarativeBytes/2),
				"bytes",
				0,
				"Split bulky declarative presets into separate focused packs.",
			)
		}
	}

	// Benchmark manifest parse latency.
	for i := 0; i < 20; i++ {
		if _, err := schema.ParsePluginManifestTOML(rawManifest); err != nil {
			return err
		}
	}
	samples := make([]time.Duration, cfg.Iterations)
	for i := 0; i < cfg.Iterations; i++ {
		t0 := time.Now()
		if _, err := schema.ParsePluginManifestTOML(rawManifest); err != nil {
			return err
		}
		samples[i] = time.Since(t0)
	}
	p50, p95 := durationPercentilesUs(samples)
	rep.record(
		"declarative.manifest_parse_p50",
		p50,
		cfg.MaxManifestParseUs*cfg.LatencyScale,
		"us",
		p95,
		"Keep marlin-plugin.toml compact so lazy-profile startup stays O(1) and sub-millisecond.",
	)
	return nil
}

func locateTSPluginPerfFile(absDir string) string {
	candidates := []string{
		filepath.Join(absDir, "src", "perf.ts"),
		filepath.Join(absDir, "perf.ts"),
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	if matches, err := filepath.Glob(filepath.Join(absDir, "src", "*.perf.ts")); err == nil && len(matches) > 0 {
		slices.Sort(matches)
		return matches[0]
	}
	return ""
}

func countUITreeBounds(root schema.UIElement) (nodes, maxDepth int) {
	var walk func(el schema.UIElement, d int)
	walk = func(el schema.UIElement, d int) {
		nodes++
		if d > maxDepth {
			maxDepth = d
		}
		for _, ch := range el.Children {
			walk(ch, d+1)
		}
	}
	walk(root, 1)
	return nodes, maxDepth
}

func durationPercentilesUs(samples []time.Duration) (p50Us, p95Us float64) {
	if len(samples) == 0 {
		return 0, 0
	}
	cp := slices.Clone(samples)
	slices.Sort(cp)
	p50 := cp[len(cp)/2]
	idx95 := (len(cp) * 95) / 100
	if idx95 >= len(cp) {
		idx95 = len(cp) - 1
	}
	p95 := cp[idx95]
	return float64(p50.Nanoseconds()) / 1000.0, float64(p95.Nanoseconds()) / 1000.0
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100.0
}

// FormatPluginPerfReport formats a human-readable CLI summary matching perf-gate style.
func FormatPluginPerfReport(rep *PluginPerfReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Marlin Plugin Perf Gate (%s v%s, runtime=%s, slot=%s)\n", rep.PluginID, rep.Version, rep.Runtime, rep.SlotID)
	fmt.Fprintf(&b, "Host environment: %s (iters=%d, scale=%.2fx)\n", rep.Environment, rep.Iterations, rep.Scale)
	for _, m := range rep.Metrics {
		mark := "PASS"
		if !m.Passed {
			mark = "FAIL"
		}
		p95Part := ""
		if m.P95 > 0 {
			p95Part = fmt.Sprintf(" (p95=%.1f %s)", m.P95, m.Unit)
		}
		fmt.Fprintf(&b, "  [%s] %-36s %8.1f / %8.1f %-5s%s\n", mark, m.Name, m.Measured, m.Budget, m.Unit, p95Part)
		if !m.Passed && m.Remediation != "" {
			fmt.Fprintf(&b, "         hint: %s\n", m.Remediation)
		}
	}
	if len(rep.Hints) > 0 && rep.Passed {
		for _, h := range rep.Hints {
			fmt.Fprintf(&b, "  [TIP]  %s\n", h)
		}
	}
	return b.String()
}
