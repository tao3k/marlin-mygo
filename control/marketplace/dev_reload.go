// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

// DevReloaderConfig configures the transactional plugin author hot-reload loop.
type DevReloaderConfig struct {
	PluginDir   string
	Host        *pluginhost.Host
	Target      schema.TargetKind
	PublisherID schema.PublisherID
	// Optional post-activation health/smoke check. If VerifyActive returns an
	// error after Host.Activate mounts the new generation, DevReloader
	// automatically rolls back to the last-known-good bundle at gen+1.
	VerifyActive func(ctx context.Context, panel *InstalledPanel) error
}

// ReloadResult summarizes one hot-reload attempt (including automatic rollback).
type ReloadResult struct {
	PluginID         schema.PluginID
	Version          string
	Runtime          schema.PanelRuntimeKind
	SlotID           schema.SlotID
	AttemptedGen     uint64
	ActiveGeneration uint64
	ActiveVersion    string
	Fingerprint      string
	Elapsed          time.Duration
	RolledBack       bool
	PreservedPrior   bool
}

// DevReloader watches a plugin author's working directory, compiles Go WASI
// reactors when needed, packs & signs ephemeral Ed25519 .marlinpkg bundles,
// hot-swaps generations monotonically via pluginhost.Host.Activate, and
// automatically preserves or rolls back to the last-known-good generation if
// validation, compilation, or post-activation verification fails.
type DevReloader struct {
	cfg     DevReloaderConfig
	pubKey  ed25519.PublicKey
	privKey ed25519.PrivateKey
	mgr     *Manager

	mu               sync.Mutex
	nextGen          uint64
	lastFingerprint  string
	lastGoodPkg      []byte
	lastGoodGen      uint64
	lastGoodVersion  string
	lastGoodPluginID schema.PluginID
}

func NewDevReloader(cfg DevReloaderConfig) (*DevReloader, error) {
	if strings.TrimSpace(cfg.PluginDir) == "" {
		return nil, errors.New("marketplace: DevReloaderConfig.PluginDir is required")
	}
	if cfg.Host == nil {
		return nil, errors.New("marketplace: DevReloaderConfig.Host is required")
	}
	if cfg.Target == "" {
		cfg.Target = schema.TargetDesktopMacOS
	}
	if cfg.PublisherID == "" {
		cfg.PublisherID = "dev.local"
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("marketplace: generate dev Ed25519 keypair: %w", err)
	}
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{
		cfg.PublisherID: pub,
	})
	return &DevReloader{
		cfg:     cfg,
		pubKey:  pub,
		privKey: priv,
		mgr:     NewManager(trust),
		nextGen: 1,
	}, nil
}

