// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package bus

import (
	"testing"
	"time"
)

func TestCoalescingRingCollapsesBurstUpdates(t *testing.T) {
	ring := NewCoalescingRing(8, PolicyCoalesceByKey)
	for i := 0; i < 100; i++ {
		ring.Push(Envelope{
			Topic:       "org.tasks",
			CoalesceKey: "org.tasks",
			Payload:     []byte{byte(i)},
		})
	}
	pending, coalesced, dropped := ring.Stats()
	if pending != 1 || coalesced != 99 || dropped != 0 {
		t.Fatalf("expected pending=1 coalesced=99 dropped=0, got %d %d %d", pending, coalesced, dropped)
	}
	out := ring.Drain(nil, 10)
	if len(out) != 1 || out[0].Payload[0] != 99 {
		t.Fatalf("expected latest coalesced payload 99, got %+v", out)
	}
}

func TestTopicBusAndCircuitBreakerIsolation(t *testing.T) {
	b := NewTopicBus()
	now := time.Now()
	healthy := &Subscriber{
		PluginID: "tao3k.healthy",
		Ring:     NewCoalescingRing(8, PolicyDropOldest),
		Breaker:  NewCircuitBreaker(2, 5*time.Second),
	}
	faulty := &Subscriber{
		PluginID: "thirdparty.faulty",
		Ring:     NewCoalescingRing(8, PolicyDropOldest),
		Breaker:  NewCircuitBreaker(2, 5*time.Second),
	}
	b.Subscribe(healthy, "stream.delta")
	b.Subscribe(faulty, "stream.delta")

	// Trip faulty plugin's breaker.
	faulty.Breaker.RecordFailure(now)
	if !faulty.Breaker.RecordFailure(now) {
		t.Fatal("expected breaker to trip on 2nd failure")
	}

	stats := b.Publish(Envelope{Topic: "stream.delta", Payload: []byte("tok")}, now.Add(time.Second))
	if stats.Delivered != 1 || stats.Skipped != 1 {
		t.Fatalf("expected 1 delivered and 1 breaker-skipped, got %+v", stats)
	}

	// After cooldown, breaker enters half-open and allows a single probe.
	stats = b.Publish(Envelope{Topic: "stream.delta", Payload: []byte("probe")}, now.Add(6*time.Second))
	if stats.Delivered != 2 || stats.Skipped != 0 {
		t.Fatalf("expected half-open probe delivery, got %+v", stats)
	}
	faulty.Breaker.RecordSuccess()
	if faulty.Breaker.State() != BreakerClosed {
		t.Fatal("expected breaker closed after successful probe")
	}
}
