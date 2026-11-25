// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

type disposeCheckRenderer struct {
	id        string
	onDispose func()
	disposed  atomic.Bool
}

func (r *disposeCheckRenderer) ID() string                        { return r.id }
func (r *disposeCheckRenderer) Trusted() bool                     { return false }
func (r *disposeCheckRenderer) Render(shell.RenderContext)        {}
func (r *disposeCheckRenderer) HandleEvent(shell.HostEvent) error { return nil }
func (r *disposeCheckRenderer) Dispose() {
	r.disposed.Store(true)
	if r.onDispose != nil {
		r.onDispose()
	}
}

// TestSlotRegistryDisposeOutsideLockNoDeadlock verifies that replacing or
// unmounting a renderer invokes Dispose() strictly outside SlotRegistry.mu,
// preventing macOS AppKit main-thread lock-inversion stalls when WKWebView.Destroy
// runs concurrently with UI frame Resolve() calls.
func TestSlotRegistryDisposeOutsideLockNoDeadlock(t *testing.T) {
	slots := shell.NewSlotRegistry()
	fb := NewUIElementRenderer("host.main", true, slots, func() schema.UIElement {
		return schema.UIElement{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Fallback"}}}
	})
	if err := slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.main", AllowUntrustedPlugin: true,
	}, fb); err != nil {
		t.Fatal(err)
	}

	resolveInsideDispose := make(chan struct{}, 2)
	r1 := &disposeCheckRenderer{
		id: "tao3k.org-agenda-planner",
		onDispose: func() {
			// If SlotRegistry.mu were held during Dispose(), calling Resolve()
			// on another goroutine would deadlock forever.
			done := make(chan struct{})
			go func() {
				_ = slots.Resolve("workbench.main")
				close(done)
			}()
			select {
			case <-done:
				resolveInsideDispose <- struct{}{}
			case <-time.After(2 * time.Second):
				t.Error("deadlock: SlotRegistry.mu was held while invoking Dispose()")
			}
		},
	}

	unmount1, err := slots.Mount("workbench.main", "", r1)
	if err != nil {
		t.Fatal(err)
	}
	// Replace same renderer ID (hot-reload/upgrade) -> disposes r1 outside lock.
	r1Gen2 := &disposeCheckRenderer{id: "tao3k.org-agenda-planner"}
	unmount1Gen2, err := slots.Mount("workbench.main", "", r1Gen2)
	if err != nil {
		t.Fatal(err)
	}
	if !r1.disposed.Load() {
		t.Fatal("expected previous generation of same renderer ID to be disposed")
	}
	select {
	case <-resolveInsideDispose:
	case <-time.After(time.Second):
		t.Fatal("expected concurrent Resolve() during Mount Dispose() to succeed without blocking")
	}

	// Mount a second distinct plugin -> r1Gen2 stays warm in the deck (NOT disposed!).
	r2 := &disposeCheckRenderer{id: "tao3k.ai-memory-inbox"}
	unmount2, err := slots.Mount("workbench.main", "", r2)
	if err != nil {
		t.Fatal(err)
	}
	if r1Gen2.disposed.Load() {
		t.Fatal("expected distinct plugin r1Gen2 to stay warm in slot deck instead of tearing down WebView")
	}
	if got := slots.ActiveRendererID("workbench.main"); got != "tao3k.ai-memory-inbox" {
		t.Fatalf("expected active renderer tao3k.ai-memory-inbox, got %q", got)
	}
	// O(1) warm focus back to r1Gen2.
	if !slots.Focus("workbench.main", "tao3k.org-agenda-planner") {
		t.Fatal("expected Focus to switch back to warm r1Gen2")
	}
	if got := slots.ActiveRendererID("workbench.main"); got != "tao3k.org-agenda-planner" {
		t.Fatalf("expected active renderer tao3k.org-agenda-planner after Focus, got %q", got)
	}

	unmount1()
	unmount1Gen2()
	unmount2()
	if !r1Gen2.disposed.Load() || !r2.disposed.Load() {
		t.Fatal("expected explicit unmount to dispose warm renderers cleanly")
	}
	if got := slots.Resolve("workbench.main"); len(got) != 1 || got[0].ID() != "host.main" {
		t.Fatalf("expected host.main fallback after all unmounts, got %+v", got)
	}
}