// ComputeDirFingerprint computes a deterministic SHA-256 digest of all source
// files in dir, ignoring VCS, dependency caches, and compiled .wasm artifacts
// so Go WASI reactor builds never trigger recursive watch loops.
func ComputeDirFingerprint(dir string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	var relPaths []string
	err = filepath.WalkDir(absDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if path != absDir && (name == ".git" || name == "node_modules" || name == "dist" || name == ".build" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".marlinpkg") || strings.HasSuffix(d.Name(), ".wasm") {
			return nil
		}
		rel, err := filepath.Rel(absDir, path)
		if err != nil {
			return err
		}
		relPaths = append(relPaths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	slices.Sort(relPaths)
	h := sha256.New()
	for _, rel := range relPaths {
		h.Write([]byte(rel))
		h.Write([]byte{0})
		data, err := os.ReadFile(filepath.Join(absDir, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(data)
		h.Write(sum[:])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReloadOnce builds (for Go WASI reactors), packs, signs, and transactionally
// activates the plugin at the next monotonic generation. On failure, the prior
// working generation is either preserved untouched or restored via rollback.
func (r *DevReloader) ReloadOnce(ctx context.Context) (ReloadResult, error) {
	start := time.Now()
	fp, err := ComputeDirFingerprint(r.cfg.PluginDir)
	if err != nil {
		return ReloadResult{}, err
	}

	r.mu.Lock()
	gen := r.nextGen
	r.nextGen++
	prevPkg := r.lastGoodPkg
	prevGen := r.lastGoodGen
	prevVer := r.lastGoodVersion
	prevID := r.lastGoodPluginID
	r.mu.Unlock()

	res := ReloadResult{
		AttemptedGen:     gen,
		ActiveGeneration: prevGen,
		ActiveVersion:    prevVer,
		PluginID:         prevID,
		Fingerprint:      fp,
	}

	stageDir, cleanup, err := r.prepareStageDir(ctx)
	if err != nil {
		res.Elapsed = time.Since(start)
		res.PreservedPrior = prevGen > 0
		return res, err
	}
	defer cleanup()

	var rev [20]byte
	if rawFP, decErr := hex.DecodeString(fp); decErr == nil && len(rawFP) >= 20 {
		copy(rev[:], rawFP[:20])
	}
	pkgBytes, _, err := PackBundle(stageDir, r.cfg.PublisherID, rev, r.privKey)
	if err != nil {
		res.Elapsed = time.Since(start)
		res.PreservedPrior = prevGen > 0
		return res, err
	}

	panel, err := r.mgr.InstallBundle(ctx, pkgBytes, r.cfg.Host, r.cfg.Target, gen)
	if err != nil {
		res.Elapsed = time.Since(start)
		res.PreservedPrior = prevGen > 0
		return res, err
	}

	scope := panel.Instance.Scope()
	res.PluginID = panel.Manifest.ID
	res.Version = panel.Manifest.Version
	res.Runtime = scope.Runtime
	res.SlotID = scope.SlotID

	if r.cfg.VerifyActive != nil {
		if verifyErr := r.cfg.VerifyActive(ctx, panel); verifyErr != nil {
			// Post-activation health check failed: transactionally roll back!
			if len(prevPkg) > 0 {
				r.mu.Lock()
				rbGen := r.nextGen
				r.nextGen++
				r.mu.Unlock()
				if _, rbErr := r.mgr.InstallBundle(ctx, prevPkg, r.cfg.Host, r.cfg.Target, rbGen); rbErr == nil {
					r.mu.Lock()
					r.lastGoodGen = rbGen
					r.mu.Unlock()
					res.RolledBack = true
					res.ActiveGeneration = rbGen
					res.ActiveVersion = prevVer
					res.Elapsed = time.Since(start)
					return res, fmt.Errorf("post-activation verify failed (rolled back to v%s gen:%d): %w", prevVer, rbGen, verifyErr)
				}
			}
			_ = r.cfg.Host.Deactivate(panel.Manifest.ID)
			res.ActiveGeneration = 0
			res.ActiveVersion = ""
			res.Elapsed = time.Since(start)
			return res, fmt.Errorf("post-activation verify failed (deactivated): %w", verifyErr)
		}
	}

	r.mu.Lock()
	r.lastFingerprint = fp
	r.lastGoodPkg = pkgBytes
	r.lastGoodGen = gen
	r.lastGoodVersion = panel.Manifest.Version
	r.lastGoodPluginID = panel.Manifest.ID
	r.mu.Unlock()

	res.ActiveGeneration = gen
	res.ActiveVersion = panel.Manifest.Version
	res.Elapsed = time.Since(start)
	return res, nil
}

// Watch performs an initial ReloadOnce and then polls PluginDir every interval,
// triggering a transactional hot-reload whenever source files change until ctx cancels.
func (r *DevReloader) Watch(ctx context.Context, interval time.Duration, onReload func(ReloadResult, error)) error {
	if interval <= 0 {
		interval = 300 * time.Millisecond
	}
	res, err := r.ReloadOnce(ctx)
	if onReload != nil {
		onReload(res, err)
	}
	if err == nil {
		r.mu.Lock()
		r.lastFingerprint = res.Fingerprint
		r.mu.Unlock()
	} else {
		if fp, fpErr := ComputeDirFingerprint(r.cfg.PluginDir); fpErr == nil {
			r.mu.Lock()
			r.lastFingerprint = fp
			r.mu.Unlock()
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			fp, fpErr := ComputeDirFingerprint(r.cfg.PluginDir)
			if fpErr != nil {
				continue
			}
			r.mu.Lock()
			changed := fp != r.lastFingerprint
			if changed {
				r.lastFingerprint = fp
			}
			r.mu.Unlock()
			if !changed {
				continue
			}
			res, err := r.ReloadOnce(ctx)
			if onReload != nil {
				onReload(res, err)
			}
		}
	}
}

func (r *DevReloader) prepareStageDir(ctx context.Context) (string, func(), error) {
	manifestPath := filepath.Join(r.cfg.PluginDir, "marlin-plugin.toml")
	rawManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", func() {}, fmt.Errorf("read marlin-plugin.toml: %w", err)
	}
	parsed, err := schema.ParsePluginManifestTOML(rawManifest)
	if err != nil {
		return "", func() {}, err
	}

	var wasmEntry string
	for _, c := range parsed.Contributions {
		if c.Panel != nil && c.Panel.Runtime == schema.RuntimeWasmUIIR {
			wasmEntry = c.Panel.EntryPath
			break
		}
	}
	if wasmEntry == "" {
		// TS WebView or Declarative plugin: stage directly from PluginDir.
		return r.cfg.PluginDir, func() {}, nil
	}

	// Go WASI Reactor: compile GOOS=wasip1 GOARCH=wasm -buildmode=c-shared into a temp staging dir.
	tmpDir, err := os.MkdirTemp("", "marlin-dev-stage-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	if err := copyFile(manifestPath, filepath.Join(tmpDir, "marlin-plugin.toml")); err != nil {
		cleanup()
		return "", func() {}, err
	}

	wasmOut := filepath.Join(tmpDir, filepath.FromSlash(wasmEntry))
	if err := os.MkdirAll(filepath.Dir(wasmOut), 0o755); err != nil {
		cleanup()
		return "", func() {}, err
	}
	absPluginDir, err := filepath.Abs(r.cfg.PluginDir)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-buildmode=c-shared", "-o", wasmOut, ".")
	cmd.Dir = absPluginDir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("compile Go WASI reactor failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return tmpDir, cleanup, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
