// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package resilience

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineOutboxCrashRecoveryAndIdempotentFlush(t *testing.T) {
	r := NewSessionResilience()
	scope := "session:main"

	// 1. Offline send queues cleanly with deterministic SubmissionID + saves unsent draft.
	r.MarkOffline("network unreachable")
	r.SetDraft(scope, "half-typed follow up question")
	item, err := r.Outbox.Enqueue(scope, "you", "", "Audit the reconnect cursor")
	if err != nil || item.SubmissionID == "" || item.State != DeliveryQueued {
		t.Fatalf("unexpected enqueued item: %+v err=%v", item, err)
	}
	if err := r.Outbox.MarkSending(item.SubmissionID); err != nil {
		t.Fatalf("MarkSending: %v", err)
	}

	// Simulate checkpoint progress before crash.
	if dup, gap := r.AdmitEvent(scope, "ev-1", 1, 1); dup || gap {
		t.Fatalf("expected ev-1 clean admit, got dup=%v gap=%v", dup, gap)
	}
	if dup, gap := r.AdmitEvent(scope, "ev-2", 1, 2); dup || gap {
		t.Fatalf("expected ev-2 clean admit, got dup=%v gap=%v", dup, gap)
	}

	// 2. Atomic save -> simulate app crash -> restore into a fresh SessionResilience instance!
	dst := filepath.Join(t.TempDir(), "recovery.json")
	if err := r.SaveFile(dst); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	restored := NewSessionResilience()
	if err := restored.LoadFile(dst); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got := restored.Draft(scope); got != "half-typed follow up question" {
		t.Fatalf("expected restored draft, got %q", got)
	}
	if cur := restored.Cursor(scope); cur.LastEventID != "ev-2" || cur.LastSequence != 2 {
		t.Fatalf("expected restored cursor ev-2 seq=2, got %+v", cur)
	}
	pending := restored.Outbox.PendingForScope(scope)
	if len(pending) != 1 || pending[0].SubmissionID != item.SubmissionID || pending[0].State != DeliveryQueued {
		t.Fatalf("expected interrupted SENDING normalized back to QUEUED with identical SubmissionID, got %+v", pending)
	}

	// 3. First Flush fails transiently -> item stays FAILED with SAME SubmissionID; second Flush succeeds!
	var seenIDs []string
	acked, failed := restored.Outbox.Flush(scope, func(it OutboxItem) error {
		seenIDs = append(seenIDs, it.SubmissionID)
		return errors.New("transient 503")
	})
	if acked != 0 || failed != 1 {
		t.Fatalf("expected 0 acked / 1 failed, got %d/%d", acked, failed)
	}
	acked, failed = restored.Outbox.Flush(scope, func(it OutboxItem) error {
		seenIDs = append(seenIDs, it.SubmissionID)
		return nil
	})
	if acked != 1 || failed != 0 || restored.Outbox.Len() != 0 {
		t.Fatalf("expected outbox drained after reconnect flush, acked=%d failed=%d len=%d", acked, failed, restored.Outbox.Len())
	}
	if len(seenIDs) != 2 || seenIDs[0] != seenIDs[1] {
		t.Fatalf("idempotency violation: retry must reuse exact SubmissionID, got %v", seenIDs)
	}
}

func TestDuplicateReplaySuppressionSequenceGapAndCorruptFileFailClosed(t *testing.T) {
	r := NewSessionResilience()
	scope := "room:ch-workspace"

	if dup, gap := r.AdmitEvent(scope, "rm-1", 0, 1); dup || gap {
		t.Fatalf("expected rm-1 clean, got dup=%v gap=%v", dup, gap)
	}
	// Replaying rm-1 must be suppressed idempotently.
	if dup, _ := r.AdmitEvent(scope, "rm-1", 0, 1); !dup {
		t.Fatal("expected duplicate replay rm-1 suppressed")
	}
	// Skipping seq 2 -> seq 4 must flag gapDetected & NeedsResync!
	if dup, gap := r.AdmitEvent(scope, "rm-4", 0, 4); dup || !gap {
		t.Fatalf("expected sequence gap detected on seq 1->4, got dup=%v gap=%v", dup, gap)
	}
	if !r.Cursor(scope).NeedsResync {
		t.Fatal("expected NeedsResync=true after sequence gap")
	}
	if b := r.Snapshot(scope).BannerText(); !strings.Contains(b, "Sequence gap") {
		t.Fatalf("expected sequence gap banner, got %q", b)
	}
	r.ClearResync(scope)
	if r.Cursor(scope).NeedsResync {
		t.Fatal("expected NeedsResync cleared")
	}

	// Corrupt recovery file fails closed cleanly without panicking.
	bad := filepath.Join(t.TempDir(), "corrupt.json")
	if err := os.WriteFile(bad, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.LoadFile(bad); err == nil {
		t.Fatal("expected corrupt recovery snapshot rejected fail-closed")
	}
}

func FuzzDecodeRecoverySnapshot(f *testing.F) {
	f.Add([]byte(`{"version":1,"cursors":{"s1":{"scope_id":"s1","last_event_id":"ev-9","last_sequence":9}}}`))
	f.Add([]byte(`{"version":99}`))
	f.Add([]byte(`{`))
	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = DecodeRecoverySnapshot(data)
	})
}
