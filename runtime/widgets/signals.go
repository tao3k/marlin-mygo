// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package widgets provides Marlin MyGo's reusable, EWW-inspired declarative
// Bento widget framework: a reactive Signals bus, dual fail-closed S-expression
// (EWW/Scheme) and TOML parsers compiling into one typed Widget IR, and an
// iOS 26 / Material 3 Expressive GPU renderer built over runtime/kit and
// runtime/style.
package widgets

import (
	"fmt"
	"strconv"
	"strings"
)

// Signals holds live reactive key-value and numeric metric bindings consumed
// by Bento widgets (equivalent to EWW's defpoll/deflisten/defvar scope graph).
type Signals struct {
	text   map[string]string
	floats map[string]float64
}

// NewSignals constructs an empty reactive Signals table.
func NewSignals() *Signals {
	return &Signals{
		text:   make(map[string]string, 32),
		floats: make(map[string]float64, 16),
	}
}

// Set stores a string signal value.
func (s *Signals) Set(key, val string) {
	if s == nil {
		return
	}
	s.text[key] = val
}

// SetInt stores an integer signal as both text and float64.
func (s *Signals) SetInt(key string, v int) {
	if s == nil {
		return
	}
	s.text[key] = strconv.Itoa(v)
	s.floats[key] = float64(v)
}

// SetFloat stores a floating-point metric (e.g. "tasks.progress" in [0, 1]).
func (s *Signals) SetFloat(key string, v float64) {
	if s == nil {
		return
	}
	s.floats[key] = v
	s.text[key] = fmt.Sprintf("%.2f", v)
}

// Get returns a string signal or empty string if absent.
func (s *Signals) Get(key string) string {
	if s == nil {
		return ""
	}
	return s.text[key]
}

// Float resolves a numeric signal or parses a literal / interpolated float in [0, 1].
func (s *Signals) Float(expr string, fallback float64) float64 {
	raw := strings.TrimSpace(expr)
	if raw == "" {
		return fallback
	}
	if s != nil && strings.HasPrefix(raw, "{{") && strings.HasSuffix(raw, "}}") {
		key := strings.TrimSpace(raw[2 : len(raw)-2])
		if f, ok := s.floats[key]; ok {
			return clamp01(f)
		}
		raw = s.Get(key)
	} else if s != nil && strings.Contains(raw, "{{") {
		raw = s.Interpolate(raw)
	}
	raw = strings.TrimSpace(strings.TrimSuffix(raw, "%"))
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	if f > 1.0 && f <= 100.0 {
		f = f / 100.0
	}
	return clamp01(f)
}

// Interpolate expands all `{{signal.key}}` placeholders inside tpl.
func (s *Signals) Interpolate(tpl string) string {
	if s == nil || !strings.Contains(tpl, "{{") {
		return tpl
	}
	var out strings.Builder
	rest := tpl
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			out.WriteString(rest)
			break
		}
		out.WriteString(rest[:start])
		rest = rest[start+2:]
		end := strings.Index(rest, "}}")
		if end < 0 {
			out.WriteString("{{")
			out.WriteString(rest)
			break
		}
		key := strings.TrimSpace(rest[:end])
		if val, ok := s.text[key]; ok {
			out.WriteString(val)
		}
		rest = rest[end+2:]
	}
	return out.String()
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
