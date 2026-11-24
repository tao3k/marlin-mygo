// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing/fstest"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

const CatalogSigningDomain = "marlin.marketplace.catalog.v1\x00"

var (
	ErrCatalogInvalid       = errors.New("marketplace: invalid or tampered catalog")
	ErrRuntimeDisallowed    = errors.New("marketplace: plugin runtime disallowed on target platform")
	ErrRestrictedSlotDenied = errors.New("marketplace: plugin targets host-restricted slot")
	ErrEntryAssetMissing    = errors.New("marketplace: declared panel entry asset missing from bundle")
)

// CatalogIndex is the deterministic, Ed25519-signed Marketplace registry manifest.
type CatalogIndex struct {
	SchemaVersion  int                `json:"schemaVersion"`
	Publisher      schema.PublisherID `json:"publisher"`
	SourceRevision string             `json:"sourceRevision"`
	Entries        []CatalogEntry     `json:"entries"`
}

// InstalledPanel captures a live, cryptographically verified panel mounted via Host.Activate.
type InstalledPanel struct {
	Manifest       *schema.PluginManifest
	Bundle         *VerifiedBundle
	ContributionID schema.ContributionID
	Instance       pluginhost.Instance
}

// CatalogSigningBytes produces the canonical domain-separated payload covered by
// the catalog publisher's Ed25519 signature.
func CatalogSigningBytes(catalogJSON []byte) []byte {
	digest := sha256.Sum256(catalogJSON)
	out := make([]byte, 0, len(CatalogSigningDomain)+len(digest))
	out = append(out, CatalogSigningDomain...)
	out = append(out, digest[:]...)
	return out
}

// BuildSignedCatalog verifies every .marlinpkg archive in pkgPaths against trust,
// derives its CatalogEntry directly from the authenticated manifest (zero drift),
// serializes deterministic JSON, and signs the digest with privKey.
func BuildSignedCatalog(
	pkgPaths []string,
	catalogPublisher schema.PublisherID,
	sourceRev [20]byte,
	trust *schema.ReleaseTrust,
	privKey ed25519.PrivateKey,
) ([]byte, []byte, CatalogIndex, error) {
	if err := schema.ValidateDotIdentifier(string(catalogPublisher)); err != nil {
		return nil, nil, CatalogIndex{}, fmt.Errorf("%w: publisher %q: %v", ErrCatalogInvalid, catalogPublisher, err)
	}
	if len(privKey) != ed25519.PrivateKeySize {
		return nil, nil, CatalogIndex{}, fmt.Errorf("%w: invalid Ed25519 private key", ErrCatalogInvalid)
	}
	if trust == nil {
		return nil, nil, CatalogIndex{}, fmt.Errorf("%w: nil trust store", ErrCatalogInvalid)
	}

	entries := make([]CatalogEntry, 0, len(pkgPaths))
	for _, p := range pkgPaths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, CatalogIndex{}, fmt.Errorf("read package %s: %w", p, err)
		}
		vb, err := VerifyBundle(raw, trust)
		if err != nil {
			return nil, nil, CatalogIndex{}, fmt.Errorf("verify package %s: %w", p, err)
		}
		manifest, err := schema.ParsePluginManifestTOML(vb.Verified.ManifestTOML)
		if err != nil {
			return nil, nil, CatalogIndex{}, fmt.Errorf("parse manifest %s: %w", p, err)
		}
		entry, err := deriveCatalogEntry(manifest, vb, filepath.Base(p), raw)
		if err != nil {
			return nil, nil, CatalogIndex{}, fmt.Errorf("derive catalog entry %s: %w", p, err)
		}
		entries = append(entries, entry)
	}

	slices.SortFunc(entries, func(a, b CatalogEntry) int {
		if a.PluginID != b.PluginID {
			return strings.Compare(string(a.PluginID), string(b.PluginID))
		}
		return strings.Compare(a.Version, b.Version)
	})

	idx := CatalogIndex{
		SchemaVersion:  1,
		Publisher:      catalogPublisher,
		SourceRevision: hex.EncodeToString(sourceRev[:]),
		Entries:        entries,
	}
	rawJSON, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return nil, nil, CatalogIndex{}, err
	}
	rawJSON = append(rawJSON, '\n')
	sig := ed25519.Sign(privKey, CatalogSigningBytes(rawJSON))
	return rawJSON, sig, idx, nil
}

