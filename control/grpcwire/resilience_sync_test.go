// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/engine/resilience"
)

func TestContinuityCoordinatorBackoffResumeAndIdempotentOutboxDrain(t *testing.T) {
	res := resilience.NewSessionResilience()
	scope := "chat:sess-backoff"
	// Record prior cursor so reconnect resumes strictly after ev-19.
	if dup, _ := res.AdmitEvent(scope, "ev-19", 3, 19); dup {
		t.Fatal("expected ev-19 admitted")
	}

	var (
		probeCalls   atomic.Int32
		resumedAfter string
		delivered    []string
		seenStates   []resilience.ConnState
		mu           sync.Mutex
	)

	// Fail first 2 probes, succeed on 3rd.
	coord := NewContinuityCoordinator(ContinuityConfig{
		ScopeID:        scope,
		Resilience:     res,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     40 * time.Millisecond,
		ProbeFn: func(_ context.Context) error {
			if probeCalls.Add(1) < 3 {
				return errors.New("dial tcp: connection refused")
			}
			return nil
		},
		ResumeCursorFn: func(_ context.Context, _ string, afterEventID string) error {
			mu.Lock()
			resumedAfter = afterEventID
			mu.Unlock()
			// Replay one duplicate (ev-19) and one fresh event (ev-20).
			if dup, _ := res.AdmitEvent(scope, "ev-19", 3, 19); !dup {
				t.Errorf("expected ev-19 deduplicated during cursor resume")
			}
			if dup, _ := res.AdmitEvent(scope, "ev-20", 3, 20); dup {
				t.Errorf("expected ev-20 admitted during cursor resume")
			}
			return nil
		},
		DeliverFn: func(_ context.Context, it resilience.OutboxItem) error {
			if probeCalls.Load() < 3 {
				return errors.New("transport unavailable")
			}
			mu.Lock()
			delivered = append(delivered, it.SubmissionID+":"+it.Text)
			mu.Unlock()
			return nil
		},
		OnStateChanged: func(snap resilience.ResilienceSnapshot) {
			mu.Lock()
			seenStates = append(seenStates, snap.State)
			mu.Unlock()
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coord.Run(ctx)

	// Submit while backend is down -> falls back to Outbox queue & kicks backoff loop.
	it1, sentNow, err := coord.SubmitOrQueue(ctx, "you", "", "First offline message")
	if err != nil {
		t.Fatalf("SubmitOrQueue 1: %v", err)
	}
	if sentNow {
		t.Fatal("expected first submit to queue while probe/deliver is down")
	}
	it2, sentNow2, err := coord.SubmitOrQueue(ctx, "you", "", "Second offline message")
	if err != nil {
		t.Fatalf("SubmitOrQueue 2: %v", err)
	}
	if sentNow2 {
		t.Fatal("expected second submit to queue while offline")
	}

	// Wait for backoff loop to recover, resume cursor, and drain both messages in order.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap := res.Snapshot(scope)
		if snap.State == resilience.ConnOnline && len(snap.PendingOutbox) == 0 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	finalSnap := res.Snapshot(scope)
	if finalSnap.State != resilience.ConnOnline {
		t.Fatalf("expected ConnOnline after backoff recovery, got %s", finalSnap.State)
	}
	if len(finalSnap.PendingOutbox) != 0 {
		t.Fatalf("expected outbox drained, got %d pending", len(finalSnap.PendingOutbox))
	}
	mu.Lock()
	defer mu.Unlock()
	if resumedAfter != "ev-19" {
		t.Fatalf("expected ResumeCursorFn called with ev-19, got %q", resumedAfter)
	}
	if cur := res.Cursor(scope); cur.LastEventID != "ev-20" {
		t.Fatalf("expected cursor advanced to ev-20, got %q", cur.LastEventID)
	}
	want1 := it1.SubmissionID + ":First offline message"
	want2 := it2.SubmissionID + ":Second offline message"
	if len(delivered) != 2 || delivered[0] != want1 || delivered[1] != want2 {
		t.Fatalf("expected FIFO idempotent delivery [%q %q], got %v", want1, want2, delivered)
	}
	sawReconnecting := false
	for _, st := range seenStates {
		if st == resilience.ConnReconnecting {
			sawReconnecting = true
			break
		}
	}
	if !sawReconnecting {
		t.Fatalf("expected ConnReconnecting notification during backoff, saw %v", seenStates)
	}
}

type dummyTargetProvider struct{}

func (dummyTargetProvider) CurrentTarget() *Target { return nil }

func TestContinuityCoordinatorWakesOnFederationTargetSwap(t *testing.T) {
	res := resilience.NewSessionResilience()
	scope := "chat:sess-swap"
	_, _ = res.AdmitEvent(scope, "ev-7", 1, 7)
	switcher := NewSwitchableTargetProvider("node-a", dummyTargetProvider{})
	switcher.Register("node-b", dummyTargetProvider{})

	resumed := make(chan string, 2)
	coord := NewContinuityCoordinator(ContinuityConfig{
		ScopeID:        scope,
		Resilience:     res,
		Provider:       switcher,
		InitialBackoff: 10 * time.Millisecond,
		ProbeFn:        func(_ context.Context) error { return nil },
		ResumeCursorFn: func(_ context.Context, _ string, afterEventID string) error {
			resumed <- afterEventID
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coord.Run(ctx)

	// Give WaitSwap watcher a moment to arm, then hot-swap target node-a -> node-b.
	time.Sleep(25 * time.Millisecond)
	ok, _ := switcher.SwitchTo("node-b")
	if !ok {
		t.Fatal("expected SwitchTo(node-b) to succeed")
	}

	select {
	case afterID := <-resumed:
		if afterID != "ev-7" {
			t.Fatalf("expected resume from ev-7 after node swap, got %q", afterID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for ContinuityCoordinator to resume after Federation node swap")
	}
}
