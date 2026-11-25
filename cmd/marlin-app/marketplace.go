// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"

	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

func wireMarketplaceUI(
	ctx context.Context,
	mktUI *mygoshell.MarketplaceController,
	host *pluginhost.Host,
	win *mygo.Window,
	catalogURL, catalogDir, catalogPubFile, trustRingFile, publisherID, profilePath string,
	maxActiveLazy int,
	idleReapInterval time.Duration,
) (*marketplace.LazySession, func()) {
	noopSync := func() {}
	hasSource := strings.TrimSpace(catalogURL) != "" || strings.TrimSpace(catalogDir) != ""
	hasTrust := strings.TrimSpace(trustRingFile) != "" || strings.TrimSpace(catalogPubFile) != ""
	if hasSource && hasTrust {
		var (
			trust *schema.ReleaseTrust
			pub   ed25519.PublicKey
		)
		if strings.TrimSpace(trustRingFile) != "" {
			ring, err := marketplace.LoadTrustRingFile(strings.TrimSpace(trustRingFile))
			if err != nil {
				mktUI.SetStatus(fmt.Sprintf("read -trust-ring failed: %v", err), true)
				return nil, noopSync
			}
			if mktUI.OfficialOnly {
				trust = ring.OfficialReleaseTrust()
			} else {
				trust = ring.ReleaseTrust()
			}
			k, ok := ring.CatalogKey(schema.PublisherID(publisherID))
			if !ok {
				mktUI.SetStatus("no catalog verification key found in -trust-ring", true)
				return nil, noopSync
			}
			pub = k
		} else {
			rawHex, err := os.ReadFile(catalogPubFile)
			if err != nil {
				mktUI.SetStatus(fmt.Sprintf("read -catalog-pub failed: %v", err), true)
				return nil, noopSync
			}
			pubBytes, err := hex.DecodeString(strings.TrimSpace(string(rawHex)))
			if err != nil || len(pubBytes) != ed25519.PublicKeySize {
				mktUI.SetStatus("invalid Ed25519 public key in -catalog-pub", true)
				return nil, noopSync
			}
			pub = ed25519.PublicKey(pubBytes)
			trust = schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{
				schema.PublisherID(publisherID): pub,
			})
		}
		regClient, err := marketplace.NewRegistryClient(marketplace.RegistryConfig{
			BaseURL:       catalogURL,
			LocalDir:      catalogDir,
			CacheDir:      filepath.Join(os.TempDir(), "marlin-mygo-cas"),
			CatalogPubKey: pub,
		})
		if err != nil {
			mktUI.SetStatus(fmt.Sprintf("marketplace registry init failed: %v", err), true)
			return nil, noopSync
		}
		sess, err := marketplace.NewLazySession(ctx, marketplace.LazySessionConfig{
			Registry:      regClient,
			Trust:         trust,
			Host:          host,
			Target:        schema.TargetDesktopMacOS,
			ProfilePath:   strings.TrimSpace(profilePath),
			MaxActiveLazy: maxActiveLazy,
		})
		if err != nil {
			mktUI.SetStatus(fmt.Sprintf("lazy plugin session init failed: %v", err), true)
			return nil, noopSync
		}

		syncUI := func() {
			st := sess.Stats()
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
			idx := sess.CachedCatalog()
			if idx == nil {
				return
			}
			statuses := sess.StatusMap()
			items := make([]mygoshell.MarketplaceItem, 0, len(idx.Entries))
			for _, e := range idx.Entries {
				rt := schema.RuntimeDeclarative
				if len(e.Runtimes) > 0 {
					rt = e.Runtimes[0]
				}
				it := mygoshell.MarketplaceItem{
					PluginID:          string(e.PluginID),
					Version:           e.Version,
					PublisherID:       string(e.PublisherID),
					Summary:           e.Summary,
					SlotID:            string(e.SlotID),
					Runtime:           rt,
					SignatureVerified: true,
					TrustTier:         "official",
					PackageBytes:      e.PackageBytes,
				}
				if prevVer, hasRB := sess.HasRollback(e.PluginID); hasRB {
					it.PreviousVersion = prevVer
				}
				if lstat, declared := statuses[e.PluginID]; declared && lstat.Spec.Enabled {
					it.Installed = true
					it.InstalledVersion = lstat.Spec.Version
					it.ActivationPolicy = string(lstat.Spec.Activation)
					it.Triggers = lstat.Spec.Triggers
					it.LastWakeDuration = lstat.LastWakeDuration
					if lstat.State == marketplace.LazyStateDormant {
						it.Dormant = true
						it.HealthState = "DORMANT"
					} else if lstat.State == marketplace.LazyStateActive {
						it.ActiveGeneration = lstat.ActiveGeneration
						it.HealthState = "HEALTHY"
						if active, ok := host.ActiveScope(e.PluginID); ok {
							it.ReadScopes = active.ReadScopes
							it.DeclaredActions = active.DeclaredActions
						}
					}
				} else if active, ok := host.ActiveScope(e.PluginID); ok {
					it.Installed = true
					it.ActiveGeneration = active.ActivationGeneration
					it.ReadScopes = active.ReadScopes
					it.DeclaredActions = active.DeclaredActions
					it.HealthState = "HEALTHY"
				}
				items = append(items, it)
			}
			mktUI.SetItems(items)
		}

		refresh := func() error {
			if _, err := sess.RefreshCatalog(ctx); err != nil {
				return err
			}
			syncUI()
			win.Invalidate()
			return nil
		}
		install := func(pluginID string) error {
			if _, err := sess.InstallOrUpgrade(ctx, schema.PluginID(pluginID)); err != nil {
				return err
			}
			syncUI()
			win.Invalidate()
			return nil
		}

		mktUI.OnRefresh = refresh
		mktUI.OnInstall = install
		mktUI.OnUpgrade = install
		mktUI.OnRollback = func(pluginID string) error {
			if _, err := sess.Rollback(ctx, schema.PluginID(pluginID)); err != nil {
				return err
			}
			syncUI()
			win.Invalidate()
			return nil
		}
		mktUI.OnWake = func(pluginID string) error {
			if _, err := sess.Wake(ctx, schema.PluginID(pluginID)); err != nil {
				return err
			}
			syncUI()
			win.Invalidate()
			return nil
		}
		mktUI.OnSleep = func(pluginID string) error {
			if err := sess.Sleep(schema.PluginID(pluginID)); err != nil {
				return err
			}
			syncUI()
			win.Invalidate()
			return nil
		}
		mktUI.OnDisable = func(pluginID string) error {
			if err := sess.Disable(ctx, schema.PluginID(pluginID)); err != nil {
				return err
			}
			syncUI()
			win.Invalidate()
			return nil
		}

		sess.StartIdleReaper(ctx, idleReapInterval, func(reaped []schema.PluginID) {
			mktUI.SetStatus(fmt.Sprintf("Reclaimed %d idle plugin(s) to 0MB RAM: %v", len(reaped), reaped), false)
			syncUI()
			win.Invalidate()
		})

		if err := refresh(); err != nil {
			syncUI()
			mktUI.SetStatus(fmt.Sprintf("initial catalog refresh failed: %v", err), true)
		}
		return sess, syncUI
	}

	// Standalone showcase items for immediate desktop Official Marketplace exploration.
	mktUI.SetItems([]mygoshell.MarketplaceItem{
		{
			PluginID:          "tao3k.org-kanban",
			Version:           "0.2.0",
			PublisherID:       "tao3k.official",
			Summary:           "Org-mode Kanban board panel (TypeScript Untrusted WebView)",
			SlotID:            "workbench.main",
			Runtime:           schema.RuntimeWebViewUntrusted,
			ReadScopes:        []string{"org.tasks", "session.summary"},
			DeclaredActions:   []string{"org.task.move-state"},
			SignatureVerified: true,
			TrustTier:         "official",
		},
		{
			PluginID:          "tao3k.graph-inspector",
			Version:           "0.2.0",
			PublisherID:       "tao3k.official",
			Summary:           "Resident Go 1.27 WASI Reactor with 22us binary DrawList GPU rendering",
			SlotID:            "workbench.inspector",
			Runtime:           schema.RuntimeWasmUIIR,
			ReadScopes:        []string{"graph.nodes", "graph.edges"},
			DeclaredActions:   []string{"graph.node.inspect"},
			SignatureVerified: true,
			TrustTier:         "official",
		},
		{
			PluginID:          "tao3k.ios-personal-capture",
			Version:           "0.2.0",
			PublisherID:       "tao3k.official",
			Summary:           "App Store-safe Declarative theme & mobile capture overlay pack",
			SlotID:            "workbench.inspector",
			Runtime:           schema.RuntimeDeclarative,
			SignatureVerified: true,
			TrustTier:         "official",
		},
	})
	var genSeq atomic.Uint64
	mktUI.OnInstall = func(pluginID string) error {
		if pluginID == "tao3k.org-kanban" {
			gen := genSeq.Add(1)
			_, err := host.Activate(ctx, pluginhost.ScopedScope{
				PluginID:             "tao3k.org-kanban",
				ContributionID:       "kanban_panel",
				ActivationGeneration: gen,
				Runtime:              schema.RuntimeWebViewUntrusted,
				SlotID:               "workbench.main",
				EntryPath:            "index.html",
				ReadScopes:           []string{"org.tasks", "session.summary"},
				DeclaredActions:      []string{"org.task.move-state"},
				Assets:               os.DirFS("examples/plugins/ts-org-kanban"),
			})
			if err != nil {
				return err
			}
			mktUI.MarkInstalled(pluginID, "0.2.0", gen, "HEALTHY")
			win.Invalidate()
			return nil
		}
		return fmt.Errorf("pass -catalog-url/-catalog-dir + -catalog-pub (or run `marlin-mygo-tool build-examples`) to install %s", pluginID)
	}
	mktUI.OnDisable = func(pluginID string) error {
		if err := host.Deactivate(schema.PluginID(pluginID)); err != nil {
			return err
		}
		mktUI.MarkDisabled(pluginID)
		win.Invalidate()
		return nil
	}
	return nil, noopSync
}

