// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package style

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// ParseDesignTokensJSON compiles a W3C Design Tokens Community Group (DTCG)
// JSON document (as exported by Style Dictionary, Tokens Studio, Figma, or
// TypeScript Tailwind v4 OKLCH token pipelines) into a fail-closed *Sheet.
//
// Supported token shapes:
//
//	{
//	  "color": {
//	    "bg":      { "$type": "color", "$value": "oklch(0.24 0.02 255)" },
//	    "accent":  { "$type": "color", "$value": "#88c0d0" },
//	    "primary": { "$type": "color", "$value": "{color.accent}" }
//	  },
//	  "radius": {
//	    "card":    { "$type": "dimension", "$value": "20px" }
//	  }
//	}
//
// Semantic token paths (color.bg, color.surface, color.accent, radius.card,
// etc.) map automatically onto Marlin's canonical CSS custom properties (--bg,
// --surface, --accent, --radius-card, ...) so the resulting Sheet cascades
// directly over any built-in theme via Sheet.Cascade.
func ParseDesignTokensJSON(raw string, contract Contract) (*Sheet, error) {
	if contract.MaxBytes <= 0 {
		contract.MaxBytes = DefaultMaxBytes
	}
	if contract.MaxVariables <= 0 {
		contract.MaxVariables = DefaultMaxVariables
	}
	if len(raw) > contract.MaxBytes {
		return nil, Diagnostic{Message: fmt.Sprintf("tokens.json exceeds max allowed bytes (%d > %d)", len(raw), contract.MaxBytes)}
	}
	if strings.TrimSpace(raw) == "" {
		return &Sheet{
			vars:     map[string]string{},
			contract: contract,
			cache:    map[string]ComputedStyle{},
		}, nil
	}

	var root map[string]any
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil, Diagnostic{Message: fmt.Sprintf("invalid tokens.json: %v", err)}
	}
	if root == nil {
		return nil, Diagnostic{Message: "tokens.json root must be a JSON object"}
	}

	type rawToken struct {
		path  string
		typ   string
		value string
	}
	flat := make(map[string]rawToken)

	var walk func(prefix string, obj map[string]any, inheritedType string, depth int) error
	walk = func(prefix string, obj map[string]any, inheritedType string, depth int) error {
		if depth > 8 {
			return Diagnostic{Message: "tokens.json nesting depth exceeds limit (8)"}
		}
		curType := inheritedType
		if t, ok := obj["$type"].(string); ok && strings.TrimSpace(t) != "" {
			curType = strings.ToLower(strings.TrimSpace(t))
		}
		if v, hasVal := obj["$value"]; hasVal {
			if prefix == "" {
				return Diagnostic{Message: "top-level $value is not allowed in tokens.json"}
			}
			valStr, err := stringifyTokenValue(v)
			if err != nil {
				return Diagnostic{Message: fmt.Sprintf("token %q: %v", prefix, err)}
			}
			flat[prefix] = rawToken{path: prefix, typ: curType, value: valStr}
			return nil
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			if strings.HasPrefix(k, "$") {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !isValidIdent(k) {
				return Diagnostic{Message: fmt.Sprintf("invalid token key %q", k)}
			}
			nextPath := k
			if prefix != "" {
				nextPath = prefix + "." + k
			}
			child, ok := obj[k].(map[string]any)
			if !ok {
				return Diagnostic{Message: fmt.Sprintf("token group %q must be an object", nextPath)}
			}
			if err := walk(nextPath, child, curType, depth+1); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk("", root, "", 0); err != nil {
		return nil, err
	}
	if len(flat) > contract.MaxVariables {
		return nil, Diagnostic{Message: fmt.Sprintf("tokens.json exceeds max variable limit (%d > %d)", len(flat), contract.MaxVariables)}
	}

	// Resolve {alias.path} references with cycle detection.
	resolved := make(map[string]rawToken, len(flat))
	var resolve func(path string, stack map[string]bool) (rawToken, error)
	resolve = func(path string, stack map[string]bool) (rawToken, error) {
		if done, ok := resolved[path]; ok {
			return done, nil
		}
		tok, ok := flat[path]
		if !ok {
			return rawToken{}, Diagnostic{Message: fmt.Sprintf("undefined token alias {%s}", path)}
		}
		if stack[path] {
			return rawToken{}, Diagnostic{Message: fmt.Sprintf("cyclic token alias involving {%s}", path)}
		}
		val := strings.TrimSpace(tok.value)
		if strings.HasPrefix(val, "{") && strings.HasSuffix(val, "}") {
			ref := strings.TrimSpace(val[1 : len(val)-1])
			stack[path] = true
			target, err := resolve(ref, stack)
			delete(stack, path)
			if err != nil {
				return rawToken{}, err
			}
			if tok.typ == "" {
				tok.typ = target.typ
			}
			tok.value = target.value
		}
		resolved[path] = tok
		return tok, nil
	}

	paths := make([]string, 0, len(flat))
	for p := range flat {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	vars := make(map[string]string, len(paths)*2)
	for _, p := range paths {
		tok, err := resolve(p, map[string]bool{})
		if err != nil {
			return nil, err
		}
		if err := validateTokenValue(tok.path, tok.typ, tok.value); err != nil {
			return nil, Diagnostic{Message: err.Error()}
		}
		// Always expose flattened variable --a-b-c and canonical Marlin alias if mapped.
		cssVar := "--" + strings.ReplaceAll(strings.ToLower(tok.path), ".", "-")
		vars[cssVar] = tok.value
		if canon := canonicalTokenVar(tok.path); canon != "" {
			vars[canon] = tok.value
		}
	}

	return &Sheet{
		vars:     vars,
		contract: contract,
		cache:    make(map[string]ComputedStyle, 16),
	}, nil
}

func stringifyTokenValue(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x), nil
	case json.Number:
		return x.String(), nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case map[string]any:
		// W3C DTCG 2025.10 Color Object: { "colorSpace": "oklch"|"srgb", "components": [...], "alpha": 0.9 }
		return stringifyDTCGColorObject(x)
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			s, ok := item.(string)
			if !ok {
				return "", fmt.Errorf("array token values must be strings")
			}
			parts = append(parts, strings.TrimSpace(s))
		}
		return strings.Join(parts, ", "), nil
	default:
		return "", fmt.Errorf("unsupported $value type %T", v)
	}
}

