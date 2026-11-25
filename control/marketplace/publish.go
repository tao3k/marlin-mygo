// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/wazeroir"
)

const maxAuditTotalBytes int64 = 16 * 1024 * 1024

var (
	ErrAuditFailed = errors.New("marketplace: plugin security certification audit failed")
)

// AuditCheck captures one discrete security or contract check in `doctor`/`publish`.
type AuditCheck struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Details string `json:"details"`
}

// AuditReport summarizes the fail-closed security certification result for one plugin.
type AuditReport struct {
	PluginID  schema.PluginID         `json:"pluginId"`
	Version   string                  `json:"version"`
	Runtime   schema.PanelRuntimeKind `json:"runtime"`
	SlotID    schema.SlotID           `json:"slotId"`
	Passed    bool                    `json:"passed"`
	Checks    []AuditCheck            `json:"checks"`
	WasmBytes []byte                  `json:"-"` // populated when wasm-ui-ir compiled during audit
}

// PublishConfig configures the 1-command security-gated plugin publisher.
type PublishConfig struct {
	PluginDir       string
	PublisherID     schema.PublisherID
	PrivateKey      ed25519.PrivateKey
	PublicKey       ed25519.PublicKey
	SourceRev       [20]byte
	OutCatalogDir   string
	ReplaceExisting bool // removes older .marlinpkg versions of the same PluginID in OutCatalogDir
}

// PublishReceipt records the authenticated publish & catalog merge output.
type PublishReceipt struct {
	PluginID       schema.PluginID         `json:"pluginId"`
	Version        string                  `json:"version"`
	Runtime        schema.PanelRuntimeKind `json:"runtime"`
	SlotID         schema.SlotID           `json:"slotId"`
	PackageFile    string                  `json:"packageFile"`
	PackageSHA256  string                  `json:"packageSha256"`
	PackageBytes   int64                   `json:"packageBytes"`
	CatalogEntries int                     `json:"catalogEntries"`
	AuditChecks    int                     `json:"auditChecks"`
}

// AuditPluginDir executes the fail-closed security certification suite for a
// plugin directory (manifest closure, restricted slot denial, bounded size,
// WebView CSP & anti-eval scan, Declarative zero-executable check, or real
// Go 1.27 wasip1 compile + wazero sandbox smoke test).
func AuditPluginDir(ctx context.Context, pluginDir string) (*AuditReport, error) {
	rep := &AuditReport{Passed: true}
	record := func(name string, ok bool, details string) {
		rep.Checks = append(rep.Checks, AuditCheck{Name: name, Passed: ok, Details: details})
		if !ok {
			rep.Passed = false
		}
	}

	manifestPath := filepath.Join(pluginDir, "marlin-plugin.toml")
	rawManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		record("manifest.exists", false, fmt.Sprintf("cannot read marlin-plugin.toml: %v", err))
		return rep, fmt.Errorf("%w: missing marlin-plugin.toml", ErrAuditFailed)
	}
	manifest, err := schema.ParsePluginManifestTOML(rawManifest)
	if err != nil {
		record("manifest.schema", false, err.Error())
		return rep, fmt.Errorf("%w: %v", ErrAuditFailed, err)
	}
	rep.PluginID = manifest.ID
	rep.Version = manifest.Version
	record("manifest.schema", true, fmt.Sprintf("id=%s version=%s contract=%d", manifest.ID, manifest.Version, manifest.ContractMajor))

	// Find declared ui-panel contribution deterministically.
	var panel *schema.PanelContribution
	var cids []schema.ContributionID
	for cid := range manifest.Contributions {
		cids = append(cids, cid)
	}
	slices.Sort(cids)
	for _, cid := range cids {
		if c := manifest.Contributions[cid]; c.Panel != nil {
			panel = c.Panel
			break
		}
	}
	if panel == nil {
		record("manifest.panel", false, "no [contributions.<id>.panel] declared")
		return rep, fmt.Errorf("%w: no panel contribution", ErrAuditFailed)
	}
	rep.Runtime = panel.Runtime
	rep.SlotID = panel.SlotID
	record("slot.isolation", !schema.IsRestrictedSlot(panel.SlotID), fmt.Sprintf("slot=%s runtime=%s", panel.SlotID, panel.Runtime))

	// Check total directory size & walk files for runtime-specific security rules.
	var totalBytes int64
	var files []string
	absDir, err := filepath.Abs(pluginDir)
	if err != nil {
		return rep, err
	}
	err = filepath.WalkDir(absDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != absDir && (d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "dist" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		totalBytes += info.Size()
		rel, _ := filepath.Rel(absDir, path)
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return rep, err
	}
	record("bundle.size", totalBytes <= maxAuditTotalBytes, fmt.Sprintf("%d bytes (limit %d)", totalBytes, maxAuditTotalBytes))

	switch panel.Runtime {
	case schema.RuntimeWebViewUntrusted:
		auditWebViewPlugin(absDir, panel, files, record)
	case schema.RuntimeDeclarative:
		auditDeclarativePlugin(absDir, panel, files, record)
	case schema.RuntimeWasmUIIR:
		wasmBytes := auditWasmReactorPlugin(ctx, absDir, panel, record)
		rep.WasmBytes = wasmBytes
	default:
		record("runtime.supported", false, fmt.Sprintf("unsupported runtime %q", panel.Runtime))
	}

	if !rep.Passed {
		return rep, fmt.Errorf("%w: %s failed security audit", ErrAuditFailed, rep.PluginID)
	}
	return rep, nil
}

