// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"context"
	"sync"

	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/async"
)

// QuickSwitcherStats records coalescing and warm-cache hit telemetry for
// sidebar quick-plugin switching.
type QuickSwitcherStats struct {
	TotalSelections uint64
	DedupSkips      uint64
	WarmHits        uint64
	ColdActivations uint64
	CoalescedDrops  uint64
}

// QuickPluginSwitcher eliminates rapid-click stutter in the sidebar plugin bar:
//   - Clicking the already-focused plugin is an O(1) no-op.
//   - Switching between already-warm plugins uses SlotRegistry.Focus() in O(1)
//     (~100 ns, zero WKWebView teardown/recreation, zero goroutines).
//   - Cold-loading a lazy plugin runs inside a single-flight latest-wins worker
//     so rapid click bursts drop stale intermediate targets instead of spawning
//     racing goroutines on the OS main thread.
type QuickPluginSwitcher struct {
	ctx          context.Context
	slots        *shell.SlotRegistry
	slotID       schema.SlotID
	activateFn   func(ctx context.Context, pluginID schema.PluginID) error
	onSync       func()
	onInvalidate func()
	onError      func(pluginID string, err error)

	mu        sync.Mutex
	activeID  string
	desiredID string
	running   bool
	stats     QuickSwitcherStats
	scope     *async.StructuredScope
	wg        sync.WaitGroup
}

// QuickPluginSwitcherConfig configures a QuickPluginSwitcher.
type QuickPluginSwitcherConfig struct {
	Context      context.Context
	Slots        *shell.SlotRegistry
	SlotID       schema.SlotID
	InitialID    string
	Activate     func(ctx context.Context, pluginID schema.PluginID) error
	OnSync       func()
	OnInvalidate func()
	OnError      func(pluginID string, err error)
}

func NewQuickPluginSwitcher(cfg QuickPluginSwitcherConfig) *QuickPluginSwitcher {
	ctx := cfg.Context
	if ctx == nil {
		ctx = context.Background()
	}
	slotID := cfg.SlotID
	if slotID == "" {
		slotID = "workbench.main"
	}
	return &QuickPluginSwitcher{
		ctx:          ctx,
		slots:        cfg.Slots,
		slotID:       slotID,
		activeID:     cfg.InitialID,
		activateFn:   cfg.Activate,
		onSync:       cfg.OnSync,
		onInvalidate: cfg.OnInvalidate,
		onError:      cfg.OnError,
		scope:        async.NewStructuredScope(ctx),
	}
}

// ActiveID returns the currently selected plugin ID for sidebar highlight rendering.
func (q *QuickPluginSwitcher) ActiveID() string {
	q.mu.Lock()
	id := q.activeID
	q.mu.Unlock()
	return id
}

// Stats returns a snapshot of warm-hit, deduplication, and coalescing counters.
func (q *QuickPluginSwitcher) Stats() QuickSwitcherStats {
	q.mu.Lock()
	st := q.stats
	q.mu.Unlock()
	return st
}

// Select switches the active plugin immediately when warm, or schedules a
// single-flight latest-wins activation when cold. Safe for rapid UI bursts.
func (q *QuickPluginSwitcher) Select(pluginID string) {
	if pluginID == "" {
		return
	}

	q.mu.Lock()
	q.stats.TotalSelections++
	if q.activeID == pluginID && q.desiredID == "" && q.slots != nil && q.slots.ActiveRendererID(q.slotID) == pluginID {
		q.stats.DedupSkips++
		q.mu.Unlock()
		return
	}
	q.activeID = pluginID

	// Fast path: plugin is already mounted warm in the slot deck -> O(1) focus swap.
	if q.slots != nil && q.slots.Focus(q.slotID, pluginID) {
		if q.desiredID != "" {
			q.stats.CoalescedDrops++
			q.desiredID = ""
		}
		q.stats.WarmHits++
		inv := q.onInvalidate
		q.mu.Unlock()
		if inv != nil {
			inv()
		}
		return
	}

	// Cold path: coalesce into single-flight latest-wins queue.
	if q.desiredID != "" && q.desiredID != pluginID {
		q.stats.CoalescedDrops++
	}
	q.desiredID = pluginID
	if q.running {
		inv := q.onInvalidate
		q.mu.Unlock()
		if inv != nil {
			inv()
		}
		return
	}
	q.running = true
	q.wg.Add(1)
	inv := q.onInvalidate
	q.mu.Unlock()

	if inv != nil {
		inv()
	}
	if !q.scope.Go(func(_ context.Context) { q.drain() }) {
		q.mu.Lock()
		q.running = false
		q.desiredID = ""
		q.mu.Unlock()
		q.wg.Done()
	}
}

func (q *QuickPluginSwitcher) drain() {
	defer q.wg.Done()
	for {
		q.mu.Lock()
		target := q.desiredID
		if target == "" {
			q.running = false
			q.mu.Unlock()
			return
		}
		q.desiredID = ""
		// Re-check warm deck in case it became resident while queued.
		if q.slots != nil && q.slots.Focus(q.slotID, target) {
			q.stats.WarmHits++
			inv := q.onInvalidate
			q.mu.Unlock()
			if inv != nil {
				inv()
			}
			continue
		}
		q.stats.ColdActivations++
		act := q.activateFn
		q.mu.Unlock()

		var err error
		if act != nil {
			err = act(q.ctx, schema.PluginID(target))
		}
		if err != nil {
			if q.onError != nil {
				q.onError(target, err)
			}
			continue
		}

		// Ensure the latest user-selected plugin stays focused even if the user
		// clicked a warm tab while this cold plugin was finishing activation.
		q.mu.Lock()
		latestActive := q.activeID
		if latestActive != "" && latestActive != target && q.slots != nil {
			q.slots.Focus(q.slotID, latestActive)
		}
		syncFn := q.onSync
		inv := q.onInvalidate
		q.mu.Unlock()

		if syncFn != nil {
			syncFn()
		}
		if inv != nil {
			inv()
		}
	}
}

// WaitIdle blocks until any in-flight cold activation drains (used by tests).
func (q *QuickPluginSwitcher) WaitIdle() {
	q.wg.Wait()
}

// Close cancels and drains any active cold-activation worker (quiescence barrier).
func (q *QuickPluginSwitcher) Close() {
	if q.scope != nil {
		q.scope.CloseAndDrain()
	}
}
