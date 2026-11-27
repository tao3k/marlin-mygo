// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

var (
	ErrCatalogRollback      = errors.New("marketplace: TUF rollback detected (catalog metadataVersion < client watermark)")
	ErrCatalogExpired       = errors.New("marketplace: TUF freeze detected (catalog metadata expired)")
	ErrCatalogMixAndMatch   = errors.New("marketplace: TUF mix-and-match detected (snapshot/target version or SHA-256 mismatch)")
	ErrPluginRollback       = errors.New("marketplace: plugin downgrade detected (target version < installed version)")
)

// TUFGuard enforces TUF-style (The Update Framework) metadata security
// invariants over signed Marketplace catalogs and .marlinpkg bundles:
//   - Anti-Rollback: catalog MetadataVersion and per-plugin target version must
//     advance monotonically against client watermarks.
//   - Anti-Freeze: catalog ExpiresAtUnix must be >= current wall clock.
//   - Anti-Mix-and-Match: catalog SnapshotVersion must equal MetadataVersion,
//     and fetched bundle SHA-256 + manifest version must match the catalog entry
//     (and optional lockfile PinnedSHA256).
//   - Publisher Trust-Ring Enforcement: verifies bundle Ed25519 signatures
//     against OfficialReleaseTrust (when officialOnly == true) or full TrustRing.
type TUFGuard struct {
	mu            sync.Mutex
	ring          *TrustRing
	officialOnly  bool
	clientMinVer  uint64
	installedVer  map[schema.PluginID]uint64
	installedHash map[schema.PluginID]string
}

// NewTUFGuard initializes a TUF metadata & bundle verifier pinned at initialMinVer.
func NewTUFGuard(ring *TrustRing, officialOnly bool, initialMinVer uint64) *TUFGuard {
	if initialMinVer == 0 {
		initialMinVer = 1
	}
	return &TUFGuard{
		ring:          ring,
		officialOnly:  officialOnly,
		clientMinVer:  initialMinVer,
		installedVer:  make(map[schema.PluginID]uint64),
		installedHash: make(map[schema.PluginID]string),
	}
}

// SeedInstalled records an already-installed baseline plugin version & digest.
func (g *TUFGuard) SeedInstalled(id schema.PluginID, ver uint64, sha256Hex string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.installedVer[id] = ver
	g.installedHash[id] = strings.ToLower(strings.TrimSpace(sha256Hex))
}

// SignTUFMetadataCatalog serializes and Ed25519-signs a CatalogIndex carrying
// explicit TUF metadataVersion, snapshotVersion, and expiresAtUnix fields.
func SignTUFMetadataCatalog(idx CatalogIndex, privKey ed25519.PrivateKey) ([]byte, []byte, error) {
	if idx.SchemaVersion == 0 {
		idx.SchemaVersion = 1
	}
	if len(privKey) != ed25519.PrivateKeySize {
		return nil, nil, fmt.Errorf("%w: invalid Ed25519 private key", ErrCatalogInvalid)
	}
	raw, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	raw = append(raw, '\n')
	sig := ed25519.Sign(privKey, CatalogSigningBytes(raw))
	return raw, sig, nil
}

// VerifyAndAdmitUpdate authenticates a signed TUF catalog and target .marlinpkg
// bundle fail-closed against anti-rollback, anti-freeze, anti-mix-and-match,
// lockfile SHA-256 pinning, and TrustRing publisher tier requirements.
func (g *TUFGuard) VerifyAndAdmitUpdate(
	now time.Time,
	catalogJSON []byte,
	catalogSig []byte,
	catalogPub ed25519.PublicKey,
	pluginID schema.PluginID,
	targetVersion uint64,
	pkgBytes []byte,
	pinnedSHA256 string,
) (*VerifiedBundle, error) {
	idx, err := VerifySignedCatalog(catalogJSON, catalogSig, catalogPub)
	if err != nil {
		return nil, err
	}
	if idx.MetadataVersion == 0 || idx.ExpiresAtUnix == 0 {
		return nil, fmt.Errorf("%w: missing TUF metadataVersion or expiresAtUnix", ErrCatalogInvalid)
	}
	if idx.ExpiresAtUnix < now.Unix() {
		return nil, fmt.Errorf("%w: expires=%d now=%d", ErrCatalogExpired, idx.ExpiresAtUnix, now.Unix())
	}
	if idx.SnapshotVersion != idx.MetadataVersion {
		return nil, fmt.Errorf("%w: snapshotVersion=%d != metadataVersion=%d", ErrCatalogMixAndMatch, idx.SnapshotVersion, idx.MetadataVersion)
	}

	g.mu.Lock()
	minVer := g.clientMinVer
	curInstalled := g.installedVer[pluginID]
	ring := g.ring
	officialOnly := g.officialOnly
	g.mu.Unlock()

	if idx.MetadataVersion < minVer {
		return nil, fmt.Errorf("%w: catalog=%d < watermark=%d", ErrCatalogRollback, idx.MetadataVersion, minVer)
	}
	if targetVersion < curInstalled {
		return nil, fmt.Errorf("%w: target=%d < installed=%d", ErrPluginRollback, targetVersion, curInstalled)
	}
	if targetVersion != idx.SnapshotVersion {
		return nil, fmt.Errorf("%w: targetVersion=%d != snapshotVersion=%d", ErrCatalogMixAndMatch, targetVersion, idx.SnapshotVersion)
	}

	var matched *CatalogEntry
	for i := range idx.Entries {
		if idx.Entries[i].PluginID == pluginID {
			e := idx.Entries[i]
			matched = &e
			break
		}
	}
	if matched == nil {
		return nil, fmt.Errorf("%w: plugin %q not in signed catalog", ErrPackageNotFound, pluginID)
	}

	sum := sha256.Sum256(pkgBytes)
	gotHex := hex.EncodeToString(sum[:])
	if !strings.EqualFold(gotHex, strings.TrimSpace(matched.PackageSHA256)) {
		return nil, fmt.Errorf("%w: bundle sha256=%s != catalog=%s", ErrCatalogMixAndMatch, gotHex, matched.PackageSHA256)
	}
	if pin := strings.TrimSpace(pinnedSHA256); pin != "" && !strings.EqualFold(gotHex, pin) {
		return nil, fmt.Errorf("%w: bundle sha256=%s != lockfile pin=%s", ErrPinMismatch, gotHex, pin)
	}

	var trust *schema.ReleaseTrust
	if ring != nil {
		if officialOnly {
			trust = ring.OfficialReleaseTrust()
		} else {
			trust = ring.ReleaseTrust()
		}
	}
	vb, err := VerifyBundle(pkgBytes, trust)
	if err != nil {
		if officialOnly && ring != nil && !ring.IsOfficialPublisher(matched.PublisherID) {
			return nil, fmt.Errorf("%w: %v", ErrOfficialPublisherRequired, err)
		}
		return nil, err
	}

	g.mu.Lock()
	if idx.MetadataVersion > g.clientMinVer {
		g.clientMinVer = idx.MetadataVersion
	}
	g.installedVer[pluginID] = targetVersion
	g.installedHash[pluginID] = gotHex
	g.mu.Unlock()

	return vb, nil
}

// State returns current (clientMinVer, installedVer[pluginID], installedHash[pluginID]).
func (g *TUFGuard) State(pluginID schema.PluginID) (minVer, instVer uint64, instHash string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.clientMinVer, g.installedVer[pluginID], g.installedHash[pluginID]
}
