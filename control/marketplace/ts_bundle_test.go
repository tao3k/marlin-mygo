// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestTSBunBuildDoctorPublishProducesZeroRuntimeDependencyBundle(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun not in PATH; skipping real bun build TypeScript plugin test")
	}
	ctx := context.Background()
	tsPluginDir := filepath.Join("..", "..", "examples", "plugins", "ts-session-radar")

	// 1. AuditPluginDir (doctor) must compile src/main.ts via bun build and pass CSP + security scan.
	rep, err := marketplace.AuditPluginDir(ctx, tsPluginDir)
	if err != nil || !rep.Passed {
		t.Fatalf("AuditPluginDir failed on ts-session-radar: rep=%+v err=%v", rep, err)
	}
	sawBundleCheck := false
	for _, chk := range rep.Checks {
		if chk.Name == "webview.ts_bundle" && chk.Passed {
			sawBundleCheck = true
		}
	}
	if !sawBundleCheck {
		t.Fatalf("expected webview.ts_bundle check in audit report, got %+v", rep.Checks)
	}

	// 2. PublishPlugin must pack the self-contained browser bundle.js + rewritten index.html (zero .ts files!).
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	outCat := t.TempDir()
	rcpt, err := marketplace.PublishPlugin(ctx, marketplace.PublishConfig{
		PluginDir:       tsPluginDir,
		PublisherID:     "tao3k.official",
		PrivateKey:      priv,
		PublicKey:       pub,
		OutCatalogDir:   outCat,
		ReplaceExisting: true,
	})
	if err != nil {
		t.Fatalf("PublishPlugin on ts-session-radar failed: %v", err)
	}

	rawPkg, err := os.ReadFile(filepath.Join(outCat, rcpt.PackageFile))
	if err != nil {
		t.Fatal(err)
	}
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{"tao3k.official": pub})
	verified, err := marketplace.VerifyBundle(rawPkg, trust)
	if err != nil {
		t.Fatalf("VerifyBundle failed: %v", err)
	}

	bundleJS, ok := verified.Assets["bundle.js"]
	if !ok || len(bundleJS) == 0 {
		t.Fatalf("expected non-empty compiled bundle.js in .marlinpkg assets, got keys: %v", mapKeys(verified.Assets))
	}
	if !strings.Contains(string(bundleJS), "/__marlin/action") || !strings.Contains(string(bundleJS), "tao3k.session-radar") {
		t.Fatalf("compiled bundle.js missing bundled @tao3k/marlin-panel-sdk logic")
	}
	indexHTML := string(verified.Assets["index.html"])
	if !strings.Contains(indexHTML, `src="./bundle.js"`) || strings.Contains(indexHTML, "main.ts") {
		t.Fatalf("staged index.html was not rewritten to ./bundle.js:\n%s", indexHTML)
	}
	for rel := range verified.Assets {
		if strings.HasSuffix(rel, ".ts") || rel == "package.json" {
			t.Fatalf("raw dev-only file %q must not ship inside production .marlinpkg", rel)
		}
	}
}

func mapKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
