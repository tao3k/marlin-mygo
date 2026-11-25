// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// LoadProfileFile reads and parses a declarative marlin-plugins.toml lockfile.
// If the file does not exist yet, it returns an empty schema v1 profile.
func LoadProfileFile(path string) (*PluginProfile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &PluginProfile{SchemaVersion: 1}, nil
		}
		return nil, err
	}
	return ParsePluginProfileTOML(raw)
}

// SaveProfileFile atomically persists p as a deterministic marlin-plugins.toml lockfile.
func SaveProfileFile(path string, p PluginProfile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicWriteFile(path, MarshalPluginProfileTOML(p), 0o644)
}

// ParsePluginProfileTOML parses a declarative Emacs-style marlin-plugins.toml
// without external dependencies.
func ParsePluginProfileTOML(raw []byte) (*PluginProfile, error) {
	if len(raw) == 0 || len(raw) > 1024*1024 {
		return nil, fmt.Errorf("%w: profile size %d out of bounds", ErrProfileInvalid, len(raw))
	}
	prof := &PluginProfile{SchemaVersion: 1}
	byID := make(map[schema.PluginID]*PluginSpec)
	var order []schema.PluginID

	var currentID schema.PluginID
	inConfig := false
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
			id, isCfg, err := parseProfileHeader(hdr)
			if err != nil {
				return nil, fmt.Errorf("%w: line %d: %v", ErrProfileInvalid, lineNum, err)
			}
			currentID = id
			inConfig = isCfg
			if _, exists := byID[id]; !exists {
				byID[id] = &PluginSpec{
					PluginID:   id,
					Enabled:    true,
					Activation: schema.ActivationOnSlot,
					Config:     make(map[string]string),
				}
				order = append(order, id)
			}
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("%w: line %d: expected key = value", ErrProfileInvalid, lineNum)
		}
		key := strings.TrimSpace(line[:eq])
		valRaw := strings.TrimSpace(line[eq+1:])
		if currentID == "" {
			if key == "schema_version" {
				n, err := strconv.Atoi(valRaw)
				if err != nil || n != 1 {
					return nil, fmt.Errorf("%w: line %d: unsupported schema_version %q", ErrProfileInvalid, lineNum, valRaw)
				}
				prof.SchemaVersion = n
				continue
			}
			return nil, fmt.Errorf("%w: line %d: unknown root field %q", ErrProfileInvalid, lineNum, key)
		}
		spec := byID[currentID]
		if inConfig {
			v, err := unquoteTOML(valRaw)
			if err != nil {
				return nil, fmt.Errorf("%w: line %d: config.%s: %v", ErrProfileInvalid, lineNum, key, err)
			}
			spec.Config[key] = v
			continue
		}
		switch key {
		case "version":
			v, err := unquoteTOML(valRaw)
			if err != nil {
				return nil, fmt.Errorf("%w: line %d: %v", ErrProfileInvalid, lineNum, err)
			}
			spec.Version = v
		case "publisher":
			v, err := unquoteTOML(valRaw)
			if err != nil {
				return nil, fmt.Errorf("%w: line %d: %v", ErrProfileInvalid, lineNum, err)
			}
			spec.PublisherID = schema.PublisherID(v)
		case "pinned_sha256":
			v, err := unquoteTOML(valRaw)
			if err != nil {
				return nil, fmt.Errorf("%w: line %d: %v", ErrProfileInvalid, lineNum, err)
			}
			spec.PinnedSHA256 = v
		case "enabled":
			b, err := strconv.ParseBool(valRaw)
			if err != nil {
				return nil, fmt.Errorf("%w: line %d: %v", ErrProfileInvalid, lineNum, err)
			}
			spec.Enabled = b
		case "pinned_active":
			b, err := strconv.ParseBool(valRaw)
			if err != nil {
				return nil, fmt.Errorf("%w: line %d: %v", ErrProfileInvalid, lineNum, err)
			}
			spec.PinnedActive = b
		case "activation":
			v, err := unquoteTOML(valRaw)
			if err != nil {
				return nil, fmt.Errorf("%w: line %d: %v", ErrProfileInvalid, lineNum, err)
			}
			pol := schema.ActivationPolicy(v)
			if !schema.ValidActivationPolicy(pol) || pol == "" {
				return nil, fmt.Errorf("%w: line %d: invalid activation %q", ErrProfileInvalid, lineNum, v)
			}
			spec.Activation = pol
		case "slot_id":
			v, err := unquoteTOML(valRaw)
			if err != nil {
				return nil, fmt.Errorf("%w: line %d: %v", ErrProfileInvalid, lineNum, err)
			}
			if schema.IsRestrictedSlot(schema.SlotID(v)) {
				return nil, fmt.Errorf("%w: line %d: restricted slot %q", ErrRestrictedSlotDenied, lineNum, v)
			}
			spec.SlotID = schema.SlotID(v)
		case "triggers":
			arr, err := parseProfileStringArray(valRaw)
			if err != nil {
				return nil, fmt.Errorf("%w: line %d: %v", ErrProfileInvalid, lineNum, err)
			}
			spec.Triggers = arr
		case "hooks":
			arr, err := parseProfileStringArray(valRaw)
			if err != nil {
				return nil, fmt.Errorf("%w: line %d: %v", ErrProfileInvalid, lineNum, err)
			}
			spec.Hooks = arr
		case "idle_unload_sec":
			n, err := strconv.ParseUint(valRaw, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("%w: line %d: %v", ErrProfileInvalid, lineNum, err)
			}
			spec.IdleUnloadSec = uint32(n)
		default:
			return nil, fmt.Errorf("%w: line %d: unknown field %q", ErrProfileInvalid, lineNum, key)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for _, id := range order {
		prof.Plugins = append(prof.Plugins, *byID[id])
	}
	return prof, nil
}

