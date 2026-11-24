// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func runKeygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	outPrefix := fs.String("out", "publisher", "output path prefix (<out>.pub and <out>.key)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*outPrefix), 0o755); err != nil {
		return err
	}
	pubPath := *outPrefix + ".pub"
	privPath := *outPrefix + ".key"
	if err := os.WriteFile(pubPath, []byte(hex.EncodeToString(pub)+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(privPath, []byte(hex.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Printf("Generated Ed25519 publisher keypair: %s (pub), %s (priv)\n", pubPath, privPath)
	return nil
}

func runPack(args []string) error {
	fs := flag.NewFlagSet("pack", flag.ContinueOnError)
	dir := fs.String("dir", "", "plugin directory containing marlin-plugin.toml")
	publisher := fs.String("publisher", "", "publisher identifier (e.g. tao3k.official)")
	keyPath := fs.String("key", "", "path to hex-encoded Ed25519 private key file")
	revHex := fs.String("rev", strings.Repeat("0", 40), "40-char hex Git source revision")
	outPath := fs.String("out", "", "output .marlinpkg archive path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" || *publisher == "" || *keyPath == "" || *outPath == "" {
		return fmt.Errorf("flags -dir, -publisher, -key, and -out are required")
	}
	keyHex, err := os.ReadFile(*keyPath)
	if err != nil {
		return err
	}
	privBytes, err := hex.DecodeString(strings.TrimSpace(string(keyHex)))
	if err != nil || len(privBytes) != ed25519.PrivateKeySize {
		return fmt.Errorf("invalid Ed25519 private key in %s", *keyPath)
	}
	revBytes, err := hex.DecodeString(strings.TrimSpace(*revHex))
	if err != nil || len(revBytes) != 20 {
		return fmt.Errorf("invalid 40-char hex -rev %q", *revHex)
	}
	var rev [20]byte
	copy(rev[:], revBytes)

	pkgBytes, att, err := marketplace.PackBundle(*dir, schema.PublisherID(*publisher), rev, ed25519.PrivateKey(privBytes))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*outPath, pkgBytes, 0o644); err != nil {
		return err
	}
	sum := sha256.Sum256(pkgBytes)
	fmt.Printf("Packed & signed %s (%d bytes, %d assets, sha256=%s)\n", *outPath, len(pkgBytes), len(att.AssetDigests), hex.EncodeToString(sum[:]))
	return nil
}

func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	pkgPath := fs.String("pkg", "", "path to .marlinpkg archive")
	publisher := fs.String("publisher", "", "expected trusted publisher ID")
	pubPath := fs.String("pub", "", "path to hex-encoded Ed25519 public key file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pkgPath == "" || *publisher == "" || *pubPath == "" {
		return fmt.Errorf("flags -pkg, -publisher, and -pub are required")
	}
	pubHex, err := os.ReadFile(*pubPath)
	if err != nil {
		return err
	}
	pubBytes, err := hex.DecodeString(strings.TrimSpace(string(pubHex)))
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid Ed25519 public key in %s", *pubPath)
	}
	pkgBytes, err := os.ReadFile(*pkgPath)
	if err != nil {
		return err
	}
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{
		schema.PublisherID(*publisher): ed25519.PublicKey(pubBytes),
	})
	vb, err := marketplace.VerifyBundle(pkgBytes, trust)
	if err != nil {
		return err
	}
	fmt.Printf("Verified %s: publisher=%s digest=%s assets=%d\n",
		*pkgPath, vb.Attestation.Publisher, hex.EncodeToString(vb.Verified.Digest[:]), len(vb.Assets))
	return nil
}
