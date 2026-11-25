// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/tao3k/marlin-mygo/control/marketplace"
)

func TestCanonicalOfficialMarketplaceRegistryJSONValid(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	regPath := filepath.Join(repoRoot, "marketplace", "registry.json")
	m, err := marketplace.LoadRegistryManifest(regPath)
	if err != nil {
		t.Fatalf("LoadRegistryManifest(%s): %v", regPath, err)
	}
	if err := marketplace.ValidateRegistryManifest(m, repoRoot); err != nil {
		t.Fatalf("ValidateRegistryManifest failed on real marketplace/registry.json: %v", err)
	}
	if len(m.Plugins) < 9 {
		t.Fatalf("expected at least 9 canonical plugins in marketplace/registry.json, got %d", len(m.Plugins))
	}
}

func TestRegistryManifestRejectsInsecureCommunitySubmissions(t *testing.T) {
	validBase := func() *marketplace.RegistryManifest {
		return &marketplace.RegistryManifest{
			SchemaVersion: 1,
			MarketplaceID: "tao3k.marlin-official",
			CanonicalRepo: "https://github.com/tao3k/marlin-marketplace",
			Categories:    []string{"developer-tools"},
			Plugins: []marketplace.RegistryPluginEntry{
				{
					ID:          "community.github-ui",
					Name:        "GitHub UI",
					Category:    "developer-tools",
					Runtime:     "webview-untrusted",
					SlotID:      "workbench.main",
					TrustTier:   marketplace.TrustTierOfficial,
					PublisherID: "tao3k.official",
					Repository:  "https://github.com/example/marlin-github-ui",
					CommitSHA:   "0123456789abcdef0123456789abcdef01234567",
					Summary:     "GitHub PR review panel",
				},
			},
		}
	}

	if err := marketplace.ValidateRegistryManifest(validBase(), ""); err != nil {
		t.Fatalf("expected valid external PR submission to pass, got %v", err)
	}

	// 1. Reject non-HTTPS repository URL.
	badHTTP := validBase()
	badHTTP.Plugins[0].Repository = "http://github.com/example/marlin-github-ui"
	if err := marketplace.ValidateRegistryManifest(badHTTP, ""); !errors.Is(err, marketplace.ErrRegistryManifestInvalid) {
		t.Fatalf("expected http:// repo rejected, got %v", err)
	}

	// 2. Reject unpinned floating branch instead of 40-hex commit_sha.
	badSHA := validBase()
	badSHA.Plugins[0].CommitSHA = "main"
	if err := marketplace.ValidateRegistryManifest(badSHA, ""); !errors.Is(err, marketplace.ErrRegistryManifestInvalid) {
		t.Fatalf("expected floating branch rejected, got %v", err)
	}

	// 3. Reject restricted host slot hijack (approval.gate).
	badSlot := validBase()
	badSlot.Plugins[0].SlotID = "approval.gate"
	if err := marketplace.ValidateRegistryManifest(badSlot, ""); !errors.Is(err, marketplace.ErrRegistryManifestInvalid) {
		t.Fatalf("expected restricted slot rejected, got %v", err)
	}

	// 4. Reject duplicate plugin IDs.
	dup := validBase()
	dup.Plugins = append(dup.Plugins, dup.Plugins[0])
	if err := marketplace.ValidateRegistryManifest(dup, ""); !errors.Is(err, marketplace.ErrRegistryManifestInvalid) {
		t.Fatalf("expected duplicate plugin id rejected, got %v", err)
	}
}
