// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package async_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/runtime/async"
)

func TestSnapshotStoreWaitFreeDuringStaging(t *testing.T) {
	store := async.NewSnapshotStore("init")
	started := make(chan struct{})
	release := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = store.StageAndPublish(
			func(base string) (any, error) {
				close(started)
				<-release
				return base + "->next", nil
			},
			func(_ string, staged any) string { return staged.(string) },
		)
	}()

	<-started
	t0 := time.Now()
	s := store.Load()
	if d := time.Since(t0); d > 5*time.Millisecond {
		t.Fatalf("Load blocked during StageAndPublish: %v", d)
	}
	if s.Gen != 1 || s.Value != "init" {
		t.Fatalf("unexpected snap during stage: %+v", s)
	}
	close(release)
	wg.Wait()
	if s2 := store.Load(); s2.Gen != 2 || s2.Value != "init->next" {
		t.Fatalf("unexpected snap after publish: %+v", s2)
	}
}

func TestFencedLaneDropsStaleCompletions(t *testing.T) {
	started := make(chan int, 4)
	release := make(chan struct{})
	var got []int
	var mu sync.Mutex

	lane := async.NewFencedLane[int, int](
		func(ctx context.Context, req int) (int, error) {
			started <- req
			if req == 1 {
				<-release
			}
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			return req * 100, nil
		},
		func(_ uint64, res int) {
			mu.Lock()
			got = append(got, res)
			mu.Unlock()
		},
	)
	defer lane.Close()

	lane.Submit(1)
	<-started
	lane.Submit(2)
	ep3 := lane.Submit(3)
	close(release)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && lane.AppliedEpoch() != ep3 {
		time.Sleep(2 * time.Millisecond)
	}
	lane.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != 300 {
		t.Fatalf("expected only latest epoch [300], got %v", got)
	}
}

func TestPermitPoolAndStructuredScopeAndWebView(t *testing.T) {
	pool := async.NewPermitPool(2, 1)
	j0, err := pool.Admit()
	if err != nil {
		t.Fatalf("admit j0: %v", err)
	}
	if _, err := pool.Admit(); !errors.Is(err, async.ErrPoolOverloaded) {
		t.Fatalf("expected queue bound error")
	}
	pool.Start(j0)
	pool.BeginCancelCleanup(j0)
	j1, err := pool.Admit()
	if err != nil {
		t.Fatalf("admit j1: %v", err)
	}
	pool.Start(j1)
	if _, err := pool.Admit(); !errors.Is(err, async.ErrPoolOverloaded) {
		t.Fatalf("expected cleanup permit retention")
	}
	pool.Drain(j0)
	pool.Complete(j1)
	pool.Drain(j1)

	scope := async.NewStructuredScope(context.Background())
	var cleaned atomic.Bool
	scope.Go(func(ctx context.Context) {
		<-ctx.Done()
		cleaned.Store(true)
	})
	scope.CloseAndDrain()
	if !cleaned.Load() {
		t.Fatalf("child did not drain before parent exit")
	}

	wv := async.NewWebViewLifecycle(async.TrustTier2Untrusted)
	wv.RequestAsyncMount()
	wv.MarkReady()
	wv.Focus()
	if err := wv.CheckHostRPC(); !errors.Is(err, async.ErrUntrustedRPCForbidden) {
		t.Fatalf("expected Tier-2 RPC forbidden")
	}
	wv.HideTab()
	if _, shown, focused, _ := wv.Snapshot(); shown || focused {
		t.Fatalf("expected hidden tab focus revoked")
	}
}
