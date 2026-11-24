// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package schema owns validated domain identifiers, signed release verification,
// and bounded UI IR structures shared across the marlin-mygo host SDK.
package schema

import (
	"errors"
	"fmt"
	"strings"
)

const (
	MaxIdentifierBytes = 128
	MaxComponentBytes  = 64
	MaxManifestBytes   = 16 * 1024
	MaxCapabilities    = 32
	MaxContributions   = 16
	MaxUITreeNodes     = 2048
	MaxUITreeDepth     = 32
)

var (
	ErrInvalidIdentifier = errors.New("schema: invalid semantic identifier")
	ErrInvalidManifest   = errors.New("schema: invalid plugin manifest")
	ErrUnknownPublisher  = errors.New("schema: unknown release publisher")
	ErrInvalidSignature  = errors.New("schema: invalid Ed25519 release signature")
	ErrUITreeBounds      = errors.New("schema: UI IR tree exceeds admitted bounds")
)

type (
	PluginID       string
	PublisherID    string
	CapabilityID   string
	ContributionID string
	PolicyModuleID string
	SchemaID       string
	SlotID         string
	ComponentID    string
	BufferID       string
	SessionID      string
	SubmissionID   string
	EventCursor    string
)

// TargetKind mirrors marlin.common.v1.TargetKind.
type TargetKind string

const (
	TargetDesktopMacOS   TargetKind = "desktop-macos"
	TargetDesktopLinux   TargetKind = "desktop-linux"
	TargetDesktopWindows TargetKind = "desktop-windows"
	TargetMobileIOS      TargetKind = "mobile-ios"
	TargetMobileAndroid  TargetKind = "mobile-android"
	TargetRemoteHeadless TargetKind = "remote-headless"
)

// SlotCardinality mirrors marlin.ui.v1.SlotCardinality.
type SlotCardinality string

const (
	SlotSingle SlotCardinality = "single"
	SlotList   SlotCardinality = "list"
	SlotKeyed  SlotCardinality = "keyed"
)

// PanelRuntimeKind mirrors marlin.plugin.v1.PanelRuntimeKind.
type PanelRuntimeKind string

const (
	RuntimeDeclarative      PanelRuntimeKind = "declarative"
	RuntimeWebViewUntrusted PanelRuntimeKind = "webview-untrusted"
	RuntimeWasmUIIR         PanelRuntimeKind = "wasm-ui-ir"
	RuntimeMyGoJSWasm       PanelRuntimeKind = "mygo-js-wasm"
)

// ValidateDotIdentifier enforces the exact grammar from
// crates/marlin-agent-runtime/src/plugin/release.rs:
// <= 128 ASCII bytes, dot-separated segments starting with alphanumeric,
// containing only [A-Za-z0-9_-].
func ValidateDotIdentifier(v string) error {
	if len(v) == 0 || len(v) > MaxIdentifierBytes {
		return fmt.Errorf("%w: length %d", ErrInvalidIdentifier, len(v))
	}
	for _, part := range strings.Split(v, ".") {
		if len(part) == 0 || !isASCIIAlphaNum(part[0]) {
			return fmt.Errorf("%w: segment %q", ErrInvalidIdentifier, part)
		}
		for i := 0; i < len(part); i++ {
			b := part[i]
			if !isASCIIAlphaNum(b) && b != '-' && b != '_' {
				return fmt.Errorf("%w: character %q in %q", ErrInvalidIdentifier, b, v)
			}
		}
	}
	return nil
}

// ParsePluginID validates a namespaced plugin ID (must contain a dot, e.g. "tao3k.org-kanban").
func ParsePluginID(v string) (PluginID, error) {
	if err := ValidateDotIdentifier(v); err != nil {
		return "", err
	}
	if !strings.Contains(v, ".") {
		return "", fmt.Errorf("%w: plugin id %q must be namespaced", ErrInvalidIdentifier, v)
	}
	return PluginID(v), nil
}

// ParseSubmissionID enforces marlin.app.v1 submission_id rules: 1..128 [A-Za-z0-9_-].
func ParseSubmissionID(v string) (SubmissionID, error) {
	if len(v) == 0 || len(v) > MaxIdentifierBytes {
		return "", fmt.Errorf("%w: submission id length %d", ErrInvalidIdentifier, len(v))
	}
	for i := 0; i < len(v); i++ {
		b := v[i]
		if !isASCIIAlphaNum(b) && b != '-' && b != '_' {
			return "", fmt.Errorf("%w: submission id %q", ErrInvalidIdentifier, v)
		}
	}
	return SubmissionID(v), nil
}

func isASCIIAlphaNum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
