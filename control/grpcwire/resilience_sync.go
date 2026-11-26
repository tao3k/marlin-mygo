// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/tao3k/marlin-mygo/engine/resilience"
)

// ContinuityConfig configures automatic reconnect backoff, LastEventID cursor
// replay, and idempotent Outbox draining for one durable session scope.
type ContinuityConfig struct {
	ScopeID        string
	Resilience     *resilience.SessionResilience
	Provider       TargetProvider
	InitialBackoff time.Duration // default 100ms
	MaxBackoff     time.Duration // default 4s

	// ProbeFn overrides target connectivity checks (defaults to Target.Ping).
	ProbeFn func(ctx context.Context) error
	// ResumeCursorFn replays missed events strictly after lastEventID once
	// connectivity is restored.
	ResumeCursorFn func(ctx context.Context, scopeID, afterEventID string) error
	// DeliverFn delivers one outbound turn/message with its stable SubmissionID.
	DeliverFn func(ctx context.Context, item resilience.OutboxItem) error
	// OnStateChanged fires whenever connection/backoff/outbox state changes so
	// Desktop, Mobile, and ChatRoom banners invalidate cleanly.
	OnStateChanged func(snap resilience.ResilienceSnapshot)
}

// ContinuityCoordinator drives exponential-backoff reconnection, Federation
// node swap detection, cursor-based stream resume, and idempotent outbox
// flushing for a single scope.
type ContinuityCoordinator struct {
	cfg    ContinuityConfig
	wakeCh chan struct{}

	mu      sync.Mutex
	attempt int
	running bool
}

// NewContinuityCoordinator constructs a ContinuityCoordinator with safe defaults.
func NewContinuityCoordinator(cfg ContinuityConfig) *ContinuityCoordinator {
	if strings.TrimSpace(cfg.ScopeID) == "" {
		cfg.ScopeID = "chat:default"
	}
	if cfg.Resilience == nil {
		cfg.Resilience = resilience.NewSessionResilience()
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 100 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 4 * time.Second
	}
	if cfg.MaxBackoff < cfg.InitialBackoff {
		cfg.MaxBackoff = cfg.InitialBackoff
	}
	return &ContinuityCoordinator{
		cfg:    cfg,
		wakeCh: make(chan struct{}, 1),
	}
}

// SubmitOrQueue attempts immediate delivery when online. If the transport is
// offline or delivery returns an error, the message is durably queued in the
// Outbox with a stable client SubmissionID and the reconnect loop is woken.
func (c *ContinuityCoordinator) SubmitOrQueue(
	ctx context.Context,
	authorID, threadRootID, text string,
) (resilience.OutboxItem, bool, error) {
	item, err := c.cfg.Resilience.Outbox.Enqueue(c.cfg.ScopeID, authorID, threadRootID, text)
	if err != nil {
		return resilience.OutboxItem{}, false, err
	}
	if c.cfg.Resilience.Snapshot(c.cfg.ScopeID).State == resilience.ConnOnline && c.cfg.DeliverFn != nil {
		if sendErr := c.cfg.DeliverFn(ctx, item); sendErr == nil {
			c.cfg.Resilience.Outbox.MarkAcked(item.SubmissionID)
			c.notifyState()
			return item, true, nil
		} else {
			_ = c.cfg.Resilience.Outbox.MarkFailed(item.SubmissionID, sendErr)
			c.cfg.Resilience.MarkOffline(sendErr.Error())
			c.notifyState()
			c.TriggerReconnect(sendErr.Error())
			return item, false, nil
		}
	}
	c.notifyState()
	c.TriggerReconnect("queued while offline")
	return item, false, nil
}

// TriggerReconnect immediately wakes the backoff loop (for example when the
// user clicks Retry Queue or switches Federation target).
func (c *ContinuityCoordinator) TriggerReconnect(reason string) {
	if c == nil {
		return
	}
	if reason != "" && c.cfg.Resilience.Snapshot(c.cfg.ScopeID).State == resilience.ConnOnline {
		cur := c.cfg.Resilience.Cursor(c.cfg.ScopeID)
		c.cfg.Resilience.MarkReconnecting(c.cfg.ScopeID, 1, cur.LastEventID, errors.New(reason))
		c.notifyState()
	}
	select {
	case c.wakeCh <- struct{}{}:
	default:
	}
}