// MarshalPluginProfileTOML emits deterministic, diff-friendly marlin-plugins.toml.
func MarshalPluginProfileTOML(p PluginProfile) []byte {
	var b strings.Builder
	// REUSE-IgnoreStart
	b.WriteString("# SPDX-FileCopyrightText: 2026 tao3k team and Contributors\n")
	b.WriteString("#\n")
	b.WriteString("# SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later\n\n")
	// REUSE-IgnoreEnd
	ver := p.SchemaVersion
	if ver == 0 {
		ver = 1
	}
	fmt.Fprintf(&b, "schema_version = %d\n", ver)
	sorted := slices.Clone(p.Plugins)
	slices.SortFunc(sorted, func(a, c PluginSpec) int {
		return strings.Compare(string(a.PluginID), string(c.PluginID))
	})
	for _, sp := range sorted {
		fmt.Fprintf(&b, "\n[plugins.%q]\n", sp.PluginID)
		if sp.Version != "" {
			fmt.Fprintf(&b, "version = %q\n", sp.Version)
		}
		if sp.PublisherID != "" {
			fmt.Fprintf(&b, "publisher = %q\n", sp.PublisherID)
		}
		if sp.PinnedSHA256 != "" {
			fmt.Fprintf(&b, "pinned_sha256 = %q\n", sp.PinnedSHA256)
		}
		fmt.Fprintf(&b, "enabled = %t\n", sp.Enabled)
		if sp.PinnedActive {
			b.WriteString("pinned_active = true\n")
		}
		act := sp.Activation
		if act == "" {
			act = schema.ActivationOnSlot
		}
		fmt.Fprintf(&b, "activation = %q\n", act)
		if sp.SlotID != "" {
			fmt.Fprintf(&b, "slot_id = %q\n", sp.SlotID)
		}
		if len(sp.Triggers) > 0 {
			fmt.Fprintf(&b, "triggers = [%s]\n", quoteArray(sp.Triggers))
		}
		if len(sp.Hooks) > 0 {
			fmt.Fprintf(&b, "hooks = [%s]\n", quoteArray(sp.Hooks))
		}
		if sp.IdleUnloadSec > 0 {
			fmt.Fprintf(&b, "idle_unload_sec = %d\n", sp.IdleUnloadSec)
		}
		if len(sp.Config) > 0 {
			fmt.Fprintf(&b, "\n[plugins.%q.config]\n", sp.PluginID)
			keys := make([]string, 0, len(sp.Config))
			for k := range sp.Config {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				fmt.Fprintf(&b, "%s = %q\n", k, sp.Config[k])
			}
		}
	}
	return []byte(b.String())
}

func quoteArray(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = strconv.Quote(s)
	}
	return strings.Join(quoted, ", ")
}

func parseProfileHeader(hdr string) (schema.PluginID, bool, error) {
	if !strings.HasPrefix(hdr, "plugins.") {
		return "", false, fmt.Errorf("expected [plugins.<id>] header, got [%s]", hdr)
	}
	rest := strings.TrimPrefix(hdr, "plugins.")
	isCfg := false
	if strings.HasSuffix(rest, ".config") {
		isCfg = true
		rest = strings.TrimSuffix(rest, ".config")
	}
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "\"") && strings.HasSuffix(rest, "\"") {
		unq, err := strconv.Unquote(rest)
		if err != nil {
			return "", false, err
		}
		rest = unq
	}
	id, err := schema.ParsePluginID(rest)
	if err != nil {
		return "", false, err
	}
	return id, isCfg, nil
}

func stripProfileComment(line string) string {
	inQ := false
	esc := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if esc {
			esc = false
			continue
		}
		if c == '\\' && inQ {
			esc = true
			continue
		}
		if c == '"' {
			inQ = !inQ
			continue
		}
		if c == '#' && !inQ {
			return line[:i]
		}
	}
	return line
}

func unquoteTOML(raw string) (string, error) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return "", fmt.Errorf("expected quoted string, got %q", raw)
	}
	return strconv.Unquote(raw)
}

func parseProfileStringArray(raw string) ([]string, error) {
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
	inQ, esc := false, false
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if esc {
			cur.WriteByte(c)
			esc = false
			continue
		}
		if c == '\\' && inQ {
			cur.WriteByte(c)
			esc = true
			continue
		}
		if c == '"' {
			inQ = !inQ
			cur.WriteByte(c)
			continue
		}
		if c == ',' && !inQ {
			if tok := strings.TrimSpace(cur.String()); tok != "" {
				s, err := unquoteTOML(tok)
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
	if inQ {
		return nil, errors.New("unterminated string in array")
	}
	if tail := strings.TrimSpace(cur.String()); tail != "" {
		s, err := unquoteTOML(tail)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
