// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestRegistryClientHTTPCASOfflineFallbackAndTamperRejection(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{
		"tao3k.official": pub,
	})

	repoDir := t.TempDir()
	pkgDir := filepath.Join(t.TempDir(), "demo-plugin")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `
id = "tao3k.theme-demo"
version = "0.2.0"
contract_major = 1
requires_features = ["ui-slot-v1"]

[contributions.theme_pack]
kind = "theme-pack"

[contributions.theme_pack.panel]
runtime = "declarative"
slot_id = "workbench.inspector"
cardinality = "single"
entry_path = "theme.toml"
`
	if err := os.WriteFile(filepath.Join(pkgDir, "marlin-plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "theme.toml"), []byte("[theme]\naccent = \"#c678dd\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var rev [20]byte
	pkgBytes, _, err := PackBundle(pkgDir, "tao3k.official", rev, priv)
	if err != nil {
		t.Fatalf("PackBundle failed: %v", err)
	}
	pkgPath := filepath.Join(repoDir, "tao3k.theme-demo-0.2.0.marlinpkg")
	if err := os.WriteFile(pkgPath, pkgBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	catJSON, catSig, _, err := BuildSignedCatalog([]string{pkgPath}, "tao3k.official", rev, trust, priv)
	if err != nil {
		t.Fatalf("BuildSignedCatalog failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "catalog.json"), catJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "catalog.json.sig"), []byte(hex.EncodeToString(catSig)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(ServeCatalogHandler(repoDir))
	cacheDir := filepath.Join(t.TempDir(), "cas-cache")

	// Path-traversal attempt against ServeCatalogHandler must return 404.
	resp, err := http.Get(srv.URL + "/../etc/passwd")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 on traversal, got %d", resp.StatusCode)
	}

	client, err := NewRegistryClient(RegistryConfig{
		BaseURL:       srv.URL,
		CacheDir:      cacheDir,
		CatalogPubKey: pub,
	})
	if err != nil {
		t.Fatalf("NewRegistryClient failed: %v", err)
	}

	ctx := context.Background()
	idx, err := client.RefreshCatalog(ctx)
	if err != nil || len(idx.Entries) != 1 || idx.Entries[0].PluginID != "tao3k.theme-demo" {
		t.Fatalf("RefreshCatalog failed: idx=%+v err=%v", idx, err)
	}

	fetched, err := client.FetchPackage(ctx, idx.Entries[0])
	if err != nil || len(fetched) != len(pkgBytes) {
		t.Fatalf("FetchPackage online failed: len=%d err=%v", len(fetched), err)
	}

	// Shut down remote HTTP server -> verify offline CAS fallback works seamlessly!
	srv.Close()
	offlineIdx, err := client.RefreshCatalog(ctx)
	if err != nil || len(offlineIdx.Entries) != 1 {
		t.Fatalf("offline RefreshCatalog fallback failed: idx=%+v err=%v", offlineIdx, err)
	}
	offlinePkg, err := client.FetchPackage(ctx, offlineIdx.Entries[0])
	if err != nil || len(offlinePkg) != len(pkgBytes) {
		t.Fatalf("offline CAS FetchPackage failed: len=%d err=%v", len(offlinePkg), err)
	}

	// Tampered remote catalog MUST fail closed (never silently fall back to cache!).
	tampered := append([]byte(nil), catJSON...)
	tampered[len(tampered)-2] = ' '
	if err := os.WriteFile(filepath.Join(repoDir, "catalog.json"), tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	srv2 := httptest.NewServer(ServeCatalogHandler(repoDir))
	defer srv2.Close()

	tamperClient, _ := NewRegistryClient(RegistryConfig{
		BaseURL:       srv2.URL,
		CacheDir:      cacheDir,
		CatalogPubKey: pub,
	})
	if _, err := tamperClient.RefreshCatalog(ctx); !errors.Is(err, ErrCatalogInvalid) {
		t.Fatalf("expected fail-closed ErrCatalogInvalid on tampered catalog, got %v", err)
	}
}