func auditWebViewPlugin(absDir string, panel *schema.PanelContribution, files []string, record func(string, bool, string)) {
	entry := panel.EntryPath
	if entry == "" {
		entry = "index.html"
	}
	entryRaw, err := os.ReadFile(filepath.Join(absDir, filepath.FromSlash(entry)))
	if err != nil {
		record("webview.entry", false, fmt.Sprintf("missing entry %q", entry))
		return
	}
	record("webview.entry", true, entry)

	htmlLower := strings.ToLower(string(entryRaw))
	hasCSP := strings.Contains(htmlLower, "content-security-policy")
	noUnsafeEval := !strings.Contains(htmlLower, "unsafe-eval")
	noRemoteScript := !containsRemoteScriptSrc(htmlLower)
	record("webview.csp", hasCSP && noUnsafeEval && noRemoteScript,
		fmt.Sprintf("csp=%t no_unsafe_eval=%t no_remote_script=%t", hasCSP, noUnsafeEval, noRemoteScript))

	// Scan JS/TS/HTML sources for forbidden dynamic code execution or host-bypass patterns.
	forbidden := []string{"eval(", "new Function(", "MARLIN_BRIDGE_TOKEN", "mygo.Bind("}
	var violation string
	for _, rel := range files {
		lower := strings.ToLower(rel)
		if !strings.HasSuffix(lower, ".html") && !strings.HasSuffix(lower, ".js") && !strings.HasSuffix(lower, ".ts") && !strings.HasSuffix(lower, ".mjs") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(absDir, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		text := string(raw)
		for _, tok := range forbidden {
			if strings.Contains(text, tok) {
				violation = fmt.Sprintf("%s contains forbidden token %q", rel, tok)
				break
			}
		}
		if violation != "" {
			break
		}
	}
	if violation != "" {
		record("webview.static_security", false, violation)
	} else {
		record("webview.static_security", true, "zero eval/Function/host-bypass patterns")
	}
}

func containsRemoteScriptSrc(htmlLower string) bool {
	return strings.Contains(htmlLower, `<script src="http://`) ||
		strings.Contains(htmlLower, `<script src="https://`) ||
		strings.Contains(htmlLower, `<script src='http://`) ||
		strings.Contains(htmlLower, `<script src='https://`)
}

func auditDeclarativePlugin(absDir string, panel *schema.PanelContribution, files []string, record func(string, bool, string)) {
	if panel.EntryPath != "" {
		_, err := os.Stat(filepath.Join(absDir, filepath.FromSlash(panel.EntryPath)))
		record("declarative.entry", err == nil, panel.EntryPath)
	}
	forbiddenExt := []string{".wasm", ".js", ".mjs", ".cjs", ".ts", ".so", ".dylib", ".dll", ".exe", ".sh"}
	var badFile string
	for _, rel := range files {
		ext := strings.ToLower(filepath.Ext(rel))
		if slices.Contains(forbiddenExt, ext) {
			badFile = rel
			break
		}
	}
	if badFile != "" {
		record("declarative.appstore_safe", false, fmt.Sprintf("executable/script asset %q forbidden in declarative pack", badFile))
	} else {
		record("declarative.appstore_safe", true, "zero executable/script assets (App Store compliant)")
	}
}

func auditWasmReactorPlugin(ctx context.Context, absDir string, panel *schema.PanelContribution, record func(string, bool, string)) []byte {
	tmpDir, err := os.MkdirTemp("", "marlin-audit-wasm-*")
	if err != nil {
		record("wasm.build", false, err.Error())
		return nil
	}
	defer os.RemoveAll(tmpDir)

	wasmOut := filepath.Join(tmpDir, "panel.wasm")
	build := exec.CommandContext(ctx, "go", "build", "-buildmode=c-shared", "-o", wasmOut, ".")
	build.Dir = absDir
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		record("wasm.build", false, fmt.Sprintf("GOOS=wasip1 build failed: %v (%s)", err, strings.TrimSpace(string(out))))
		return nil
	}
	wasmBytes, err := os.ReadFile(wasmOut)
	if err != nil {
		record("wasm.build", false, err.Error())
		return nil
	}
	record("wasm.build", true, fmt.Sprintf("compiled %d bytes (GOOS=wasip1)", len(wasmBytes)))

	// Smoke-instantiate in a real wazero sandbox with memory limits & verify render export!
	eng, err := wazeroir.NewModuleEngine(ctx)
	if err != nil {
		record("wasm.sandbox_smoke", false, err.Error())
		return nil
	}
	defer eng.Close(ctx)

	resident, err := eng.InstantiateResident(ctx, wasmBytes)
	if err != nil {
		record("wasm.sandbox_smoke", false, fmt.Sprintf("wazero instantiate failed: %v", err))
		return nil
	}
	defer resident.Close(ctx)

	if tree, err := resident.Render(ctx); err != nil {
		record("wasm.sandbox_smoke", false, fmt.Sprintf("marlin_ui_render failed: %v", err))
		return nil
	} else {
		record("wasm.sandbox_smoke", true, fmt.Sprintf("wazero reactor verified (root kind=%s)", tree.Kind))
	}
	return wasmBytes
}