func stringifyDTCGColorObject(obj map[string]any) (string, error) {
	cs, _ := obj["colorSpace"].(string)
	cs = strings.ToLower(strings.TrimSpace(cs))
	comps, ok := obj["components"].([]any)
	if !ok || len(comps) != 3 {
		return "", fmt.Errorf("DTCG color object requires colorSpace and 3 numeric components")
	}
	nums := make([]float64, 3)
	for i, c := range comps {
		switch n := c.(type) {
		case json.Number:
			f, err := n.Float64()
			if err != nil {
				return "", fmt.Errorf("invalid DTCG color component %v", c)
			}
			nums[i] = f
		case float64:
			nums[i] = n
		default:
			return "", fmt.Errorf("DTCG color component must be numeric, got %T", c)
		}
	}
	alpha := 1.0
	hasAlpha := false
	if a, ok := obj["alpha"]; ok {
		hasAlpha = true
		switch n := a.(type) {
		case json.Number:
			f, err := n.Float64()
			if err != nil {
				return "", fmt.Errorf("invalid DTCG alpha %v", a)
			}
			alpha = f
		case float64:
			alpha = n
		default:
			return "", fmt.Errorf("DTCG alpha must be numeric")
		}
	}
	switch cs {
	case "oklch":
		if hasAlpha {
			return fmt.Sprintf("oklch(%.4f %.4f %.2f / %.3f)", nums[0], nums[1], nums[2], alpha), nil
		}
		return fmt.Sprintf("oklch(%.4f %.4f %.2f)", nums[0], nums[1], nums[2]), nil
	case "srgb":
		r := int(math.Round(math.Max(0, math.Min(1, nums[0])) * 255))
		g := int(math.Round(math.Max(0, math.Min(1, nums[1])) * 255))
		b := int(math.Round(math.Max(0, math.Min(1, nums[2])) * 255))
		if hasAlpha {
			return fmt.Sprintf("rgba(%d, %d, %d, %.3f)", r, g, b, alpha), nil
		}
		return fmt.Sprintf("#%02x%02x%02x", r, g, b), nil
	default:
		return "", fmt.Errorf("unsupported DTCG colorSpace %q (expected oklch or srgb)", cs)
	}
}

