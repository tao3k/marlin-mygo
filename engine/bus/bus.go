// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package bus implements the lock-free Copy-on-Write (COW) plugin event router,
// power-of-two coalescing backpressure ring buffers, and O(1) circuit breakers
// guaranteeing that slow or misbehaving TS/Go plugins never stall gRPC or UI frames.
package bus

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// BackpressurePolicy defines queue behavior when a plugin lags behind frame rate.
type BackpressurePolicy uint8

const (
	PolicyDropOldest    BackpressurePolicy = iota + 1 // Ring overwrites oldest entry
	PolicyCoalesceByKey                               // In-place overwrite for same coalesce key
)

// Envelope is one bounded event routed from Marlin gRPC into isolated plugins.
type Envelope struct {
	EventID     string
	Topic       string
	CoalesceKey string
	Generation  uint64
	Payload     []byte
}

// CoalescingRing is a bounded, power-of-two ring buffer with zero per-push
// heap allocations after initialization.
type CoalescingRing struct {
	mu        sync.Mutex
	buf       []Envelope
	mask      uint32
	head      uint32
	count     uint32
	policy    BackpressurePolicy
	keySlots  map[string]uint32
	dropped   uint64
	coalesced uint64
}

func NewCoalescingRing(minCapacity int, policy BackpressurePolicy) *CoalescingRing {
	capPow2 := uint32(1)
	if minCapacity < 2 {
		minCapacity = 2
	}
	for int(capPow2) < minCapacity {
		capPow2 <<= 1
	}
	r := &CoalescingRing{
		buf:    make([]Envelope, capPow2),
		mask:   capPow2 - 1,
		policy: policy,
	}
	if policy == PolicyCoalesceByKey {
		r.keySlots = make(map[string]uint32, capPow2)
	}
	return r
}

// Push enqueues ev in O(1), coalescing in-place or dropping the oldest item
// under backpressure rather than blocking the producer.
func (r *CoalescingRing) Push(ev Envelope) (coalesced bool, dropped bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.policy == PolicyCoalesceByKey && ev.CoalesceKey != "" {
		if slot, ok := r.keySlots[ev.CoalesceKey]; ok {
			r.buf[slot] = ev
			r.coalesced++
			return true, false
		}
	}

	capLen := r.mask + 1
	if r.count == capLen {
		// Evict oldest element to guarantee bounded memory and non-blocking producer.
		evicted := r.buf[r.head]
		if r.keySlots != nil && evicted.CoalesceKey != "" {
			delete(r.keySlots, evicted.CoalesceKey)
		}
		r.head = (r.head + 1) & r.mask
		r.count--
		r.dropped++
		dropped = true
	}

	tail := (r.head + r.count) & r.mask
	r.buf[tail] = ev
	if r.keySlots != nil && ev.CoalesceKey != "" {
		r.keySlots[ev.CoalesceKey] = tail
	}
	r.count++
	return false, dropped
}

// Drain appends up to maxItems pending events into dst in FIFO order.
func (r *CoalescingRing) Drain(dst []Envelope, maxItems int) []Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	if maxItems <= 0 || maxItems > int(r.count) {
		maxItems = int(r.count)
	}
	for i := 0; i < maxItems; i++ {
		ev := r.buf[r.head]
		r.buf[r.head] = Envelope{}
		if r.keySlots != nil && ev.CoalesceKey != "" {
			delete(r.keySlots, ev.CoalesceKey)
		}
		r.head = (r.head + 1) & r.mask
		r.count--
		dst = append(dst, ev)
	}
	return dst
}

func (r *CoalescingRing) Stats() (pending int, coalesced, dropped uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return int(r.count), r.coalesced, r.dropped
}

// BreakerState mirrors standard three-state fault isolation.
type BreakerState uint32

const (
	BreakerClosed   BreakerState = 0
	BreakerOpen     BreakerState = 1
	BreakerHalfOpen BreakerState = 2
)

