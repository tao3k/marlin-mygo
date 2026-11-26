// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package async

import (
	"context"
	"sync"
	"sync/atomic"
)

// FencedLane is a bounded, generation-fenced coalescing async executor:
//   - Single-slot latest-wins coalescing prevents mailbox floods under bursts.
//   - Every Submit advances a monotonic epoch and cancels stale in-flight work.
//   - Completions carrying epoch < activeEpoch are deterministically dropped.
type FencedLane[Req, Res any] struct {
	mu           sync.Mutex
	activeEpoch  atomic.Uint64
	appliedEpoch atomic.Uint64
	hasPending   bool
	pendingReq   Req
	pendingEpoch uint64
	running      bool
	cancelRun    context.CancelFunc
	closed       bool
	drained      chan struct{}
	worker       func(ctx context.Context, req Req) (Res, error)
	onCommit     func(epoch uint64, res Res)
}

// NewFencedLane constructs a FencedLane backed by worker and onCommit.
func NewFencedLane[Req, Res any](
	worker func(ctx context.Context, req Req) (Res, error),
	onCommit func(epoch uint64, res Res),
) *FencedLane[Req, Res] {
	return &FencedLane[Req, Res]{
		worker:   worker,
		onCommit: onCommit,
	}
}

// Submit enqueues req under a fresh monotonic epoch, coalescing any queued
// request (latest-wins) and cancelling stale in-flight work.
func (l *FencedLane[Req, Res]) Submit(req Req) uint64 {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return 0
	}
	ep := l.activeEpoch.Add(1)
	l.pendingReq = req
	l.pendingEpoch = ep
	l.hasPending = true
	if l.cancelRun != nil {
		l.cancelRun()
	}
	if !l.running {
		l.running = true
		l.drained = make(chan struct{})
		go l.runLoop()
	}
	l.mu.Unlock()
	return ep
}

// AdvanceEpoch bumps the active epoch directly (e.g. on an immediate user
// interaction) so any older in-flight async completion is dropped.
func (l *FencedLane[Req, Res]) AdvanceEpoch() uint64 {
	l.mu.Lock()
	ep := l.activeEpoch.Add(1)
	l.appliedEpoch.Store(ep)
	if l.cancelRun != nil {
		l.cancelRun()
	}
	l.mu.Unlock()
	return ep
}

// ActiveEpoch returns the highest issued epoch.
func (l *FencedLane[Req, Res]) ActiveEpoch() uint64 { return l.activeEpoch.Load() }

// AppliedEpoch returns the highest committed epoch.
func (l *FencedLane[Req, Res]) AppliedEpoch() uint64 { return l.appliedEpoch.Load() }

// Close cancels in-flight work, rejects future submissions, and waits for drain.
func (l *FencedLane[Req, Res]) Close() {
	l.mu.Lock()
	if l.closed {
		ch := l.drained
		l.mu.Unlock()
		if ch != nil {
			<-ch
		}
		return
	}
	l.closed = true
	l.hasPending = false
	if l.cancelRun != nil {
		l.cancelRun()
	}
	ch := l.drained
	l.mu.Unlock()
	if ch != nil {
		<-ch
	}
}

func (l *FencedLane[Req, Res]) runLoop() {
	for {
		l.mu.Lock()
		if l.closed || !l.hasPending {
			l.running = false
			l.cancelRun = nil
			ch := l.drained
			l.mu.Unlock()
			if ch != nil {
				close(ch)
			}
			return
		}
		req := l.pendingReq
		ep := l.pendingEpoch
		l.hasPending = false
		ctx, cancel := context.WithCancel(context.Background())
		l.cancelRun = cancel
		l.mu.Unlock()

		res, err := l.worker(ctx, req)
		cancel()

		if err != nil {
			continue
		}
		l.mu.Lock()
		if !l.closed && ep == l.activeEpoch.Load() && ep >= l.appliedEpoch.Load() {
			l.appliedEpoch.Store(ep)
			cb := l.onCommit
			l.mu.Unlock()
			if cb != nil {
				cb(ep, res)
			}
			continue
		}
		l.mu.Unlock()
	}
}
