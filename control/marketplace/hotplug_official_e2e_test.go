// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

type trackingWebViewRuntime struct {
	mu             sync.Mutex
	activeByPlugin map[schema.PluginID]uint64
	lastProxy      map[schema.PluginID]*pluginhost.ScopedProxy
	activeCount    int
	totalMounts    int
	totalUnmounts  int
	lastEntryHTML  map[schema.PluginID]string
}

func newTrackingWebViewRuntime() *trackingWebViewRuntime {
	return &trackingWebViewRuntime{
		activeByPlugin: make(map[schema.PluginID]uint64),
		lastProxy:      make(map[schema.PluginID]*pluginhost.ScopedProxy),
		lastEntryHTML:  make(map[schema.PluginID]string),
	}
}

func (r *trackingWebViewRuntime) Kind() schema.PanelRuntimeKind {
	return schema.RuntimeWebViewUntrusted
}

func (r *trackingWebViewRuntime) Instantiate(_ context.Context, scope pluginhost.ScopedScope, proxy *pluginhost.ScopedProxy) (pluginhost.Instance, error) {
	var htmlBytes []byte
	if scope.Assets != nil {
		htmlBytes, _ = fs.ReadFile(scope.Assets, "index.html")
	}
	r.mu.Lock()
	r.activeByPlugin[scope.PluginID] = scope.ActivationGeneration
	r.lastProxy[scope.PluginID] = proxy
	r.activeCount++
	r.totalMounts++
	r.lastEntryHTML[scope.PluginID] = string(htmlBytes)
	r.mu.Unlock()
	return &trackingWebViewInstance{
		rt:       r,
		scope:    scope,
		renderer: devNoopRenderer{id: string(scope.PluginID)},
	}, nil
}

type trackingWebViewInstance struct {
	rt       *trackingWebViewRuntime
	scope    pluginhost.ScopedScope
	renderer shell.SlotRenderer
	closed   bool
	mu       sync.Mutex
}

func (i *trackingWebViewInstance) Scope() pluginhost.ScopedScope         { return i.scope }
func (i *trackingWebViewInstance) Renderer() shell.SlotRenderer          { return i.renderer }
func (i *trackingWebViewInstance) DeliverEvent(pluginhost.ScopedEvent) error { return nil }
func (i *trackingWebViewInstance) Close() error {
	i.mu.Lock()
	if i.closed {
		i.mu.Unlock()
		return nil
	}
	i.closed = true
	i.mu.Unlock()

	i.rt.mu.Lock()
	i.rt.activeCount--
	i.rt.totalUnmounts++
	if i.rt.activeByPlugin[i.scope.PluginID] == i.scope.ActivationGeneration {
		delete(i.rt.activeByPlugin, i.scope.PluginID)
	}
	i.rt.mu.Unlock()
	return nil
}

func TestOfficialMarketplaceOnlyRejectsUnofficialAndTamperedBundles(t *testing.T) {
	ctx := context.Background()
	offPub, offPriv, _ := ed25519.GenerateKey(rand.Reader)
	comPub, comPriv, _ := ed25519.GenerateKey(rand.Reader)
	devPub, devPriv, _ := ed25519.GenerateKey(rand.Reader)

	ringTOML := fmt.Sprintf(`
schema_version = 1

[publishers."tao3k.official"]
tier = "official"
ed25519_pub = %q
allow_catalog = true

[publishers."community.vendor"]
tier = "verified"
ed25519_pub = %q

[publishers."dev.local"]
tier = "dev"
ed25519_pub = %q
`, hex.EncodeToString(offPub), hex.EncodeToString(comPub), hex.EncodeToString(devPub))

	ring, err := marketplace.ParseTrustRingTOML([]byte(ringTOML))
	if err != nil {
		t.Fatalf("ParseTrustRingTOML: %v", err)
	}
	if !ring.IsOfficialPublisher("tao3k.official") || ring.IsOfficialPublisher("community.vendor") || ring.IsOfficialPublisher("dev.local") {
		t.Fatal("unexpected IsOfficialPublisher classification")
	}

	dir := t.TempDir()
	writeDemoPlugin(t, dir, "1.0.0", "workbench.inspector", "#6352e8")

	officialPkg, _, err := marketplace.PackBundle(dir, "tao3k.official", [20]byte{1}, offPriv)
	if err != nil {
		t.Fatalf("pack official: %v", err)
	}
	communityPkg, _, err := marketplace.PackBundle(dir, "community.vendor", [20]byte{2}, comPriv)
	if err != nil {
		t.Fatalf("pack community: %v", err)
	}
	devPkg, _, err := marketplace.PackBundle(dir, "dev.local", [20]byte{3}, devPriv)
	if err != nil {
		t.Fatalf("pack dev: %v", err)
	}
	tamperedPkg := append([]byte(nil), officialPkg...)
	tamperedPkg[len(tamperedPkg)/2] ^= 0xff

	// Official-Only Manager strictly rejects community, dev, and tampered bundles!
	officialMgr := marketplace.NewManager(ring.OfficialReleaseTrust())
	slots := shell.NewSlotRegistry()
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.inspector", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.fb", AllowUntrustedPlugin: true,
	}, devNoopRenderer{id: "host.fb"})
	host := pluginhost.NewHost(slots, devDispatcher{})
	host.RegisterRuntime(&pluginhost.DeclarativeRuntimeFactory{})

	if _, err := officialMgr.InstallBundle(ctx, communityPkg, host, schema.TargetDesktopMacOS, 1); err == nil {
		t.Fatal("expected community-signed bundle rejected in Official-Only mode")
	}
	if _, err := officialMgr.InstallBundle(ctx, devPkg, host, schema.TargetDesktopMacOS, 2); err == nil {
		t.Fatal("expected dev-signed bundle rejected in Official-Only mode")
	}
	if _, err := officialMgr.InstallBundle(ctx, tamperedPkg, host, schema.TargetDesktopMacOS, 3); err == nil {
		t.Fatal("expected 1-byte tampered official bundle rejected")
	}

	// Official bundle succeeds cleanly!
	panel, err := officialMgr.InstallBundle(ctx, officialPkg, host, schema.TargetDesktopMacOS, 4)
	if err != nil || panel.Manifest.ID != "acme.live-theme" {
		t.Fatalf("expected official bundle admitted, got panel=%+v err=%v", panel, err)
	}
}

