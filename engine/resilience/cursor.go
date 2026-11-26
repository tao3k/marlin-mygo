// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package resilience

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ConnState describes the user-visible streaming & checkpoint continuity state.
type ConnState string

const (
	ConnOnline       ConnState = "ONLINE"
	ConnReconnecting ConnState = "RECONNECTING"
	ConnReplaying    ConnState = "RESUMING"
	ConnOffline      ConnState = "OFFLINE"
)

// ScopeCursor tracks the exact durable checkpoint resume position for one
// Chat session or Chat Room channel so reconnects never lose or duplicate data.
type ScopeCursor struct {
	ScopeID          string    `json:"scope_id"`
	LastEventID      string    `json:"last_event_id,omitempty"`
	LastTurnOrdinal  uint64    `json:"last_turn_ordinal,omitempty"`
	LastSequence     uint64    `json:"last_sequence,omitempty"`
	NeedsResync      bool      `json:"needs_resync,omitempty"`
	LastSyncedAt     time.Time `json:"last_synced_at,omitempty"`
}

// ResilienceSnapshot is an immutable 120Hz-safe status projection for UI banners.
type ResilienceSnapshot struct {
	State            ConnState
	ReconnectAttempt int
	ActiveCursor     ScopeCursor
	PendingOutbox    []OutboxItem
	LastError        string
}

// BannerText returns a concise, actionable continuity banner message, or empty when healthy & idle.
func (s ResilienceSnapshot) BannerText() string {
	queued := len(s.PendingOutbox)
	switch s.State {
	case ConnReconnecting:
		cur := s.ActiveCursor.LastEventID
		if cur == "" {
			cur = "start"
		}
		if queued > 0 {
			return fmt.Sprintf("Reconnecting (attempt #%d · cursor %s · %d queued)", s.ReconnectAttempt, cur, queued)
		}
		return fmt.Sprintf("Reconnecting (attempt #%d · resuming after %s)", s.ReconnectAttempt, cur)
	case ConnReplaying:
		return fmt.Sprintf("Resuming from checkpoint %s...", s.ActiveCursor.LastEventID)
	case ConnOffline:
		if queued > 0 {
			return fmt.Sprintf("Offline · %d message(s) safely queued for auto-send", queued)
		}
		return "Offline · messages will queue locally until connection resumes"
	default:
		if s.ActiveCursor.NeedsResync {
			return "Sequence gap detected · checkpoint resync requested"
		}
		if queued > 0 {
			return fmt.Sprintf("%d message(s) awaiting delivery retry", queued)
		}
		return ""
	}
}

const maxDedupPerScope = 2048

type dedupRing struct {
	seen map[string]struct{}
	ring []string
	head int
}

func (d *dedupRing) admit(id string) bool {
	if id == "" {
		return true
	}
	if d.seen == nil {
		d.seen = make(map[string]struct{}, 64)
	}
	if _, dup := d.seen[id]; dup {
		return false
	}
	if len(d.ring) >= maxDedupPerScope {
		delete(d.seen, d.ring[d.head])
		d.ring[d.head] = id
		d.head = (d.head + 1) % maxDedupPerScope
	} else {
		d.ring = append(d.ring, id)
	}
	d.seen[id] = struct{}{}
	return true
}

// DraftMention is a lightweight persisted `@` context reference restored with unsent drafts.
type DraftMention struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Label  string `json:"label"`
}

// SessionResilience coordinates live connection state, per-scope checkpoint
// cursors, duplicate suppression, sequence-gap detection, drafts, and Outbox.
type SessionResilience struct {
	mu               sync.RWMutex
	state            ConnState
	reconnectAttempt int
	lastError        string
	cursors          map[string]ScopeCursor
	dedups           map[string]*dedupRing
	drafts           map[string]string
	mentions         map[string][]DraftMention
	Outbox           *Outbox
}

// NewSessionResilience constructs an online continuity coordinator.
func NewSessionResilience() *SessionResilience {
	return &SessionResilience{
		state:    ConnOnline,
		cursors:  make(map[string]ScopeCursor),
		dedups:   make(map[string]*dedupRing),
		drafts:   make(map[string]string),
		mentions: make(map[string][]DraftMention),
		Outbox:   NewOutbox(),
	}
}

// MarkOnline transitions connection state to ConnOnline and clears transient error state.
func (r *SessionResilience) MarkOnline() {
	r.mu.Lock()
	r.state = ConnOnline
	r.reconnectAttempt = 0
	r.lastError = ""
	r.mu.Unlock()
}

// MarkOffline records an offline state so user sends queue gracefully in Outbox.
func (r *SessionResilience) MarkOffline(reason string) {
	r.mu.Lock()
	r.state = ConnOffline
	r.lastError = strings.TrimSpace(reason)
	r.mu.Unlock()
}

