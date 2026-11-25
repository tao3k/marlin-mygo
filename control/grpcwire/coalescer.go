// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"sync"
	"sync/atomic"
	"time"
)

// DefaultFrameInterval targets 120Hz GPU presentation (~8.33ms per frame).
const DefaultFrameInterval = 8333 * time.Microsecond

// CoalescerStats reports coalescing efficiency for telemetry and tests.
type CoalescerStats struct {
	Signals uint64
	Flushes uint64
}

// FrameCoalescer collapses high-frequency gRPC stream bursts (e.g. 2,000+ Rust
// token/projection events per second) into bounded 120Hz UI invalidations
// without blocking the stream reader or dropping the trailing frame.
//
// The first MarkDirty() in an idle interval fires immediately (0ms leading-edge
// latency); subsequent signals arriving within Interval are coalesced into one
// trailing flush so the GPU UI stays silky-smooth at 120Hz.
type FrameCoalescer struct {
	interval time.Duration
	onFlush  func()

	dirty   atomic.Bool
	signals atomic.Uint64
	flushes atomic.Uint64
	closed  atomic.Bool

	wakeCh chan struct{}
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewFrameCoalescer creates and starts a bounded frame coalescer invoking
// onFlush at most once per interval (defaults to DefaultFrameInterval = 120Hz).
func NewFrameCoalescer(interval time.Duration, onFlush func()) *FrameCoalescer {
	if interval <= 0 {
		interval = DefaultFrameInterval
	}
	c := &FrameCoalescer{
		interval: interval,
		onFlush:  onFlush,
		wakeCh:   make(chan struct{}, 1),
		stopCh:   make(chan struct{}),
	}
	c.wg.Add(1)
	go c.loop()
	return c
}

// MarkDirty records that upstream gRPC state changed and schedules a coalesced
// UI invalidation. Safe for concurrent lock-free calls from any stream pump.
func (c *FrameCoalescer) MarkDirty() {
	if c == nil || c.closed.Load() {
		return
	}
	c.signals.Add(1)
	if !c.dirty.Swap(true) {
		select {
		case c.wakeCh <- struct{}{}:
		default:
		}
	}
}

// Flush immediately dispatches any pending dirty state synchronously.
func (c *FrameCoalescer) Flush() {
	if c == nil {
		return
	}
	if c.dirty.Swap(false) {
		c.flushes.Add(1)
		if c.onFlush != nil {
			c.onFlush()
		}
	}
}

// Stats returns atomic signal and flush counts.
func (c *FrameCoalescer) Stats() CoalescerStats {
	if c == nil {
		return CoalescerStats{}
	}
	return CoalescerStats{
		Signals: c.signals.Load(),
		Flushes: c.flushes.Load(),
	}
}

// Close flushes any final pending frame, stops the background loop, and waits
// for clean goroutine termination.
func (c *FrameCoalescer) Close() {
	if c == nil || !c.closed.CompareAndSwap(false, true) {
		return
	}
	close(c.stopCh)
	c.wg.Wait()
	c.Flush()
}

func (c *FrameCoalescer) loop() {
	defer c.wg.Done()
	for {
		select {
		case <-c.stopCh:
			return
		case <-c.wakeCh:
			// Leading-edge immediate flush for crisp 0ms first-token latency.
			c.Flush()
			// Coalesce burst arrivals during the rest of the 120Hz frame window.
			timer := time.NewTimer(c.interval)
			select {
			case <-c.stopCh:
				timer.Stop()
				return
			case <-timer.C:
				c.Flush()
			}
		}
	}
}