// TestQuickPluginSwitcherWarmInstantAndBurstCoalescing validates the exact
// rapid-click top-left sidebar scenario:
//   - Warm switching is O(1) sub-millisecond with zero cold re-activations.
//   - Clicking the already-active plugin is a deduped no-op.
//   - Rapid bursts during cold loading coalesce latest-wins without blocking UI.
func TestQuickPluginSwitcherWarmInstantAndBurstCoalescing(t *testing.T) {
	slots := shell.NewSlotRegistry()
	fb := NewUIElementRenderer("host.main", true, slots, func() schema.UIElement {
		return schema.UIElement{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Fallback"}}}
	})
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.main", AllowUntrustedPlugin: true,
	}, fb)

	plugins := []string{
		"tao3k.org-agenda-planner",
		"tao3k.ai-memory-inbox",
		"tao3k.ai-workflow-cockpit",
	}

	// Phase 1: Cold burst coalescing (50 rapid clicks while cold load takes 15ms).
	var coldCalls atomic.Int32
	var invalidations atomic.Int32
	sw := NewQuickPluginSwitcher(QuickPluginSwitcherConfig{
		Context:   context.Background(),
		Slots:     slots,
		SlotID:    "workbench.main",
		InitialID: plugins[0],
		Activate: func(_ context.Context, id schema.PluginID) error {
			coldCalls.Add(1)
			time.Sleep(15 * time.Millisecond)
			_, err := slots.Mount("workbench.main", "", NewUIElementRenderer(string(id), false, slots, func() schema.UIElement {
				return schema.UIElement{Kind: schema.ElemPanel, Title: string(id)}
			}))
			return err
		},
		OnInvalidate: func() { invalidations.Add(1) },
	})

	burstStart := time.Now()
	const coldBurst = 500
	for i := 0; i < coldBurst; i++ {
		sw.Select(plugins[i%len(plugins)])
	}
	// Final desired plugin is plugins[(coldBurst-1)%3].
	wantFinal := plugins[(coldBurst-1)%len(plugins)]
	burstElapsed := time.Since(burstStart)
	if burstElapsed > 50*time.Millisecond {
		t.Fatalf("%d rapid Select() calls must never block UI thread; took %v", coldBurst, burstElapsed)
	}
	sw.WaitIdle()

	if got := sw.ActiveID(); got != wantFinal {
		t.Fatalf("expected latest-wins ActiveID=%q, got %q", wantFinal, got)
	}
	if got := slots.ActiveRendererID("workbench.main"); got != wantFinal {
		t.Fatalf("expected focused slot renderer=%q, got %q", wantFinal, got)
	}
	stCold := sw.Stats()
	if stCold.CoalescedDrops == 0 {
		t.Fatalf("expected rapid cold burst to coalesce intermediate clicks, stats=%+v", stCold)
	}
	if coldCalls.Load() > 3 {
		t.Fatalf("expected single-flight coalescing to cap cold activations (<=3), got %d (stats=%+v)", coldCalls.Load(), stCold)
	}

	// Ensure all 3 plugins are warm in the deck.
	for _, id := range plugins {
		sw.Select(id)
		sw.WaitIdle()
	}
	coldBeforeWarmBurst := coldCalls.Load()

	// Phase 2: Warm rapid-click burst (10,000 clicks alternating + duplicates).
	const warmBurst = 10000
	latencies := make([]time.Duration, 0, warmBurst)
	for i := 0; i < warmBurst; i++ {
		target := plugins[i%len(plugins)]
		if i%5 == 0 {
			// Repeated click on currently active plugin -> must dedup-skip.
			target = sw.ActiveID()
		}
		t0 := time.Now()
		sw.Select(target)
		latencies = append(latencies, time.Since(t0))
	}
	sw.WaitIdle()

	if coldCalls.Load() != coldBeforeWarmBurst {
		t.Fatalf("expected ZERO cold activations during %d warm clicks, got %d extra", warmBurst, coldCalls.Load()-coldBeforeWarmBurst)
	}
	stWarm := sw.Stats()
	if stWarm.WarmHits < 7000 || stWarm.DedupSkips < 1500 {
		t.Fatalf("expected warm hits and dedup skips, got stats=%+v", stWarm)
	}
	p50, p95, maxLat := summarizeLatencies(latencies)
	t.Logf("[perf] QuickPluginSwitcher warm %d-click burst: p50=%v p95=%v max=%v stats=%+v", warmBurst, p50, p95, maxLat, stWarm)
	warmBudget := 500 * time.Microsecond * time.Duration(raceBudgetScale)
	if p95 > warmBudget {
		t.Fatalf("warm plugin switch p95=%v exceeded %v budget", p95, warmBudget)
	}
}

