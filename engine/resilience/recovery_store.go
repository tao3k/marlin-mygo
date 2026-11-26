// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package resilience

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	RecoverySchemaVersion = 1
	maxRecoveryFileBytes  = 256 << 10 // 256 KiB fail-closed ceiling
)

var (
	ErrRecoveryTooLarge   = errors.New("resilience: recovery snapshot exceeds 256 KiB ceiling")
	ErrRecoveryBadVersion = errors.New("resilience: unsupported recovery snapshot schema version")
)

// RecoverySnapshot is the bounded, atomic JSON envelope persisted locally for
// frontend crash continuity (unsent drafts, unacknowledged outbox items, and
// checkpoint cursors). Never stores durable conversation history or credentials.
type RecoverySnapshot struct {
	Version int                    `json:"version"`
	SavedAt time.Time              `json:"saved_at"`
	Cursors map[string]ScopeCursor `json:"cursors,omitempty"`
	Drafts  map[string]string      `json:"drafts,omitempty"`
	Outbox  []OutboxItem           `json:"outbox,omitempty"`
}

// ExportSnapshot captures an immutable RecoverySnapshot from r.
func (r *SessionResilience) ExportSnapshot() RecoverySnapshot {
	r.mu.RLock()
	cursors := make(map[string]ScopeCursor, len(r.cursors))
	for k, v := range r.cursors {
		cursors[k] = v
	}
	drafts := make(map[string]string, len(r.drafts))
	for k, v := range r.drafts {
		drafts[k] = v
	}
	r.mu.RUnlock()

	return RecoverySnapshot{
		Version: RecoverySchemaVersion,
		SavedAt: time.Now().UTC(),
		Cursors: cursors,
		Drafts:  drafts,
		Outbox:  r.Outbox.PendingForScope(""),
	}
}

// ImportSnapshot restores cursors, unsent drafts, and unacknowledged outbox
// items into r after a crash or app restart.
func (r *SessionResilience) ImportSnapshot(snap RecoverySnapshot) error {
	if snap.Version != RecoverySchemaVersion {
		return fmt.Errorf("%w: %d", ErrRecoveryBadVersion, snap.Version)
	}
	r.mu.Lock()
	for k, v := range snap.Cursors {
		if k != "" {
			v.ScopeID = k
			r.cursors[k] = v
		}
	}
	for k, v := range snap.Drafts {
		if k != "" && v != "" {
			r.drafts[k] = v
		}
	}
	r.mu.Unlock()
	if r.Outbox != nil && len(snap.Outbox) > 0 {
		r.Outbox.Restore(snap.Outbox)
	}
	return nil
}

// EncodeRecoverySnapshot serializes snap to bounded JSON.
func EncodeRecoverySnapshot(snap RecoverySnapshot) ([]byte, error) {
	if snap.Version == 0 {
		snap.Version = RecoverySchemaVersion
	}
	raw, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(raw) > maxRecoveryFileBytes {
		return nil, ErrRecoveryTooLarge
	}
	return raw, nil
}

// DecodeRecoverySnapshot validates and parses a RecoverySnapshot fail-closed.
func DecodeRecoverySnapshot(raw []byte) (RecoverySnapshot, error) {
	if len(raw) == 0 || len(raw) > maxRecoveryFileBytes {
		return RecoverySnapshot{}, ErrRecoveryTooLarge
	}
	var snap RecoverySnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return RecoverySnapshot{}, fmt.Errorf("resilience: decode recovery snapshot: %w", err)
	}
	if snap.Version != RecoverySchemaVersion {
		return RecoverySnapshot{}, fmt.Errorf("%w: %d", ErrRecoveryBadVersion, snap.Version)
	}
	return snap, nil
}

// SaveFile atomically persists r's continuity snapshot via 0600 temp file + rename
// so a mid-write crash can never corrupt the recovery file.
func (r *SessionResilience) SaveFile(path string) error {
	raw, err := EncodeRecoverySnapshot(r.ExportSnapshot())
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".marlin-recovery-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	// fsync temp file before atomic rename so sudden power loss never yields a 0-byte or torn file.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// LoadFile reads and restores a continuity snapshot from path fail-closed.
func (r *SessionResilience) LoadFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() > maxRecoveryFileBytes {
		return ErrRecoveryTooLarge
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	snap, err := DecodeRecoverySnapshot(raw)
	if err != nil {
		return err
	}
	return r.ImportSnapshot(snap)
}
