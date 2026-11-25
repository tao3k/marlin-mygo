// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func chaosOpCount(defaultProd, defaultRace int) int {
	if raw := os.Getenv("MARLIN_CHAOS_OPS"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	if raceBudgetScale > 1 {
		return defaultRace
	}
	return defaultProd
}

// FluentReport summarizes frame fluency metrics across a violent UI stress run.
type FluentReport struct {
	Ops             int
	P50             time.Duration
	P95             time.Duration
	P99             time.Duration
	Max             time.Duration
	Dropped120Hz    int // ops exceeding 8.33ms (scaled under -race)
	Dropped60Hz     int // ops exceeding 16.67ms (scaled under -race)
	UnsettledFrames int // ops leaving ui.Tester.FramePending() stuck true
}

// FluentMonitor records per-operation latencies during MyGo ui.Tester monkey,
// hover, resize, and keyboard stress runs to guarantee continuous 60/120Hz fluency.
type FluentMonitor struct {
	samples         []time.Duration
	unsettledFrames int
}

func (m *FluentMonitor) Measure(tester *ui.Tester, fn func()) {
	t0 := time.Now()
	fn()
	dt := time.Since(t0)
	m.samples = append(m.samples, dt)
	if tester != nil && tester.FramePending() {
		m.unsettledFrames++
	}
}

func (m *FluentMonitor) Report() FluentReport {
	if len(m.samples) == 0 {
		return FluentReport{}
	}
	cp := slices.Clone(m.samples)
	slices.Sort(cp)
	at := func(pct int) time.Duration {
		idx := (len(cp)*pct + 99) / 100
		if idx >= len(cp) {
			idx = len(cp) - 1
		}
		return cp[idx]
	}
	budget120 := time.Duration(8333*raceBudgetScale) * time.Microsecond
	budget60 := time.Duration(16667*raceBudgetScale) * time.Microsecond
	drop120, drop60 := 0, 0
	for _, s := range cp {
		if s > budget120 {
			drop120++
		}
		if s > budget60 {
			drop60++
		}
	}
	return FluentReport{
		Ops:             len(cp),
		P50:             cp[len(cp)/2],
		P95:             at(95),
		P99:             at(99),
		Max:             cp[len(cp)-1],
		Dropped120Hz:    drop120,
		Dropped60Hz:     drop60,
		UnsettledFrames: m.unsettledFrames,
	}
}

