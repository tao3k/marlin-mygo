// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tao3k/marlin-mygo/control/marketplace"
)

func TestAuditAndPublishCertificationGateBlocksUnsafePluginsAndMergesCatalog(t *testing.T) {
	ctx := context.Background()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Unsafe TS WebView plugin missing CSP and using eval() MUST be blocked by AuditPluginDir!
	badWebDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(badWebDir, "marlin-plugin.toml"), []byte(`
id = "evil.xss-panel"
version = "0.1.0"
contract_major = 1
[contributions.p]
kind = "ui-panel"
[contributions.p.panel]
runtime = "webview-untrusted"
slot_id = "workbench.main"
entry_path = "index.html"
`), 0o644)
	_ = os.WriteFile(filepath.Join(badWebDir, "index.html"), []byte(`<html><body><script>eval("alert(1)")</script></body></html>`), 0o644)

	rep, err := marketplace.AuditPluginDir(ctx, badWebDir)
	if !errors.Is(err, marketplace.ErrAuditFailed) || rep.Passed {
		t.Fatalf("expected AuditPluginDir to reject unsafe WebView plugin, got rep=%+v err=%v", rep, err)
	}

	// 2. Unsafe Declarative plugin containing a .js script MUST fail App Store safety check!
	badDeclDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(badDeclDir, "marlin-plugin.toml"), []byte(`
id = "evil.hidden-js"
version = "0.1.0"
contract_major = 1
[contributions.p]
kind = "theme-pack"
[contributions.p.panel]
runtime = "declarative"
slot_id = "workbench.inspector"
entry_path = "theme.toml"
`), 0o644)
	_ = os.WriteFile(filepath.Join(badDeclDir, "theme.toml"), []byte("[theme]\naccent = \"#fff\"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(badDeclDir, "sneaky.js"), []byte("console.log('nope')"), 0o644)

	if _, err := marketplace.AuditPluginDir(ctx, badDeclDir); !errors.Is(err, marketplace.ErrAuditFailed) {
		t.Fatalf("expected declarative audit to reject sneaky.js, got %v", err)
	}

	// 3. Publish two clean plugins sequentially into the SAME catalog directory -> both merged & signed!
	outCat := t.TempDir()
	goodDecl := t.TempDir()
	writeDemoPlugin(t, goodDecl, "0.1.0", "workbench.inspector", "#c678dd")

	rcpt1, err := marketplace.PublishPlugin(ctx, marketplace.PublishConfig{
		PluginDir:       goodDecl,
		PublisherID:     "tao3k.official",
		PrivateKey:      priv,
		PublicKey:       pub,
		OutCatalogDir:   outCat,
		ReplaceExisting: true,
	})
	if err != nil || rcpt1.CatalogEntries != 1 {
		t.Fatalf("PublishPlugin #1 failed: rcpt=%+v err=%v", rcpt1, err)
	}

	goodWeb := filepath.Join("..", "..", "examples", "plugins", "ts-org-kanban")
	rcpt2, err := marketplace.PublishPlugin(ctx, marketplace.PublishConfig{
		PluginDir:       goodWeb,
		PublisherID:     "tao3k.official",
		PrivateKey:      priv,
		PublicKey:       pub,
		OutCatalogDir:   outCat,
		ReplaceExisting: true,
	})
	if err != nil || rcpt2.CatalogEntries != 2 {
		t.Fatalf("PublishPlugin #2 (merge) failed: rcpt=%+v err=%v", rcpt2, err)
	}

	// Verify the merged catalog is cryptographically valid via RegistryClient!
	regClient, err := marketplace.NewRegistryClient(marketplace.RegistryConfig{
		LocalDir:      outCat,
		CatalogPubKey: pub,
	})
	if err != nil {
		t.Fatal(err)
	}
	idx, err := regClient.RefreshCatalog(ctx)
	if err != nil || len(idx.Entries) != 2 {
		t.Fatalf("expected 2 verified entries in merged published catalog, got idx=%+v err=%v", idx, err)
	}
}