type componentPerfBudget struct {
	Name   string
	Iters  int
	MaxP95 time.Duration
	Setup  func() func()
}

// TestPerComponentUIPerformanceBudgets measures frame and interaction latency
// for EVERY native MyGo GPU UI component individually against strict 60/120Hz
// frame budgets, catching jank regressions before they reach desktop users.
func TestPerComponentUIPerformanceBudgets(t *testing.T) {
	cases := []componentPerfBudget{
		{
			Name:   "Sidebar.QuickPluginSwitcher.BurstClick",
			Iters:  chaosOpCount(600, 120),
			MaxP95: 3 * time.Millisecond,
			Setup:  setupQuickSwitcherComponentPerf,
		},
		{
			Name:   "Sidebar.Federation12Nodes",
			Iters:  chaosOpCount(600, 100),
			MaxP95: 3 * time.Millisecond,
			Setup:  setupFederationSidebarComponentPerf,
		},
		{
			Name:   "TopBar.CommandPalette500AndGate",
			Iters:  chaosOpCount(300, 80),
			MaxP95: 10 * time.Millisecond,
			Setup:  setupTopBarAndPaletteComponentPerf,
		},
		{
			Name:   "IRRenderer.13PrimitivesAndDrawList256",
			Iters:  chaosOpCount(600, 120),
			MaxP95: 3 * time.Millisecond,
			Setup:  setupIRRendererComponentPerf,
		},
		{
			Name:   "Transcript.Fenwick10kRowsScrollAndStream",
			Iters:  chaosOpCount(300, 80),
			MaxP95: 8 * time.Millisecond,
			Setup:  setupTranscript10kComponentPerf,
		},
		{
			Name:   "Marketplace.Drawer100PluginsSearch",
			Iters:  chaosOpCount(300, 80),
			MaxP95: 10 * time.Millisecond,
			Setup:  setupMarketplace100ComponentPerf,
		},
		{
			Name:   "SchemaForm.Bounded12Fields",
			Iters:  chaosOpCount(600, 100),
			MaxP95: 3 * time.Millisecond,
			Setup:  setupSchemaFormComponentPerf,
		},
		{
			Name:   "Workbench.StudioFullFrame1340x860",
			Iters:  chaosOpCount(1000, 120),
			MaxP95: 3 * time.Millisecond,
			Setup:  setupFullStudioWorkbenchPerf,
		},
	}

	t.Logf("Per-Component MyGo GPU UI Performance Report (raceScale=%dx):", raceBudgetScale)
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			step := tc.Setup()
			// Warmup 5 frames.
			for i := 0; i < 5; i++ {
				step()
			}
			samples := make([]time.Duration, tc.Iters)
			for i := 0; i < tc.Iters; i++ {
				t0 := time.Now()
				step()
				samples[i] = time.Since(t0)
			}
			p50, p95, maxLat := summarizeLatencies(samples)
			budget := tc.MaxP95 * time.Duration(raceBudgetScale)
			status := "PASS"
			if p95 > budget {
				status = "FAIL"
			}
			t.Logf("  [%s] %-42s p50=%8v  p95=%8v  max=%8v  (budget p95 <= %v)",
				status, tc.Name, p50.Round(time.Microsecond), p95.Round(time.Microsecond), maxLat.Round(time.Microsecond), budget)
			if p95 > budget {
				t.Fatalf("component %s p95=%v exceeded budget %v (p50=%v max=%v)",
					tc.Name, p95, budget, p50, maxLat)
			}
		})
	}
}

