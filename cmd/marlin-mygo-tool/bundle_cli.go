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
	"os/exec"
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

func runCatalog(args []string) error {
	fs := flag.NewFlagSet("catalog", flag.ContinueOnError)
	dir := fs.String("dir", "", "directory containing signed .marlinpkg archives")
	publisher := fs.String("publisher", "", "trusted publisher identifier (e.g. tao3k.official)")
	keyPath := fs.String("key", "", "path to hex-encoded Ed25519 private key")
	pubPath := fs.String("pub", "", "path to hex-encoded Ed25519 public key")
	revHex := fs.String("rev", strings.Repeat("0", 40), "40-char hex Git source revision")
	outPath := fs.String("out", "catalog.json", "output path for catalog.json (signature written to <out>.sig)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" || *publisher == "" || *keyPath == "" || *pubPath == "" {
		return fmt.Errorf("flags -dir, -publisher, -key, and -pub are required")
	}
	privBytes, err := readHexKey(*keyPath, ed25519.PrivateKeySize)
	if err != nil {
		return err
	}
	pubBytes, err := readHexKey(*pubPath, ed25519.PublicKeySize)
	if err != nil {
		return err
	}
	revBytes, err := hex.DecodeString(strings.TrimSpace(*revHex))
	if err != nil || len(revBytes) != 20 {
		return fmt.Errorf("invalid 40-char hex -rev %q", *revHex)
	}
	var rev [20]byte
	copy(rev[:], revBytes)

	pkgPaths, err := filepath.Glob(filepath.Join(*dir, "*.marlinpkg"))
	if err != nil || len(pkgPaths) == 0 {
		return fmt.Errorf("no .marlinpkg archives found in %s", *dir)
	}
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{
		schema.PublisherID(*publisher): ed25519.PublicKey(pubBytes),
	})
	rawJSON, sig, idx, err := marketplace.BuildSignedCatalog(pkgPaths, schema.PublisherID(*publisher), rev, trust, ed25519.PrivateKey(privBytes))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*outPath, rawJSON, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(*outPath+".sig", []byte(hex.EncodeToString(sig)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("Signed Marketplace catalog %s (%d entries, sig=%s.sig)\n", *outPath, len(idx.Entries), *outPath)
	return nil
}

func runBuildExamples(args []string) error {
	fs := flag.NewFlagSet("build-examples", flag.ContinueOnError)
	outDir := fs.String("out", "./dist/marketplace", "output directory for signed .marlinpkg bundles and catalog.json")
	publisher := fs.String("publisher", "tao3k.official", "publisher identifier")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	pubFile := filepath.Join(*outDir, "publisher.pub")
	if err := os.WriteFile(pubFile, []byte(hex.EncodeToString(pub)+"\n"), 0o644); err != nil {
		return err
	}

	// Stage go-graph-inspector-reactor with freshly compiled GOOS=wasip1 panel.wasm.
	tmpReactor, err := os.MkdirTemp("", "marlin-reactor-stage-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpReactor)

	reactorManifest, err := os.ReadFile(filepath.Join("examples", "plugins", "go-graph-inspector-reactor", "marlin-plugin.toml"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmpReactor, "marlin-plugin.toml"), reactorManifest, 0o644); err != nil {
		return err
	}
	wasmOut := filepath.Join(tmpReactor, "panel.wasm")
	buildCmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmOut, "./examples/plugins/go-graph-inspector-reactor")
	buildCmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("compile reactor wasm: %w\n%s", err, string(out))
	}

	var rev [20]byte
	bundles := []struct {
		dir  string
		name string
	}{
		{dir: tmpReactor, name: "tao3k.graph-inspector-0.2.0.marlinpkg"},
		{dir: filepath.Join("examples", "plugins", "ts-org-kanban"), name: "tao3k.org-kanban-0.2.0.marlinpkg"},
		{dir: filepath.Join("examples", "plugins", "ios-personal-overlay"), name: "tao3k.ios-personal-capture-0.2.0.marlinpkg"},
	}
	var pkgPaths []string
	for _, b := range bundles {
		pkgBytes, _, err := marketplace.PackBundle(b.dir, schema.PublisherID(*publisher), rev, priv)
		if err != nil {
			return fmt.Errorf("pack %s: %w", b.name, err)
		}
		dest := filepath.Join(*outDir, b.name)
		if err := os.WriteFile(dest, pkgBytes, 0o644); err != nil {
			return err
		}
		pkgPaths = append(pkgPaths, dest)
	}
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{
		schema.PublisherID(*publisher): pub,
	})
	rawJSON, sig, idx, err := marketplace.BuildSignedCatalog(pkgPaths, schema.PublisherID(*publisher), rev, trust, priv)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*outDir, "catalog.json"), rawJSON, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*outDir, "catalog.json.sig"), []byte(hex.EncodeToString(sig)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("Built & signed %d example .marlinpkg bundles + catalog.json in %s\n", len(idx.Entries), *outDir)
	return nil
}

func readHexKey(path string, wantLen int) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(decoded) != wantLen {
		return nil, fmt.Errorf("invalid %d-byte hex key in %s", wantLen, path)
	}
	return decoded, nil
}