// PublishPlugin executes AuditPluginDir fail-closed, packs & Ed25519-signs the
// deterministic .marlinpkg archive, independently verifies it, writes it into
// OutCatalogDir, and atomically updates + re-signs catalog.json and catalog.json.sig.
func PublishPlugin(ctx context.Context, cfg PublishConfig) (*PublishReceipt, error) {
	if cfg.PluginDir == "" || cfg.OutCatalogDir == "" || cfg.PublisherID == "" {
		return nil, errors.New("marketplace: PluginDir, PublisherID, and OutCatalogDir are required")
	}
	if len(cfg.PrivateKey) != ed25519.PrivateKeySize || len(cfg.PublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("marketplace: valid Ed25519 keypair is required for publish")
	}

	audit, err := AuditPluginDir(ctx, cfg.PluginDir)
	if err != nil {
		return nil, err
	}

	stageDir := cfg.PluginDir
	if audit.Runtime == schema.RuntimeWasmUIIR {
		tmpStage, err := os.MkdirTemp("", "marlin-publish-stage-*")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmpStage)
		if err := copyFile(filepath.Join(cfg.PluginDir, "marlin-plugin.toml"), filepath.Join(tmpStage, "marlin-plugin.toml")); err != nil {
			return nil, err
		}
		manifest, _ := schema.ParsePluginManifestTOML(mustRead(filepath.Join(cfg.PluginDir, "marlin-plugin.toml")))
		entry := "panel.wasm"
		for _, c := range manifest.Contributions {
			if c.Panel != nil && c.Panel.EntryPath != "" {
				entry = c.Panel.EntryPath
				break
			}
		}
		wasmDest := filepath.Join(tmpStage, filepath.FromSlash(entry))
		if err := os.MkdirAll(filepath.Dir(wasmDest), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(wasmDest, audit.WasmBytes, 0o644); err != nil {
			return nil, err
		}
		stageDir = tmpStage
	}

	pkgBytes, _, err := PackBundle(stageDir, cfg.PublisherID, cfg.SourceRev, cfg.PrivateKey)
	if err != nil {
		return nil, err
	}
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{
		cfg.PublisherID: cfg.PublicKey,
	})
	if _, err := VerifyBundle(pkgBytes, trust); err != nil {
		return nil, fmt.Errorf("post-pack verification failed: %w", err)
	}

	if err := os.MkdirAll(cfg.OutCatalogDir, 0o755); err != nil {
		return nil, err
	}
	pkgFile := fmt.Sprintf("%s-%s.marlinpkg", audit.PluginID, audit.Version)
	pkgDest := filepath.Join(cfg.OutCatalogDir, pkgFile)

	if cfg.ReplaceExisting {
		existing, _ := filepath.Glob(filepath.Join(cfg.OutCatalogDir, string(audit.PluginID)+"-*.marlinpkg"))
		for _, old := range existing {
			if filepath.Base(old) != pkgFile {
				_ = os.Remove(old)
			}
		}
	}
	if err := atomicWriteFile(pkgDest, pkgBytes, 0o644); err != nil {
		return nil, err
	}

	allPkgs, err := filepath.Glob(filepath.Join(cfg.OutCatalogDir, "*.marlinpkg"))
	if err != nil || len(allPkgs) == 0 {
		return nil, fmt.Errorf("no packages found in %s after publish", cfg.OutCatalogDir)
	}
	catJSON, catSig, idx, err := BuildSignedCatalog(allPkgs, cfg.PublisherID, cfg.SourceRev, trust, cfg.PrivateKey)
	if err != nil {
		return nil, err
	}
	if err := atomicWriteFile(filepath.Join(cfg.OutCatalogDir, "catalog.json"), catJSON, 0o644); err != nil {
		return nil, err
	}
	if err := atomicWriteFile(filepath.Join(cfg.OutCatalogDir, "catalog.json.sig"), []byte(hex.EncodeToString(catSig)+"\n"), 0o644); err != nil {
		return nil, err
	}

	sum := sha256.Sum256(pkgBytes)
	return &PublishReceipt{
		PluginID:       audit.PluginID,
		Version:        audit.Version,
		Runtime:        audit.Runtime,
		SlotID:         audit.SlotID,
		PackageFile:    pkgFile,
		PackageSHA256:  hex.EncodeToString(sum[:]),
		PackageBytes:   int64(len(pkgBytes)),
		CatalogEntries: len(idx.Entries),
		AuditChecks:    len(audit.Checks),
	}, nil
}

func mustRead(path string) []byte {
	b, _ := os.ReadFile(path)
	return b
}