func summarizeLatencies(samples []time.Duration) (p50, p95, maxLat time.Duration) {
	if len(samples) == 0 {
		return 0, 0, 0
	}
	cp := slices.Clone(samples)
	slices.Sort(cp)
	p50 = cp[len(cp)/2]
	idx95 := (len(cp)*95 + 99) / 100
	if idx95 >= len(cp) {
		idx95 = len(cp) - 1
	}
	p95 = cp[idx95]
	maxLat = cp[len(cp)-1]
	return p50, p95, maxLat
}

func buildStudioBenchHarness() (*WorkbenchApp, *shell.SlotRegistry, *QuickPluginSwitcher) {
	targets := transport.NewTargetRegistry()
	federation := transport.NewFederationSupervisor(targets)
	_ = targets.Register(&testTarget{meta: transport.TargetMeta{
		ID: "local-mac", DisplayName: "Local Sidecar", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})
	_ = targets.Register(&testTarget{meta: transport.TargetMeta{
		ID: "remote-linux", DisplayName: "Remote H100 Agent", Mode: transport.ModeRemoteGRPC,
		TargetKind: schema.TargetDesktopLinux, Phase: transport.PhaseReady,
	}})
	_ = targets.Register(&testTarget{meta: transport.TargetMeta{
		ID: "iphone-personal", DisplayName: "iPhone Personal", Mode: transport.ModeRemoteGRPC,
		TargetKind: schema.TargetMobileIOS, Phase: transport.PhaseReady,
	}})
	federation.ProbeAll(context.Background(), time.Second)

	slots := shell.NewSlotRegistry()
	fbMain := NewUIElementRenderer("host.main", true, slots, func() schema.UIElement {
		return schema.UIElement{Kind: schema.ElemPanel, Title: "Fallback Main"}
	})
	fbInsp := NewUIElementRenderer("host.inspector", true, slots, func() schema.UIElement {
		return schema.UIElement{
			Kind:  schema.ElemPanel,
			Title: "FOCUS CLOCK DOCK · GO 1.27 WASI GPU",
			Tone:  schema.ToneAccent,
			Children: []schema.UIElement{
				{Kind: schema.ElemColumns, Children: []schema.UIElement{
					{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "25:00 RUNNING", Tone: schema.ToneSuccess}}},
					{Kind: schema.ElemButton, Title: "Clock In 25m", Action: "org.clock.in", Tone: schema.ToneAccent},
				}},
			},
		}
	})
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.main", AllowUntrustedPlugin: true,
	}, fbMain)
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.inspector", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.inspector", AllowUntrustedPlugin: true,
	}, fbInsp)

	plugins := []QuickPluginEntry{
		{PluginID: "tao3k.org-agenda-planner", Label: "Org Agenda (React 19)"},
		{PluginID: "tao3k.ai-memory-inbox", Label: "AI Memory Inbox (React 19)"},
		{PluginID: "tao3k.ai-workflow-cockpit", Label: "AI Workflow Cockpit (React 19)"},
	}
	for _, qp := range plugins {
		label := qp.Label
		_, _ = slots.Mount("workbench.main", "", NewUIElementRenderer(qp.PluginID, false, slots, func() schema.UIElement {
			return schema.UIElement{
				Kind:  schema.ElemPanel,
				Title: label,
				Flex:  1,
				Tone:  schema.ToneInfo,
				Children: []schema.UIElement{
					{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Active Panel: " + label, Tone: schema.ToneSuccess}}},
				},
			}
		}))
	}
	slots.Focus("workbench.main", plugins[0].PluginID)

	wb := shell.NewWorkbenchController(slots)
	_ = wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation:  1,
		ProfileName: "studio-showcase",
		Theme:       StudioDarkPalette(),
		FrameTree: schema.UIElement{
			Kind: schema.ElemRows,
			Children: []schema.UIElement{
				{Kind: schema.ElemSlot, SlotID: "workbench.main", Flex: 1},
				{Kind: schema.ElemSlot, SlotID: "workbench.inspector"},
			},
		},
	})

	sw := NewQuickPluginSwitcher(QuickPluginSwitcherConfig{
		Slots:     slots,
		SlotID:    "workbench.main",
		InitialID: plugins[0].PluginID,
	})
	app := NewWorkbenchApp(targets, slots, wb)
	app.Federation = federation
	app.BrandTitle = "Marlin Studio"
	app.BrandSubtitle = "MyGo GPU · React 19 + WASI"
	app.Transcript = NewTranscriptController()
	app.TranscriptHidden = true
	app.Marketplace = NewMarketplaceController()
	app.QuickPlugins = plugins
	app.ActiveQuickPluginID = sw.ActiveID
	app.OnQuickPluginSelect = sw.Select
	return app, slots, sw
}

