// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package bus

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestCoalescingRingPropertyBoundsAccountingAndKeyUniqueness(t *testing.T) {
	rng := rand.New(rand.NewPCG(77, 99))
	caps := []int{2, 4, 8, 16, 64}
	policies := []BackpressurePolicy{PolicyDropOldest, PolicyCoalesceByKey}

	for _, policy := range policies {
		for _, capSize := range caps {
			ring := NewCoalescingRing(capSize, policy)
			var totalPushed, totalDrained uint64
			latestByKey := make(map[string]uint64)

			for step := 0; step < 300; step++ {
				if rng.IntN(4) != 0 { // 75% push
					key := fmt.Sprintf("k-%d", rng.IntN(capSize+4))
					gen := uint64(step + 1)
					ring.Push(Envelope{
						EventID:     fmt.Sprintf("ev-%d", step),
						Topic:       "org.tasks",
						CoalesceKey: key,
						Generation:  gen,
					})
					totalPushed++
					latestByKey[key] = gen
				} else { // 25% drain
					drained := ring.Drain(nil, 1+rng.IntN(capSize))
					totalDrained += uint64(len(drained))
					if policy == PolicyCoalesceByKey {
						seen := make(map[string]struct{}, len(drained))
						for _, ev := range drained {
							if _, dup := seen[ev.CoalesceKey]; dup {
								t.Fatalf("duplicate coalesce key %q in single drain batch", ev.CoalesceKey)
							}
							seen[ev.CoalesceKey] = struct{}{}
							if ev.Generation != latestByKey[ev.CoalesceKey] {
								t.Fatalf("stale generation for coalesced key %q: got %d want %d", ev.CoalesceKey, ev.Generation, latestByKey[ev.CoalesceKey])
							}
						}
					}
				}
				pending, coalesced, dropped := ring.Stats()
				if pending > capSize {
					t.Fatalf("ring overflow: pending %d > cap %d", pending, capSize)
				}
				if got := totalDrained + uint64(pending) + coalesced + dropped; got != totalPushed {
					t.Fatalf("accounting mismatch: drained(%d)+pending(%d)+coalesced(%d)+dropped(%d)=%d != pushed(%d)",
						totalDrained, pending, coalesced, dropped, got, totalPushed)
				}
			}
		}
	}
}

func TestTopicBusConcurrentChaosProperty(t *testing.T) {
	tb := NewTopicBus()
	const pluginCount = 8
	subs := make([]*Subscriber, pluginCount)
	for i := 0; i < pluginCount; i++ {
		subs[i] = &Subscriber{
			PluginID: schema.PluginID(fmt.Sprintf("tao3k.p-%d", i)),
			Ring:     NewCoalescingRing(16, PolicyCoalesceByKey),
			Breaker:  NewCircuitBreaker(3, 5*time.Millisecond),
		}
		tb.Subscribe(subs[i], "stream.delta", "org.tasks")
	}

	var stop atomic.Bool
	var wg sync.WaitGroup

	// 4 concurrent publishers
	for p := 0; p < 4; p++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			now := time.Now()
			for i := 0; i < 400; i++ {
				tb.Publish(Envelope{
					EventID:     fmt.Sprintf("p%d-%d", id, i),
					Topic:       "stream.delta",
					CoalesceKey: fmt.Sprintf("slot-%d", i%6),
					Generation:  uint64(i + 1),
				}, now.Add(time.Duration(i)*time.Microsecond))
			}
		}(p)
	}

	// Dynamic subscribe/unsubscribe churner
	wg.Add(1)
	go func() {
		defer wg.Done()
		churnSub := &Subscriber{
			PluginID: "tao3k.churn",
			Ring:     NewCoalescingRing(8, PolicyDropOldest),
			Breaker:  NewCircuitBreaker(2, time.Millisecond),
		}
		for i := 0; i < 100; i++ {
			tb.Subscribe(churnSub, "stream.delta")
			tb.Unsubscribe("tao3k.churn")
		}
	}()

	// Fault-injection & recovery goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		now := time.Now()
		for i := 0; i < 60; i++ {
			b := subs[i%pluginCount].Breaker
			b.RecordFailure(now)
			b.RecordFailure(now)
			b.RecordFailure(now)
			b.RecordSuccess()
		}
	}()

	// Concurrent consumers draining rings
	for i := 0; i < pluginCount; i++ {
		wg.Add(1)
		go func(sub *Subscriber) {
			defer wg.Done()
			var buf []Envelope
			for !stop.Load() {
				buf = sub.Ring.Drain(buf[:0], 8)
				pending, _, _ := sub.Ring.Stats()
				if pending > 16 {
					t.Errorf("plugin %s ring exceeded capacity: %d", sub.PluginID, pending)
				}
			}
			_ = sub.Ring.Drain(buf[:0], 64)
		}(subs[i])
	}

	// Wait for publishers + churn + fault injector, then stop consumers.
	time.Sleep(25 * time.Millisecond)
	stop.Store(true)
	wg.Wait()

	for _, sub := range subs {
		pending, _, _ := sub.Ring.Stats()
		if pending > 16 {
			t.Fatalf("final ring overflow on %s: %d", sub.PluginID, pending)
		}
	}
}