func TestOfficialHotPlugUpgradeRollbackUnplug50CyclesZeroLeaks(t *testing.T) {
	ctx := context.Background()
	offPub, offPriv, _ := ed25519.GenerateKey(rand.Reader)
	mgr := marketplace.NewManager(schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{
		"tao3k.official": offPub,
	}))

	slots := shell.NewSlotRegistry()
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.main", AllowUntrustedPlugin: true,
	}, devNoopRenderer{id: "host.main"})

	host := pluginhost.NewHost(slots, devDispatcher{})
	wvRT := newTrackingWebViewRuntime()
	host.RegisterRuntime(wvRT)

	makeWebPkg := func(version, body string) []byte {
		dir := t.TempDir()
		manifest := fmt.Sprintf(`
id = "tao3k.hotplug-react"
version = %q
contract_major = 1
requires_features = ["ui-slot-v1"]

[contributions.main_panel]
kind = "panel"

[contributions.main_panel.panel]
runtime = "webview-untrusted"
slot_id = "workbench.main"
cardinality = "single"
entry_path = "index.html"
read_scopes = ["theme.live", "layout.live"]
declared_actions = ["panel.ping"]
`, version)
		_ = os.WriteFile(filepath.Join(dir, "marlin-plugin.toml"), []byte(manifest), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><html><body>"+body+"</body></html>"), 0o644)
		pkg, _, err := marketplace.PackBundle(dir, "tao3k.official", [20]byte{byte(len(version))}, offPriv)
		if err != nil {
			t.Fatalf("PackBundle v%s: %v", version, err)
		}
		return pkg
	}

	pkgV1 := makeWebPkg("1.0.0", "V1")
	pkgV2 := makeWebPkg("2.0.0", "V2")

	var gen uint64
	for cycle := 0; cycle < 50; cycle++ {
		gen++
		p1, err := mgr.InstallBundle(ctx, pkgV1, host, schema.TargetDesktopMacOS, gen)
		if err != nil || p1.Instance.Scope().ActivationGeneration != gen {
			t.Fatalf("cycle %d install v1 failed: %v", cycle, err)
		}
		wvRT.mu.Lock()
		oldProxy := wvRT.lastProxy["tao3k.hotplug-react"]
		wvRT.mu.Unlock()

		// Upgrade to v2 -> old gen proxy MUST be immediately revoked!
		gen++
		p2, err := mgr.InstallBundle(ctx, pkgV2, host, schema.TargetDesktopMacOS, gen)
		if err != nil || p2.Instance.Scope().ActivationGeneration != gen {
			t.Fatalf("cycle %d upgrade v2 failed: %v", cycle, err)
		}
		if _, err := oldProxy.SubmitAction(ctx, "panel.ping", json.RawMessage(`{}`), "k-stale"); !errors.Is(err, pluginhost.ErrProxyRevoked) {
			t.Fatalf("cycle %d expected old proxy revoked after upgrade, got %v", cycle, err)
		}

		// Rollback to v1 at monotonic gen+1.
		gen++
		pRb, err := mgr.InstallBundle(ctx, pkgV1, host, schema.TargetDesktopMacOS, gen)
		if err != nil || pRb.Manifest.Version != "1.0.0" {
			t.Fatalf("cycle %d rollback failed: %v", cycle, err)
		}
		// Hot-unplug completely -> restores host fallback cleanly.
		if err := host.Deactivate("tao3k.hotplug-react"); err != nil {
			t.Fatalf("cycle %d deactivate failed: %v", cycle, err)
		}
	}

	wvRT.mu.Lock()
	active := wvRT.activeCount
	mounts := wvRT.totalMounts
	unmounts := wvRT.totalUnmounts
	wvRT.mu.Unlock()

	if active != 0 || mounts != 150 || unmounts != 150 {
		t.Fatalf("leak detected after 50 hot-plug/upgrade/rollback/unplug cycles: active=%d mounts=%d unmounts=%d",
			active, mounts, unmounts)
	}
	if cur := slots.Resolve("workbench.main"); len(cur) != 1 || cur[0].ID() != "host.main" {
		t.Fatalf("expected fallback renderer host.main restored after hot-unplug, got %+v", cur)
	}
}

