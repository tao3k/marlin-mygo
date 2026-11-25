// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

var (
	ErrRegistryManifestInvalid = errors.New("marketplace: invalid registry.json manifest")
	restrictedRegistrySlots    = []schema.SlotID{"approval.gate", "host.auth", "host.secrets"}
)

// RegistryPluginEntry declares one official or community-submitted plugin inside
// the top-level Official Marketplace `marketplace/registry.json`.
type RegistryPluginEntry struct {
	ID          schema.PluginID         `json:"id"`
	Name        string                  `json:"name"`
	Category    string                  `json:"category"`
	Runtime     schema.PanelRuntimeKind `json:"runtime"`
	SlotID      schema.SlotID           `json:"slot_id"`
	TrustTier   TrustTier               `json:"trust_tier"`
	PublisherID schema.PublisherID      `json:"publisher_id"`
	LocalPath   string                  `json:"local_path,omitempty"`
	Repository  string                  `json:"repository,omitempty"`
	CommitSHA   string                  `json:"commit_sha,omitempty"`
	Subdir      string                  `json:"subdir,omitempty"`
	Summary     string                  `json:"summary"`
}

// RegistryThemeEntry declares one official or community-submitted 8-color base
// theme pack (projected into all 34 --marlin-* semantic tokens at runtime).
type RegistryThemeEntry struct {
	ID          schema.PluginID    `json:"id"`
	Name        string             `json:"name"`
	Appearance  string             `json:"appearance"` // "dark" | "light"
	TrustTier   TrustTier          `json:"trust_tier"`
	PublisherID schema.PublisherID `json:"publisher_id"`
	LocalPath   string             `json:"local_path,omitempty"`
	Repository  string             `json:"repository,omitempty"`
	CommitSHA   string             `json:"commit_sha,omitempty"`
	Summary     string             `json:"summary"`
}

// RegistryLayoutEntry declares one shareable Emacs composable workspace
// perspective / LayoutSpec pack submitted to the Official Marketplace.
type RegistryLayoutEntry struct {
	ID          schema.PluginID    `json:"id"`
	Name        string             `json:"name"`
	Perspective string             `json:"perspective"`
	TrustTier   TrustTier          `json:"trust_tier"`
	PublisherID schema.PublisherID `json:"publisher_id"`
	LocalPath   string             `json:"local_path,omitempty"`
	Repository  string             `json:"repository,omitempty"`
	CommitSHA   string             `json:"commit_sha,omitempty"`
	Summary     string             `json:"summary"`
}

// RegistryManifest is the canonical schema for the Official Marketplace
// (`tao3k/marlin-marketplace` registry.json), governing plugins, curated themes,
// and shareable Emacs workspace layouts with pinned Git commit SHAs.
type RegistryManifest struct {
	SchemaVersion int                   `json:"schema_version"`
	MarketplaceID string                `json:"marketplace_id"`
	CanonicalRepo string                `json:"canonical_repo"`
	SigningPolicy string                `json:"signing_policy"`
	Categories    []string              `json:"categories"`
	Plugins       []RegistryPluginEntry `json:"plugins"`
	Themes        []RegistryThemeEntry  `json:"themes,omitempty"`
	Layouts       []RegistryLayoutEntry `json:"layouts,omitempty"`
}

// LoadRegistryManifest reads and parses a marketplace/registry.json file.
func LoadRegistryManifest(path string) (*RegistryManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > 2*1024*1024 {
		return nil, fmt.Errorf("%w: size %d out of bounds", ErrRegistryManifestInvalid, len(raw))
	}
	var m RegistryManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRegistryManifestInvalid, err)
	}
	return &m, nil
}

