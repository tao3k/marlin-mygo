// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package schema

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// RestrictedSlots cannot host untrusted Marketplace plugins on any surface.
var RestrictedSlots = []SlotID{"approval.gate", "host.auth", "host.secrets"}

// IsRestrictedSlot reports whether slotID is reserved exclusively for Tier-0 host.* renderers.
func IsRestrictedSlot(slotID SlotID) bool {
	for _, r := range RestrictedSlots {
		if slotID == r {
			return true
		}
	}
	return false
}

// RuntimeAllowedOnTarget enforces App Store / Play Store execution policy:
// iOS and Android admit declarative packs only; desktop admits WebView + WASM runtimes.
func RuntimeAllowedOnTarget(rt PanelRuntimeKind, target TargetKind) bool {
	if rt == RuntimeDeclarative {
		return true
	}
	switch target {
	case TargetDesktopMacOS, TargetDesktopLinux, TargetDesktopWindows:
		return rt == RuntimeWebViewUntrusted || rt == RuntimeWasmUIIR || rt == RuntimeMyGoJSWasm
	default:
		return false
	}
}

// ParsePluginManifestTOML parses and strictly validates a canonical Marlin v1
// marlin-plugin.toml document without external TOML dependencies.
// Supports root keys plus [capabilities.<id>], [contributions.<id>], and
// [contributions.<id>.panel] tables matching Rust UnifiedPanelManifest.
func ParsePluginManifestTOML(raw []byte) (*PluginManifest, error) {
	if len(raw) == 0 || len(raw) > MaxManifestBytes {
		return nil, fmt.Errorf("%w: manifest size %d out of bounds", ErrInvalidManifest, len(raw))
	}
	m := &PluginManifest{
		Capabilities:  make(map[CapabilityID]CapabilityDecl),
		Contributions: make(map[ContributionID]ContributionDecl),
	}

	section := ""
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(stripTOMLComment(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			if section == "" {
				return nil, fmt.Errorf("%w: line %d: empty table header", ErrInvalidManifest, lineNum)
			}
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("%w: line %d: expected key = value", ErrInvalidManifest, lineNum)
		}
		key := strings.TrimSpace(line[:eq])
		valRaw := strings.TrimSpace(line[eq+1:])
		if err := assignManifestField(m, section, key, valRaw); err != nil {
			return nil, fmt.Errorf("%w: line %d (%s.%s): %v", ErrInvalidManifest, lineNum, section, key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: scan: %v", ErrInvalidManifest, err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	for cid, contrib := range m.Contributions {
		if contrib.Panel != nil && IsRestrictedSlot(contrib.Panel.SlotID) {
			return nil, fmt.Errorf("%w: contribution %q targets host-restricted slot %q", ErrInvalidManifest, cid, contrib.Panel.SlotID)
		}
	}
	return m, nil
}

func assignManifestField(m *PluginManifest, section, key, valRaw string) error {
	if section == "" {
		switch key {
		case "id":
			s, err := parseTOMLString(valRaw)
			if err != nil {
				return err
			}
			m.ID = PluginID(s)
		case "version":
			s, err := parseTOMLString(valRaw)
			if err != nil {
				return err
			}
			m.Version = s
		case "contract_major":
			n, err := strconv.ParseUint(valRaw, 10, 16)
			if err != nil {
				return err
			}
			m.ContractMajor = uint16(n)
		case "requires_features":
			arr, err := parseTOMLStringArray(valRaw)
			if err != nil {
				return err
			}
			m.RequiresFeatures = arr
		default:
			return fmt.Errorf("unknown root field %q", key)
		}
		return nil
	}

	parts := strings.Split(section, ".")
	if len(parts) == 2 && parts[0] == "capabilities" {
		capID := CapabilityID(parts[1])
		decl := m.Capabilities[capID]
		switch key {
		case "kind":
			s, err := parseTOMLString(valRaw)
			if err != nil {
				return err
			}
			decl.Kind = s
		case "path":
			s, err := parseTOMLString(valRaw)
			if err != nil {
				return err
			}
			decl.Path = s
		case "surface":
			s, err := parseTOMLString(valRaw)
			if err != nil {
				return err
			}
			decl.Surface = s
		case "target":
			s, err := parseTOMLString(valRaw)
			if err != nil {
				return err
			}
			decl.Target = TargetKind(s)
		case "protocol":
			s, err := parseTOMLString(valRaw)
			if err != nil {
				return err
			}
			decl.Protocol = s
		case "optional":
			b, err := strconv.ParseBool(valRaw)
			if err != nil {
				return err
			}
			decl.Optional = b
		default:
			return fmt.Errorf("unknown capability field %q", key)
		}
		m.Capabilities[capID] = decl
		return nil
	}

	if len(parts) == 2 && parts[0] == "contributions" {
		cid := ContributionID(parts[1])
		decl := m.Contributions[cid]
		switch key {
		case "kind":
			s, err := parseTOMLString(valRaw)
			if err != nil {
				return err
			}
			decl.Kind = s
		case "policy_module":
			s, err := parseTOMLString(valRaw)
			if err != nil {
				return err
			}
			decl.PolicyModule = PolicyModuleID(s)
		case "settings_schema":
			s, err := parseTOMLString(valRaw)
			if err != nil {
				return err
			}
			decl.SettingsSchema = SchemaID(s)
		case "requires":
			arr, err := parseTOMLStringArray(valRaw)
			if err != nil {
				return err
			}
			reqs := make([]CapabilityID, len(arr))
			for i, item := range arr {
				reqs[i] = CapabilityID(item)
			}
			decl.Requires = reqs
		default:
			return fmt.Errorf("unknown contribution field %q", key)
		}
		m.Contributions[cid] = decl
		return nil
	}

	if len(parts) == 3 && parts[0] == "contributions" && parts[2] == "panel" {
		cid := ContributionID(parts[1])
		decl := m.Contributions[cid]
		if decl.Panel == nil {
			decl.Panel = &PanelContribution{Cardinality: SlotSingle}
		}
		switch key {
		case "runtime":
			s, err := parseTOMLString(valRaw)
			if err != nil {
				return err
			}
			decl.Panel.Runtime = PanelRuntimeKind(s)
		case "slot_id":
			s, err := parseTOMLString(valRaw)
			if err != nil {
				return err
			}
			decl.Panel.SlotID = SlotID(s)
		case "cardinality":
			s, err := parseTOMLString(valRaw)
			if err != nil {
				return err
			}
			switch SlotCardinality(s) {
			case SlotSingle, SlotList, SlotKeyed:
				decl.Panel.Cardinality = SlotCardinality(s)
			default:
				return fmt.Errorf("invalid cardinality %q", s)
			}
		case "slot_key":
			s, err := parseTOMLString(valRaw)
			if err != nil {
				return err
			}
			decl.Panel.SlotKey = s
		case "entry_path":
			s, err := parseTOMLString(valRaw)
			if err != nil {
				return err
			}
			decl.Panel.EntryPath = s
		case "read_scopes":
			arr, err := parseTOMLStringArray(valRaw)
			if err != nil {
				return err
			}
			decl.Panel.ReadScopes = arr
		case "declared_actions":
			arr, err := parseTOMLStringArray(valRaw)
			if err != nil {
				return err
			}
			decl.Panel.DeclaredActions = arr
		default:
			return fmt.Errorf("unknown panel field %q", key)
		}
		m.Contributions[cid] = decl
		return nil
	}

	return fmt.Errorf("unsupported table [%s]", section)
}

func stripTOMLComment(line string) string {
	inQuotes := false
	escaped := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && inQuotes {
			escaped = true
			continue
		}
		if c == '"' {
			inQuotes = !inQuotes
			continue
		}
		if c == '#' && !inQuotes {
			return line[:i]
		}
	}
	return line
}

func parseTOMLString(raw string) (string, error) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return "", fmt.Errorf("expected quoted string, got %q", raw)
	}
	return strconv.Unquote(raw)
}

func parseTOMLStringArray(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '[' || raw[len(raw)-1] != ']' {
		return nil, fmt.Errorf("expected string array, got %q", raw)
	}
	inner := strings.TrimSpace(raw[1 : len(raw)-1])
	if inner == "" {
		return nil, nil
	}
	var out []string
	var cur strings.Builder
	inQuotes := false
	escaped := false
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if escaped {
			cur.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' && inQuotes {
			cur.WriteByte(c)
			escaped = true
			continue
		}
		if c == '"' {
			inQuotes = !inQuotes
			cur.WriteByte(c)
			continue
		}
		if c == ',' && !inQuotes {
			tok := strings.TrimSpace(cur.String())
			if tok != "" {
				s, err := parseTOMLString(tok)
				if err != nil {
					return nil, err
				}
				out = append(out, s)
			}
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	if inQuotes {
		return nil, fmt.Errorf("unterminated string in array")
	}
	if tail := strings.TrimSpace(cur.String()); tail != "" {
		s, err := parseTOMLString(tail)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
