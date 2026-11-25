// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

var (
	ErrTrustRingInvalid          = errors.New("marketplace: invalid trust-ring.toml configuration")
	ErrOfficialPublisherRequired = errors.New("marketplace: only official verified marketplace publishers are permitted")
)

// TrustTier classifies a publisher in a multi-publisher TrustRing.
type TrustTier string

const (
	TrustTierOfficial TrustTier = "official" // Tier 1: tao3k.* official keys
	TrustTierVerified TrustTier = "verified" // Tier 2: audited community publishers
	TrustTierDev      TrustTier = "dev"      // Tier 3: explicit local development keys
)

// PublisherTrustEntry binds one publisher ID to its Ed25519 verification key and tier.
type PublisherTrustEntry struct {
	PublisherID  schema.PublisherID
	Tier         TrustTier
	PublicKey    ed25519.PublicKey
	AllowCatalog bool
}

// TrustRing holds deployment-owned multi-publisher Ed25519 verification keys
// loaded from declarative trust-ring.toml.
type TrustRing struct {
	SchemaVersion int
	Publishers    []PublisherTrustEntry
}

// ParseTrustRingTOML parses a declarative trust-ring.toml without external dependencies.
func ParseTrustRingTOML(raw []byte) (*TrustRing, error) {
	if len(raw) == 0 || len(raw) > 256*1024 {
		return nil, fmt.Errorf("%w: size %d out of bounds", ErrTrustRingInvalid, len(raw))
	}
	ring := &TrustRing{SchemaVersion: 1}
	byID := make(map[schema.PublisherID]*PublisherTrustEntry)
	var order []schema.PublisherID
	var curID schema.PublisherID

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(stripProfileComment(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			hdr := strings.TrimSpace(line[1 : len(line)-1])
			if !strings.HasPrefix(hdr, "publishers.") {
				return nil, fmt.Errorf("%w: line %d: expected [publishers.<id>], got [%s]", ErrTrustRingInvalid, lineNum, hdr)
			}
			rest := strings.TrimSpace(strings.TrimPrefix(hdr, "publishers."))
			if strings.HasPrefix(rest, "\"") && strings.HasSuffix(rest, "\"") {
				unq, err := strconv.Unquote(rest)
				if err != nil {
					return nil, fmt.Errorf("%w: line %d: %v", ErrTrustRingInvalid, lineNum, err)
				}
				rest = unq
			}
			if err := schema.ValidateDotIdentifier(rest); err != nil {
				return nil, fmt.Errorf("%w: line %d: invalid publisher %q: %v", ErrTrustRingInvalid, lineNum, rest, err)
			}
			curID = schema.PublisherID(rest)
			if _, exists := byID[curID]; !exists {
				byID[curID] = &PublisherTrustEntry{
					PublisherID: curID,
					Tier:        TrustTierVerified,
				}
				order = append(order, curID)
			}
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("%w: line %d: expected key = value", ErrTrustRingInvalid, lineNum)
		}
		key := strings.TrimSpace(line[:eq])
		valRaw := strings.TrimSpace(line[eq+1:])
		if curID == "" {
			if key == "schema_version" {
				v, err := strconv.Atoi(valRaw)
				if err != nil || v != 1 {
					return nil, fmt.Errorf("%w: line %d: unsupported schema_version %q", ErrTrustRingInvalid, lineNum, valRaw)
				}
				ring.SchemaVersion = v
				continue
			}
			return nil, fmt.Errorf("%w: line %d: unexpected top-level key %q", ErrTrustRingInvalid, lineNum, key)
		}
		ent := byID[curID]
		switch key {
		case "tier":
			v, err := unquoteTOML(valRaw)
			if err != nil {
				return nil, fmt.Errorf("%w: line %d: %v", ErrTrustRingInvalid, lineNum, err)
			}
			switch TrustTier(v) {
			case TrustTierOfficial, TrustTierVerified, TrustTierDev:
				ent.Tier = TrustTier(v)
			default:
				return nil, fmt.Errorf("%w: line %d: invalid trust tier %q", ErrTrustRingInvalid, lineNum, v)
			}
		case "ed25519_pub":
			v, err := unquoteTOML(valRaw)
			if err != nil {
				return nil, fmt.Errorf("%w: line %d: %v", ErrTrustRingInvalid, lineNum, err)
			}
			pubBytes, err := hex.DecodeString(strings.TrimSpace(v))
			if err != nil || len(pubBytes) != ed25519.PublicKeySize {
				return nil, fmt.Errorf("%w: line %d: invalid 64-hex Ed25519 public key", ErrTrustRingInvalid, lineNum)
			}
			ent.PublicKey = ed25519.PublicKey(pubBytes)
		case "allow_catalog":
			b, err := strconv.ParseBool(valRaw)
			if err != nil {
				return nil, fmt.Errorf("%w: line %d: %v", ErrTrustRingInvalid, lineNum, err)
			}
			ent.AllowCatalog = b
		default:
			return nil, fmt.Errorf("%w: line %d: unknown field %q", ErrTrustRingInvalid, lineNum, key)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for _, id := range order {
		ent := byID[id]
		if len(ent.PublicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: publisher %q missing ed25519_pub", ErrTrustRingInvalid, id)
		}
		ring.Publishers = append(ring.Publishers, *ent)
	}
	if len(ring.Publishers) == 0 {
		return nil, fmt.Errorf("%w: trust ring must declare at least one publisher", ErrTrustRingInvalid)
	}
	return ring, nil
}

// LoadTrustRingFile reads and parses a declarative trust-ring.toml file.
func LoadTrustRingFile(path string) (*TrustRing, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseTrustRingTOML(raw)
}

// ReleaseTrust builds a multi-publisher schema.ReleaseTrust store.
func (r *TrustRing) ReleaseTrust() *schema.ReleaseTrust {
	keys := make(map[schema.PublisherID]ed25519.PublicKey, len(r.Publishers))
	for _, p := range r.Publishers {
		keys[p.PublisherID] = p.PublicKey
	}
	return schema.NewReleaseTrust(keys)
}

// OfficialReleaseTrust builds a strict schema.ReleaseTrust containing ONLY
// TrustTierOfficial publisher keys so third-party, dev, or unsigned bundles
// are cryptographically rejected fail-closed in Official-Marketplace-Only mode.
func (r *TrustRing) OfficialReleaseTrust() *schema.ReleaseTrust {
	keys := make(map[schema.PublisherID]ed25519.PublicKey)
	for _, p := range r.Publishers {
		if p.Tier == TrustTierOfficial {
			keys[p.PublisherID] = p.PublicKey
		}
	}
	return schema.NewReleaseTrust(keys)
}

// IsOfficialPublisher reports whether publisherID belongs to TrustTierOfficial.
func (r *TrustRing) IsOfficialPublisher(id schema.PublisherID) bool {
	for _, p := range r.Publishers {
		if p.PublisherID == id && p.Tier == TrustTierOfficial {
			return true
		}
	}
	return false
}

// CatalogKey resolves the Ed25519 catalog verification key for publisherID, or
// the first publisher marked allow_catalog = true (falling back to official tier).
func (r *TrustRing) CatalogKey(publisherID schema.PublisherID) (ed25519.PublicKey, bool) {
	if publisherID != "" {
		for _, p := range r.Publishers {
			if p.PublisherID == publisherID {
				return slices.Clone(p.PublicKey), true
			}
		}
	}
	for _, p := range r.Publishers {
		if p.AllowCatalog {
			return slices.Clone(p.PublicKey), true
		}
	}
	for _, p := range r.Publishers {
		if p.Tier == TrustTierOfficial {
			return slices.Clone(p.PublicKey), true
		}
	}
	return nil, false
}
