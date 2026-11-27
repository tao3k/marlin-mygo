// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package schema

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

func TestIdentifierValidation(t *testing.T) {
	if _, err := ParsePluginID("tao3k.org-kanban"); err != nil {
		t.Fatalf("expected valid plugin id, got %v", err)
	}
	if _, err := ParsePluginID("unnamespaced"); err == nil {
		t.Fatal("expected error for unnamespaced plugin id")
	}
	if _, err := ParsePluginID("tao3k..bad"); err == nil {
		t.Fatal("expected error for empty segment")
	}
	if _, err := ParseSubmissionID("send_2026-11-24_01"); err != nil {
		t.Fatalf("expected valid submission id, got %v", err)
	}
	if _, err := ParseSubmissionID("bad.dot"); err == nil {
		t.Fatal("expected submission id to reject dots")
	}
}

func TestReleaseTrustVerifyAndTamperRejection(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := NewReleaseTrust(map[PublisherID]ed25519.PublicKey{
		"tao3k.official": pub,
	})
	manifestBytes := []byte(`id = "tao3k.org-kanban"`)
	att := ReleaseAttestation{Publisher: "tao3k.official"}
	sig := ed25519.Sign(priv, SigningBytes(manifestBytes, att))
	var sigArr [ed25519.SignatureSize]byte
	copy(sigArr[:], sig)

	verified, err := trust.Verify(SignedRelease{
		ManifestTOML: manifestBytes,
		Attestation:  att,
		Signature:    sigArr,
	})
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if verified.Digest == ([32]byte{}) {
		t.Fatal("expected non-zero SHA-256 digest")
	}

	tampered := SignedRelease{
		ManifestTOML: []byte(`id = "tao3k.evil"`),
		Attestation:  att,
		Signature:    sigArr,
	}
	if _, err := trust.Verify(tampered); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature, got %v", err)
	}
}

func TestUIElementBoundsAndThemeProjection(t *testing.T) {
	tree := UIElement{
		Kind: ElemRows,
		Children: []UIElement{
			{Kind: ElemSlot, SlotID: "workbench.main"},
			{Kind: ElemText, Spans: []UISpan{{Text: "ready", Tone: ToneSuccess}}},
		},
	}
	if err := tree.ValidateBounds(10, 4); err != nil {
		t.Fatalf("unexpected bounds error: %v", err)
	}
	if err := tree.ValidateBounds(2, 4); !errors.Is(err, ErrUITreeBounds) {
		t.Fatalf("expected ErrUITreeBounds on node cap, got %v", err)
	}
	vars := DefaultUITheme().CSSVariables()
	if got := vars["--marlin-accent"]; got != "#c678dd" {
		t.Fatalf("unexpected accent hex %q", got)
	}
}