func applyOfficialSnapshotToMarketplaceUI(mktUI *mygoshell.MarketplaceController, snap *marketplace.OfficialRegistrySnapshot) {
	if mktUI == nil || snap == nil {
		return
	}
	if len(snap.Themes) > 0 {
		themes := make([]mygoshell.MarketplaceThemeItem, 0, len(snap.Themes))
		for _, th := range snap.Themes {
			themes = append(themes, mygoshell.MarketplaceThemeItem{
				ThemeID:     string(th.Entry.ID),
				Name:        th.Pack.Name,
				Appearance:  th.Pack.Appearance,
				PublisherID: string(th.Entry.PublisherID),
				Summary:     th.Entry.Summary,
				TrustTier:   string(th.Entry.TrustTier),
				Base:        th.Pack.Palette,
			})
		}
		mktUI.SetThemes(themes)
	}
	if len(snap.Layouts) > 0 {
		layouts := make([]mygoshell.MarketplaceLayoutItem, 0, len(snap.Layouts))
		for _, ly := range snap.Layouts {
			layouts = append(layouts, mygoshell.MarketplaceLayoutItem{
				LayoutID:    string(ly.Entry.ID),
				Name:        ly.Entry.Name,
				Perspective: ly.Spec.Perspective,
				PublisherID: string(ly.Entry.PublisherID),
				Summary:     ly.Entry.Summary,
				TrustTier:   string(ly.Entry.TrustTier),
				Spec:        ly.Spec,
			})
		}
		mktUI.SetLayouts(layouts)
	}
}
