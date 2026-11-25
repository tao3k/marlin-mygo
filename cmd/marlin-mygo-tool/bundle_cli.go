// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package main

import (
	"context"
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

	var rev [20]byte
	exampleDirs := []string{
		filepath.Join("examples", "plugins", "go-graph-inspector-reactor"),
		filepath.Join("examples", "plugins", "go-focus-clock-reactor"),
		filepath.Join("examples", "plugins", "ts-org-kanban"),
		filepath.Join("examples", "plugins", "ts-session-radar"),
		filepath.Join("examples", "plugins", "ts-org-agenda-planner"),
		filepath.Join("examples", "plugins", "react-ai-memory-inbox"),
		filepath.Join("examples", "plugins", "react-ai-workflow-cockpit"),
		filepath.Join("examples", "plugins", "ios-personal-overlay"),
		filepath.Join("examples", "plugins", "declarative-daily-planner-pack"),
	}
	ctx := context.Background()
	var totalEntries int
	for _, dir := range exampleDirs {
		rcpt, err := marketplace.PublishPlugin(ctx, marketplace.PublishConfig{
			PluginDir:       dir,
			PublisherID:     schema.PublisherID(*publisher),
			PrivateKey:      priv,
			PublicKey:       pub,
			SourceRev:       rev,
			OutCatalogDir:   *outDir,
			ReplaceExisting: true,
		})
		if err != nil {
			return fmt.Errorf("publish example %s: %w", dir, err)
		}
		totalEntries = rcpt.CatalogEntries
		fmt.Printf("  - %-30s (%s, %d bytes, %d audit checks)\n", rcpt.PluginID, rcpt.Runtime, rcpt.PackageBytes, rcpt.AuditChecks)
	}
	fmt.Printf("Built, certified & signed %d example .marlinpkg bundles + catalog.json in %s\n", totalEntries, *outDir)
	trustRing := fmt.Sprintf("# SPDX-FileCopyrightText: 2026 tao3k team and Contributors\n#\n# SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later\n\nschema_version = 1\n\n[publishers.%q]\ned25519_pub_hex = %q\n", *publisher, hex.EncodeToString(pub))
	trustRingPath := filepath.Join(*outDir, "trust-ring.toml")
	if err := os.WriteFile(trustRingPath, []byte(trustRing), 0o644); err != nil {
		return fmt.Errorf("write trust-ring.toml: %w", err)
	}
	fmt.Printf("Emitted official trust ring: %s\n", trustRingPath)
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

func runE2ERust(args []string) error {
	fs := flag.NewFlagSet("e2e-rust", flag.ContinueOnError)
	coreDir := fs.String("core", "../..", "path to marlin-agent-core checkout")
	binPath := fs.String("bin", "", "optional pre-built marlin-mygo-host binary path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	hostBin := *binPath
	if hostBin != "" {
		absBin, err := filepath.Abs(hostBin)
		if err != nil {
			return err
		}
		hostBin = absBin
	} else {
		absCore, err := filepath.Abs(*coreDir)
		if err != nil {
			return err
		}
		fmt.Printf("[1/2] Building Rust marlin-mygo-host (with turso-host) in %s...\n", absCore)
		build := exec.Command("cargo", "build", "--locked", "-p", "marlin-agent-app-server", "--features", "turso-host", "--bin", "marlin-mygo-host")
		build.Dir = absCore
		build.Stdout = os.Stdout
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			return fmt.Errorf("cargo build marlin-mygo-host failed: %w", err)
		}
		hostBin = filepath.Join(absCore, "target", "debug", "marlin-mygo-host")
	}
	fmt.Printf("[2/2] Running real cross-process Rust <-> Go E2E suite against %s...\n", hostBin)
	testCmd := exec.Command("go", "test", "-v", "-race", "-run", "Test(ManagedSidecarLaunchesRealRustBridge|GrandUnifiedFullStackRealIntegration|RealDurableSessionStreamingToMyGoTranscript|RealTursoCrashRestartRecoveryAndCancellationReceipt|RealSIGKILLAutoRestartAndTursoSelfHealing)", "./control/grpcwire")
	testCmd.Env = append(os.Environ(), "CGO_ENABLED=0", "MARLIN_RUST_SIDECAR_BIN="+hostBin)
	testCmd.Stdout = os.Stdout
	testCmd.Stderr = os.Stderr
	return testCmd.Run()
}
