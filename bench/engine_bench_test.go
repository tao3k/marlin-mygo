// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package bench provides end-to-end Frontend Engine microbenchmarks covering:
//   - Plane 1 (Engine Core): BFS Arena + Dual Merkle Diff, Lock-Free COW Bus +
//     Coalescing Ring, Compiled KeyTrie, Zero-Alloc Fuzzy Ranker, 100k-row Fenwick
//     VirtualHeightIndex
//   - Plane 2 (Runtimes): AOT-Cached wazero Go WASM-IR execution, Headless MyGo
//     GPU Workbench frame render
//   - Plane 3 (Control Plane): Real HTTP/2 loopback gRPC ScopedPanelAction RTT
package bench

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"google.golang.org/grpc"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/arena"
	"github.com/tao3k/marlin-mygo/engine/bus"
	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/engine/viewport"
	commonv1 "github.com/tao3k/marlin-mygo/gen/marlin/common/v1"
	pluginv1 "github.com/tao3k/marlin-mygo/gen/marlin/plugin/v1"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
	"github.com/tao3k/marlin-mygo/runtime/wazeroir"
)

func makeThousandNodeTree(leafText string, tone schema.UITone) schema.UIElement {
	// 1 root + 25 panels + 25*39 text leaves = 1001 nodes.
	panels := make([]schema.UIElement, 25)
	for i := 0; i < 25; i++ {
		rows := make([]schema.UIElement, 39)
		for j := 0; j < 39; j++ {
			txt := fmt.Sprintf("item-%02d-%02d", i, j)
			if i == 24 && j == 38 && leafText != "" {
				txt = leafText
			}
			rows[j] = schema.UIElement{
				Kind:  schema.ElemText,
				Spans: []schema.UISpan{{Text: txt, Tone: tone}},
			}
		}
		panels[i] = schema.UIElement{
			Kind:     schema.ElemPanel,
			ID:       schema.ComponentID(fmt.Sprintf("panel-%02d", i)),
			Title:    fmt.Sprintf("Panel %d", i),
			Tone:     tone,
			Children: rows,
		}
	}
	return schema.UIElement{Kind: schema.ElemRows, Children: panels}
}

func BenchmarkArenaCompile_1000Nodes(b *testing.B) {
	tree := makeThousandNodeTree("", schema.ToneNormal)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := arena.Compile(tree, 2048, 32); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkArenaIncrementalDiff_1LeafOf1000(b *testing.B) {
	a1, err := arena.Compile(makeThousandNodeTree("token-a", schema.ToneNormal), 2048, 32)
	if err != nil {
		b.Fatal(err)
	}
	a2, err := arena.Compile(makeThousandNodeTree("token-b", schema.ToneNormal), 2048, 32)
	if err != nil {
		b.Fatal(err)
	}
	patchBuf := make([]arena.PatchOp, 0, 16)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		patchBuf = arena.Diff(a1, a2, patchBuf)
		if len(patchBuf) != 1 {
			b.Fatalf("expected 1 patch op, got %d", len(patchBuf))
		}
	}
}

func BenchmarkArenaRestyleOnly_1000Nodes(b *testing.B) {
	a1, _ := arena.Compile(makeThousandNodeTree("", schema.ToneNormal), 2048, 32)
	a2, _ := arena.Compile(makeThousandNodeTree("", schema.ToneAccent), 2048, 32)
	patchBuf := make([]arena.PatchOp, 0, 16)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		patchBuf = arena.Diff(a1, a2, patchBuf)
		if len(patchBuf) != 1 || patchBuf[0].Kind != arena.OpRestyleSubtree {
			b.Fatalf("expected OpRestyleSubtree, got %+v", patchBuf)
		}
	}
}

func BenchmarkTopicBusLockFreeFanOut_16Plugins_Coalesced(b *testing.B) {
	tb := bus.NewTopicBus()
	for i := 0; i < 16; i++ {
		sub := &bus.Subscriber{
			PluginID: schema.PluginID(fmt.Sprintf("bench.plugin-%02d", i)),
			Ring:     bus.NewCoalescingRing(64, bus.PolicyCoalesceByKey),
			Breaker:  bus.NewCircuitBreaker(5, time.Second),
		}
		tb.Subscribe(sub, "org.tasks", "stream.delta")
	}
	ev := bus.Envelope{
		EventID:     "ev-1",
		Topic:       "org.tasks",
		CoalesceKey: "org.tasks",
		Generation:  1,
		Payload:     []byte(`{"count":42}`),
	}
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tb.Publish(ev, now)
	}
}

