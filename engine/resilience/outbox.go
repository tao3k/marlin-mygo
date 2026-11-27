// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package resilience provides frontend continuity and checkpoint-recovery
// primitives for Chat Transcripts, Composers, and Multi-Role Chat Rooms:
//   - Bounded idempotent delivery outbox (offline queueing, retry, dedup)
//   - Per-scope stream checkpoint cursors with duplicate suppression & gap detection
//   - Crash-safe atomic local continuity snapshots (drafts + pending outbox + cursors)
//
// Durable conversation history remains exclusively owned by the Rust backend;
// this package persists ONLY ephemeral frontend continuity state.
package resilience

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrOutboxFull         = errors.New("resilience: offline outbox limit reached")
	ErrOutboxItemNotFound = errors.New("resilience: outbox item not found")
	ErrEmptyPayload       = errors.New("resilience: outbox payload cannot be empty")
)

const (
	maxOutboxItems   = 128
	maxRetryAttempts = 8
)

// DeliveryState models the optimistic delivery lifecycle of a user message or turn.
type DeliveryState string

const (
	DeliveryQueued  DeliveryState = "QUEUED"
	DeliverySending DeliveryState = "SENDING"
	DeliveryAcked   DeliveryState = "ACKED"
	DeliveryFailed  DeliveryState = "FAILED"
)

// OutboxItem represents one idempotently keyed outbound Chat turn or Chat Room message.
type OutboxItem struct {
	SubmissionID string        `json:"submission_id"`
	ScopeID      string        `json:"scope_id"` // e.g. "session:gui-session" or "room:ch-workspace"
	ThreadRootID string        `json:"thread_root_id,omitempty"`
	AuthorID     string        `json:"author_id,omitempty"`
	Text         string        `json:"text"`
	State        DeliveryState `json:"state"`
	Attempts     int           `json:"attempts"`
	LastError    string        `json:"last_error,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// Outbox is a bounded, thread-safe, idempotent FIFO queue for offline/retrying sends.
type Outbox struct {
	mu    sync.RWMutex
	items []OutboxItem
	seq   uint64
}

// NewOutbox creates an empty bounded delivery outbox.
func NewOutbox() *Outbox {
	return &Outbox{
		items: make([]OutboxItem, 0, 16),
	}
}

// Enqueue stages a new outbound message with a stable deterministic SubmissionID
// so reconnect retries are strictly idempotent on the Rust backend.
func (o *Outbox) Enqueue(scopeID, authorID, threadRootID, text string) (OutboxItem, error) {
	body := strings.TrimSpace(text)
	if body == "" {
		return OutboxItem{}, ErrEmptyPayload
	}
	scopeID = strings.TrimSpace(scopeID)
	if scopeID == "" {
		scopeID = "default"
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.items) >= maxOutboxItems {
		return OutboxItem{}, ErrOutboxFull
	}
	o.seq++
	now := time.Now().UTC()
	item := OutboxItem{
		SubmissionID: fmt.Sprintf("sub-%d-%d", now.UnixMilli(), o.seq),
		ScopeID:      scopeID,
		ThreadRootID: strings.TrimSpace(threadRootID),
		AuthorID:     strings.TrimSpace(authorID),
		Text:         body,
		State:        DeliveryQueued,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	o.items = append(o.items, item)
	return item, nil
}

// MarkSending transitions submissionID to DeliverySending and increments Attempts.
func (o *Outbox) MarkSending(submissionID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := range o.items {
		if o.items[i].SubmissionID == submissionID {
			o.items[i].State = DeliverySending
			o.items[i].Attempts++
			o.items[i].LastError = ""
			o.items[i].UpdatedAt = time.Now().UTC()
			return nil
		}
	}
	return ErrOutboxItemNotFound
}

// MarkAcked removes a confirmed message from the pending outbox.
func (o *Outbox) MarkAcked(submissionID string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := range o.items {
		if o.items[i].SubmissionID == submissionID {
			o.items = append(o.items[:i], o.items[i+1:]...)
			return true
		}
	}
	return false
}

// MarkFailed marks submissionID as DeliveryFailed (retaining its exact SubmissionID for retry).
func (o *Outbox) MarkFailed(submissionID string, err error) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := range o.items {
		if o.items[i].SubmissionID == submissionID {
			o.items[i].State = DeliveryFailed
			if err != nil {
				o.items[i].LastError = err.Error()
			}
			o.items[i].UpdatedAt = time.Now().UTC()
			return nil
		}
	}
	return ErrOutboxItemNotFound
}

// Discard explicitly removes a failed or queued item upon user request.
func (o *Outbox) Discard(submissionID string) bool {
	return o.MarkAcked(submissionID)
}

// Flush attempts to deliver all queued/failed items for scopeID (or all scopes
// if scopeID == "") in FIFO order using deliverFn. Items that succeed are
// removed; failures remain queued with DeliveryFailed and their original SubmissionID.
func (o *Outbox) Flush(scopeID string, deliverFn func(item OutboxItem) error) (acked int, failed int) {
	if deliverFn == nil {
		return 0, 0
	}
	pending := o.PendingForScope(scopeID)
	for _, it := range pending {
		if it.Attempts >= maxRetryAttempts && it.State == DeliveryFailed {
			// Reset attempt ceiling when user/reconnect explicitly flushes.
			o.mu.Lock()
			for i := range o.items {
				if o.items[i].SubmissionID == it.SubmissionID {
					o.items[i].Attempts = 0
					break
				}
			}
			o.mu.Unlock()
		}
		if err := o.MarkSending(it.SubmissionID); err != nil {
			continue
		}
		if err := deliverFn(it); err != nil {
			_ = o.MarkFailed(it.SubmissionID, err)
			failed++
		} else {
			o.MarkAcked(it.SubmissionID)
			acked++
		}
	}
	return acked, failed
}

// PendingForScope returns a FIFO snapshot of pending items for scopeID (or all if empty).
func (o *Outbox) PendingForScope(scopeID string) []OutboxItem {
	if o == nil {
		return nil
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	var out []OutboxItem
	for _, it := range o.items {
		if scopeID == "" || it.ScopeID == scopeID {
			out = append(out, it)
		}
	}
	return out
}

// Len returns total unacknowledged items across all scopes.
func (o *Outbox) Len() int {
	if o == nil {
		return 0
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	return len(o.items)
}

// Restore replaces pending items from a persisted continuity snapshot,
// normalizing any interrupted SENDING items back to QUEUED so they flush cleanly.
func (o *Outbox) Restore(items []OutboxItem) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.items = o.items[:0]
	for _, it := range items {
		if strings.TrimSpace(it.SubmissionID) == "" || strings.TrimSpace(it.Text) == "" {
			continue
		}
		if it.State == DeliverySending || it.State == "" {
			it.State = DeliveryQueued
		}
		o.items = append(o.items, it)
		if len(o.items) >= maxOutboxItems {
			break
		}
	}
}
