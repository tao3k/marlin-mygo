// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package composer

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ContextUsage tracks backend-reported prompt/session token budget for one
// active model window so the composer displays live context headroom cleanly.
type ContextUsage struct {
	UsedTokens int
	MaxTokens  int
}

// Fraction returns clamped [0.0, 1.0] utilization ratio.
func (u ContextUsage) Fraction() float32 {
	if u.MaxTokens <= 0 || u.UsedTokens <= 0 {
		return 0
	}
	f := float32(u.UsedTokens) / float32(u.MaxTokens)
	if f > 1 {
		return 1
	}
	return f
}

// Label renders a compact budget pill (e.g. "34K / 200K · 17%").
func (u ContextUsage) Label() string {
	if u.MaxTokens <= 0 {
		return ""
	}
	pct := int(u.Fraction() * 100)
	return fmt.Sprintf("%s / %s · %d%%", formatTokenCount(u.UsedTokens), formatTokenCount(u.MaxTokens), pct)
}

func formatTokenCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dK", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// StreamMetrics tracks live streaming throughput (approx tokens/sec & elapsed)
// for the currently streaming assistant turn without blocking 120Hz renders.
type StreamMetrics struct {
	mu        sync.RWMutex
	active    bool
	startedAt time.Time
	chars     int
	elapsed   time.Duration
}

// Begin resets and starts streaming telemetry for a new assistant turn.
func (m *StreamMetrics) Begin() {
	m.mu.Lock()
	m.active = true
	m.startedAt = time.Now()
	m.chars = 0
	m.elapsed = 0
	m.mu.Unlock()
}

// RecordDelta records an incoming streamed text delta.
func (m *StreamMetrics) RecordDelta(delta string) {
	if delta == "" {
		return
	}
	m.mu.Lock()
	if !m.active {
		m.active = true
		m.startedAt = time.Now()
	}
	m.chars += len(delta)
	m.elapsed = time.Since(m.startedAt)
	m.mu.Unlock()
}

// Finish freezes elapsed time when the turn completes.
func (m *StreamMetrics) Finish() {
	m.mu.Lock()
	if m.active && !m.startedAt.IsZero() {
		m.elapsed = time.Since(m.startedAt)
	}
	m.active = false
	m.mu.Unlock()
}

// Label formats a compact throughput pill (e.g. "48 tok/s · 2.4s") or empty when idle/zero.
func (m *StreamMetrics) Label() string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	active := m.active
	chars := m.chars
	el := m.elapsed
	if active && !m.startedAt.IsZero() {
		el = time.Since(m.startedAt)
	}
	m.mu.RUnlock()
	if chars == 0 || el <= 0 {
		return ""
	}
	approxTokens := float64(chars) / 4.0
	if approxTokens < 1 {
		approxTokens = 1
	}
	secs := el.Seconds()
	if secs < 0.05 {
		secs = 0.05
	}
	tps := approxTokens / secs
	return fmt.Sprintf("%.0f tok/s · %.1fs", tps, el.Seconds())
}

// ParseContextWindowTokens parses human context labels advertised by models
// (e.g. "128K", "200K", "1M", "1.5M") into an integer token ceiling.
func ParseContextWindowTokens(label string) int {
	s := strings.ToUpper(strings.TrimSpace(label))
	if s == "" {
		return 0
	}
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "M"):
		mult = 1_000_000
		s = strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "K"):
		mult = 1_000
		s = strings.TrimSuffix(s, "K")
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v <= 0 {
		return 0
	}
	return int(v * mult)
}

// EstimateTokenCount returns a fast, allocation-free approximation of prompt or
// streamed response token count (~4 UTF-8 bytes/token) for UI context headroom.
func EstimateTokenCount(text string) int {
	n := len(strings.TrimSpace(text))
	if n == 0 {
		return 0
	}
	toks := n / 4
	if toks < 1 {
		return 1
	}
	return toks
}
