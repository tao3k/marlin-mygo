// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package async

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestMBTUiMainThreadRcuSnapshotNoTornReads verifies the Go SnapshotStore
// against the Quint UiMainThreadRcuSnapshot safety invariant: readers observe
// monotonically non-decreasing generations and never observe partially written
// (torn) snapshots under heavy concurrent writers.
func TestMBTUiMainThreadRcuSnapshotNoTornReads(t *testing.T) {
	type pair struct {
		A int
		B int
	}
	store := NewSnapshotStore(pair{A: 0, B: 0})

	const writers = 4
	const commitsPerWriter = 250
	var done atomic.Bool
	var wg sync.WaitGroup

	// Concurrent 120Hz render-thread readers performing wait-free atomic loads.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var lastGen uint64
			for !done.Load() {
				s := store.Load()
				if s.Gen < lastGen {
					t.Errorf("generation regressed: %d < %d", s.Gen, lastGen)
					return
				}
				if s.Value.A != s.Value.B {
					t.Errorf("torn snapshot observed: A=%d B=%d gen=%d", s.Value.A, s.Value.B, s.Gen)
					return
				}
				lastGen = s.Gen
			}
		}()
	}

	var wwg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wwg.Add(1)
		go func() {
			defer wwg.Done()
			for i := 0; i < commitsPerWriter; i++ {
				store.Commit(func(cur pair) pair {
					n := cur.A + 1
					return pair{A: n, B: n}
				})
			}
		}()
	}
	wwg.Wait()
	done.Store(true)
	wg.Wait()

	final := store.Load()
	if final.Value.A != writers*commitsPerWriter || final.Value.B != final.Value.A {
		t.Fatalf("expected final value %d, got %+v", writers*commitsPerWriter, final.Value)
	}
}

// TestMBTFencedLaneEpochMonotonicity verifies the Go FencedLane against the
// Quint MarlinFencedLaneOverlay invariant: stale (paused) requests cannot
// overwrite newer committed epochs on the UI thread.
func TestMBTFencedLaneEpochMonotonicity(t *testing.T) {
	c1Started := make(chan struct{}, 1)
	releaseC1 := make(chan struct{})
	var mu sync.Mutex
	var committed []int

	lane := NewFencedLane[int, int](
		func(ctx context.Context, req int) (int, error) {
			if req == 1 {
				select {
				case c1Started <- struct{}{}:
				default:
				}
				select {
				case <-releaseC1:
				case <-ctx.Done():
					return 0, ctx.Err()
				}
			}
			return req, nil
		},
		func(_ uint64, res int) {
			mu.Lock()
			committed = append(committed, res)
			mu.Unlock()
		},
	)
	defer lane.Close()

	// Submit C1 (paused), then supersede with C2, then release C1.
	lane.Submit(1)
	<-c1Started
	lane.Submit(2)
	close(releaseC1)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(committed)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(committed) == 0 || committed[len(committed)-1] != 2 {
		t.Fatalf("expected latest committed result 2 without stale C1 overwrite, got %v", committed)
	}
	for i := 1; i < len(committed); i++ {
		if committed[i] < committed[i-1] {
			t.Fatalf("non-monotonic UI commit sequence: %v", committed)
		}
	}
}

// TestMBTPermitPoolBoundedAdmissionAndCancelCleanup verifies Go PermitPool
// against the Quint MarlinPermitPoolOverlay safety invariant: active permits
// never exceed capacity, queue stays within queueLimit, and cancelled workers
// retain permits until explicit Drain.
func TestMBTPermitPoolBoundedAdmissionAndCancelCleanup(t *testing.T) {
	pool := NewPermitPool(2, 1)

	j0, err := pool.Admit()
	if err != nil {
		t.Fatalf("admit j0: %v", err)
	}
	// Queue limit is 1: second admit while j0 is still queued MUST fail closed.
	if _, err := pool.Admit(); err == nil {
		t.Fatalf("expected ErrPoolOverloaded when queueLimit=1 is full")
	}
	if !pool.Start(j0) {
		t.Fatalf("expected Start(j0) to succeed")
	}
	j1, err := pool.Admit()
	if err != nil {
		t.Fatalf("admit j1 after j0 started: %v", err)
	}
	// Total limit is 2: third admit while j0 running + j1 queued MUST fail closed.
	if _, err := pool.Admit(); err == nil {
		t.Fatalf("expected ErrPoolOverloaded when limit=2 is reached")
	}
	// Cancel j0 -> enters Cleanup and RETAINS permit until Drain!
	if !pool.BeginCancelCleanup(j0) {
		t.Fatalf("expected BeginCancelCleanup(j0) to succeed")
	}
	admitted, _ := pool.Stats()
	if admitted != 2 {
		t.Fatalf("expected cancelled job j0 to retain permit until Drain, admitted=%d", admitted)
	}
	if _, err := pool.Admit(); err == nil {
		t.Fatalf("expected over-capacity admit rejected while j0 is in Cleanup")
	}
	if !pool.Drain(j0) {
		t.Fatalf("expected Drain(j0) to succeed")
	}
	if !pool.Start(j1) || !pool.Complete(j1) || !pool.Drain(j1) {
		t.Fatalf("expected j1 lifecycle Start -> Complete -> Drain to succeed")
	}
	if admitted, queued := pool.Stats(); admitted != 0 || queued != 0 {
		t.Fatalf("expected clean empty pool after Drain, got admitted=%d queued=%d", admitted, queued)
	}
}