func setupQuickSwitcherComponentPerf() func() {
	app, _, _ := buildStudioBenchHarness()
	tester := ui.NewTester(app.View, 1340, 860)
	labels := []string{
		"Org Agenda (React 19)",
		"AI Memory Inbox (React 19)",
		"AI Workflow Cockpit (React 19)",
	}
	idx := 0
	return func() {
		_ = tester.Click(labels[idx%len(labels)])
		idx++
	}
}

func setupFederationSidebarComponentPerf() func() {
	targets := transport.NewTargetRegistry()
	federation := transport.NewFederationSupervisor(targets)
	for i := 0; i < 12; i++ {
		kind := schema.TargetDesktopLinux
		if i == 0 {
			kind = schema.TargetDesktopMacOS
		} else if i >= 9 {
			kind = schema.TargetMobileIOS
		}
		_ = targets.Register(&testTarget{meta: transport.TargetMeta{
			ID: fmt.Sprintf("node-%02d", i), DisplayName: fmt.Sprintf("Marlin Node %02d", i),
			Mode: transport.ModeRemoteGRPC, TargetKind: kind, Phase: transport.PhaseReady,
		}})
	}
	federation.ProbeAll(context.Background(), time.Second)
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	app := NewWorkbenchApp(targets, slots, wb)
	app.Federation = federation
	tester := ui.NewTester(app.View, 1340, 860)
	idx := 0
	return func() {
		_, _ = targets.Switch(fmt.Sprintf("node-%02d", idx%12))
		idx++
		tester.Frame()
	}
}

func setupTopBarAndPaletteComponentPerf() func() {
	app, _, _ := buildStudioBenchHarness()
	cmds := make([]input.CommandEntry, 500)
	for i := range cmds {
		cmds[i] = input.CommandEntry{
			Name:        fmt.Sprintf("org.workflow.command-%03d", i),
			Description: fmt.Sprintf("Run deterministic workflow command #%d", i),
		}
	}
	app.Commands = cmds
	app.SetPendingGate(PendingGateView{
		GateID:      "gate-perf-1",
		ToolName:    "device.ios.notify.send",
		Prompt:      "Approve iPhone notification dispatch?",
		Description: "Tier-0 host.gate modal overlay benchmark.",
	}, false)
	app.paletteOpen = true
	queries := []string{"org", "workflow", "cmd", "42", "plan", ""}
	tester := ui.NewTester(app.View, 1340, 860)
	idx := 0
	return func() {
		showGate := idx%4 == 0
		app.paletteOpen = !showGate
		app.gateOpen = showGate
		app.paletteQuery = queries[idx%len(queries)]
		idx++
		tester.Frame()
	}
}

func setupIRRendererComponentPerf() func() {
	slots := shell.NewSlotRegistry()
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.main", AllowUntrustedPlugin: true,
	}, NewUIElementRenderer("host.main", true, slots, func() schema.UIElement {
		return schema.UIElement{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Slot Body"}}}
	}))

	children := make([]schema.UIElement, 0, 20)
	for i := 0; i < 16; i++ {
		children = append(children, schema.UIElement{
			Kind:  schema.ElemPanel,
			Title: fmt.Sprintf("Panel {{env}} #%d", i),
			Tone:  schema.ToneInfo,
			Children: []schema.UIElement{
				{Kind: schema.ElemTemplate, Spans: []schema.UISpan{
					{Text: "Item {{idx}}: ", Tone: schema.ToneMuted},
					{Text: "Ready", Tone: schema.ToneSuccess},
				}},
				{Kind: schema.ElemButton, ID: schema.ComponentID(fmt.Sprintf("btn-%d", i)), Title: "Run", Action: "demo.run"},
			},
		})
	}
	children = append(children,
		schema.UIElement{Kind: schema.ElemChoice, Children: []schema.UIElement{
			{Kind: schema.ElemText, ID: "is_running", Spans: []schema.UISpan{{Text: "RUNNING", Tone: schema.ToneSuccess}}},
			{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "IDLE", Tone: schema.ToneMuted}}},
		}},
		schema.UIElement{Kind: schema.ElemBar, Children: []schema.UIElement{
			{Kind: schema.ElemPrompt, Title: "PROMPT", Spans: []schema.UISpan{{Text: "marlin>"}}},
		}},
		schema.UIElement{Kind: schema.ElemSlot, SlotID: "workbench.main"},
	)
	tree := schema.UIElement{Kind: schema.ElemRows, Children: children}

	cmds := make([]schema.DrawCmd, 256)
	for i := range cmds {
		cmds[i] = schema.DrawCmd{
			Op:     schema.DrawOpFillRect,
			W:      float32(16 + (i % 64)),
			H:      8,
			Radius: 4,
			RGBA:   0x7c6af7ff,
		}
	}

	rctx := shell.RenderContext{
		Theme:    StudioDarkPalette(),
		Facts:    map[string]bool{"is_running": true},
		Bindings: map[string]string{"env": "studio", "idx": "42"},
	}
	tester := ui.NewTester(func(c *ui.Context) {
		ApplyMarlinTheme(c, rctx.Theme)
		rctx.Surface = c
		RenderUIElement(c, tree, slots, rctx)
		RenderDrawList(c, cmds)
	}, 1200, 800)
	return func() {
		tester.Frame()
	}
}

