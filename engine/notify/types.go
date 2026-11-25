// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package notify implements Marlin's Tier-0 Host Notification, Toast, and
// Activity Center state engine with toast-storm deduplication, Do-Not-Disturb
// filtering, bounded ring-buffer history, and strict untrusted-plugin
// anti-spoofing and per-plugin rate limiting.
package notify

import (
	"errors"
	"strings"
	"time"
)

// Severity classifies the visual tone and DND urgency of a notification.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeveritySuccess Severity = "success"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// SourceKind identifies the architectural authority plane emitting a notification.
// Untrusted plugins are strictly confined to SourcePlugin and cannot spoof
// Tier-0 Host, Auth, Gate, or gRPC system alerts.
type SourceKind string

const (
	SourceHost   SourceKind = "host"
	SourceGRPC   SourceKind = "grpc"
	SourceAuth   SourceKind = "auth"
	SourceGate   SourceKind = "gate"
	SourcePlugin SourceKind = "plugin"
)

// FilterMode selects which items appear in the Activity Center drawer.
type FilterMode string

const (
	FilterAll     FilterMode = "all"
	FilterUnread  FilterMode = "unread"
	FilterAlerts  FilterMode = "alerts"
	FilterPlugins FilterMode = "plugins"
)

var (
	ErrEmptyTitle         = errors.New("notify: notification title is required")
	ErrPluginRateLimited  = errors.New("notify: plugin notification rate limit exceeded")
	ErrPluginIDRequired   = errors.New("notify: plugin source requires non-empty PluginID")
	ErrSpoofedHostSource  = errors.New("notify: untrusted plugin cannot emit Tier-0 host/auth/gate notification")
)

const (
	MaxTitleBytes   = 120
	MaxMessageBytes = 360
	MaxActions      = 3
)

// Action describes an inline CTA button rendered on a toast or Activity Center row.
type Action struct {
	ID      string
	Label   string
	Primary bool
}

// NotificationSpec is the input contract for publishing a notification.
type NotificationSpec struct {
	Severity    Severity
	Source      SourceKind
	PluginID    string        // required when Source == SourcePlugin
	Title       string
	Message     string
	DedupeKey   string        // optional key collapsing repeated alerts (e.g. "grpc:reconnect:h100")
	TTL         time.Duration // 0 uses severity default; < 0 means persistent until dismissed
	Persistent  bool          // never auto-dismissed (forced true for SourceGate)
	HasProgress bool
	Progress    float32 // 0.0 .. 1.0
	Actions     []Action
}

// Item is an immutable notification record stored in the Activity Center.
type Item struct {
	ID          uint64
	Severity    Severity
	Source      SourceKind
	PluginID    string
	Title       string
	Message     string
	DedupeKey   string
	Count       int
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ExpiresAt   time.Time // zero if persistent
	Persistent  bool
	Read        bool
	Dismissed   bool // hidden from floating toast stack (still kept in Activity Center history)
	HasProgress bool
	Progress    float32
	Actions     []Action
}

// Snapshot is an immutable, version-tagged view safe for 120Hz GPU rendering.
type Snapshot struct {
	Version        uint64
	DoNotDisturb   bool
	UnreadCount    int
	AlertCount     int // unread warnings + errors + gates
	FloatingToasts []Item
	History        []Item // newest-first bounded ring history
}

// sanitizeSingleLine strips control characters and enforces bounded byte length
// so untrusted payloads cannot forge multi-line layout spoofs.
func sanitizeText(raw string, maxBytes int) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(min(len(trimmed), maxBytes))
	for _, r := range trimmed {
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		} else if r < 0x20 || r == 0x7f {
			continue
		}
		if b.Len()+len(string(r)) > maxBytes {
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func defaultTTLFor(sev Severity, src SourceKind) (time.Duration, bool) {
	if src == SourceGate {
		return 0, true
	}
	switch sev {
	case SeverityError:
		return 0, true
	case SeverityWarning:
		return 8 * time.Second, false
	case SeveritySuccess:
		return 4 * time.Second, false
	default:
		return 5 * time.Second, false
	}
}
