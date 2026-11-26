// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package style

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// Parse validates and compiles a CSS-subset stylesheet strictly against contract.
// Any forbidden at-rule (@import), external url(), unknown class (when
// contract.AllowedClasses is set), or disallowed property fails closed with an
// exact line-numbered Diagnostic.
func Parse(source string, contract Contract) (*Sheet, error) {
	if contract.MaxBytes <= 0 {
		contract.MaxBytes = DefaultMaxBytes
	}
	if contract.MaxRules <= 0 {
		contract.MaxRules = DefaultMaxRules
	}
	if contract.MaxVariables <= 0 {
		contract.MaxVariables = DefaultMaxVariables
	}
	if len(contract.AllowedProperties) == 0 {
		contract.AllowedProperties = DefaultAllowedProperties()
	}
	if len(source) > contract.MaxBytes {
		return nil, Diagnostic{Line: 1, Message: fmt.Sprintf("stylesheet exceeds max size (%d > %d bytes)", len(source), contract.MaxBytes)}
	}

	cleaned := stripCommentsPreservingNewlines(source)
	vars := make(map[string]string)
	var rules []rule
	order := 0

	i := 0
	line := 1
	n := len(cleaned)
	for i < n {
		// Skip whitespace.
		for i < n && isSpace(cleaned[i]) {
			if cleaned[i] == '\n' {
				line++
			}
			i++
		}
		if i >= n {
			break
		}

		// Reject forbidden at-rules (@import, @font-face, @keyframes, etc.).
		if cleaned[i] == '@' {
			return nil, Diagnostic{Line: line, Message: "at-rules (@import, @keyframes, etc.) are forbidden by Marlin UI style contract"}
		}

		selStart := i
		selLine := line
		for i < n && cleaned[i] != '{' {
			if cleaned[i] == '\n' {
				line++
			}
			i++
		}
		if i >= n {
			return nil, Diagnostic{Line: selLine, Message: "unexpected EOF: missing '{' after selector"}
		}
		rawSelector := strings.TrimSpace(cleaned[selStart:i])
		i++ // consume '{'

		bodyStart := i
		bodyLine := line
		for i < n && cleaned[i] != '}' {
			if cleaned[i] == '{' {
				return nil, Diagnostic{Line: line, Message: "nested blocks are not supported in Marlin UI CSS contract"}
			}
			if cleaned[i] == '\n' {
				line++
			}
			i++
		}
		if i >= n {
			return nil, Diagnostic{Line: bodyLine, Message: "unexpected EOF: unclosed '{' block"}
		}
		rawBody := cleaned[bodyStart:i]
		i++ // consume '}'

		if rawSelector == ":root" {
			if err := parseRootVars(rawBody, bodyLine, vars, contract.MaxVariables); err != nil {
				return nil, err
			}
			continue
		}

		decls, err := parseDeclarations(rawBody, bodyLine, contract)
		if err != nil {
			return nil, err
		}

		// Support comma-separated selector lists (e.g. ".btn, .tab").
		for _, part := range strings.Split(rawSelector, ",") {
			sel, err := parseSelector(strings.TrimSpace(part), selLine, contract)
			if err != nil {
				return nil, err
			}
			order++
			if order > contract.MaxRules {
				return nil, Diagnostic{Line: selLine, Message: fmt.Sprintf("stylesheet exceeds max rule count (%d)", contract.MaxRules)}
			}
			rules = append(rules, rule{sel: sel, decls: decls, order: order})
		}
	}

	sh := &Sheet{
		vars:     vars,
		rules:    rules,
		contract: contract,
		cache:    make(map[string]ComputedStyle, 32),
	}
	// Pre-validate every declaration value with variable resolution fail-closed.
	if err := sh.validateAll(); err != nil {
		return nil, err
	}
	return sh, nil
}

