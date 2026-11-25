// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package shell

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	WorkspaceProfileSchemaVersion = 1
	maxWorkspaceProfileBytes      = 256 * 1024
)

var (
	ErrWorkspaceProfileInvalid = errors.New("shell: invalid workspace profile JSON")
	ErrWorkspaceProfileTooBig  = errors.New("shell: workspace profile exceeds 256 KiB limit")
)

// WorkspaceProfile is a secret-free, portable UI workspace configuration
// capturing theme preset, sidebar collapse state, active perspective, and
// composable Emacs WindowTree layout for cross-device sync or export/import.
type WorkspaceProfile struct {
	SchemaVersion    int            `json:"schema_version"`
	Name             string         `json:"name"`
	ThemeID          string         `json:"theme_id"`
	SidebarCollapsed bool           `json:"sidebar_collapsed"`
	Perspective      string         `json:"perspective,omitempty"`
	Layout           *LayoutSpec    `json:"layout,omitempty"`
}

// ExportWorkspaceProfile captures a portable, secret-free WorkspaceProfile
// from the live WindowTree and shell UI preferences.
func ExportWorkspaceProfile(name, themeID string, sidebarCollapsed bool, perspective string, tree *WindowTree) WorkspaceProfile {
	if strings.TrimSpace(name) == "" {
		name = "default"
	}
	if strings.TrimSpace(themeID) == "" {
		themeID = "midnight"
	}
	prof := WorkspaceProfile{
		SchemaVersion:    WorkspaceProfileSchemaVersion,
		Name:             strings.TrimSpace(name),
		ThemeID:          strings.TrimSpace(themeID),
		SidebarCollapsed: sidebarCollapsed,
		Perspective:      strings.TrimSpace(perspective),
	}
	if tree != nil {
		spec := ExportLayoutSpec(tree)
		prof.Layout = &spec
	}
	return prof
}

// EncodeWorkspaceProfileJSON serializes a WorkspaceProfile as indented JSON.
func EncodeWorkspaceProfileJSON(prof WorkspaceProfile) ([]byte, error) {
	if prof.SchemaVersion == 0 {
		prof.SchemaVersion = WorkspaceProfileSchemaVersion
	}
	if prof.SchemaVersion != WorkspaceProfileSchemaVersion {
		return nil, fmt.Errorf("%w: unsupported schema_version %d", ErrWorkspaceProfileInvalid, prof.SchemaVersion)
	}
	return json.MarshalIndent(prof, "", "  ")
}

// DecodeWorkspaceProfileJSON validates and decodes a WorkspaceProfile JSON
// payload with fail-closed size and schema-version enforcement.
func DecodeWorkspaceProfileJSON(raw []byte) (WorkspaceProfile, error) {
	if len(raw) == 0 {
		return WorkspaceProfile{}, fmt.Errorf("%w: empty payload", ErrWorkspaceProfileInvalid)
	}
	if len(raw) > maxWorkspaceProfileBytes {
		return WorkspaceProfile{}, fmt.Errorf("%w (%d > %d)", ErrWorkspaceProfileTooBig, len(raw), maxWorkspaceProfileBytes)
	}
	var prof WorkspaceProfile
	if err := json.Unmarshal(raw, &prof); err != nil {
		return WorkspaceProfile{}, fmt.Errorf("%w: %v", ErrWorkspaceProfileInvalid, err)
	}
	if prof.SchemaVersion != WorkspaceProfileSchemaVersion {
		return WorkspaceProfile{}, fmt.Errorf("%w: unsupported schema_version %d", ErrWorkspaceProfileInvalid, prof.SchemaVersion)
	}
	prof.Name = strings.TrimSpace(prof.Name)
	if prof.Name == "" {
		prof.Name = "imported"
	}
	prof.ThemeID = strings.TrimSpace(prof.ThemeID)
	if prof.ThemeID == "" {
		prof.ThemeID = "midnight"
	}
	return prof, nil
}