func validateTokenValue(path, typ, val string) error {
	if val == "" {
		return fmt.Errorf("token %q has empty $value", path)
	}
	switch typ {
	case "color":
		if _, err := parseColor(val); err != nil {
			return fmt.Errorf("token %q: %w", path, err)
		}
	case "dimension":
		if _, err := parsePx(val); err != nil {
			return fmt.Errorf("token %q: %w", path, err)
		}
	case "fontweight", "number":
		if _, err := strconv.ParseFloat(val, 64); err != nil {
			low := strings.ToLower(val)
			if low != "normal" && low != "medium" && low != "semibold" && low != "bold" {
				return fmt.Errorf("token %q: invalid numeric/weight value %q", path, val)
			}
		}
	case "fontfamily", "":
		// Allow clean font family strings or untyped values if non-empty and free of url()/import injection.
		low := strings.ToLower(val)
		if strings.Contains(low, "url(") || strings.Contains(low, "@import") {
			return fmt.Errorf("token %q: forbidden external reference in value", path)
		}
	default:
		return fmt.Errorf("token %q: unsupported $type %q", path, typ)
	}
	return nil
}

func canonicalTokenVar(path string) string {
	switch strings.ToLower(path) {
	case "color.bg", "color.canvas", "surface.canvas":
		return "--bg"
	case "color.bar", "surface.bar":
		return "--bar"
	case "color.surface", "surface.card", "surface.elevated":
		return "--surface"
	case "color.hover", "surface.hover":
		return "--hover"
	case "color.border":
		return "--border"
	case "color.border-subtle", "color.border.subtle":
		return "--border-subtle"
	case "color.fg", "color.ink", "color.text":
		return "--fg"
	case "color.muted", "color.ink-muted", "color.text.muted":
		return "--muted"
	case "color.accent", "color.primary":
		return "--accent"
	case "color.info":
		return "--info"
	case "color.success":
		return "--success"
	case "color.warning":
		return "--warning"
	case "color.danger", "color.error":
		return "--danger"
	case "radius.card":
		return "--radius-card"
	case "radius.control":
		return "--radius-control"
	case "radius.small":
		return "--radius-small"
	case "radius.pill":
		return "--radius-pill"
	case "glass.opacity", "surface.glass-opacity":
		return "--glass-opacity"
	case "chrome.opacity", "surface.chrome-opacity":
		return "--chrome-opacity"
	case "viewport.opacity", "surface.viewport-opacity":
		return "--viewport-opacity"
	case "font.display", "typography.display":
		return "--font-display"
	case "font.mono", "typography.mono":
		return "--font-mono"
	case "spacing.card":
		return "--spacing-card"
	case "spacing.gap":
		return "--spacing-gap"
	default:
		return ""
	}
}

// ExportVars returns a defensive copy of all resolved CSS custom properties
// (`--*`) in the sheet so hybrid WebView islands and diagnostics can inherit
// the exact same W3C OKLCH Design Tokens as the Metal GPU surface.
func (s *Sheet) ExportVars() map[string]string {
	if s == nil || len(s.vars) == 0 {
		return nil
	}
	out := make(map[string]string, len(s.vars))
	for k, v := range s.vars {
		out[k] = v
	}
	return out
}

