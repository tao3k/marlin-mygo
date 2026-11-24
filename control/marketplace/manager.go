// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package marketplace manages signed plugin release discovery, local CAS
// integrity verification, and transactional hot-install / rollback against
// pkg/pluginhost.
package marketplace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

var (
	ErrAssetDigestMismatch = errors.New("marketplace: asset SHA-256 does not match signed manifest")
	ErrPackageNotFound     = errors.New("marketplace: package not found in registry")
)

// CatalogEntry represents one discoverable item in a signed Marketplace registry index.
type CatalogEntry struct {
	PluginID       schema.PluginID           `json:"pluginId"`
	Version        string                    `json:"version"`
	PublisherID    schema.PublisherID        `json:"publisherId"`
	Summary        string                    `json:"summary"`
	SlotID         schema.SlotID             `json:"slotId,omitempty"`
	Runtimes       []schema.PanelRuntimeKind `json:"runtimes"`
	Targets        []schema.TargetKind       `json:"targets"`
	PackageFile    string                    `json:"packageFile,omitempty"`
	PackageSHA256  string                    `json:"packageSha256,omitempty"`
	PackageBytes   int64                     `json:"packageBytes,omitempty"`
	ManifestDigest string                    `json:"manifestDigest,omitempty"`
	DownloadURL    string                    `json:"downloadUrl,omitempty"`
}

// StagedBundle holds an authenticated release plus hash-verified client asset bytes.
type StagedBundle struct {
	Verified *schema.VerifiedRelease
	Assets   map[schema.ContributionID][]byte
}

// Manager verifies publisher signatures and asset SHA-256 digests before any
// bundle may enter pluginhost.Host.Activate.
type Manager struct {
	trust   *schema.ReleaseTrust
	mu      sync.RWMutex
	catalog map[schema.PluginID]CatalogEntry
	staged  map[string]*StagedBundle // keyed by hex(bundleDigest)
}

func NewManager(trust *schema.ReleaseTrust) *Manager {
	return &Manager{
		trust:   trust,
		catalog: make(map[schema.PluginID]CatalogEntry),
		staged:  make(map[string]*StagedBundle),
	}
}

func (m *Manager) LoadCatalog(entries []CatalogEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range entries {
		if _, err := schema.ParsePluginID(string(e.PluginID)); err != nil {
			return err
		}
		m.catalog[e.PluginID] = e
	}
	return nil
}

func (m *Manager) ListCatalog() []CatalogEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]CatalogEntry, 0, len(m.catalog))
	for _, e := range m.catalog {
		out = append(out, e)
	}
	return out
}

// VerifyAndStage checks the publisher Ed25519 signature over the TOML manifest
// and verifies every client asset's SHA-256 against its signed descriptor.
func (m *Manager) VerifyAndStage(
	rel schema.SignedRelease,
	expectedAssets map[schema.ContributionID][32]byte,
	rawAssets map[schema.ContributionID][]byte,
) (*StagedBundle, error) {
	verified, err := m.trust.Verify(rel)
	if err != nil {
		return nil, err
	}
	verifiedAssets := make(map[schema.ContributionID][]byte, len(expectedAssets))
	for cid, wantHash := range expectedAssets {
		data, ok := rawAssets[cid]
		if !ok {
			return nil, fmt.Errorf("%w: missing asset %q", ErrAssetDigestMismatch, cid)
		}
		got := sha256.Sum256(data)
		if got != wantHash {
			return nil, fmt.Errorf("%w: asset %q digest mismatch", ErrAssetDigestMismatch, cid)
		}
		cp := make([]byte, len(data))
		copy(cp, data)
		verifiedAssets[cid] = cp
	}
	bundle := &StagedBundle{Verified: verified, Assets: verifiedAssets}
	key := hex.EncodeToString(verified.Digest[:])
	m.mu.Lock()
	m.staged[key] = bundle
	m.mu.Unlock()
	return bundle, nil
}