// ValidateRegistryManifest enforces supply-chain, slot-isolation, and manifest
// integrity invariants on every entry in marketplace/registry.json.
// When repoRoot != "", any in-tree `local_path` entry is verified on disk to
// contain a valid marlin-plugin.toml matching its declared ID and runtime.
func ValidateRegistryManifest(m *RegistryManifest, repoRoot string) error {
	if m == nil {
		return fmt.Errorf("%w: nil manifest", ErrRegistryManifestInvalid)
	}
	if m.SchemaVersion != 1 {
		return fmt.Errorf("%w: unsupported schema_version %d", ErrRegistryManifestInvalid, m.SchemaVersion)
	}
	if err := schema.ValidateDotIdentifier(m.MarketplaceID); err != nil {
		return fmt.Errorf("%w: invalid marketplace_id %q: %v", ErrRegistryManifestInvalid, m.MarketplaceID, err)
	}
	if !strings.HasPrefix(m.CanonicalRepo, "https://") {
		return fmt.Errorf("%w: canonical_repo must use https://", ErrRegistryManifestInvalid)
	}
	if len(m.Plugins) == 0 {
		return fmt.Errorf("%w: registry must contain at least one plugin", ErrRegistryManifestInvalid)
	}

	seen := make(map[schema.PluginID]struct{}, len(m.Plugins))
	for i, p := range m.Plugins {
		if err := schema.ValidateDotIdentifier(string(p.ID)); err != nil {
			return fmt.Errorf("%w: plugin[%d] invalid id %q: %v", ErrRegistryManifestInvalid, i, p.ID, err)
		}
		if _, dup := seen[p.ID]; dup {
			return fmt.Errorf("%w: duplicate plugin id %q", ErrRegistryManifestInvalid, p.ID)
		}
		seen[p.ID] = struct{}{}

		if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Summary) == "" {
			return fmt.Errorf("%w: plugin %q requires non-empty name and summary", ErrRegistryManifestInvalid, p.ID)
		}
		if len(m.Categories) > 0 && !slices.Contains(m.Categories, p.Category) {
			return fmt.Errorf("%w: plugin %q category %q not in declared categories", ErrRegistryManifestInvalid, p.ID, p.Category)
		}
		switch p.Runtime {
		case schema.RuntimeWebViewUntrusted, schema.RuntimeWasmUIIR, schema.RuntimeDeclarative, schema.RuntimeMyGoJSWasm:
		default:
			return fmt.Errorf("%w: plugin %q unsupported runtime %q", ErrRegistryManifestInvalid, p.ID, p.Runtime)
		}
		if err := schema.ValidateDotIdentifier(string(p.SlotID)); err != nil {
			return fmt.Errorf("%w: plugin %q invalid slot_id %q: %v", ErrRegistryManifestInvalid, p.ID, p.SlotID, err)
		}
		if slices.Contains(restrictedRegistrySlots, p.SlotID) {
			return fmt.Errorf("%w: plugin %q cannot target restricted host slot %q", ErrRegistryManifestInvalid, p.ID, p.SlotID)
		}
		if p.TrustTier != TrustTierOfficial && p.TrustTier != TrustTierVerified {
			return fmt.Errorf("%w: plugin %q invalid registry trust_tier %q", ErrRegistryManifestInvalid, p.ID, p.TrustTier)
		}
		if err := schema.ValidateDotIdentifier(string(p.PublisherID)); err != nil {
			return fmt.Errorf("%w: plugin %q invalid publisher_id %q: %v", ErrRegistryManifestInvalid, p.ID, p.PublisherID, err)
		}

		hasLocal := strings.TrimSpace(p.LocalPath) != ""
		hasRepo := strings.TrimSpace(p.Repository) != ""
		if hasLocal == hasRepo {
			return fmt.Errorf("%w: plugin %q must specify exactly one of local_path or repository", ErrRegistryManifestInvalid, p.ID)
		}
		if hasRepo {
			if !strings.HasPrefix(p.Repository, "https://") {
				return fmt.Errorf("%w: plugin %q repository must use https://", ErrRegistryManifestInvalid, p.ID)
			}
			if !isPinnedSHA40(p.CommitSHA) {
				return fmt.Errorf("%w: plugin %q external repository must pin a 40-hex commit_sha", ErrRegistryManifestInvalid, p.ID)
			}
			if strings.Contains(p.Subdir, "..") {
				return fmt.Errorf("%w: plugin %q subdir must not contain '..'", ErrRegistryManifestInvalid, p.ID)
			}
		}
		if hasLocal {
			if filepath.IsAbs(p.LocalPath) || strings.Contains(p.LocalPath, "..") {
				return fmt.Errorf("%w: plugin %q local_path must be a safe relative path", ErrRegistryManifestInvalid, p.ID)
			}
			if repoRoot != "" {
				manifestPath := filepath.Join(repoRoot, filepath.FromSlash(p.LocalPath), "marlin-plugin.toml")
				rawManifest, err := os.ReadFile(manifestPath)
				if err != nil {
					return fmt.Errorf("%w: plugin %q missing marlin-plugin.toml at %s: %v", ErrRegistryManifestInvalid, p.ID, p.LocalPath, err)
				}
				parsed, err := schema.ParsePluginManifestTOML(rawManifest)
				if err != nil {
					return fmt.Errorf("%w: plugin %q invalid marlin-plugin.toml: %v", ErrRegistryManifestInvalid, p.ID, err)
				}
				if parsed.ID != p.ID {
					return fmt.Errorf("%w: plugin %q manifest id mismatch: got %q", ErrRegistryManifestInvalid, p.ID, parsed.ID)
				}
			}
		}
	}

	seenThemes := make(map[schema.PluginID]struct{}, len(m.Themes))
	for i, th := range m.Themes {
		if err := schema.ValidateDotIdentifier(string(th.ID)); err != nil {
			return fmt.Errorf("%w: theme[%d] invalid id %q: %v", ErrRegistryManifestInvalid, i, th.ID, err)
		}
		if _, dup := seenThemes[th.ID]; dup {
			return fmt.Errorf("%w: duplicate theme id %q", ErrRegistryManifestInvalid, th.ID)
		}
		seenThemes[th.ID] = struct{}{}
		if th.Appearance != "dark" && th.Appearance != "light" {
			return fmt.Errorf("%w: theme %q appearance must be dark or light", ErrRegistryManifestInvalid, th.ID)
		}
		if strings.TrimSpace(th.Name) == "" || strings.TrimSpace(th.Summary) == "" {
			return fmt.Errorf("%w: theme %q requires name and summary", ErrRegistryManifestInvalid, th.ID)
		}
		if err := validateExternalSource(string(th.ID), th.LocalPath, th.Repository, th.CommitSHA); err != nil {
			return err
		}
	}

	seenLayouts := make(map[schema.PluginID]struct{}, len(m.Layouts))
	for i, ly := range m.Layouts {
		if err := schema.ValidateDotIdentifier(string(ly.ID)); err != nil {
			return fmt.Errorf("%w: layout[%d] invalid id %q: %v", ErrRegistryManifestInvalid, i, ly.ID, err)
		}
		if _, dup := seenLayouts[ly.ID]; dup {
			return fmt.Errorf("%w: duplicate layout id %q", ErrRegistryManifestInvalid, ly.ID)
		}
		seenLayouts[ly.ID] = struct{}{}
		if strings.TrimSpace(ly.Name) == "" || strings.TrimSpace(ly.Perspective) == "" || strings.TrimSpace(ly.Summary) == "" {
			return fmt.Errorf("%w: layout %q requires name, perspective, and summary", ErrRegistryManifestInvalid, ly.ID)
		}
		if err := validateExternalSource(string(ly.ID), ly.LocalPath, ly.Repository, ly.CommitSHA); err != nil {
			return err
		}
	}
	return nil
}

func validateExternalSource(id, localPath, repo, sha string) error {
	hasLocal := strings.TrimSpace(localPath) != ""
	hasRepo := strings.TrimSpace(repo) != ""
	if hasLocal && hasRepo {
		return fmt.Errorf("%w: entry %q cannot set both local_path and repository", ErrRegistryManifestInvalid, id)
	}
	if hasRepo {
		if !strings.HasPrefix(repo, "https://") {
			return fmt.Errorf("%w: entry %q repository must use https://", ErrRegistryManifestInvalid, id)
		}
		if !isPinnedSHA40(sha) {
			return fmt.Errorf("%w: entry %q external repository must pin a 40-hex commit_sha", ErrRegistryManifestInvalid, id)
		}
	}
	if hasLocal && (filepath.IsAbs(localPath) || strings.Contains(localPath, "..")) {
		return fmt.Errorf("%w: entry %q local_path must be a safe relative path", ErrRegistryManifestInvalid, id)
	}
	return nil
}

func isPinnedSHA40(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