func TestDevReloaderReact19TypeScriptHotReloadAndAutoRollback(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun not in PATH; skipping live TypeScript/React hot-reload test")
	}
	ctx := context.Background()
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "src"), 0o755)

	manifest := `
id = "dev.ts-hot-panel"
version = "0.1.0"
contract_major = 1
requires_features = ["ui-slot-v1"]

[contributions.main_panel]
kind = "panel"

[contributions.main_panel.panel]
runtime = "webview-untrusted"
slot_id = "workbench.main"
cardinality = "single"
entry_path = "index.html"
read_scopes = ["theme.live", "layout.live"]
declared_actions = ["panel.ping"]
`
	indexHTML := `<!doctype html><html><body><div id="root"></div><script type="module" src="./src/main.ts"></script></body></html>`
	_ = os.WriteFile(filepath.Join(dir, "marlin-plugin.toml"), []byte(manifest), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte(indexHTML), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte(`document.getElementById("root")!.textContent = "GEN_ONE";`), 0o644)

	slots := shell.NewSlotRegistry()
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.main", AllowUntrustedPlugin: true,
	}, devNoopRenderer{id: "host.main"})
	host := pluginhost.NewHost(slots, devDispatcher{})
	wvRT := newTrackingWebViewRuntime()
	host.RegisterRuntime(wvRT)

	reloader, err := marketplace.NewDevReloader(marketplace.DevReloaderConfig{
		PluginDir: dir,
		Host:      host,
		Target:    schema.TargetDesktopMacOS,
	})
	if err != nil {
		t.Fatalf("NewDevReloader: %v", err)
	}

	// 1. Initial load -> gen 1.
	res1, err := reloader.ReloadOnce(ctx)
	if err != nil || res1.ActiveGeneration != 1 {
		t.Fatalf("initial TS reload failed: res=%+v err=%v", res1, err)
	}
	wvRT.mu.Lock()
	gen1Proxy := wvRT.lastProxy["dev.ts-hot-panel"]
	wvRT.mu.Unlock()

	// 2. Live source edit -> hot-reloads to gen 2 & immediately revokes gen 1 proxy!
	_ = os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte(`document.getElementById("root")!.textContent = "GEN_TWO";`), 0o644)
	res2, err := reloader.ReloadOnce(ctx)
	if err != nil || res2.ActiveGeneration != 2 {
		t.Fatalf("second TS hot-reload failed: res=%+v err=%v", res2, err)
	}
	if _, err := gen1Proxy.SubmitAction(ctx, "panel.ping", json.RawMessage(`{}`), "k-old"); !errors.Is(err, pluginhost.ErrProxyRevoked) {
		t.Fatalf("expected gen 1 proxy revoked after TS hot-reload, got %v", err)
	}

	// 3. Broken TS syntax edit -> compilation fails & preserves gen 2 untouched!
	_ = os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte(`const broken = (((;`), 0o644)
	res3, err := reloader.ReloadOnce(ctx)
	if err == nil || !res3.PreservedPrior || res3.ActiveGeneration != 2 {
		t.Fatalf("expected broken TS edit to preserve gen 2, got res=%+v err=%v", res3, err)
	}

	wvRT.mu.Lock()
	active := wvRT.activeCount
	curGen := wvRT.activeByPlugin["dev.ts-hot-panel"]
	wvRT.mu.Unlock()
	if active != 1 || curGen != 2 {
		t.Fatalf("expected exactly 1 live instance at gen 2 after rollback protection, got active=%d gen=%d", active, curGen)
	}
}
