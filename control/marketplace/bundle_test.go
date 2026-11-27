// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestPackAndVerifyBundleDeterminismAndTamperResistance(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{"tao3k.official": pub})

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marlin-plugin.toml"), []byte("id = \"tao3k.demo\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>Demo</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("export const ok = true;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var rev [20]byte
	rev[0] = 0xab
	pkg1, att1, err := PackBundle(dir, "tao3k.official", rev, priv)
	if err != nil {
		t.Fatalf("PackBundle failed: %v", err)
	}
	pkg2, _, err := PackBundle(dir, "tao3k.official", rev, priv)
	if err != nil {
		t.Fatalf("second PackBundle failed: %v", err)
	}
	// 1. Byte-for-byte deterministic reproducibility.
	if sha256.Sum256(pkg1) != sha256.Sum256(pkg2) {
		t.Fatal("PackBundle output is not deterministic")
	}
	if len(att1.AssetDigests) != 2 {
		t.Fatalf("expected 2 attested assets, got %+v", att1.AssetDigests)
	}

	// 2. Valid bundle passes verification.
	vb, err := VerifyBundle(pkg1, trust)
	if err != nil {
		t.Fatalf("VerifyBundle rejected valid package: %v", err)
	}
	if string(vb.Assets["index.html"]) != "<h1>Demo</h1>" {
		t.Fatalf("unexpected extracted asset: %q", string(vb.Assets["index.html"]))
	}

	// 3. Untrusted publisher key fails.
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	wrongTrust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{"tao3k.official": otherPub})
	if _, err := VerifyBundle(pkg1, wrongTrust); !errors.Is(err, ErrBundleTampered) {
		t.Fatalf("expected ErrBundleTampered for wrong key, got %v", err)
	}

	// 4. Tampering 1 byte inside assets/index.html after signing MUST fail.
	tampered := tamperArchiveEntry(t, pkg1, "assets/index.html", []byte("<h1>PWNED</h1>"))
	if _, err := VerifyBundle(tampered, trust); !errors.Is(err, ErrBundleTampered) {
		t.Fatalf("expected ErrBundleTampered for tampered asset, got %v", err)
	}
}

func tamperArchiveEntry(t *testing.T, pkg []byte, targetName string, replacement []byte) []byte {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(pkg))
	if err != nil {
		t.Fatal(err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)

	var out bytes.Buffer
	gw := gzip.NewWriter(&out)
	tw := tar.NewWriter(gw)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Name == targetName {
			data = replacement
			hdr.Size = int64(len(data))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = gw.Close()
	return out.Bytes()
}