// CircuitBreaker isolates plugins that repeatedly panic, trap, or miss frame budgets.
type CircuitBreaker struct {
	maxFailures uint32
	cooldown    time.Duration
	mu          sync.Mutex
	state       BreakerState
	failures    uint32
	openedAt    time.Time
}

func NewCircuitBreaker(maxFailures uint32, cooldown time.Duration) *CircuitBreaker {
	if maxFailures == 0 {
		maxFailures = 3
	}
	return &CircuitBreaker{maxFailures: maxFailures, cooldown: cooldown}
}

func (b *CircuitBreaker) Allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case BreakerClosed:
		return true
	case BreakerOpen:
		if now.Sub(b.openedAt) >= b.cooldown {
			b.state = BreakerHalfOpen
			return true
		}
		return false
	case BreakerHalfOpen:
		return false // single probe already in flight
	default:
		return false
	}
}

func (b *CircuitBreaker) RecordSuccess() {
	b.mu.Lock()
	b.failures = 0
	b.state = BreakerClosed
	b.mu.Unlock()
}

func (b *CircuitBreaker) RecordFailure(now time.Time) (tripped bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.state == BreakerHalfOpen || b.failures >= b.maxFailures {
		b.state = BreakerOpen
		b.openedAt = now
		return true
	}
	return false
}

func (b *CircuitBreaker) State() BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Subscriber is one registered plugin consumer attached to the TopicBus.
type Subscriber struct {
	PluginID schema.PluginID
	Ring     *CoalescingRing
	Breaker  *CircuitBreaker
}

type routeTable struct {
	byTopic map[string][]*Subscriber
}

// PublishStats summarizes one lock-free Publish fan-out.
type PublishStats struct {
	Delivered int
	Coalesced int
	Dropped   int
	Skipped   int // circuit breaker open
}

// TopicBus uses an atomic Copy-on-Write inverted routing table so Publish never
// acquires a global mutex on the hot streaming path.
type TopicBus struct {
	writeMu sync.Mutex
	table   atomic.Pointer[routeTable]
}

func NewTopicBus() *TopicBus {
	b := &TopicBus{}
	b.table.Store(&routeTable{byTopic: make(map[string][]*Subscriber)})
	return b
}

func (b *TopicBus) Subscribe(sub *Subscriber, topics ...string) {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()

	old := b.table.Load()
	nextMap := make(map[string][]*Subscriber, len(old.byTopic)+len(topics))
	for k, v := range old.byTopic {
		cp := make([]*Subscriber, len(v))
		copy(cp, v)
		nextMap[k] = cp
	}
	for _, topic := range topics {
		nextMap[topic] = append(nextMap[topic], sub)
	}
	b.table.Store(&routeTable{byTopic: nextMap})
}

func (b *TopicBus) Unsubscribe(pluginID schema.PluginID) {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()

	old := b.table.Load()
	nextMap := make(map[string][]*Subscriber, len(old.byTopic))
	for topic, subs := range old.byTopic {
		kept := make([]*Subscriber, 0, len(subs))
		for _, s := range subs {
			if s.PluginID != pluginID {
				kept = append(kept, s)
			}
		}
		if len(kept) > 0 {
			nextMap[topic] = kept
		}
	}
	b.table.Store(&routeTable{byTopic: nextMap})
}

// Publish routes ev to all subscribers of ev.Topic with zero bus-level locks.
func (b *TopicBus) Publish(ev Envelope, now time.Time) PublishStats {
	tbl := b.table.Load()
	subs := tbl.byTopic[ev.Topic]
	var stats PublishStats
	for _, s := range subs {
		if s.Breaker != nil && !s.Breaker.Allow(now) {
			stats.Skipped++
			continue
		}
		coalesced, dropped := s.Ring.Push(ev)
		if coalesced {
			stats.Coalesced++
		} else {
			stats.Delivered++
		}
		if dropped {
			stats.Dropped++
		}
	}
	return stats
}