func parseRootVars(body string, startLine int, vars map[string]string, maxVars int) error {
	line := startLine
	for _, stmt := range strings.Split(body, ";") {
		nl := strings.Count(stmt, "\n")
		trimmed := strings.TrimSpace(stmt)
		if trimmed == "" {
			line += nl
			continue
		}
		k, v, ok := strings.Cut(trimmed, ":")
		if !ok {
			return Diagnostic{Line: line, Message: fmt.Sprintf("invalid :root variable declaration %q", trimmed)}
		}
		name := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if !strings.HasPrefix(name, "--") || len(name) < 3 {
			return Diagnostic{Line: line, Message: fmt.Sprintf(":root only admits custom variables (--name), got %q", name)}
		}
		if strings.Contains(strings.ToLower(val), "url(") {
			return Diagnostic{Line: line, Message: "external url() references are forbidden"}
		}
		if _, exists := vars[name]; !exists && len(vars) >= maxVars {
			return Diagnostic{Line: line, Message: fmt.Sprintf(":root exceeds max custom variables (%d)", maxVars)}
		}
		vars[name] = val
		line += nl
	}
	return nil
}

func parseDeclarations(body string, startLine int, contract Contract) ([]declaration, error) {
	line := startLine
	var out []declaration
	for _, stmt := range strings.Split(body, ";") {
		nl := strings.Count(stmt, "\n")
		trimmed := strings.TrimSpace(stmt)
		if trimmed == "" {
			line += nl
			continue
		}
		k, v, ok := strings.Cut(trimmed, ":")
		if !ok {
			return nil, Diagnostic{Line: line, Message: fmt.Sprintf("invalid declaration %q (expected property: value)", trimmed)}
		}
		prop := strings.ToLower(strings.TrimSpace(k))
		val := strings.TrimSpace(v)
		if strings.Contains(strings.ToLower(val), "url(") {
			return nil, Diagnostic{Line: line, Message: fmt.Sprintf("property %q: external url() references are forbidden", prop)}
		}
		if !contract.AllowedProperties[prop] {
			return nil, Diagnostic{Line: line, Message: fmt.Sprintf("property %q is not admitted by Marlin UI style contract", prop)}
		}
		out = append(out, declaration{prop: prop, value: val, line: line})
		line += nl
	}
	return out, nil
}

func parseSelector(raw string, line int, contract Contract) (selector, error) {
	if raw == "" {
		return selector{}, Diagnostic{Line: line, Message: "empty selector"}
	}
	if strings.ContainsAny(raw, " \t>+~[]#*") {
		return selector{}, Diagnostic{Line: line, Message: fmt.Sprintf("selector %q: only flat compound class selectors (.class.mod:state) are admitted", raw)}
	}
	if !strings.HasPrefix(raw, ".") {
		return selector{}, Diagnostic{Line: line, Message: fmt.Sprintf("selector %q must start with '.' class selector", raw)}
	}

	// Split pseudo-classes from class list (e.g. ".tab.active:hover").
	base := raw
	var pseudo PseudoState
	if idx := strings.IndexByte(raw, ':'); idx >= 0 {
		base = raw[:idx]
		for _, ps := range strings.Split(raw[idx+1:], ":") {
			switch strings.ToLower(strings.TrimSpace(ps)) {
			case "hover":
				pseudo |= StateHover
			case "active":
				pseudo |= StateActive
			case "selected":
				pseudo |= StateSelected
			case "focus":
				pseudo |= StateFocus
			default:
				return selector{}, Diagnostic{Line: line, Message: fmt.Sprintf("unsupported pseudo-class :%s (allowed: :hover, :active, :selected, :focus)", ps)}
			}
		}
	}

	var classes []string
	for _, cls := range strings.Split(strings.TrimPrefix(base, "."), ".") {
		if !isValidIdent(cls) {
			return selector{}, Diagnostic{Line: line, Message: fmt.Sprintf("invalid class name %q in selector %q", cls, raw)}
		}
		if len(contract.AllowedClasses) > 0 && !contract.AllowedClasses[cls] {
			return selector{}, Diagnostic{Line: line, Message: fmt.Sprintf("class '.%s' is not declared in Marlin UI style contract", cls)}
		}
		classes = append(classes, cls)
	}
	if len(classes) == 0 {
		return selector{}, Diagnostic{Line: line, Message: fmt.Sprintf("selector %q has no classes", raw)}
	}
	spec := len(classes)*10 + popcount(uint8(pseudo))*5
	return selector{classes: classes, pseudo: pseudo, specificity: spec}, nil
}