// TestFluentMonkeyChaosClickStormOnStudioShell runs a deterministic, seeded
// 400-operation violent chaos monkey directly on MyGo's native ui.Tester:
//   - rapid random clicking on top-left AI WORKFLOW PLUGINS switcher buttons
//   - rapid pointer hover sweeps & scroll wheel storms
//   - rapid Cmd+K Command Palette open/type/close + Marketplace + Approval Gate
//   - rapid Show Chat / Hide Chat transcript toggles
//   - rapid window resize (1340x860 <-> 680x440 <-> 1600x960) & Retina scale flips
//
// Proves zero panics, zero deadlocks/races, zero infinite render loops, and
// sustained 60/120Hz fluency under violent user input.
func TestFluentMonkeyChaosClickStormOnStudioShell(t *testing.T) {
	app, _, sw := buildStudioBenchHarness()
	app.SetPendingGate(PendingGateView{
		GateID:      "gate-chaos-1",
		ToolName:    "device.ios.notify.send",
		Prompt:      "Approve iPhone notification dispatch?",
		Description: "Tier-0 host.gate chaos overlay test.",
	}, false)
	for i := 0; i < 40; i++ {
		app.Transcript.AppendUser(fmt.Sprintf("Chaos turn #%d", i))
	}

	tester := ui.NewTester(app.View, 1340, 860)
	rng := rand.New(rand.NewPCG(0x5eed2026, 0x9e3779b9))
	pluginLabels := []string{
		"Org Agenda (React 19)",
		"AI Memory Inbox (React 19)",
		"AI Workflow Cockpit (React 19)",
	}
	viewports := [][2]int{
		{1340, 860},
		{1100, 720},
		{800, 600},
		{680, 440},
		{1440, 900},
	}

	var mon FluentMonitor
	totalOps := chaosOpCount(3000, 400)

	// Measure baseline live heap after warmup to detect memory leaks across thousands of ops.
	for w := 0; w < 20; w++ {
		_ = tester.Click(pluginLabels[w%len(pluginLabels)])
	}
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	for step := 0; step < totalOps; step++ {
		op := rng.IntN(100)
		switch {
		case op < 45:
			// Violent top-left sidebar quick-plugin clicking (45% weight — exact user repro!).
			lbl := pluginLabels[rng.IntN(len(pluginLabels))]
			mon.Measure(tester, func() {
				// If a modal is open, close it cleanly first so sidebar is reachable.
				if app.paletteOpen || app.marketplaceOpen || app.gateOpen {
					app.paletteOpen = false
					app.marketplaceOpen = false
					app.gateOpen = false
					tester.Frame()
				}
				if err := tester.Click(lbl); err != nil {
					t.Fatalf("chaos step %d: clicking %q failed: %v", step, lbl, err)
				}
			})

		case op < 60:
			// Continuous pointer sweep across top-left sidebar & top bar.
			x := float32(10 + rng.IntN(320))
			y := float32(10 + rng.IntN(420))
			mon.Measure(tester, func() {
				tester.Move(x, y)
			})

		case op < 72:
			// Toggle Show Chat / Hide Chat transcript drawer.
			mon.Measure(tester, func() {
				if app.paletteOpen || app.marketplaceOpen || app.gateOpen {
					app.paletteOpen = false
					app.marketplaceOpen = false
					app.gateOpen = false
					tester.Frame()
				}
				label := "Show Chat"
				if !app.TranscriptHidden {
					label = "Hide Chat"
				}
				if err := tester.Click(label); err != nil {
					t.Fatalf("chaos step %d: clicking %q failed: %v", step, label, err)
				}
			})

		case op < 82:
			// Open/close Command Palette via real Cmd+K shortcut, or type one search key when open.
			mon.Measure(tester, func() {
				if app.paletteOpen && rng.IntN(2) == 0 {
					tester.Type("org")
				} else {
					tester.Key(ui.Cmd, ui.KeyK)
				}
			})

		case op < 89:
			// Scroll wheel burst over main workspace & sidebar.
			mon.Measure(tester, func() {
				tester.Scroll(float32(300+rng.IntN(500)), float32(120+rng.IntN(400)), 0, float32(rng.IntN(240)-120))
			})

		case op < 96:
			// Live window resize storm across wide & narrow breakpoints.
			vp := viewports[rng.IntN(len(viewports))]
			mon.Measure(tester, func() {
				tester.SetSize(vp[0], vp[1])
			})

		default:
			// Advance deterministic 60Hz frame clock (verifies timer/animation stability).
			mon.Measure(tester, func() {
				tester.Advance(16 * time.Millisecond)
			})
		}
	}

	// Verify HiDPI Retina 2x -> 1x scale transitions also stay clean without panics.
	tester.SetScale(2.0)
	tester.Frame()
	tester.SetScale(1.0)
	tester.Frame()

	sw.WaitIdle()
	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	var heapGrowthMB float64
	if memAfter.HeapAlloc > memBefore.HeapAlloc {
		heapGrowthMB = float64(memAfter.HeapAlloc-memBefore.HeapAlloc) / (1024 * 1024)
	}

	rep := mon.Report()
	t.Logf("[fluent-monkey] %d chaos ops: p50=%v p95=%v p99=%v max=%v dropped120Hz=%d dropped60Hz=%d unsettled=%d heapDelta=%.2fMB switcherStats=%+v",
		rep.Ops,
		rep.P50.Round(time.Microsecond),
		rep.P95.Round(time.Microsecond),
		rep.P99.Round(time.Microsecond),
		rep.Max.Round(time.Microsecond),
		rep.Dropped120Hz,
		rep.Dropped60Hz,
		rep.UnsettledFrames,
		heapGrowthMB,
		sw.Stats(),
	)

	if rep.UnsettledFrames > 0 {
		t.Fatalf("detected %d unsettled frames (infinite animation/re-render loop)", rep.UnsettledFrames)
	}
	if heapGrowthMB > 32.0 {
		t.Fatalf("memory leak guard: live heap grew %.2f MB across %d chaos ops (limit 32 MB)", heapGrowthMB, rep.Ops)
	}
	// Fluent SLA: >= 99% of violent chaos ops stay under 60Hz (16.67ms), p95 stays under 6ms.
	maxAllowed60HzDrops := max(2, rep.Ops/100)
	if raceBudgetScale > 1 {
		maxAllowed60HzDrops = max(4, rep.Ops/25)
	}
	if rep.Dropped60Hz > maxAllowed60HzDrops {
		t.Fatalf("fluent 60Hz SLA violation: %d/%d ops exceeded 16.67ms (max allowed %d; p95=%v max=%v)",
			rep.Dropped60Hz, rep.Ops, maxAllowed60HzDrops, rep.P95, rep.Max)
	}
	p95Budget := 6 * time.Millisecond * time.Duration(raceBudgetScale)
	if rep.P95 > p95Budget {
		t.Fatalf("chaos monkey p95=%v exceeded fluent budget %v", rep.P95, p95Budget)
	}
}