func setupTranscript10kComponentPerf() func() {
	tc := NewTranscriptController()
	for i := 0; i < 10000; i++ {
		switch i % 5 {
		case 0:
			tc.AppendUser(fmt.Sprintf("User turn #%d: summarize org agenda and clock rollup.", i))
		case 1:
			tc.AppendThinking("Chain of thought", "Checking orgize DTO invariants and scheduling constraints...", true)
		case 2:
			tc.UpsertToolCall(ToolCallCardView{
				CallID:    fmt.Sprintf("call-%d", i),
				ToolName:  "orgize.agenda.view",
				Phase:     "COMPLETED",
				Title:     "orgize.agenda.view",
				Summary:   "Returned 12 agenda cards in 0.4ms",
				ReceiptID: fmt.Sprintf("rcpt-%d", i),
			}, true)
		default:
			ord := uint64(i + 1)
			tc.BeginAssistantTurn(ord)
			tc.ApplyCheckpoint(ord, fmt.Sprintf("ev-%d", i), "Scheduled deep work block 09:30-11:00.")
			tc.CompleteTurn(ord, "COMPLETED")
		}
	}
	th := StudioDarkPalette()
	tester := ui.NewTester(func(c *ui.Context) {
		t := ApplyMarlinTheme(c, th)
		tc.Render(c, t, nil, nil)
	}, 1100, 720)
	stepIdx := uint64(0)
	totalH := tc.TotalHeight()
	if totalH == 0 {
		totalH = 1
	}
	return func() {
		tc.mu.Lock()
		tc.autoFollow = false
		tc.scrollY = (stepIdx * 420) % totalH
		tc.mu.Unlock()
		stepIdx++
		tester.Frame()
	}
}

func setupMarketplace100ComponentPerf() func() {
	mkt := NewMarketplaceController()
	items := make([]MarketplaceItem, 100)
	for i := range items {
		rt := schema.RuntimeWebViewUntrusted
		if i%3 == 1 {
			rt = schema.RuntimeWasmUIIR
		} else if i%3 == 2 {
			rt = schema.RuntimeDeclarative
		}
		items[i] = MarketplaceItem{
			PluginID:          fmt.Sprintf("tao3k.plugin-%03d", i),
			Version:           "1.2.0",
			PublisherID:       "tao3k.official",
			Summary:           fmt.Sprintf("Signed modular AI workflow plugin #%d", i),
			SlotID:            "workbench.main",
			Runtime:           rt,
			ReadScopes:        []string{"org.agenda", "org.clock"},
			DeclaredActions:   []string{"org.capture.plan"},
			SignatureVerified: true,
			Installed:         i%2 == 0,
			Dormant:           i%4 == 0,
			InstalledVersion:  "1.2.0",
			ActiveGeneration:  uint64(i + 1),
		}
	}
	mkt.SetItems(items)
	mkt.SetProfilerSummary(LazyProfilerSummary{
		TotalDeclared: 100, ActiveCount: 8, DormantCount: 92, MaxActiveLazy: 8, TotalWakes: 24,
	})
	th := StudioDarkPalette()
	queries := []string{"", "plugin-0", "agenda", "05"}
	tester := ui.NewTester(func(c *ui.Context) {
		t := ApplyMarlinTheme(c, th)
		mkt.Render(c, t, func() {})
	}, 1100, 760)
	idx := 0
	return func() {
		mkt.SetQuery(queries[idx%len(queries)])
		idx++
		tester.Frame()
	}
}