// MarkReconnecting updates reconnect telemetry and active checkpoint cursor.
func (r *SessionResilience) MarkReconnecting(scopeID string, attempt int, afterEventID string, err error) {
	r.mu.Lock()
	r.state = ConnReconnecting
	r.reconnectAttempt = attempt
	if err != nil {
		r.lastError = err.Error()
	}
	if scopeID != "" && afterEventID != "" {
		cur := r.cursors[scopeID]
		cur.ScopeID = scopeID
		cur.LastEventID = afterEventID
		r.cursors[scopeID] = cur
	}
	r.mu.Unlock()
}

// MarkReplaying marks a scope as actively replaying missed checkpoints from Rust.
func (r *SessionResilience) MarkReplaying(scopeID string) {
	r.mu.Lock()
	r.state = ConnReplaying
	if scopeID != "" {
		cur := r.cursors[scopeID]
		cur.ScopeID = scopeID
		r.cursors[scopeID] = cur
	}
	r.mu.Unlock()
}

// AdmitEvent records one streamed event into scopeID's deduplication ring and
// sequence tracker. Returns (duplicate=true) when eventID was already applied,
// and (gapDetected=true) when seq skips ahead of LastSequence+1.
func (r *SessionResilience) AdmitEvent(scopeID, eventID string, turnOrdinal, seq uint64) (duplicate bool, gapDetected bool) {
	if scopeID == "" {
		scopeID = "default"
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	ring := r.dedups[scopeID]
	if ring == nil {
		ring = &dedupRing{}
		r.dedups[scopeID] = ring
	}
	if eventID != "" && !ring.admit(eventID) {
		return true, false
	}

	cur := r.cursors[scopeID]
	cur.ScopeID = scopeID
	if seq > 0 && cur.LastSequence > 0 && seq > cur.LastSequence+1 {
		gapDetected = true
		cur.NeedsResync = true
	}
	if seq > cur.LastSequence {
		cur.LastSequence = seq
	}
	if turnOrdinal > cur.LastTurnOrdinal {
		cur.LastTurnOrdinal = turnOrdinal
	}
	if eventID != "" {
		cur.LastEventID = eventID
	}
	cur.LastSyncedAt = time.Now().UTC()
	r.cursors[scopeID] = cur
	return false, gapDetected
}

// ClearResync resets the NeedsResync flag after a successful checkpoint replay.
func (r *SessionResilience) ClearResync(scopeID string) {
	r.mu.Lock()
	if cur, ok := r.cursors[scopeID]; ok {
		cur.NeedsResync = false
		r.cursors[scopeID] = cur
	}
	if r.state == ConnReplaying {
		r.state = ConnOnline
	}
	r.mu.Unlock()
}

// Cursor returns the latest checkpoint cursor for scopeID.
func (r *SessionResilience) Cursor(scopeID string) ScopeCursor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cursors[scopeID]
}

// SetDraft stores an ephemeral unsent composer draft for scopeID.
func (r *SessionResilience) SetDraft(scopeID, text string) {
	if scopeID == "" {
		return
	}
	r.mu.Lock()
	if strings.TrimSpace(text) == "" {
		delete(r.drafts, scopeID)
	} else {
		r.drafts[scopeID] = text
	}
	r.mu.Unlock()
}

// Draft returns the saved unsent draft for scopeID, if any.
func (r *SessionResilience) Draft(scopeID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.drafts[scopeID]
}

// SetDraftMentions stores lightweight `@` context mentions staged alongside an unsent draft.
func (r *SessionResilience) SetDraftMentions(scopeID string, list []DraftMention) {
	if scopeID == "" {
		return
	}
	r.mu.Lock()
	if len(list) == 0 {
		delete(r.mentions, scopeID)
	} else {
		r.mentions[scopeID] = append([]DraftMention(nil), list...)
	}
	r.mu.Unlock()
}

// DraftMentions returns any persisted `@` context mentions for scopeID.
func (r *SessionResilience) DraftMentions(scopeID string) []DraftMention {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]DraftMention(nil), r.mentions[scopeID]...)
}

// Snapshot returns an immutable continuity snapshot for scopeID.
func (r *SessionResilience) Snapshot(scopeID string) ResilienceSnapshot {
	if r == nil {
		return ResilienceSnapshot{State: ConnOnline}
	}
	r.mu.RLock()
	snap := ResilienceSnapshot{
		State:            r.state,
		ReconnectAttempt: r.reconnectAttempt,
		ActiveCursor:     r.cursors[scopeID],
		LastError:        r.lastError,
	}
	r.mu.RUnlock()
	if r.Outbox != nil {
		snap.PendingOutbox = r.Outbox.PendingForScope(scopeID)
	}
	return snap
}
