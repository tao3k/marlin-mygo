// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package schema

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
)

// ReleaseSigningDomain binds Ed25519 signatures to the Marlin v1 unified
// release envelope format (extending marlin.workflow-only.release.v0).
const ReleaseSigningDomain = "marlin.unified.release.v1\x00"

// ReleaseAttestation mirrors marlin.plugin.v1.ReleaseAttestation and
// crates/marlin-agent-runtime/src/plugin/release.rs::ReleaseAttestation.
type ReleaseAttestation struct {
	Publisher           PublisherID
	SourceRevision      [20]byte
	BuildEvidenceDigest [32]byte
}

// SignedRelease carries the exact raw TOML bytes covered by Signature.
type SignedRelease struct {
	ManifestTOML []byte
	Attestation  ReleaseAttestation
	Signature    [ed25519.SignatureSize]byte
}

// CapabilityDecl describes one requested host, projection, or device capability.
type CapabilityDecl struct {
	Kind     string     `json:"kind"`
	Path     string     `json:"path,omitempty"`
	Surface  string     `json:"surface,omitempty"`
	Target   TargetKind `json:"target,omitempty"`
	Protocol string     `json:"protocol,omitempty"`
	Optional bool       `json:"optional,omitempty"`
}

// PanelContribution describes a client-side hot-pluggable UI slot asset.
type PanelContribution struct {
	Runtime         PanelRuntimeKind `json:"runtime"`
	SlotID          SlotID           `json:"slotId"`
	Cardinality     SlotCardinality  `json:"cardinality"`
	SlotKey         string           `json:"slotKey,omitempty"`
	EntryPath       string           `json:"entryPath,omitempty"`
	ArchiveSHA256   [32]byte         `json:"archiveSha256"`
	ByteSize        uint64           `json:"byteSize,string"`
	ReadScopes      []string         `json:"readScopes,omitempty"`
	DeclaredActions []string         `json:"declaredActions,omitempty"`
}

// ContributionDecl represents either a host workflow preset or a client UI panel.
type ContributionDecl struct {
	Kind           string             `json:"kind"` // "workflow-preset" | "ui-panel" | "theme-pack"
	PolicyModule   PolicyModuleID     `json:"policyModule,omitempty"`
	SettingsSchema SchemaID           `json:"settingsSchema,omitempty"`
	Requires       []CapabilityID     `json:"requires,omitempty"`
	Panel          *PanelContribution `json:"panel,omitempty"`
}

// PluginManifest is the validated, bounded declaration decoded from marlin-plugin.toml.
type PluginManifest struct {
	ID               PluginID                            `json:"id"`
	Version          string                              `json:"version"`
	ContractMajor    uint16                              `json:"contractMajor"`
	RequiresFeatures []string                            `json:"requiresFeatures"`
	Capabilities     map[CapabilityID]CapabilityDecl     `json:"capabilities"`
	Contributions    map[ContributionID]ContributionDecl `json:"contributions"`
}