func setupSchemaFormComponentPerf() func() {
	spec := schema.BoundedSchema{
		ID:    "studio.settings",
		Title: "Marlin Studio Runtime Preferences",
		Fields: []schema.FieldSpec{
			{Key: "theme", Label: "Theme", Kind: schema.FieldEnum, EnumValues: []string{"obsidian", "slate", "light"}, Default: "obsidian"},
			{Key: "runtime", Label: "Default Runtime", Kind: schema.FieldEnum, EnumValues: []string{"react19", "go-wasm", "declarative"}, Default: "react19"},
			{Key: "lazy_cap", Label: "Max Active Lazy Plugins", Kind: schema.FieldInt, Default: 8},
			{Key: "idle_sec", Label: "Idle Unload Seconds", Kind: schema.FieldInt, Default: 180},
			{Key: "aot_cache", Label: "Persist wazero AOT Cache", Kind: schema.FieldBool, Default: true},
			{Key: "strict_csp", Label: "Enforce WebKit Egress Blocker", Kind: schema.FieldBool, Default: true},
			{Key: "auto_follow", Label: "Auto-Follow Transcript", Kind: schema.FieldBool, Default: true},
			{Key: "org_dir", Label: "Org Workspace Path", Kind: schema.FieldString, Default: "~/org"},
			{Key: "session_id", Label: "Default Session ID", Kind: schema.FieldString, Default: "studio-main"},
			{Key: "publisher", Label: "Trusted Publisher", Kind: schema.FieldString, Default: "tao3k.official"},
			{Key: "clock_min", Label: "Focus Block Minutes", Kind: schema.FieldInt, Default: 25},
			{Key: "notify_ios", Label: "Forward Gates to iPhone", Kind: schema.FieldBool, Default: false},
		},
	}
	state := NewSchemaFormState(spec, nil)
	th := StudioDarkPalette()
	tester := ui.NewTester(func(c *ui.Context) {
		ApplyMarlinTheme(c, th)
		RenderBoundedForm(c, spec, state, func(map[string]any) {})
	}, 900, 720)
	return func() {
		tester.Frame()
	}
}

func setupFullStudioWorkbenchPerf() func() {
	app, _, _ := buildStudioBenchHarness()
	tester := ui.NewTester(app.View, 1340, 860)
	return func() {
		tester.Frame()
	}
}

// === Go Microbenchmarks (-bench=BenchmarkComponent -benchmem) ===

func BenchmarkComponentQuickPluginWarmSwitch(b *testing.B) {
	_, _, sw := buildStudioBenchHarness()
	ids := []string{"tao3k.org-agenda-planner", "tao3k.ai-memory-inbox", "tao3k.ai-workflow-cockpit"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sw.Select(ids[i%len(ids)])
	}
}

func BenchmarkComponentWorkbenchStudioFrame(b *testing.B) {
	step := setupFullStudioWorkbenchPerf()
	step()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		step()
	}
}

func BenchmarkComponentTranscript10kFrame(b *testing.B) {
	step := setupTranscript10kComponentPerf()
	step()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		step()
	}
}

func BenchmarkComponentMarketplace100Frame(b *testing.B) {
	step := setupMarketplace100ComponentPerf()
	step()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		step()
	}
}

func BenchmarkComponentIRRendererAndDrawList(b *testing.B) {
	step := setupIRRendererComponentPerf()
	step()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		step()
	}
}

func BenchmarkComponentCommandPalette500(b *testing.B) {
	step := setupTopBarAndPaletteComponentPerf()
	step()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		step()
	}
}

func BenchmarkComponentSchemaForm12Fields(b *testing.B) {
	step := setupSchemaFormComponentPerf()
	step()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		step()
	}
}

var _ = sync.Mutex{}