// parseOKLCH parses perceptual CSS Color 4 oklch(L C H [/ alpha]) syntax used by
// Tailwind v4, Apple HIG, and modern W3C Design Tokens, converting cleanly into
// clamped sRGB ui.Color with zero external dependencies.
func parseOKLCH(raw string) (ui.Color, error) {
	s := strings.TrimSpace(strings.ToLower(raw))
	if !strings.HasPrefix(s, "oklch(") || !strings.HasSuffix(s, ")") {
		return ui.Color{}, fmt.Errorf("invalid oklch() %q", raw)
	}
	inner := strings.TrimSpace(s[6 : len(s)-1])
	alpha := 1.0
	if lhs, rhs, hasSlash := strings.Cut(inner, "/"); hasSlash {
		inner = strings.TrimSpace(lhs)
		aStr := strings.TrimSpace(rhs)
		if strings.HasSuffix(aStr, "%") {
			pct, err := strconv.ParseFloat(strings.TrimSuffix(aStr, "%"), 64)
			if err != nil {
				return ui.Color{}, fmt.Errorf("invalid oklch alpha %q", raw)
			}
			alpha = pct / 100.0
		} else {
			a, err := strconv.ParseFloat(aStr, 64)
			if err != nil {
				return ui.Color{}, fmt.Errorf("invalid oklch alpha %q", raw)
			}
			alpha = a
		}
	}
	parts := strings.Fields(inner)
	if len(parts) != 3 {
		return ui.Color{}, fmt.Errorf("oklch() expects 3 channels (L C H), got %q", raw)
	}
	lStr := parts[0]
	var L float64
	var err error
	if strings.HasSuffix(lStr, "%") {
		L, err = strconv.ParseFloat(strings.TrimSuffix(lStr, "%"), 64)
		L /= 100.0
	} else {
		L, err = strconv.ParseFloat(lStr, 64)
	}
	if err != nil {
		return ui.Color{}, fmt.Errorf("invalid oklch lightness %q", raw)
	}
	C, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return ui.Color{}, fmt.Errorf("invalid oklch chroma %q", raw)
	}
	hStr := strings.TrimSuffix(parts[2], "deg")
	H, err := strconv.ParseFloat(hStr, 64)
	if err != nil {
		return ui.Color{}, fmt.Errorf("invalid oklch hue %q", raw)
	}

	// OKLCH -> Oklab -> linear sRGB -> gamma sRGB (Bjorn Ottosson reference matrix).
	rad := H * math.Pi / 180.0
	a := C * math.Cos(rad)
	b := C * math.Sin(rad)

	l_ := L + 0.3963377774*a + 0.2158037573*b
	m_ := L - 0.1055613458*a - 0.0638541728*b
	s_ := L - 0.0894841775*a - 1.2914855480*b

	l3 := l_ * l_ * l_
	m3 := m_ * m_ * m_
	s3 := s_ * s_ * s_

	lr := +4.0767416621*l3 - 3.3077115913*m3 + 0.2309699292*s3
	lg := -1.2684380046*l3 + 2.6097574011*m3 - 0.3413193965*s3
	lb := -0.0041960863*l3 - 0.7034186147*m3 + 1.7076147010*s3

	r := srgbByte(lr)
	g := srgbByte(lg)
	bl := srgbByte(lb)
	if alpha < 0 {
		alpha = 0
	}
	if alpha > 1 {
		alpha = 1
	}
	return ui.RGBA(r, g, bl, float32(alpha)), nil
}

func srgbByte(linear float64) uint8 {
	if linear <= 0 {
		return 0
	}
	if linear >= 1 {
		return 255
	}
	var gamma float64
	if linear <= 0.0031308 {
		gamma = 12.92 * linear
	} else {
		gamma = 1.055*math.Pow(linear, 1.0/2.4) - 0.055
	}
	v := math.Round(gamma * 255.0)
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}
