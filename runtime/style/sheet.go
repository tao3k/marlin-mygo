// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package style

import (
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
)

// Sheet is a compiled, thread-safe, 120Hz-cached Marlin UI stylesheet.
type Sheet struct {
	vars     map[string]string
	rules    []rule
	contract Contract

	mu    sync.RWMutex
	cache map[string]ComputedStyle
}

// Var returns a resolved :root custom variable value (e.g. "--bg").
func (s *Sheet) Var(name string) (string, bool) {
	if s == nil {
		return "", false
	}
	v, ok := s.vars[name]
	return v, ok
}

// VarColor parses a :root custom color variable directly into ui.Color.
func (s *Sheet) VarColor(name string, fallback ui.Color) ui.Color {
	if s == nil {
		return fallback
	}
	raw, ok := s.vars[name]
	if !ok {
		return fallback
	}
	resolved, err := s.resolveValue(raw)
	if err != nil {
		return fallback
	}
	col, err := parseColor(resolved)
	if err != nil {
		return fallback
	}
	return col
}

// VarPx parses a :root custom numeric pixel variable (e.g. "--radius-control").
func (s *Sheet) VarPx(name string, fallback float32) float32 {
	if s == nil {
		return fallback
	}
	raw, ok := s.vars[name]
	if !ok {
		return fallback
	}
	resolved, err := s.resolveValue(raw)
	if err != nil {
		return fallback
	}
	px, err := parsePx(resolved)
	if err != nil {
		return fallback
	}
	return px
}

// Cascade creates a new immutable Sheet layering override on top of s
// (e.g. built-in Doom Emacs theme + user ~/.config/marlin/ui/theme.css).
func (s *Sheet) Cascade(override *Sheet) *Sheet {
	if s == nil {
		return override
	}
	if override == nil {
		return s
	}
	mergedVars := make(map[string]string, len(s.vars)+len(override.vars))
	maps.Copy(mergedVars, s.vars)
	maps.Copy(mergedVars, override.vars)

	mergedRules := make([]rule, 0, len(s.rules)+len(override.rules))
	mergedRules = append(mergedRules, s.rules...)
	offset := len(s.rules)
	for _, r := range override.rules {
		cp := r
		cp.order += offset
		mergedRules = append(mergedRules, cp)
	}
	return &Sheet{
		vars:     mergedVars,
		rules:    mergedRules,
		contract: s.contract,
		cache:    make(map[string]ComputedStyle, 32),
	}
}

// Resolve computes (and caches) the cascaded ComputedStyle for the given
// semantic class list and interactive pseudo-state.
func (s *Sheet) Resolve(classes []string, state PseudoState) ComputedStyle {
	if s == nil || len(classes) == 0 {
		return ComputedStyle{}
	}
	key := cacheKey(classes, state)
	s.mu.RLock()
	if cached, ok := s.cache[key]; ok {
		s.mu.RUnlock()
		return cached
	}
	s.mu.RUnlock()

	classSet := make(map[string]bool, len(classes))
	for _, c := range classes {
		classSet[c] = true
	}

	var matched []rule
	for _, r := range s.rules {
		if (r.sel.pseudo & state) != r.sel.pseudo {
			continue
		}
		allMatch := true
		for _, need := range r.sel.classes {
			if !classSet[need] {
				allMatch = false
				break
			}
		}
		if allMatch {
			matched = append(matched, r)
		}
	}

	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].sel.specificity != matched[j].sel.specificity {
			return matched[i].sel.specificity < matched[j].sel.specificity
		}
		return matched[i].order < matched[j].order
	})

	var st ComputedStyle
	for _, r := range matched {
		for _, d := range r.decls {
			_ = s.applyDecl(&st, d)
		}
	}

	s.mu.Lock()
	if len(s.cache) < 512 {
		s.cache[key] = st
	}
	s.mu.Unlock()
	return st
}

func (s *Sheet) validateAll() error {
	var scratch ComputedStyle
	for _, r := range s.rules {
		for _, d := range r.decls {
			if err := s.applyDecl(&scratch, d); err != nil {
				return Diagnostic{Line: d.line, Message: err.Error()}
			}
		}
	}
	return nil
}

