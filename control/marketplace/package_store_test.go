// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageStoreRefreshInstallUpdateRemoveLifecycle(t *testing.T) {
	// Build a minimal valid local Official Marketplace mirror (plugins + themes + layouts).
	repoDir := t.TempDir()
	uiDir := t.TempDir()
	stateFile := filepath.Join(uiDir, "packages-state.json")

	if err := os.MkdirAll(filepath.Join(repoDir, "themes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoDir, "layouts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoDir, "plugins", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	pluginTOML := `schema_version = 1
id = "tao3k.demo-plugin"
name = "Demo Plugin"
version = "1.0.0"
publisher_id = "tao3k.official"
runtime = "declarative"
slot_id = "workbench.main"
`
	if err := os.WriteFile(filepath.Join(repoDir, "plugins", "demo", "marlin-plugin.toml"), []byte(pluginTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	themeTOMLv1 := `schema_version = 1
id = "tao3k.theme-aurora"
name = "Aurora Glass"
appearance = "dark"

[palette]
canvas = "#111622"
bar = "#182032"
ink = "#eef2ff"
accent = "#7dd3fc"
info = "#60a5fa"
success = "#4ade80"
warning = "#fbbf24"
muted = "#94a3b8"
`
	themePath := filepath.Join(repoDir, "themes", "aurora.toml")
	if err := os.WriteFile(themePath, []byte(themeTOMLv1), 0o644); err != nil {
		t.Fatal(err)
	}
	layoutJSON := `{
  "perspective": "focus",
  "root": { "bufferId": "slot:workbench.main", "title": "Main", "kind": "native-gpu" }
}`
	if err := os.WriteFile(filepath.Join(repoDir, "layouts", "focus.json"), []byte(layoutJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	regJSON := `{
  "schema_version": 1,
  "marketplace_id": "tao3k.marlin-ui-official",
  "canonical_repo": "https://github.com/tao3k/marlin-ui-marketplace",
  "signing_policy": "official-ci-ed25519-only",
  "categories": ["developer-tools"],
  "plugins": [
    {
      "id": "tao3k.demo-plugin",
      "name": "Demo Plugin",
      "category": "developer-tools",
      "runtime": "declarative",
      "slot_id": "workbench.main",
      "trust_tier": "official",
      "publisher_id": "tao3k.official",
      "repository": "https://github.com/tao3k/marlin-mygo",
      "commit_sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "summary": "Official test plugin"
    }
  ],
  "themes": [
    {
      "id": "tao3k.theme-aurora",
      "name": "Aurora Glass",
      "appearance": "dark",
      "trust_tier": "official",
      "publisher_id": "tao3k.official",
      "local_path": "themes/aurora.toml",
      "summary": "Official aurora frosted theme"
    }
  ],
  "layouts": [
    {
      "id": "tao3k.layout-focus",
      "name": "Focus Layout",
      "perspective": "focus",
      "trust_tier": "official",
      "publisher_id": "tao3k.official",
      "local_path": "layouts/focus.json",
      "summary": "Official focus layout"
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(repoDir, "registry.json"), []byte(regJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	syncClient, err := NewOfficialSyncClient(OfficialSyncConfig{LocalRepoDir: repoDir})
	if err != nil {
		t.Fatalf("NewOfficialSyncClient: %v", err)
	}
	store, err := NewPackageStore(PackageStoreConfig{
		SyncClient: syncClient,
		StateFile:  stateFile,
		UIDir:      uiDir,
	})
	if err != nil {
		t.Fatalf("NewPackageStore: %v", err)
	}
	// Pre-seeded offline packages should exist immediately before Refresh.
	if len(store.List("all")) == 0 {
		t.Fatalf("expected built-in offline packages before Refresh")
	}

	// 1. Refresh from Official Marketplace mirror.
	snap, err := store.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(snap.Packages) != 3 {
		t.Fatalf("expected 3 synced packages (theme+plugin+layout), got %d", len(snap.Packages))
	}

	// 2. Reject unknown/unregistered package fail-closed.
	if _, err := store.Install("evil.unregistered-theme"); err == nil {
		t.Fatalf("expected fail-closed rejection for unregistered package")
	}

	// 3. Install theme package and verify governed materialization on disk.
	inst, err := store.Install("tao3k.theme-aurora")
	if err != nil {
		t.Fatalf("Install theme: %v", err)
	}
	if inst.Status != PackageStatusInstalled {
		t.Fatalf("expected installed status, got %s", inst.Status)
	}
	cssBytes, err := os.ReadFile(filepath.Join(uiDir, "themes", "aurora", "style.css"))
	if err != nil || !strings.Contains(string(cssBytes), "--accent: #7dd3fc") {
		t.Fatalf("expected materialized aurora style.css, err=%v", err)
	}

	// 4. Upstream theme updates -> Refresh detects update-available -> Update upgrades cleanly.
	themeTOMLv2 := strings.Replace(themeTOMLv1, "#7dd3fc", "#f472b6", 1)
	if err := os.WriteFile(themePath, []byte(themeTOMLv2), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh v2: %v", err)
	}
	themes := store.List("theme")
	if len(themes) != 1 || themes[0].Status != PackageStatusUpdateAvailable {
		t.Fatalf("expected update-available after upstream theme revision, got %+v", themes)
	}
	updated, err := store.Update("tao3k.theme-aurora")
	if err != nil {
		t.Fatalf("Update theme: %v", err)
	}
	if updated.Status != PackageStatusInstalled {
		t.Fatalf("expected installed status after Update, got %s", updated.Status)
	}
	cssBytes, _ = os.ReadFile(filepath.Join(uiDir, "themes", "aurora", "style.css"))
	if !strings.Contains(string(cssBytes), "--accent: #f472b6") {
		t.Fatalf("expected updated accent #f472b6 in style.css, got %s", string(cssBytes))
	}

	// 5. Persistence across restart: fresh PackageStore restores installed state.
	reloaded, err := NewPackageStore(PackageStoreConfig{
		SyncClient: syncClient,
		StateFile:  stateFile,
		UIDir:      uiDir,
	})
	if err != nil {
		t.Fatalf("reloaded NewPackageStore: %v", err)
	}
	if _, err := reloaded.Refresh(context.Background()); err != nil {
		t.Fatalf("reloaded Refresh: %v", err)
	}
	if got := reloaded.List("theme")[0].Status; got != PackageStatusInstalled {
		t.Fatalf("expected persisted installed state across restart, got %s", got)
	}

	// 6. Remove uninstalls cleanly and deletes materialized theme directory.
	if err := reloaded.Remove("tao3k.theme-aurora"); err != nil {
		t.Fatalf("Remove theme: %v", err)
	}
	if _, err := os.Stat(filepath.Join(uiDir, "themes", "aurora")); !os.IsNotExist(err) {
		t.Fatalf("expected aurora theme dir removed after Remove")
	}
}