func BenchmarkKeyTrieLookup_3KeyChord(b *testing.B) {
	bindings := []input.Binding{
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "w", "v"}, ActionKind: "invoke", CommandName: "split-vertical"},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "w", "s"}, ActionKind: "invoke", CommandName: "split-horizontal"},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "b", "b"}, ActionKind: "invoke", CommandName: "switch-buffer"},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "p", "f"}, ActionKind: "invoke", CommandName: "find-file"},
		{Scope: input.ScopeGlobal, Sequence: []string{"C-x", "C-f"}, ActionKind: "invoke", CommandName: "open-file"},
		{Scope: input.ScopeGlobal, Sequence: []string{"C-g"}, ActionKind: "cancel"},
	}
	trie, err := input.CompileKeyTrie(bindings)
	if err != nil {
		b.Fatal(err)
	}
	seq := []string{"SPC", "w", "v"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		kind, _ := trie.Lookup(input.ScopeNormal, seq)
		if kind != input.MatchExact {
			b.Fatal("expected exact match")
		}
	}
}

func BenchmarkFuzzyRankCommands_1000(b *testing.B) {
	cmds := make([]input.CommandEntry, 1000)
	for i := range cmds {
		cmds[i] = input.CommandEntry{
			Name: fmt.Sprintf("marlin-plugin-%03d-select-buffer-view", i),
		}
	}
	cmds[777] = input.CommandEntry{Name: "select-buffer"}
	dst := make([]input.RankedCommand, 0, 1000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst = input.RankCommands("sbv", cmds, dst)
	}
}

func BenchmarkVirtualHeightIndex_100kRows_FindRowAtY(b *testing.B) {
	idx := viewport.NewVirtualHeightIndex(100_000)
	for i := 0; i < 100_000; i++ {
		idx.Append(uint32(20 + (i % 40)))
	}
	total := idx.TotalHeight()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		y := uint64(i*997) % total
		row, _ := idx.FindRowAtY(y)
		if row < 0 {
			b.Fatal("invalid row")
		}
	}
}

func BenchmarkVirtualHeightIndex_100kRows_FoldUpdate(b *testing.B) {
	idx := viewport.NewVirtualHeightIndex(100_000)
	for i := 0; i < 100_000; i++ {
		idx.Append(32)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		row := (i * 131) % 100_000
		if i&1 == 0 {
			idx.UpdateHeight(row, 0) // fold thinking block
		} else {
			idx.UpdateHeight(row, 240) // unfold thinking block
		}
	}
}

func BenchmarkWazeroIR_CachedExecute(b *testing.B) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		b.Skip("go not in PATH")
	}
	wasmOut := filepath.Join(b.TempDir(), "graph-inspector.wasm")
	build := exec.Command(goBin, "build", "-o", wasmOut, "../examples/plugins/go-graph-inspector")
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		b.Fatalf("build wasip1 plugin failed: %v\n%s", err, string(out))
	}
	wasmBytes, err := os.ReadFile(wasmOut)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	eng, err := wazeroir.NewModuleEngine(ctx)
	if err != nil {
		b.Fatal(err)
	}
	defer eng.Close(ctx)
	// Warm AOT compilation cache once before timing cached instantiations.
	if _, err := eng.CompileOrGet(ctx, wasmBytes); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := eng.ExecuteCached(ctx, wasmBytes); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWazeroIR_ResidentReactorRender(b *testing.B) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		b.Skip("go not in PATH")
	}
	wasmOut := filepath.Join(b.TempDir(), "graph-inspector-reactor.wasm")
	build := exec.Command(goBin, "build", "-buildmode=c-shared", "-o", wasmOut, "../examples/plugins/go-graph-inspector-reactor")
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		b.Fatalf("build wasip1 reactor plugin failed: %v\n%s", err, string(out))
	}
	wasmBytes, err := os.ReadFile(wasmOut)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	eng, err := wazeroir.NewModuleEngine(ctx)
	if err != nil {
		b.Fatal(err)
	}
	defer eng.Close(ctx)

	resident, err := eng.InstantiateResident(ctx, wasmBytes)
	if err != nil {
		b.Fatal(err)
	}
	defer resident.Close(ctx)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := resident.Render(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWazeroIR_ZeroCopyDrawList_64Cmds(b *testing.B) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		b.Skip("go not in PATH")
	}
	wasmOut := filepath.Join(b.TempDir(), "graph-inspector-reactor.wasm")
	build := exec.Command(goBin, "build", "-buildmode=c-shared", "-o", wasmOut, "../examples/plugins/go-graph-inspector-reactor")
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		b.Fatalf("build wasip1 reactor plugin failed: %v\n%s", err, string(out))
	}
	wasmBytes, err := os.ReadFile(wasmOut)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	eng, err := wazeroir.NewModuleEngine(ctx)
	if err != nil {
		b.Fatal(err)
	}
	defer eng.Close(ctx)

	resident, err := eng.InstantiateResident(ctx, wasmBytes)
	if err != nil {
		b.Fatal(err)
	}
	defer resident.Close(ctx)

	var cmdBuf [64]schema.DrawCmd
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmds, err := resident.RenderDrawList(ctx, cmdBuf[:0])
		if err != nil || len(cmds) != 64 {
			b.Fatalf("drawlist failed: len=%d err=%v", len(cmds), err)
		}
	}
}