func parseColor(raw string) (ui.Color, error) {
	s := strings.TrimSpace(strings.ToLower(raw))
	if s == "transparent" || s == "none" {
		return ui.Transparent, nil
	}
	if strings.HasPrefix(s, "#") {
		hex := s[1:]
		switch len(hex) {
		case 3:
			r, err1 := strconv.ParseUint(hex[0:1]+hex[0:1], 16, 8)
			g, err2 := strconv.ParseUint(hex[1:2]+hex[1:2], 16, 8)
			b, err3 := strconv.ParseUint(hex[2:3]+hex[2:3], 16, 8)
			if err1 != nil || err2 != nil || err3 != nil {
				return ui.Color{}, fmt.Errorf("invalid hex color %q", raw)
			}
			return ui.RGB(uint8(r), uint8(g), uint8(b)), nil
		case 6:
			r, err1 := strconv.ParseUint(hex[0:2], 16, 8)
			g, err2 := strconv.ParseUint(hex[2:4], 16, 8)
			b, err3 := strconv.ParseUint(hex[4:6], 16, 8)
			if err1 != nil || err2 != nil || err3 != nil {
				return ui.Color{}, fmt.Errorf("invalid hex color %q", raw)
			}
			return ui.RGB(uint8(r), uint8(g), uint8(b)), nil
		case 8:
			r, err1 := strconv.ParseUint(hex[0:2], 16, 8)
			g, err2 := strconv.ParseUint(hex[2:4], 16, 8)
			b, err3 := strconv.ParseUint(hex[4:6], 16, 8)
			a, err4 := strconv.ParseUint(hex[6:8], 16, 8)
			if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
				return ui.Color{}, fmt.Errorf("invalid hex color %q", raw)
			}
			return ui.RGBA(uint8(r), uint8(g), uint8(b), float32(a)/255.0), nil
		}
		return ui.Color{}, fmt.Errorf("invalid hex color %q", raw)
	}
	if strings.HasPrefix(s, "rgba(") && strings.HasSuffix(s, ")") {
		parts := strings.Split(s[5:len(s)-1], ",")
		if len(parts) != 4 {
			return ui.Color{}, fmt.Errorf("invalid rgba() %q", raw)
		}
		r, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		g, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		b, err3 := strconv.Atoi(strings.TrimSpace(parts[2]))
		af, err4 := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			return ui.Color{}, fmt.Errorf("invalid rgba() %q", raw)
		}
		alpha := float32(1.0)
		if af >= 0 && af <= 1.0 {
			alpha = float32(af)
		} else if af > 1.0 && af <= 255.0 {
			alpha = float32(af / 255.0)
		}
		return ui.RGBA(clampU8(r), clampU8(g), clampU8(b), alpha), nil
	}
	if strings.HasPrefix(s, "oklch(") && strings.HasSuffix(s, ")") {
		return parseOKLCH(raw)
	}
	if strings.HasPrefix(s, "rgb(") && strings.HasSuffix(s, ")") {
		parts := strings.Split(s[4:len(s)-1], ",")
		if len(parts) != 3 {
			return ui.Color{}, fmt.Errorf("invalid rgb() %q", raw)
		}
		r, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		g, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		b, err3 := strconv.Atoi(strings.TrimSpace(parts[2]))
		if err1 != nil || err2 != nil || err3 != nil {
			return ui.Color{}, fmt.Errorf("invalid rgb() %q", raw)
		}
		return ui.RGB(clampU8(r), clampU8(g), clampU8(b)), nil
	}
	return ui.Color{}, fmt.Errorf("unsupported color %q", raw)
}

func parsePx(raw string) (float32, error) {
	s := strings.TrimSpace(strings.TrimSuffix(strings.ToLower(raw), "px"))
	v, err := strconv.ParseFloat(s, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid numeric dimension %q", raw)
	}
	return float32(v), nil
}

func stripCommentsPreservingNewlines(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	for i := 0; i < len(src); {
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '*' {
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				if src[i] == '\n' {
					b.WriteByte('\n')
				}
				i++
			}
			if i+1 < len(src) {
				i += 2
			}
			continue
		}
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '/' && (i == 0 || isSpace(src[i-1])) {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		b.WriteByte(src[i])
		i++
	}
	return b.String()
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

func isValidIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
		if !ok {
			return false
		}
	}
	return true
}

func popcount(x uint8) int {
	n := 0
	for x != 0 {
		n += int(x & 1)
		x >>= 1
	}
	return n
}

func clampU8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}
