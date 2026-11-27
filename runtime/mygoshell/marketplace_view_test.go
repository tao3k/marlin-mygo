// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

type mktTestTarget struct{ meta transport.TargetMeta }

func (t mktTestTarget) Meta() transport.TargetMeta { return t.meta }
func (t mktTestTarget) Ping(context.Context) error { return nil }
func (t mktTestTarget) Close() error               { return nil }

type mktTestDispatcher struct{}

func (mktTestDispatcher) SubmitScopedPanelAction(context.Context, pluginhost.ScopedActionRequest) (pluginhost.ScopedActionReceipt, error) {
	return pluginhost.ScopedActionReceipt{Admitted: true, ReceiptID: "rcpt-mkt"}, nil
}

func buildSignedDeclarativePkg(t *testing.T, priv ed25519.PrivateKey, pluginID, version, accent string) []byte {
	t.Helper()
	dir := t.TempDir()
	manifest := `
id = "` + pluginID + `"
version = "` + version + `"
contract_major = 1
requires_features = ["ui-slot-v1"]

[contributions.theme_pack]
kind = "theme-pack"

[contributions.theme_pack.panel]
runtime = "declarative"
slot_id = "workbench.inspector"
cardinality = "single"
entry_path = "theme.toml"
read_scopes = ["theme.live"]
declared_actions = ["theme.apply"]
`
	if err := os.WriteFile(filepath.Join(dir, "marlin-plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "theme.toml"), []byte("[theme]\naccent = \""+accent+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var rev [20]byte
	pkg, _, err := marketplace.PackBundle(dir, "tao3k.official", rev, priv)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func publishRepoCatalog(t *testing.T, repoDir string, pub ed25519.PublicKey, priv ed25519.PrivateKey, pkgs map[string][]byte) {
	t.Helper()
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{"tao3k.official": pub})
	var paths []string
	for name, data := range pkgs {
		p := filepath.Join(repoDir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	var rev [20]byte
	catJSON, catSig, _, err := marketplace.BuildSignedCatalog(paths, "tao3k.official", rev, trust, priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "catalog.json"), catJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "catalog.json.sig"), []byte(hex.EncodeToString(catSig)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuiltInMyGoGPUMarketplaceInstallUpgradeRollbackDisableEndToEnd(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{"tao3k.official": pub})
	repoDir := t.TempDir()

	pkgV1 := buildSignedDeclarativePkg(t, priv, "tao3k.theme-demo", "0.1.0", "#c678dd")
	pkgV2 := buildSignedDeclarativePkg(t, priv, "tao3k.theme-demo", "0.2.0", "#51afef")
	publishRepoCatalog(t, repoDir, pub, priv, map[string][]byte{
		"tao3k.theme-demo-0.1.0.marlinpkg": pkgV1,
	})

	srv := httptest.NewServer(marketplace.ServeCatalogHandler(repoDir))
	defer srv.Close()

	regClient, err := marketplace.NewRegistryClient(marketplace.RegistryConfig{
		BaseURL:       srv.URL,
		CacheDir:      filepath.Join(t.TempDir(), "cas"),
		CatalogPubKey: pub,
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr := marketplace.NewManager(trust)

	slots := shell.NewSlotRegistry()
	fb := mygoshell.NewUIElementRenderer("host.inspector", true, slots, func() schema.UIElement {
		return schema.UIElement{
			Kind:  schema.ElemPanel,
			Title: "Host Inspector Fallback",
			Tone:  schema.ToneInfo,
		}
	})
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.inspector", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.inspector", AllowUntrustedPlugin: true,
	}, fb)

	pHost := pluginhost.NewHost(slots, mktTestDispatcher{})
	pHost.RegisterRuntime(&pluginhost.DeclarativeRuntimeFactory{})

	wb := shell.NewWorkbenchController(slots)
	_ = wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation: 1, ProfileName: "desktop-dev", Theme: schema.DefaultUITheme(),
		FrameTree: schema.UIElement{
			Kind:     schema.ElemRows,
			Children: []schema.UIElement{{Kind: schema.ElemSlot, SlotID: "workbench.inspector"}},
		},
	})

	targets := transport.NewTargetRegistry()
	_ = targets.Register(mktTestTarget{meta: transport.TargetMeta{
		ID: "local-mac", DisplayName: "Local Mac", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})

	ctx := context.Background()
	mktUI := mygoshell.NewMarketplaceController()
	var genSeq atomic.Uint64
	installedHistory := make(map[string][]byte) // stores previous version bytes for rollback

	syncCatalogToUI := func() error {
		idx, err := regClient.RefreshCatalog(ctx)
		if err != nil {
			return err
		}
		var items []mygoshell.MarketplaceItem
		for _, e := range idx.Entries {
			rt := schema.RuntimeDeclarative
			if len(e.Runtimes) > 0 {
				rt = e.Runtimes[0]
			}
			item := mygoshell.MarketplaceItem{
				PluginID:          string(e.PluginID),
				Version:           e.Version,
				PublisherID:       string(e.PublisherID),
				Summary:           e.Summary,
				SlotID:            string(e.SlotID),
				Runtime:           rt,
				ReadScopes:        []string{"theme.live"},
				DeclaredActions:   []string{"theme.apply"},
				SignatureVerified: true,
				PackageBytes:      e.PackageBytes,
			}
			if active, ok := pHost.ActiveScope(e.PluginID); ok {
				item.Installed = true
				item.ActiveGeneration = active.ActivationGeneration
				if len(installedHistory[string(e.PluginID)]) > 0 {
					item.InstalledVersion = "0.1.0"
				}
				item.HealthState = "HEALTHY"
			}
			items = append(items, item)
		}
		mktUI.SetItems(items)
		return nil
	}

	installOrUpgrade := func(pluginID string) error {
		entry, ok := regClient.LookupEntry(schema.PluginID(pluginID))
		if !ok {
			return marketplace.ErrPackageNotFound
		}
		raw, err := regClient.FetchPackage(ctx, entry)
		if err != nil {
			return err
		}
		gen := genSeq.Add(1)
		panel, err := mgr.InstallBundle(ctx, raw, pHost, schema.TargetDesktopMacOS, gen)
		if err != nil {
			return err
		}
		if entry.Version == "0.1.0" {
			installedHistory[pluginID] = raw
		}
		mktUI.MarkInstalled(pluginID, panel.Manifest.Version, gen, "HEALTHY")
		return nil
	}

	mktUI.OnRefresh = syncCatalogToUI
	mktUI.OnInstall = installOrUpgrade
	mktUI.OnUpgrade = installOrUpgrade
	mktUI.OnRollback = func(pluginID string) error {
		prev := installedHistory[pluginID]
		gen := genSeq.Add(1)
		panel, err := mgr.InstallBundle(ctx, prev, pHost, schema.TargetDesktopMacOS, gen)
		if err != nil {
			return err
		}
		mktUI.MarkInstalled(pluginID, panel.Manifest.Version, gen, "HEALTHY")
		return nil
	}
	mktUI.OnDisable = func(pluginID string) error {
		if err := pHost.Deactivate(schema.PluginID(pluginID)); err != nil {
			return err
		}
		mktUI.MarkDisabled(pluginID)
		return nil
	}

	if err := syncCatalogToUI(); err != nil {
		t.Fatal(err)
	}

	app := mygoshell.NewWorkbenchApp(targets, slots, wb)
	app.Marketplace = mktUI

	tt := ui.NewTester(app.View, 1200, 800)
	if !tt.HasText("Marketplace") {
		t.Fatalf("expected Marketplace button in top bar, got %q", tt.Texts())
	}

	// 1. Open Tier-0 Marketplace GPU drawer via top bar button.
	if err := tt.Click("Marketplace"); err != nil {
		t.Fatalf("clicking Marketplace button failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("MARLIN UI MARKETPLACE (HOST TIER-0)") || !tt.HasText("VERIFIED · tao3k.official") || !tt.HasText("scopes:[theme.live]  actions:[theme.apply]") {
		t.Fatalf("expected verified Tier-0 Marketplace catalog card, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 2. Click Install -> mounts signed v0.1.0 at gen:1!
	if err := tt.Click("Install tao3k.theme-demo"); err != nil {
		t.Fatalf("clicking Install failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("ACTIVE v0.1.0 · gen:1 · HEALTHY") {
		t.Fatalf("expected ACTIVE v0.1.0 gen:1 after Install, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 3. Publish v0.2.0 to remote HTTP registry, click Refresh Catalog, then click Upgrade -> gen:2!
	publishRepoCatalog(t, repoDir, pub, priv, map[string][]byte{
		"tao3k.theme-demo-0.2.0.marlinpkg": pkgV2,
	})
	if err := tt.Click("Refresh Catalog"); err != nil {
		t.Fatalf("clicking Refresh Catalog failed: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Upgrade tao3k.theme-demo"); err != nil {
		t.Fatalf("clicking Upgrade failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("ACTIVE v0.2.0 · gen:2 · HEALTHY") || !tt.HasText("Rollback tao3k.theme-demo") {
		t.Fatalf("expected ACTIVE v0.2.0 gen:2 and Rollback button after Upgrade, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 4. Click Rollback -> restores v0.1.0 at monotonic gen:3!
	if err := tt.Click("Rollback tao3k.theme-demo"); err != nil {
		t.Fatalf("clicking Rollback failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("ACTIVE v0.1.0 · gen:3 · HEALTHY") {
		t.Fatalf("expected ACTIVE v0.1.0 gen:3 after Rollback, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 5. Click Disable -> unmounts plugin and restores host.inspector fallback!
	if err := tt.Click("Disable tao3k.theme-demo"); err != nil {
		t.Fatalf("clicking Disable failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("NOT INSTALLED") {
		t.Fatalf("expected NOT INSTALLED after Disable, got %q", strings.Join(tt.Texts(), " | "))
	}
	if _, active := pHost.ActiveScope("tao3k.theme-demo"); active {
		t.Fatalf("expected plugin deactivated from pluginhost.Host")
	}
	if resolved := slots.Resolve("workbench.inspector"); len(resolved) != 1 || resolved[0].ID() != "host.inspector" {
		t.Fatalf("expected host.inspector fallback restored after Disable, got %+v", resolved)
	}
}

func TestBuiltInMarketplaceLazyDormantWakeAndSleepLifecycle(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{"tao3k.official": pub})
	pkg := buildSignedDeclarativePkg(t, priv, "tao3k.lazy-theme", "0.1.0", "#98c379")

	mgr := marketplace.NewManager(trust)
	slots := shell.NewSlotRegistry()
	fb := mygoshell.NewUIElementRenderer("host.inspector", true, slots, func() schema.UIElement {
		return schema.UIElement{Kind: schema.ElemPanel, Title: "Host Inspector Fallback", Tone: schema.ToneInfo}
	})
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.inspector", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.inspector", AllowUntrustedPlugin: true,
	}, fb)

	pHost := pluginhost.NewHost(slots, mktTestDispatcher{})
	pHost.RegisterRuntime(&pluginhost.DeclarativeRuntimeFactory{})

	ctx := context.Background()
	loader, err := marketplace.NewLazyLoader(marketplace.LazyLoaderConfig{
		Manager:       mgr,
		Host:          pHost,
		Target:        schema.TargetDesktopMacOS,
		MaxActiveLazy: 4,
		Fetch:         func(context.Context, marketplace.PluginSpec) ([]byte, error) { return pkg, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := loader.Reconcile(ctx, marketplace.PluginProfile{
		SchemaVersion: 1,
		Plugins: []marketplace.PluginSpec{{
			PluginID:      "tao3k.lazy-theme",
			Version:       "0.1.0",
			Enabled:       true,
			Activation:    schema.ActivationOnSlot,
			SlotID:        "workbench.inspector",
			IdleUnloadSec: 300,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	wb := shell.NewWorkbenchController(slots)
	_ = wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation: 1, ProfileName: "desktop-dev", Theme: schema.DefaultUITheme(),
		FrameTree: schema.UIElement{
			Kind:     schema.ElemRows,
			Children: []schema.UIElement{{Kind: schema.ElemSlot, SlotID: "workbench.inspector"}},
		},
	})
	targets := transport.NewTargetRegistry()
	_ = targets.Register(mktTestTarget{meta: transport.TargetMeta{
		ID: "local-mac", DisplayName: "Local Mac", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})

	mktUI := mygoshell.NewMarketplaceController()
	mktUI.SetItems([]mygoshell.MarketplaceItem{{
		PluginID:          "tao3k.lazy-theme",
		Version:           "0.1.0",
		PublisherID:       "tao3k.official",
		Summary:           "Lazy Emacs-style theme pack",
		SlotID:            "workbench.inspector",
		Runtime:           schema.RuntimeDeclarative,
		SignatureVerified: true,
		Installed:         true,
		Dormant:           true,
		ActivationPolicy:  string(schema.ActivationOnSlot),
		InstalledVersion:  "0.1.0",
	}})
	syncStats := func() {
		st := loader.Stats()
		mktUI.SetProfilerSummary(mygoshell.LazyProfilerSummary{
			TotalDeclared:     st.TotalDeclared,
			ActiveCount:       st.ActiveCount,
			DormantCount:      st.DormantCount,
			MaxActiveLazy:     st.MaxActiveLazy,
			TotalWakes:        st.TotalWakes,
			TotalReaped:       st.TotalReaped,
			TotalEvicted:      st.TotalEvicted,
			ReconcileDuration: st.LastReconcileDuration,
		})
	}
	syncStats()
	mktUI.OnWake = func(pluginID string) error {
		panel, err := loader.Wake(ctx, schema.PluginID(pluginID))
		if err != nil {
			return err
		}
		mktUI.MarkInstalled(pluginID, panel.Manifest.Version, panel.Instance.Scope().ActivationGeneration, "HEALTHY")
		syncStats()
		return nil
	}
	mktUI.OnSleep = func(pluginID string) error {
		if err := loader.Sleep(schema.PluginID(pluginID)); err != nil {
			return err
		}
		mktUI.MarkDormant(pluginID, "0.1.0", string(schema.ActivationOnSlot))
		syncStats()
		return nil
	}

	app := mygoshell.NewWorkbenchApp(targets, slots, wb)
	app.Marketplace = mktUI
	tt := ui.NewTester(app.View, 1200, 800)

	if err := tt.Click("Marketplace"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("DORMANT v0.1.0 · lazy(on-slot) · 0MB RAM") || !tt.HasText("Wake tao3k.lazy-theme") || !tt.HasText("Lazy Engine: 0 active / 1 dormant (0MB RAM)") {
		t.Fatalf("expected dormant lazy badge, profiler strip, and Wake button, got %q", strings.Join(tt.Texts(), " | "))
	}

	// Click Wake -> mounts on-demand at gen:1 and exposes Sleep button.
	if err := tt.Click("Wake tao3k.lazy-theme"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("ACTIVE v0.1.0 · gen:1 · HEALTHY") || !tt.HasText("Sleep tao3k.lazy-theme") || !tt.HasText("Lazy Engine: 1 active / 0 dormant (0MB RAM)") {
		t.Fatalf("expected ACTIVE gen:1, profiler 1 active, and Sleep button after Wake, got %q", strings.Join(tt.Texts(), " | "))
	}

	// Click Sleep -> unloads plugin back to 0MB RAM dormant state and restores host.inspector fallback.
	if err := tt.Click("Sleep tao3k.lazy-theme"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("DORMANT v0.1.0 · lazy(on-slot) · 0MB RAM") || !tt.HasText("Lazy Engine: 0 active / 1 dormant (0MB RAM)") {
		t.Fatalf("expected DORMANT badge and 0 active profiler strip after Sleep, got %q", strings.Join(tt.Texts(), " | "))
	}
	if _, active := pHost.ActiveScope("tao3k.lazy-theme"); active {
		t.Fatal("expected lazy plugin unloaded from host after Sleep")
	}
	if resolved := slots.Resolve("workbench.inspector"); len(resolved) != 1 || resolved[0].ID() != "host.inspector" {
		t.Fatalf("expected host.inspector fallback restored after Sleep, got %+v", resolved)
	}
}

func TestUIElementActionButtonAndUntrustedWebViewContentRules(t *testing.T) {
	// 1. Verify MyGo WebKit ContentRules JSON blocks third-party http(s):// and wss:// egress.
	var rules []struct {
		Trigger struct {
			URLFilter string `json:"url-filter"`
		} `json:"trigger"`
		Action struct {
			Type string `json:"type"`
		} `json:"action"`
	}
	if err := json.Unmarshal([]byte(mygoshell.UntrustedPluginContentRulesJSON), &rules); err != nil {
		t.Fatalf("invalid UntrustedPluginContentRulesJSON: %v", err)
	}
	if len(rules) < 2 || rules[0].Action.Type != "block" || rules[1].Action.Type != "block" {
		t.Fatalf("expected >=2 block rules for http(s) and wss egress, got %+v", rules)
	}

	// 2. Verify interactive ElemButton inside UIElement routes clicks to ScopedProxy.
	var gotAction, gotArgs string
	slots := shell.NewSlotRegistry()
	rend := mygoshell.NewUIElementRenderer("tao3k.graph-inspector", false, slots, func() schema.UIElement {
		return schema.UIElement{
			Kind:  schema.ElemPanel,
			Title: "Interactive WASM Panel",
			Tone:  schema.ToneInfo,
			Children: []schema.UIElement{
				{
					Kind:       schema.ElemButton,
					Title:      "Inspect Node",
					Tone:       schema.ToneAccent,
					Action:     "graph.node.inspect",
					ActionArgs: `{"nodeId":"plan_01"}`,
				},
			},
		}
	})
	rend.OnAction = func(action, argsJSON string) {
		gotAction = action
		gotArgs = argsJSON
	}

	tt := ui.NewTester(func(c *ui.Context) {
		rend.Render(shell.RenderContext{Surface: c, Theme: schema.DefaultUITheme()})
	}, 640, 400)
	if !tt.HasText("Inspect Node") {
		t.Fatalf("expected ActionButton label in rendered GPU UI, got %q", tt.Texts())
	}
	if err := tt.Click("Inspect Node"); err != nil {
		t.Fatalf("clicking GPU ActionButton failed: %v", err)
	}
	tt.Frame()
	if gotAction != "graph.node.inspect" || gotArgs != `{"nodeId":"plan_01"}` {
		t.Fatalf("expected action graph.node.inspect with args, got action=%q args=%q", gotAction, gotArgs)
	}
}

func TestBuiltInMarketplaceUpgradePrivilegeEscalationRequiresConfirmation(t *testing.T) {
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	targets := transport.NewTargetRegistry()
	_ = targets.Register(mktTestTarget{meta: transport.TargetMeta{
		ID: "local-mac", DisplayName: "Local Mac", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})

	mktUI := mygoshell.NewMarketplaceController()
	upgraded := 0
	mktUI.OnUpgrade = func(pluginID string) error {
		upgraded++
		mktUI.MarkInstalled(pluginID, "0.2.0", 2, "HEALTHY")
		return nil
	}
	mktUI.SetItems([]mygoshell.MarketplaceItem{{
		PluginID:             "acme.board",
		Version:              "0.2.0",
		PublisherID:          "acme.community",
		Summary:              "Community board requesting write action in v0.2.0",
		SlotID:               "workbench.main",
		Runtime:              schema.RuntimeWebViewUntrusted,
		SignatureVerified:    true,
		Installed:            true,
		InstalledVersion:     "0.1.0",
		ActiveGeneration:     1,
		ReadScopes:           []string{"org.tasks"},
		DeclaredActions:      []string{"org.task.move-state"},
		AddedDeclaredActions: []string{"org.task.move-state"},
	}})

	app := mygoshell.NewWorkbenchApp(targets, slots, wb)
	app.Marketplace = mktUI
	tt := ui.NewTester(app.View, 1200, 800)

	if err := tt.Click("Marketplace"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("NEW PERMISSIONS ON UPGRADE: +actions:[org.task.move-state]") {
		t.Fatalf("expected visible privilege escalation warning, got %q", strings.Join(tt.Texts(), " | "))
	}

	// First click on Upgrade MUST NOT upgrade immediately; it must require explicit confirmation.
	if err := tt.Click("Upgrade acme.board"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if upgraded != 0 || !tt.HasText("Confirm Upgrade acme.board") {
		t.Fatalf("expected 1st click to block upgrade and show Confirm Upgrade button (upgraded=%d), got %q", upgraded, strings.Join(tt.Texts(), " | "))
	}

	// Explicit 2nd click confirms the permission expansion and executes OnUpgrade.
	if err := tt.Click("Confirm Upgrade acme.board"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if upgraded != 1 {
		t.Fatalf("expected OnUpgrade called once after explicit confirmation, got %d", upgraded)
	}
}