type benchPluginSrv struct {
	pluginv1.UnimplementedPluginServiceServer
}

func (benchPluginSrv) SubmitScopedPanelAction(_ context.Context, _ *pluginv1.SubmitScopedPanelActionRequest) (*pluginv1.SubmitScopedPanelActionResponse, error) {
	return &pluginv1.SubmitScopedPanelActionResponse{
		Receipt: &pluginv1.ScopedPanelActionReceipt{
			Admitted:   true,
			ResultJson: []byte(`{"ok":true}`),
			Receipt:    &commonv1.ReceiptRef{ReceiptId: "r-1"},
		},
	}, nil
}

func BenchmarkGRPC_LoopbackScopedPanelAction(b *testing.B) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	srv := grpc.NewServer()
	pluginv1.RegisterPluginServiceServer(srv, benchPluginSrv{})
	go func() { _ = srv.Serve(ln) }()
	defer srv.Stop()

	target, err := grpcwire.Dial(grpcwire.DialConfig{
		ID: "bench-host", DisplayName: "Bench", Mode: transport.ModeLocalSidecar,
		Address: ln.Addr().String(), AuthToken: "tok",
	})
	if err != nil {
		b.Fatal(err)
	}
	defer target.Close()

	proxy := pluginhost.NewScopedProxy(pluginhost.ScopedScope{
		PluginID: "tao3k.org-kanban", ContributionID: "kanban", ActivationGeneration: 1,
		DeclaredActions: []string{"org.task.move-state"},
	}, &grpcwire.PanelActionDispatcher{Target: target, ProjectID: "p1", SessionID: "s1"})

	ctx := context.Background()
	args := json.RawMessage(`{"id":"T-1"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := proxy.SubmitAction(ctx, "org.task.move-state", args, "k1"); err != nil {
			b.Fatal(err)
		}
	}
}

type benchTarget struct{ meta transport.TargetMeta }

func (t benchTarget) Meta() transport.TargetMeta { return t.meta }
func (t benchTarget) Ping(context.Context) error { return nil }
func (t benchTarget) Close() error               { return nil }

func BenchmarkMyGoShell_HeadlessFrame(b *testing.B) {
	targets := transport.NewTargetRegistry()
	_ = targets.Register(benchTarget{meta: transport.TargetMeta{
		ID: "local", DisplayName: "Local Mac", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})
	slots := shell.NewSlotRegistry()
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.main", AllowUntrustedPlugin: true,
	}, mygoshell.NewUIElementRenderer("host.main", true, slots, func() schema.UIElement {
		return schema.UIElement{
			Kind:  schema.ElemPanel,
			Title: "Transcript",
			Tone:  schema.ToneInfo,
			Children: []schema.UIElement{
				{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Ready", Tone: schema.ToneSuccess}}},
			},
		}
	}))
	wb := shell.NewWorkbenchController(slots)
	_ = wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation: 1, ProfileName: "macos-dev", Theme: schema.DefaultUITheme(),
		FrameTree: schema.UIElement{
			Kind:     schema.ElemRows,
			Children: []schema.UIElement{{Kind: schema.ElemSlot, SlotID: "workbench.main"}},
		},
	})
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)
	tt := ui.NewTester(app.View, 1280, 800)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tt.Frame()
	}
}

func BenchmarkMyGoShell_100kRowVirtualizedTranscriptFrame(b *testing.B) {
	targets := transport.NewTargetRegistry()
	_ = targets.Register(benchTarget{meta: transport.TargetMeta{
		ID: "local", DisplayName: "Local Mac", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)

	tc := mygoshell.NewTranscriptController()
	for i := 0; i < 100_000; i++ {
		tc.AppendUser("streaming turn history entry")
	}
	tc.UpsertToolCall(mygoshell.ToolCallCardView{
		CallID: "call-1", ToolName: "workspace.diff", Phase: "SUCCEEDED",
		Kind: "DIFF", Title: "session.rs", Summary: "+12 -2", ReceiptID: "rcpt-1",
	}, false)
	tc.ApplyCancellationReceipt(mygoshell.CancellationReceiptView{
		TurnOrdinal: 1, Completed: true, CancellationRequested: true,
		TrackerClosed: true, TrackedTasks: 1, RemainingTasks: 0,
		Status:  "RUNTIME_RETIREMENT_STATUS_COMPLETED",
		Outcome: "SESSION_TURN_OUTCOME_REMOTE_UNCERTAIN",
	})

	app := mygoshell.NewWorkbenchApp(targets, slots, wb)
	app.Transcript = tc
	tt := ui.NewTester(app.View, 1280, 800)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tt.Frame()
	}
}

type benchLazyDispatcher struct{}

func (benchLazyDispatcher) SubmitScopedPanelAction(context.Context, pluginhost.ScopedActionRequest) (pluginhost.ScopedActionReceipt, error) {
	return pluginhost.ScopedActionReceipt{Admitted: true}, nil
}

func setup100PluginLazyLoader(b *testing.B) (*marketplace.LazyLoader, time.Time) {
	b.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		b.Fatal(err)
	}
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{"tao3k.official": pub})
	mgr := marketplace.NewManager(trust)

	slots := shell.NewSlotRegistry()
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.inspector", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.inspector", AllowUntrustedPlugin: true,
	}, mygoshell.NewUIElementRenderer("host.inspector", true, slots, func() schema.UIElement {
		return schema.UIElement{Kind: schema.ElemText}
	}))
	host := pluginhost.NewHost(slots, benchLazyDispatcher{})
	host.RegisterRuntime(&pluginhost.DeclarativeRuntimeFactory{})

	dir := b.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "marlin-plugin.toml"), []byte(`
id = "tao3k.warm-hook"
version = "0.1.0"
contract_major = 1
[contributions.p]
kind = "theme-pack"
[contributions.p.panel]
runtime = "declarative"
slot_id = "workbench.inspector"
entry_path = "theme.toml"
`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "theme.toml"), []byte("[theme]\naccent = \"#c678dd\"\n"), 0o644)
	var rev [20]byte
	pkg, _, err := marketplace.PackBundle(dir, "tao3k.official", rev, priv)
	if err != nil {
		b.Fatal(err)
	}

	now := time.Date(2026, 11, 25, 11, 0, 0, 0, time.UTC)
	loader, err := marketplace.NewLazyLoader(marketplace.LazyLoaderConfig{
		Manager:       mgr,
		Host:          host,
		Target:        schema.TargetDesktopMacOS,
		MaxActiveLazy: 8,
		Now:           func() time.Time { return now },
		Fetch:         func(context.Context, marketplace.PluginSpec) ([]byte, error) { return pkg, nil },
	})
	if err != nil {
		b.Fatal(err)
	}

	specs := make([]marketplace.PluginSpec, 0, 100)
	specs = append(specs, marketplace.PluginSpec{
		PluginID:      "tao3k.warm-hook",
		Version:       "0.1.0",
		Enabled:       true,
		Activation:    schema.ActivationOnCommand,
		SlotID:        "workbench.inspector",
		Triggers:      []string{"warm.cmd"},
		Hooks:         []string{"after-turn-complete"},
		IdleUnloadSec: 300,
	})
	for i := 1; i < 100; i++ {
		specs = append(specs, marketplace.PluginSpec{
			PluginID:      schema.PluginID(fmt.Sprintf("ecosystem.lazy-%03d", i)),
			Version:       "0.1.0",
			Enabled:       true,
			Activation:    schema.ActivationOnCommand,
			Triggers:      []string{fmt.Sprintf("cmd.%03d", i)},
			Hooks:         []string{fmt.Sprintf("hook.%03d", i)},
			IdleUnloadSec: 300,
		})
	}
	ctx := context.Background()
	if err := loader.Reconcile(ctx, marketplace.PluginProfile{SchemaVersion: 1, Plugins: specs}); err != nil {
		b.Fatal(err)
	}
	if _, err := loader.Wake(ctx, "tao3k.warm-hook"); err != nil {
		b.Fatal(err)
	}
	return loader, now
}

func BenchmarkLazyLoader_100Plugins_HookDispatch_ZeroAlloc(b *testing.B) {
	loader, _ := setup100PluginLazyLoader(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := loader.EnsureForHook(ctx, "after-turn-complete"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLazyLoader_100Plugins_IdleReapCheck_O1(b *testing.B) {
	loader, now := setup100PluginLazyLoader(b)
	checkAt := now.Add(30 * time.Second) // below 300s threshold -> O(1) tail fast path
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if reaped := loader.ReapIdle(checkAt); len(reaped) != 0 {
			b.Fatalf("unexpected reap: %v", reaped)
		}
	}
}
