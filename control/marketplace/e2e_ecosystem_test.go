// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
	"github.com/tao3k/marlin-mygo/runtime/wazeroir"
)

type stubDispatcher struct {
	mu       sync.Mutex
	admitted []pluginhost.ScopedActionRequest
}

func (d *stubDispatcher) SubmitScopedPanelAction(_ context.Context, req pluginhost.ScopedActionRequest) (pluginhost.ScopedActionReceipt, error) {
	d.mu.Lock()
	d.admitted = append(d.admitted, req)
	d.mu.Unlock()
	return pluginhost.ScopedActionReceipt{Admitted: true, ReceiptID: "rcpt-" + req.IdempotencyKey}, nil
}

type stubWebViewFactory struct{}

func (f *stubWebViewFactory) Kind() schema.PanelRuntimeKind { return schema.RuntimeWebViewUntrusted }
func (f *stubWebViewFactory) Instantiate(_ context.Context, scope pluginhost.ScopedScope, _ *pluginhost.ScopedProxy) (pluginhost.Instance, error) {
	return &stubWebViewInst{scope: scope}, nil
}

type stubWebViewInst struct{ scope pluginhost.ScopedScope }

func (i *stubWebViewInst) Scope() pluginhost.ScopedScope { return i.scope }
func (i *stubWebViewInst) Renderer() shell.SlotRenderer {
	return &stubRenderer{id: string(i.scope.PluginID)}
}
func (i *stubWebViewInst) DeliverEvent(pluginhost.ScopedEvent) error { return nil }
func (i *stubWebViewInst) Close() error                              { return nil }

type stubRenderer struct{ id string }

func (r *stubRenderer) ID() string                        { return r.id }
func (r *stubRenderer) Trusted() bool                     { return false }
func (r *stubRenderer) Render(shell.RenderContext)        {}
func (r *stubRenderer) HandleEvent(shell.HostEvent) error { return nil }
func (r *stubRenderer) Dispose()                          {}

type stubTarget struct{ meta transport.TargetMeta }

func (t *stubTarget) Meta() transport.TargetMeta { return t.meta }
func (t *stubTarget) Ping(context.Context) error { return nil }
func (t *stubTarget) Close() error               { return nil }

type drawListProvider interface {
	RenderDrawList(ctx context.Context, dst []schema.DrawCmd) ([]schema.DrawCmd, error)
}

