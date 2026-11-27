// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestMarketplaceVerifyAndStageRejectsTamperedAsset(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{
		"tao3k.official": pub,
	}))
	manifest := []byte(`id = "tao3k.graph-inspector"`)
	att := schema.ReleaseAttestation{Publisher: "tao3k.official"}
	sig := ed25519.Sign(priv, schema.SigningBytes(manifest, att))
	var sigArr [ed25519.SignatureSize]byte
	copy(sigArr[:], sig)
	rel := schema.SignedRelease{ManifestTOML: manifest, Attestation: att, Signature: sigArr}

	goodAsset := []byte("<html>ok</html>")
	goodHash := sha256.Sum256(goodAsset)

	if _, err := mgr.VerifyAndStage(
		rel,
		map[schema.ContributionID][32]byte{"panel": goodHash},
		map[schema.ContributionID][]byte{"panel": []byte("<html>tampered</html>")},
	); !errors.Is(err, ErrAssetDigestMismatch) {
		t.Fatalf("expected ErrAssetDigestMismatch, got %v", err)
	}

	staged, err := mgr.VerifyAndStage(
		rel,
		map[schema.ContributionID][32]byte{"panel": goodHash},
		map[schema.ContributionID][]byte{"panel": goodAsset},
	)
	if err != nil {
		t.Fatalf("expected clean staging, got %v", err)
	}
	if string(staged.Assets["panel"]) != "<html>ok</html>" {
		t.Fatalf("unexpected staged bytes: %q", staged.Assets["panel"])
	}
}