// VerifySignedCatalog authenticates catalogJSON against sig using the catalog
// publisher's pinned Ed25519 public key in pubKey.
func VerifySignedCatalog(catalogJSON []byte, sig []byte, pubKey ed25519.PublicKey) (*CatalogIndex, error) {
	if len(catalogJSON) == 0 || len(sig) != ed25519.SignatureSize || len(pubKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: invalid catalog payload, signature, or key size", ErrCatalogInvalid)
	}
	if !ed25519.Verify(pubKey, CatalogSigningBytes(catalogJSON), sig) {
		return nil, fmt.Errorf("%w: Ed25519 signature verification failed", ErrCatalogInvalid)
	}
	var idx CatalogIndex
	if err := json.Unmarshal(catalogJSON, &idx); err != nil {
		return nil, fmt.Errorf("%w: decode JSON: %v", ErrCatalogInvalid, err)
	}
	if idx.SchemaVersion != 1 {
		return nil, fmt.Errorf("%w: unsupported schemaVersion %d", ErrCatalogInvalid, idx.SchemaVersion)
	}
	for _, e := range idx.Entries {
		if _, err := schema.ParsePluginID(string(e.PluginID)); err != nil {
			return nil, fmt.Errorf("%w: invalid pluginId %q: %v", ErrCatalogInvalid, e.PluginID, err)
		}
		if e.SlotID != "" && schema.IsRestrictedSlot(e.SlotID) {
			return nil, fmt.Errorf("%w: entry %q targets restricted slot %q", ErrRestrictedSlotDenied, e.PluginID, e.SlotID)
		}
	}
	return &idx, nil
}

// InstallBundle verifies a signed .marlinpkg archive end-to-end, enforces target
// runtime policy (e.g. declarative-only on iOS/Android) and restricted slot
// denial, packages verified assets into an in-memory read-only fs.FS, and mounts
// the panel contribution transactionally via host.Activate.
func (m *Manager) InstallBundle(
	ctx context.Context,
	pkgBytes []byte,
	host *pluginhost.Host,
	target schema.TargetKind,
	activationGen uint64,
) (*InstalledPanel, error) {
	if host == nil {
		return nil, fmt.Errorf("marketplace: nil plugin host")
	}
	vb, err := VerifyBundle(pkgBytes, m.trust)
	if err != nil {
		return nil, err
	}
	manifest, err := schema.ParsePluginManifestTOML(vb.Verified.ManifestTOML)
	if err != nil {
		return nil, err
	}

	var chosenID schema.ContributionID
	var chosenPanel *schema.PanelContribution
	var sortedIDs []schema.ContributionID
	for cid := range manifest.Contributions {
		sortedIDs = append(sortedIDs, cid)
	}
	slices.Sort(sortedIDs)
	for _, cid := range sortedIDs {
		c := manifest.Contributions[cid]
		if c.Panel != nil {
			chosenID = cid
			chosenPanel = c.Panel
			break
		}
	}
	if chosenPanel == nil {
		return nil, fmt.Errorf("%w: plugin %q declares no ui-panel contribution", ErrInvalidBundle, manifest.ID)
	}
	if schema.IsRestrictedSlot(chosenPanel.SlotID) {
		return nil, fmt.Errorf("%w: %q", ErrRestrictedSlotDenied, chosenPanel.SlotID)
	}
	if !schema.RuntimeAllowedOnTarget(chosenPanel.Runtime, target) {
		return nil, fmt.Errorf("%w: runtime %q forbidden on target %q", ErrRuntimeDisallowed, chosenPanel.Runtime, target)
	}
	if chosenPanel.EntryPath != "" {
		if _, ok := vb.Assets[chosenPanel.EntryPath]; !ok {
			return nil, fmt.Errorf("%w: %q not in bundle assets", ErrEntryAssetMissing, chosenPanel.EntryPath)
		}
	}

	memFS := make(fstest.MapFS, len(vb.Assets))
	for rel, data := range vb.Assets {
		cp := make([]byte, len(data))
		copy(cp, data)
		memFS[rel] = &fstest.MapFile{Data: cp, Mode: 0o444}
	}

	scope := pluginhost.ScopedScope{
		PluginID:             manifest.ID,
		ContributionID:       chosenID,
		ActivationGeneration: activationGen,
		Runtime:              chosenPanel.Runtime,
		SlotID:               chosenPanel.SlotID,
		SlotKey:              chosenPanel.SlotKey,
		EntryPath:            chosenPanel.EntryPath,
		ReadScopes:           slices.Clone(chosenPanel.ReadScopes),
		DeclaredActions:      slices.Clone(chosenPanel.DeclaredActions),
		Assets:               memFS,
	}
	inst, err := host.Activate(ctx, scope)
	if err != nil {
		return nil, err
	}
	return &InstalledPanel{
		Manifest:       manifest,
		Bundle:         vb,
		ContributionID: chosenID,
		Instance:       inst,
	}, nil
}