func TestPhaseC_SignedCatalogHotPlugEcosystemE2E(t *testing.T) {
	ctx := context.Background()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{
		"tao3k.official": pub,
	})

	// 1. Compile real Go 1.27 WASI Reactor (go-graph-inspector-reactor) into staged dir.
	stageDir := t.TempDir()
	reactorStage := filepath.Join(stageDir, "reactor")
	if err := os.MkdirAll(reactorStage, 0o755); err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Join("..", "..")
	reactorManifest, err := os.ReadFile(filepath.Join(repoRoot, "examples", "plugins", "go-graph-inspector-reactor", "marlin-plugin.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reactorStage, "marlin-plugin.toml"), reactorManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	wasmOut := filepath.Join(reactorStage, "panel.wasm")
	buildCmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmOut, "./examples/plugins/go-graph-inspector-reactor")
	buildCmd.Dir = repoRoot
	buildCmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build go-graph-inspector-reactor wasm failed: %v\n%s", err, string(out))
	}

	// 2. Pack & Ed25519-sign all 3 reference plugins into deterministic .marlinpkg bundles.
	pkgDir := filepath.Join(stageDir, "packages")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var rev [20]byte
	copy(rev[:], []byte("phase-c-catalog-e2e!"))

	packOne := func(srcDir, fileName string) (string, []byte) {
		pkgBytes, _, err := marketplace.PackBundle(srcDir, "tao3k.official", rev, priv)
		if err != nil {
			t.Fatalf("PackBundle(%s): %v", srcDir, err)
		}
		p := filepath.Join(pkgDir, fileName)
		if err := os.WriteFile(p, pkgBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		return p, pkgBytes
	}

	reactorPkgPath, reactorPkg := packOne(reactorStage, "tao3k.graph-inspector.marlinpkg")
	kanbanPkgPath, kanbanPkg := packOne(filepath.Join(repoRoot, "examples", "plugins", "ts-org-kanban"), "tao3k.org-kanban.marlinpkg")
	iosPkgPath, iosPkg := packOne(filepath.Join(repoRoot, "examples", "plugins", "ios-personal-overlay"), "tao3k.ios-personal-capture.marlinpkg")

	pkgStore := map[string][]byte{
		"tao3k.graph-inspector.marlinpkg":      reactorPkg,
		"tao3k.org-kanban.marlinpkg":           kanbanPkg,
		"tao3k.ios-personal-capture.marlinpkg": iosPkg,
	}

	// 3. Build & Ed25519-sign Marketplace catalog.json from all 3 verified .marlinpkg bundles.
	catJSON, catSig, idx, err := marketplace.BuildSignedCatalog(
		[]string{reactorPkgPath, kanbanPkgPath, iosPkgPath},
		"tao3k.official",
		rev,
		trust,
		priv,
	)
	if err != nil {
		t.Fatalf("BuildSignedCatalog failed: %v", err)
	}
	if len(idx.Entries) != 3 {
		t.Fatalf("expected 3 catalog entries, got %d", len(idx.Entries))
	}

	// 4. Boot live MyGo Workbench shell with host.* fallbacks + wazeroir + WebView + Declarative runtimes.
	slots := shell.NewSlotRegistry()
	declareHostFallback := func(slotID schema.SlotID, hostID, label string, allowUntrusted bool) {
		fb := mygoshell.NewUIElementRenderer(hostID, true, slots, func() schema.UIElement {
			return schema.UIElement{
				Kind:  schema.ElemPanel,
				Title: label,
				Tone:  schema.ToneInfo,
				Children: []schema.UIElement{
					{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: label, Tone: schema.ToneMuted}}},
				},
			}
		})
		if err := slots.Declare(schema.SlotDeclaration{
			SlotID:               slotID,
			Cardinality:          schema.SlotSingle,
			FallbackRenderer:     hostID,
			AllowUntrustedPlugin: allowUntrusted,
		}, fb); err != nil {
			t.Fatal(err)
		}
	}
	declareHostFallback("workbench.main", "host.main", "Host Main Fallback", true)
	declareHostFallback("workbench.inspector", "host.inspector", "Host Inspector Fallback", true)
	declareHostFallback("approval.gate", "host.gate", "Host Gate", false)

	wb := shell.NewWorkbenchController(slots)
	if err := wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation:  1,
		ProfileName: "desktop-dev",
		Theme:       schema.DefaultUITheme(),
		FrameTree: schema.UIElement{
			Kind: schema.ElemRows,
			Children: []schema.UIElement{
				{Kind: schema.ElemSlot, SlotID: "workbench.main"},
				{Kind: schema.ElemSlot, SlotID: "workbench.inspector"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	targets := transport.NewTargetRegistry()
	_ = targets.Register(&stubTarget{meta: transport.TargetMeta{
		ID: "local-mac", DisplayName: "Local Mac", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)

	wasmEng, err := wazeroir.NewModuleEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer wasmEng.Close(ctx)

	disp := &stubDispatcher{}
	host := pluginhost.NewHost(slots, disp)
	host.RegisterRuntime(wazeroir.NewRuntime(slots, wasmEng))
	host.RegisterRuntime(&stubWebViewFactory{})
	host.RegisterRuntime(&pluginhost.DeclarativeRuntimeFactory{})

	mgr := marketplace.NewManager(trust)
	fetch := func(e marketplace.CatalogEntry) ([]byte, error) { return pkgStore[e.PackageFile], nil }

	// 5. Install Go WASM-IR Reactor from signed catalog -> renders live in MyGo UI + 25µs DrawList + bidirectional OnEvent!
	installedReactor, err := mgr.InstallFromCatalog(ctx, catJSON, catSig, pub, "tao3k.graph-inspector", fetch, host, schema.TargetDesktopMacOS, 1)
	if err != nil {
		t.Fatalf("InstallFromCatalog(tao3k.graph-inspector) failed: %v", err)
	}

	tt := ui.NewTester(app.View, 1000, 700)
	if !tt.HasText("Graph Trajectory (Resident Reactor)") {
		t.Fatalf("expected live WASM-IR reactor title in MyGo frame, got texts=%q", tt.Texts())
	}

	// Verify zero-JSON binary DrawList channel directly from mounted instance.
	dlp, ok := installedReactor.Instance.(drawListProvider)
	if !ok {
		t.Fatal("mounted wazeroir instance does not expose RenderDrawList")
	}
	cmds, err := dlp.RenderDrawList(ctx, nil)
	if err != nil || len(cmds) != 64 {
		t.Fatalf("expected 64 binary DrawCmds from mounted reactor, got len=%d err=%v", len(cmds), err)
	}

	// Deliver allowed scoped event -> reactor emits declared action graph.node.inspect -> admitted!
	if err := host.RouteEvent(pluginhost.ScopedEvent{
		EventID:              "ev-1",
		PluginID:             "tao3k.graph-inspector",
		ActivationGeneration: 1,
		Topic:                "graph.trajectory",
		Payload:              json.RawMessage(`{"nodeId":"verify_receipts"}`),
	}); err != nil {
		t.Fatalf("RouteEvent allowed failed: %v", err)
	}
	// Deliver adversarial probe -> reactor tries undeclared shell.exec -> ScopedProxy MUST block!
	if err := host.RouteEvent(pluginhost.ScopedEvent{
		EventID:              "ev-2",
		PluginID:             "tao3k.graph-inspector",
		ActivationGeneration: 1,
		Topic:                "graph.trajectory",
		Payload:              json.RawMessage(`{"triggerProbe":true}`),
	}); err != nil {
		t.Fatalf("RouteEvent probe failed: %v", err)
	}
	disp.mu.Lock()
	if len(disp.admitted) != 1 || disp.admitted[0].ActionName != "graph.node.inspect" {
		t.Fatalf("expected exactly 1 admitted action (graph.node.inspect), got %+v", disp.admitted)
	}
	disp.mu.Unlock()

	// Hot-unmount restores host.inspector fallback atomically.
	if err := host.Deactivate("tao3k.graph-inspector"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Host Inspector Fallback") {
		t.Fatalf("expected host.inspector fallback restored after unmount, got %q", tt.Texts())
	}

	// 6. Verify App Store / target policy: TS Kanban admitted on Desktop, REJECTED on iOS; iOS overlay admitted on iOS!
	if _, err := mgr.InstallFromCatalog(ctx, catJSON, catSig, pub, "tao3k.org-kanban", fetch, host, schema.TargetMobileIOS, 1); !errors.Is(err, marketplace.ErrRuntimeDisallowed) {
		t.Fatalf("expected ErrRuntimeDisallowed for webview-untrusted on mobile-ios, got %v", err)
	}
	if _, err := mgr.InstallFromCatalog(ctx, catJSON, catSig, pub, "tao3k.org-kanban", fetch, host, schema.TargetDesktopMacOS, 1); err != nil {
		t.Fatalf("expected TS kanban admitted on desktop-macos, got %v", err)
	}
	if _, err := mgr.InstallFromCatalog(ctx, catJSON, catSig, pub, "tao3k.ios-personal-capture", fetch, host, schema.TargetMobileIOS, 1); err != nil {
		t.Fatalf("expected declarative iOS overlay admitted on mobile-ios, got %v", err)
	}

	// 7. Tampered catalog bytes or tampered package bytes MUST be rejected.
	tamperedPkg := append([]byte(nil), kanbanPkg...)
	tamperedPkg[len(tamperedPkg)-1] ^= 0xff
	_, err = mgr.InstallFromCatalog(ctx, catJSON, catSig, pub, "tao3k.org-kanban",
		func(marketplace.CatalogEntry) ([]byte, error) { return tamperedPkg, nil },
		host, schema.TargetDesktopMacOS, 2)
	if !errors.Is(err, marketplace.ErrBundleTampered) {
		t.Fatalf("expected ErrBundleTampered for modified .marlinpkg, got %v", err)
	}
}