func (s *Sheet) resolveValue(raw string) (string, error) {
	val := strings.TrimSpace(raw)
	for depth := 0; depth < 8 && strings.Contains(val, "var("); depth++ {
		start := strings.Index(val, "var(")
		end := strings.IndexByte(val[start:], ')')
		if end < 0 {
			return "", fmt.Errorf("unclosed var(...) in %q", raw)
		}
		inner := strings.TrimSpace(val[start+4 : start+end])
		varName, fallback, hasFallback := strings.Cut(inner, ",")
		varName = strings.TrimSpace(varName)
		rep, ok := s.vars[varName]
		if !ok {
			if hasFallback {
				rep = strings.TrimSpace(fallback)
			} else {
				return "", fmt.Errorf("undefined CSS variable %q", varName)
			}
		}
		val = val[:start] + rep + val[start+end+1:]
	}
	return strings.TrimSpace(val), nil
}

func (s *Sheet) applyDecl(st *ComputedStyle, d declaration) error {
	val, err := s.resolveValue(d.value)
	if err != nil {
		return err
	}
	switch d.prop {
	case "background", "background-color":
		col, err := parseColor(val)
		if err != nil {
			return err
		}
		st.HasBackground = true
		st.Background = col
	case "color":
		col, err := parseColor(val)
		if err != nil {
			return err
		}
		st.HasColor = true
		st.Color = col
	case "border-radius":
		v, err := parsePx(val)
		if err != nil {
			return err
		}
		st.HasBorderRadius = true
		st.BorderRadius = v
	case "border-width":
		v, err := parsePx(val)
		if err != nil {
			return err
		}
		st.HasBorderWidth = true
		st.BorderWidth = v
	case "border-color":
		col, err := parseColor(val)
		if err != nil {
			return err
		}
		st.HasBorderColor = true
		st.BorderColor = col
	case "padding":
		parts := strings.Fields(val)
		switch len(parts) {
		case 1:
			v, err := parsePx(parts[0])
			if err != nil {
				return err
			}
			st.HasPaddingY, st.PaddingY = true, v
			st.HasPaddingX, st.PaddingX = true, v
		case 2:
			vy, err1 := parsePx(parts[0])
			vx, err2 := parsePx(parts[1])
			if err1 != nil || err2 != nil {
				return fmt.Errorf("invalid padding %q", val)
			}
			st.HasPaddingY, st.PaddingY = true, vy
			st.HasPaddingX, st.PaddingX = true, vx
		default:
			return fmt.Errorf("padding accepts 1 or 2 values (y x), got %q", val)
		}
	case "padding-x":
		v, err := parsePx(val)
		if err != nil {
			return err
		}
		st.HasPaddingX, st.PaddingX = true, v
	case "padding-y":
		v, err := parsePx(val)
		if err != nil {
			return err
		}
		st.HasPaddingY, st.PaddingY = true, v
	case "gap":
		v, err := parsePx(val)
		if err != nil {
			return err
		}
		st.HasGap, st.Gap = true, v
	case "width":
		v, err := parsePx(val)
		if err != nil {
			return err
		}
		st.HasWidth, st.Width = true, v
	case "height":
		v, err := parsePx(val)
		if err != nil {
			return err
		}
		st.HasHeight, st.Height = true, v
	case "min-width":
		v, err := parsePx(val)
		if err != nil {
			return err
		}
		st.HasMinWidth, st.MinWidth = true, v
	case "min-height":
		v, err := parsePx(val)
		if err != nil {
			return err
		}
		st.HasMinHeight, st.MinHeight = true, v
	case "font-family":
		st.HasFontFamily = true
		st.FontFamily = strings.Trim(val, `"'`)
	case "font-size":
		v, err := parsePx(val)
		if err != nil {
			return err
		}
		st.HasFontSize, st.FontSize = true, v
	case "font-weight":
		switch strings.ToLower(val) {
		case "normal":
			st.HasFontWeight, st.FontWeight = true, 400
		case "medium":
			st.HasFontWeight, st.FontWeight = true, 500
		case "semibold":
			st.HasFontWeight, st.FontWeight = true, 600
		case "bold":
			st.HasFontWeight, st.FontWeight = true, 700
		default:
			w, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("invalid font-weight %q", val)
			}
			st.HasFontWeight, st.FontWeight = true, w
		}
	}
	return nil
}

func cacheKey(classes []string, state PseudoState) string {
	var b strings.Builder
	for i, c := range classes {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(c)
	}
	b.WriteByte(':')
	b.WriteByte(byte('0' + state))
	return b.String()
}