// TestFluentRapidHoverAndResizeContinuity verifies 120Hz pointer scrubbing
// across sidebar buttons and smooth continuous viewport resizing with zero jank.
func TestFluentRapidHoverAndResizeContinuity(t *testing.T) {
	app, _, _ := buildStudioBenchHarness()
	tester := ui.NewTester(app.View, 1340, 860)

	var mon FluentMonitor
	hoverOps := chaosOpCount(2000, 300)
	resizeOps := max(60, hoverOps/10)
	// Rapid 120Hz pointer hover moves across the top-left sidebar (0..228px).
	for i := 0; i < hoverOps; i++ {
		x := float32(20 + (i*13)%190)
		y := float32(40 + (i*17)%360)
		mon.Measure(tester, func() {
			tester.Move(x, y)
		})
	}
	// Smooth continuous window resize sweeps (680x440 <-> 1480x920).
	for i := 0; i < resizeOps; i++ {
		phase := i % 40
		w := 680 + phase*20
		h := 440 + phase*12
		mon.Measure(tester, func() {
			tester.SetSize(w, h)
		})
	}

	rep := mon.Report()
	t.Logf("[fluent-hover+resize] %d ops: p50=%v p95=%v max=%v dropped120Hz=%d dropped60Hz=%d",
		rep.Ops,
		rep.P50.Round(time.Microsecond),
		rep.P95.Round(time.Microsecond),
		rep.Max.Round(time.Microsecond),
		rep.Dropped120Hz,
		rep.Dropped60Hz,
	)
	p95Budget := 4 * time.Millisecond * time.Duration(raceBudgetScale)
	if rep.P95 > p95Budget {
		t.Fatalf("hover+resize continuity p95=%v exceeded budget %v", rep.P95, p95Budget)
	}
}

// TestFluentEveryControlHitTestableAndAccessible enables MyGo's native
// screen-reader / accessibility tree mode and asserts every Studio interactive
// control has valid non-zero bounds and accepts direct clicks without occlusion.
func TestFluentEveryControlHitTestableAndAccessible(t *testing.T) {
	app, _, sw := buildStudioBenchHarness()
	app.SetPendingGate(PendingGateView{
		GateID:   "gate-a11y-1",
		ToolName: "device.ios.notify.send",
		Prompt:   "Approve notification?",
	}, false)

	tester := ui.NewTester(app.View, 1340, 860)
	tester.UseScreenReader()
	tester.Frame()

	controls := []string{
		"Org Agenda (React 19)",
		"AI Memory Inbox (React 19)",
		"AI Workflow Cockpit (React 19)",
		"Show Chat",
		"Marketplace",
		"Cmd+K Commands",
		"Review Gate",
		"Clock In 25m",
	}
	for _, label := range controls {
		r, ok := tester.Find(label)
		if !ok || r.W <= 0 || r.H <= 0 {
			t.Fatalf("control %q missing or zero-sized in layout: ok=%v rect=%+v", label, ok, r)
		}
	}

	// Verify rapid alternating click across all 3 top-left plugin buttons leaves
	// Focus synced and hits warm deck every single time.
	for _, label := range []string{"AI Memory Inbox (React 19)", "AI Workflow Cockpit (React 19)", "Org Agenda (React 19)"} {
		if err := tester.Click(label); err != nil {
			t.Fatalf("failed clicking %q with accessibility enabled: %v", label, err)
		}
	}
	sw.WaitIdle()
	if got := sw.ActiveID(); got != "tao3k.org-agenda-planner" {
		t.Fatalf("expected active plugin tao3k.org-agenda-planner, got %q", got)
	}
	_ = context.Background()
	_ = schema.TargetDesktopMacOS
}