// Validate enforces structural bounds and capability reference closure.
func (m *PluginManifest) Validate() error {
	if _, err := ParsePluginID(string(m.ID)); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("%w: empty version", ErrInvalidManifest)
	}
	if m.ContractMajor > 1 {
		return fmt.Errorf("%w: unsupported contract_major %d", ErrInvalidManifest, m.ContractMajor)
	}
	if len(m.Capabilities) > MaxCapabilities {
		return fmt.Errorf("%w: too many capabilities (%d > %d)", ErrInvalidManifest, len(m.Capabilities), MaxCapabilities)
	}
	if len(m.Contributions) == 0 || len(m.Contributions) > MaxContributions {
		return fmt.Errorf("%w: invalid contribution count %d", ErrInvalidManifest, len(m.Contributions))
	}
	for capID := range m.Capabilities {
		if err := ValidateDotIdentifier(string(capID)); err != nil {
			return fmt.Errorf("%w: capability %q: %v", ErrInvalidManifest, capID, err)
		}
	}
	for contribID, contrib := range m.Contributions {
		if err := ValidateDotIdentifier(string(contribID)); err != nil {
			return fmt.Errorf("%w: contribution %q: %v", ErrInvalidManifest, contribID, err)
		}
		seen := make(map[CapabilityID]struct{}, len(contrib.Requires))
		for _, req := range contrib.Requires {
			if _, ok := m.Capabilities[req]; !ok {
				return fmt.Errorf("%w: contribution %q requires undeclared capability %q", ErrInvalidManifest, contribID, req)
			}
			if _, dup := seen[req]; dup {
				return fmt.Errorf("%w: contribution %q duplicates capability %q", ErrInvalidManifest, contribID, req)
			}
			seen[req] = struct{}{}
		}
		if contrib.Kind == "ui-panel" {
			if contrib.Panel == nil {
				return fmt.Errorf("%w: ui-panel %q missing panel config", ErrInvalidManifest, contribID)
			}
			if err := ValidateDotIdentifier(string(contrib.Panel.SlotID)); err != nil {
				return fmt.Errorf("%w: ui-panel %q invalid slot %q", ErrInvalidManifest, contribID, contrib.Panel.SlotID)
			}
			switch contrib.Panel.Runtime {
			case RuntimeDeclarative, RuntimeWebViewUntrusted, RuntimeWasmUIIR, RuntimeMyGoJSWasm:
			default:
				return fmt.Errorf("%w: unsupported panel runtime %q", ErrInvalidManifest, contrib.Panel.Runtime)
			}
		}
	}
	return nil
}

// SigningBytes produces the canonical domain-separated byte sequence covered by
// Ed25519 signatures, mirroring SignedRelease::signing_bytes in release.rs.
func SigningBytes(manifest []byte, att ReleaseAttestation) []byte {
	pub := []byte(att.Publisher)
	out := make([]byte, 0, len(ReleaseSigningDomain)+8+len(pub)+20+32+len(manifest))
	out = append(out, ReleaseSigningDomain...)
	out = binary.BigEndian.AppendUint64(out, uint64(len(pub)))
	out = append(out, pub...)
	out = append(out, att.SourceRevision[:]...)
	out = append(out, att.BuildEvidenceDigest[:]...)
	out = append(out, manifest...)
	return out
}

// ReleaseTrust holds deployment-owned Ed25519 publisher keys.
// Packages can never inject trust roots into this store.
type ReleaseTrust struct {
	keys map[PublisherID]ed25519.PublicKey
}

func NewReleaseTrust(keys map[PublisherID]ed25519.PublicKey) *ReleaseTrust {
	copied := make(map[PublisherID]ed25519.PublicKey, len(keys))
	for k, v := range keys {
		keyCopy := make(ed25519.PublicKey, len(v))
		copy(keyCopy, v)
		copied[k] = keyCopy
	}
	return &ReleaseTrust{keys: copied}
}

// VerifiedRelease proves publisher authenticity and binds the canonical SHA-256 digest.
type VerifiedRelease struct {
	Digest       [32]byte
	ManifestTOML []byte
	Attestation  ReleaseAttestation
}

// Verify checks size bounds, publisher key lookup, and Ed25519 signature integrity.
func (t *ReleaseTrust) Verify(rel SignedRelease) (*VerifiedRelease, error) {
	if len(rel.ManifestTOML) == 0 || len(rel.ManifestTOML) > MaxManifestBytes {
		return nil, fmt.Errorf("%w: manifest size %d", ErrInvalidManifest, len(rel.ManifestTOML))
	}
	pubKey, ok := t.keys[rel.Attestation.Publisher]
	if !ok || len(pubKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: %q", ErrUnknownPublisher, rel.Attestation.Publisher)
	}
	msg := SigningBytes(rel.ManifestTOML, rel.Attestation)
	if !ed25519.Verify(pubKey, msg, rel.Signature[:]) {
		return nil, ErrInvalidSignature
	}
	digest := sha256.Sum256(msg)
	rawCopy := make([]byte, len(rel.ManifestTOML))
	copy(rawCopy, rel.ManifestTOML)
	return &VerifiedRelease{
		Digest:       digest,
		ManifestTOML: rawCopy,
		Attestation:  rel.Attestation,
	}, nil
}
