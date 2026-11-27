// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateOfficialRegistryURL(t *testing.T) {
	valid := []string{
		DefaultOfficialRegistryURL,
		"https://raw.githubusercontent.com/tao3k/marlin-ui-marketplace/v1.0.0/registry.json",
		"https://github.com/tao3k/marlin-ui-marketplace/raw/main/registry.json",
	}
	for _, u := range valid {
		if err := ValidateOfficialRegistryURL(u); err != nil {
			t.Fatalf("expected valid official URL %q, got err: %v", u, err)
		}
	}

	rejected := []string{
		"http://raw.githubusercontent.com/tao3k/marlin-ui-marketplace/main/registry.json",
		"https://raw.githubusercontent.com/evil/marlin-ui-marketplace/main/registry.json",
		"https://evil.example.com/tao3k/marlin-ui-marketplace/registry.json",
		"https://user:pass@raw.githubusercontent.com/tao3k/marlin-ui-marketplace/main/registry.json",
		"https://raw.githubusercontent.com/tao3k/marlin-ui-marketplace/../../evil/registry.json",
		"file:///etc/passwd",
	}
	for _, u := range rejected {
		if err := ValidateOfficialRegistryURL(u); !errors.Is(err, ErrOfficialURLRejected) {
			t.Fatalf("expected ErrOfficialURLRejected for %q, got %v", u, err)
		}
	}
}

func TestOfficialSyncClientRemoteTLS3Stream(t *testing.T) {
	const regJSON = `{
  "schema_version": 1,
  "marketplace_id": "tao3k.marlin-ui-official",
  "canonical_repo": "https://github.com/tao3k/marlin-ui-marketplace",
  "signing_policy": "official-ci-ed25519-only",
  "categories": ["developer-tools"],
  "plugins": [
    {
      "id": "tao3k.git-workbench",
      "name": "Git Workbench",
      "category": "developer-tools",
      "runtime": "webview-untrusted",
      "slot_id": "workbench.main",
      "trust_tier": "official",
      "publisher_id": "tao3k.official",
      "repository": "https://github.com/tao3k/marlin-mygo",
      "commit_sha": "0123456789abcdef0123456789abcdef01234567",
      "summary": "Official Git status and hunk staging panel."
    }
  ],
  "themes": [
    {
      "id": "tao3k.theme-nord",
      "name": "Nord Polar",
      "appearance": "dark",
      "trust_tier": "official",
      "publisher_id": "tao3k.official",
      "local_path": "themes/nord.toml",
      "summary": "Arctic Nord Polar Night 8-color semantic palette."
    }
  ],
  "layouts": [
    {
      "id": "tao3k.layout-review",
      "name": "PR Review Split",
      "perspective": "review",
      "trust_tier": "official",
      "publisher_id": "tao3k.official",
      "local_path": "layouts/review.json",
      "summary": "Dual-pane PR diff + AI review transcript."
    }
  ]
}`
	const nordTOML = `schema_version = 1
id = "tao3k.theme-nord"
name = "Nord Polar"
appearance = "dark"

[palette]
canvas = "#2e3440"
bar = "#3b4252"
ink = "#eceff4"
accent = "#88c0d0"
info = "#81a1c1"
success = "#a3be8c"
warning = "#ebcb8b"
muted = "#7b88a1"
`
	const reviewLayoutJSON = `{
  "perspective": "review",
  "root": {
    "axis": "row",
    "ratio": 0.58,
    "first": { "bufferId": "slot:workbench.main", "title": "Git Diff", "kind": "webview" },
    "second": { "bufferId": "builtin:transcript", "title": "AI Reviewer", "kind": "native-gpu" }
  }
}`

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/registry.json":
			_, _ = w.Write([]byte(regJSON))
		case "/themes/nord.toml":
			_, _ = w.Write([]byte(nordTOML))
		case "/layouts/review.json":
			_, _ = w.Write([]byte(reviewLayoutJSON))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Production constructor MUST reject arbitrary TLS hosts.
	if _, err := NewOfficialSyncClient(OfficialSyncConfig{
		RegistryURL: srv.URL + "/registry.json",
		HTTPClient:  srv.Client(),
	}); !errors.Is(err, ErrOfficialURLRejected) {
		t.Fatalf("expected production origin gate to reject %s, got %v", srv.URL, err)
	}

	// Test-enabled HTTPS mirror verifies full 3-stream parsing & asset resolution.
	client, err := NewOfficialSyncClient(OfficialSyncConfig{
		RegistryURL:              srv.URL + "/registry.json",
		HTTPClient:               srv.Client(),
		allowUnsafeOriginForTest: true,
	})
	if err != nil {
		t.Fatalf("NewOfficialSyncClient: %v", err)
	}
	snap, err := client.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	if len(snap.Manifest.Plugins) != 1 || len(snap.Themes) != 1 || len(snap.Layouts) != 1 {
		t.Fatalf("unexpected 3-stream counts: plugins=%d themes=%d layouts=%d",
			len(snap.Manifest.Plugins), len(snap.Themes), len(snap.Layouts))
	}
	th := snap.Themes[0].Pack
	if th.Name != "Nord Polar" || th.Palette.Canvas.R != 0x2e || th.Palette.Accent.B != 0xd0 {
		t.Fatalf("unexpected parsed theme palette: %+v", th)
	}
	ly := snap.Layouts[0].Spec
	if ly.Perspective != "review" || ly.Root == nil || ly.Root.First.BufferID != "slot:workbench.main" {
		t.Fatalf("unexpected parsed layout spec: %+v", ly)
	}
}

func TestOfficialSyncClientRejectsCorruptThemePack(t *testing.T) {
	badTOML := `schema_version = 1
id = "tao3k.theme-bad"
name = "Bad"
appearance = "dark"
[palette]
canvas = "not-a-hex"
`
	if _, err := ParseThemePackTOML([]byte(badTOML)); !errors.Is(err, ErrThemePackInvalid) {
		t.Fatalf("expected ErrThemePackInvalid, got %v", err)
	}

	badLayout := `{"perspective":"broken","root":{"axis":"row","ratio":0.5}}`
	if _, err := ParseLayoutPackJSON([]byte(badLayout)); !errors.Is(err, ErrLayoutPackInvalid) {
		t.Fatalf("expected ErrLayoutPackInvalid, got %v", err)
	}
}

func TestOfficialSyncClientAgainstLocalMarketplaceRepo(t *testing.T) {
	// Validates real sibling tao3k/marlin-ui-marketplace checkout when present locally.
	repoDir := filepath.Join("..", "..", "..", "marlin-ui-marketplace")
	if _, err := os.Stat(filepath.Join(repoDir, "registry.json")); err != nil {
		t.Skipf("sibling marlin-ui-marketplace checkout not present at %s", repoDir)
	}
	client, err := NewOfficialSyncClient(OfficialSyncConfig{LocalRepoDir: repoDir})
	if err != nil {
		t.Fatalf("NewOfficialSyncClient(local): %v", err)
	}
	snap, err := client.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync local marlin-ui-marketplace failed: %v", err)
	}
	if len(snap.Manifest.Plugins) < 9 || len(snap.Themes) < 5 || len(snap.Layouts) < 2 {
		t.Fatalf("expected >=9 plugins, >=5 themes, >=2 layouts; got %d / %d / %d",
			len(snap.Manifest.Plugins), len(snap.Themes), len(snap.Layouts))
	}
}
