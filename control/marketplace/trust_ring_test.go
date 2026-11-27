// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tao3k/marlin-mygo/control/marketplace"
)

func TestMultiPublisherTrustRingVerifiesOfficialAndCommunityKeysFailClosed(t *testing.T) {
	pubOfficial, privOfficial, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubCommunity, privCommunity, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, privRogue, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	raw := `
schema_version = 1

[publishers."tao3k.official"]
tier = "official"
allow_catalog = true
ed25519_pub = "` + hex.EncodeToString(pubOfficial) + `"

[publishers."acme.community"]
tier = "verified"
ed25519_pub = "` + hex.EncodeToString(pubCommunity) + `"
`
	ring, err := marketplace.ParseTrustRingTOML([]byte(raw))
	if err != nil {
		t.Fatalf("ParseTrustRingTOML failed: %v", err)
	}
	trust := ring.ReleaseTrust()

	catKey, ok := ring.CatalogKey("")
	if !ok || hex.EncodeToString(catKey) != hex.EncodeToString(pubOfficial) {
		t.Fatalf("expected official catalog key, got ok=%t", ok)
	}

	dir := t.TempDir()
	writeDemoPlugin(t, dir, "0.1.0", "workbench.inspector", "#98c379")
	var rev [20]byte

	// Both official and verified community publishers verify cleanly against the ring.
	pkgOff, _, err := marketplace.PackBundle(dir, "tao3k.official", rev, privOfficial)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := marketplace.VerifyBundle(pkgOff, trust); err != nil {
		t.Fatalf("official bundle failed trust ring verify: %v", err)
	}

	pkgComm, _, err := marketplace.PackBundle(dir, "acme.community", rev, privCommunity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := marketplace.VerifyBundle(pkgComm, trust); err != nil {
		t.Fatalf("community bundle failed trust ring verify: %v", err)
	}

	// Untrusted rogue publisher MUST fail closed.
	pkgRogue, _, err := marketplace.PackBundle(dir, "rogue.actor", rev, privRogue)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := marketplace.VerifyBundle(pkgRogue, trust); err == nil {
		t.Fatal("expected rogue publisher bundle to be rejected by trust ring")
	}

	// Invalid tier must fail closed.
	if _, err := marketplace.ParseTrustRingTOML([]byte(`
[publishers."bad.tier"]
tier = "root-admin"
ed25519_pub = "` + hex.EncodeToString(pubOfficial) + `"
`)); !errors.Is(err, marketplace.ErrTrustRingInvalid) {
		t.Fatalf("expected ErrTrustRingInvalid for unknown tier, got %v", err)
	}

	ringPath := filepath.Join(t.TempDir(), "trust-ring.toml")
	if err := os.WriteFile(ringPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := marketplace.LoadTrustRingFile(ringPath)
	if err != nil || len(loaded.Publishers) != 2 {
		t.Fatalf("LoadTrustRingFile failed: loaded=%+v err=%v", loaded, err)
	}
}