// InstallFromCatalog authenticates a signed catalog.json, verifies the fetched
// .marlinpkg matches the catalog entry's SHA-256 digest, and calls InstallBundle.
func (m *Manager) InstallFromCatalog(
	ctx context.Context,
	catalogJSON []byte,
	catalogSig []byte,
	catalogPubKey ed25519.PublicKey,
	pluginID schema.PluginID,
	fetchPkg func(entry CatalogEntry) ([]byte, error),
	host *pluginhost.Host,
	target schema.TargetKind,
	activationGen uint64,
) (*InstalledPanel, error) {
	idx, err := VerifySignedCatalog(catalogJSON, catalogSig, catalogPubKey)
	if err != nil {
		return nil, err
	}
	if err := m.LoadCatalog(idx.Entries); err != nil {
		return nil, err
	}
	var matched *CatalogEntry
	for i := range idx.Entries {
		if idx.Entries[i].PluginID == pluginID {
			matched = &idx.Entries[i]
			break
		}
	}
	if matched == nil {
		return nil, fmt.Errorf("%w: %s", ErrPackageNotFound, pluginID)
	}
	pkgBytes, err := fetchPkg(*matched)
	if err != nil {
		return nil, err
	}
	gotSum := sha256.Sum256(pkgBytes)
	if matched.PackageSHA256 != "" && hex.EncodeToString(gotSum[:]) != matched.PackageSHA256 {
		return nil, fmt.Errorf("%w: package %q SHA-256 mismatch against signed catalog", ErrBundleTampered, pluginID)
	}
	return m.InstallBundle(ctx, pkgBytes, host, target, activationGen)
}

func deriveCatalogEntry(m *schema.PluginManifest, vb *VerifiedBundle, pkgFile string, pkgBytes []byte) (CatalogEntry, error) {
	rtSet := make(map[schema.PanelRuntimeKind]struct{})
	var slotID schema.SlotID
	var runtimes []schema.PanelRuntimeKind
	for _, c := range m.Contributions {
		if c.Panel != nil {
			if slotID == "" {
				slotID = c.Panel.SlotID
			}
			if _, ok := rtSet[c.Panel.Runtime]; !ok {
				rtSet[c.Panel.Runtime] = struct{}{}
				runtimes = append(runtimes, c.Panel.Runtime)
			}
		}
	}
	slices.Sort(runtimes)

	allTargets := []schema.TargetKind{
		schema.TargetDesktopMacOS,
		schema.TargetDesktopLinux,
		schema.TargetDesktopWindows,
		schema.TargetMobileIOS,
		schema.TargetMobileAndroid,
	}
	var targets []schema.TargetKind
	for _, tk := range allTargets {
		ok := true
		for _, rt := range runtimes {
			if !schema.RuntimeAllowedOnTarget(rt, tk) {
				ok = false
				break
			}
		}
		if ok {
			targets = append(targets, tk)
		}
	}
	pkgSum := sha256.Sum256(pkgBytes)
	return CatalogEntry{
		PluginID:       m.ID,
		Version:        m.Version,
		PublisherID:    vb.Attestation.Publisher,
		Summary:        fmt.Sprintf("%s (%s)", m.ID, slotID),
		SlotID:         slotID,
		Runtimes:       runtimes,
		Targets:        targets,
		PackageFile:    pkgFile,
		PackageSHA256:  hex.EncodeToString(pkgSum[:]),
		PackageBytes:   int64(len(pkgBytes)),
		ManifestDigest: hex.EncodeToString(vb.Verified.Digest[:]),
	}, nil
}