// RunOnce executes one probe -> cursor resume -> outbox drain cycle. It returns
// nil when connectivity is healthy and all queued items have been drained.
func (c *ContinuityCoordinator) RunOnce(ctx context.Context) error {
	c.mu.Lock()
	c.attempt++
	attempt := c.attempt
	c.mu.Unlock()

	cur := c.cfg.Resilience.Cursor(c.cfg.ScopeID)
	if err := c.probe(ctx); err != nil {
		c.cfg.Resilience.MarkReconnecting(c.cfg.ScopeID, attempt, cur.LastEventID, err)
		c.notifyState()
		return err
	}

	if c.cfg.ResumeCursorFn != nil {
		c.cfg.Resilience.MarkReplaying(c.cfg.ScopeID)
		c.notifyState()
		if err := c.cfg.ResumeCursorFn(ctx, c.cfg.ScopeID, cur.LastEventID); err != nil {
			c.cfg.Resilience.MarkReconnecting(c.cfg.ScopeID, attempt, cur.LastEventID, err)
			c.notifyState()
			return err
		}
		c.cfg.Resilience.ClearResync(c.cfg.ScopeID)
	}

	if c.cfg.DeliverFn != nil && len(c.cfg.Resilience.Outbox.PendingForScope(c.cfg.ScopeID)) > 0 {
		var firstErr error
		_, failed := c.cfg.Resilience.Outbox.Flush(c.cfg.ScopeID, func(it resilience.OutboxItem) error {
			err := c.cfg.DeliverFn(ctx, it)
			if err != nil && firstErr == nil {
				firstErr = err
			}
			return err
		})
		if failed > 0 {
			if firstErr == nil {
				firstErr = errors.New("outbox delivery failed")
			}
			c.cfg.Resilience.MarkReconnecting(c.cfg.ScopeID, attempt, cur.LastEventID, firstErr)
			c.notifyState()
			return firstErr
		}
	}

	c.mu.Lock()
	c.attempt = 0
	c.mu.Unlock()
	c.cfg.Resilience.MarkOnline()
	c.notifyState()
	return nil
}

// Run drives the background reconnect backoff & swap-watch loop until ctx is
// cancelled. Safe to call in a goroutine.
func (c *ContinuityCoordinator) Run(ctx context.Context) {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()
	}()

	// Watch Federation / TargetProvider swaps so hot-swapping nodes triggers an
	// immediate cursor resume & outbox drain.
	if sw, ok := c.cfg.Provider.(swapNotifier); ok && sw != nil {
		go func() {
			gen := sw.Generation()
			for {
				nextGen, ok := sw.WaitSwap(ctx, gen)
				if !ok {
					return
				}
				gen = nextGen
				c.TriggerReconnect("target hot-swapped")
			}
		}()
	}

	backoff := c.cfg.InitialBackoff
	var timer *time.Timer
	stopTimer := func() {
		if timer != nil {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer = nil
		}
	}
	defer stopTimer()

	needsSync := func() bool {
		snap := c.cfg.Resilience.Snapshot(c.cfg.ScopeID)
		return snap.State != resilience.ConnOnline || len(snap.PendingOutbox) > 0
	}

	for {
		if needsSync() {
			if err := c.RunOnce(ctx); err != nil {
				stopTimer()
				timer = time.NewTimer(backoff)
				backoff *= 2
				if backoff > c.cfg.MaxBackoff {
					backoff = c.cfg.MaxBackoff
				}
				select {
				case <-ctx.Done():
					return
				case <-c.wakeCh:
					backoff = c.cfg.InitialBackoff
				case <-timer.C:
				}
				continue
			}
			backoff = c.cfg.InitialBackoff
		}

		select {
		case <-ctx.Done():
			return
		case <-c.wakeCh:
			backoff = c.cfg.InitialBackoff
		}
	}
}

func (c *ContinuityCoordinator) probe(ctx context.Context) error {
	if c.cfg.ProbeFn != nil {
		return c.cfg.ProbeFn(ctx)
	}
	if c.cfg.Provider != nil {
		t := c.cfg.Provider.CurrentTarget()
		if t == nil {
			return errors.New("no active gRPC target")
		}
		return t.Ping(ctx)
	}
	return nil
}

func (c *ContinuityCoordinator) notifyState() {
	if c.cfg.OnStateChanged != nil {
		c.cfg.OnStateChanged(c.cfg.Resilience.Snapshot(c.cfg.ScopeID))
	}
}