// TestMBTStructuredScopeQuiescenceBarrier verifies Go StructuredScope against
// the Quint MarlinStructuredScopeOverlay safety invariant: CloseAndDrain never
// returns while child tasks are still active (zero orphan effects).
func TestMBTStructuredScopeQuiescenceBarrier(t *testing.T) {
	scope := NewStructuredScope(context.Background())
	var closed atomic.Bool
	var orphanEffects atomic.Int32
	var completed atomic.Int32

	for i := 0; i < 6; i++ {
		if !scope.Go(func(ctx context.Context) {
			<-ctx.Done()
			time.Sleep(5 * time.Millisecond)
			if closed.Load() {
				orphanEffects.Add(1)
			}
			completed.Add(1)
		}) {
			t.Fatalf("expected scope.Go to admit child %d", i)
		}
	}

	scope.CloseAndDrain()
	closed.Store(true)

	if scope.Go(func(context.Context) {}) {
		t.Fatalf("expected scope.Go to reject spawns after CloseAndDrain")
	}
	if completed.Load() != 6 {
		t.Fatalf("expected all 6 children drained before CloseAndDrain returned, got %d", completed.Load())
	}
	if orphanEffects.Load() != 0 {
		t.Fatalf("detected %d orphan effects after CloseAndDrain", orphanEffects.Load())
	}
}

// TestMBTWebViewFocusLifecycleTierIsolation verifies Go WebViewLifecycle
// against the Quint HybridWebViewFocusLifecycle invariants: Tier-2 Untrusted
// islands never obtain privileged host RPC, hidden tabs revoke focus, and
// crashed WebViews recover cleanly.
func TestMBTWebViewFocusLifecycleTierIsolation(t *testing.T) {
	t0 := NewWebViewLifecycle(TrustTier0FirstParty)
	t2 := NewWebViewLifecycle(TrustTier2Untrusted)

	if !t2.RequestAsyncMount() || !t2.MarkReady() {
		t.Fatalf("expected Tier-2 island async mount -> ready")
	}
	if err := t2.CheckHostRPC(); err == nil {
		t.Fatalf("Tier-2 Untrusted WebView must NEVER be allowed privileged host RPC")
	}

	if !t0.RequestAsyncMount() || !t0.MarkReady() {
		t.Fatalf("expected Tier-0 studio async mount -> ready")
	}
	if err := t0.CheckHostRPC(); err != nil {
		t.Fatalf("Tier-0 First-Party Studio should allow host RPC when ready: %v", err)
	}
	if !t0.Focus() {
		t.Fatalf("expected Ready+Shown Tier-0 WebView to accept focus")
	}
	// HideTab must revoke keyboard focus back to native GPU frame.
	t0.HideTab()
	if _, shown, focused, _ := t0.Snapshot(); shown || focused {
		t.Fatalf("expected HideTab to clear shown & focused, got shown=%v focused=%v", shown, focused)
	}
	if t0.Focus() {
		t.Fatalf("hidden WebView must not accept focus")
	}
	// Crash & recover cycle: MarkCrashed revokes focus, Recover -> MarkReady restores Ready.
	t0.ShowTab()
	t0.Focus()
	t0.MarkCrashed()
	if st, _, focused, _ := t0.Snapshot(); st != WebViewCrashed || focused {
		t.Fatalf("expected WebViewCrashed & unfocused after crash, got st=%v focused=%v", st, focused)
	}
	if err := t0.CheckHostRPC(); err == nil {
		t.Fatalf("crashed WebView must not allow host RPC")
	}
	if !t0.Recover() || !t0.MarkReady() {
		t.Fatalf("expected Recover -> MarkReady to restore WebView")
	}
	if st, _, _, _ := t0.Snapshot(); st != WebViewReady {
		t.Fatalf("expected WebViewReady after recovery, got %v", st)
	}
}
