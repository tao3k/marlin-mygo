// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package scheduler

import (
	"fmt"
	"testing"
	"time"
)

func TestFrameSchedulerNeverStarvesP0UnderBackgroundFlood(t *testing.T) {
	sched := NewFrameScheduler(64)
	p0Ran := 0
	for i := 0; i < 50; i++ {
		sched.Enqueue(Task{
			Label:    "bg-plugin",
			Priority: PriorityBackground,
			Run:      func() { time.Sleep(200 * time.Microsecond) },
		})
	}
	for i := 0; i < 5; i++ {
		sched.Enqueue(Task{
			Label:    "keystroke",
			Priority: PriorityCritical,
			Run:      func() { p0Ran++ },
		})
	}

	rep := sched.RunFrame(1 * time.Millisecond)
	if rep.ExecutedP0 != 5 || p0Ran != 5 {
		t.Fatalf("expected all 5 P0 tasks executed, got %d", rep.ExecutedP0)
	}
	if rep.DeferredP2 == 0 {
		t.Fatal("expected excess P2 background tasks to defer past 80% budget cutoff")
	}
	pct := sched.Percentiles()
	if pct.Samples != 1 || pct.P99 <= 0 {
		t.Fatalf("unexpected percentiles: %+v", pct)
	}
}

func TestTaskQueueFIFOOrderAndCompaction(t *testing.T) {
	var q taskQueue
	for i := 0; i < 100; i++ {
		i := i
		q.push(Task{Label: fmt.Sprintf("t%d", i), Run: func() { _ = i }})
	}
	if q.len() != 100 {
		t.Fatalf("expected 100 queued, got %d", q.len())
	}
	// Pop 70 items and verify strict FIFO order.
	for want := 0; want < 70; want++ {
		task, ok := q.pop()
		if !ok {
			t.Fatalf("queue drained early at %d", want)
		}
		if task.Label != fmt.Sprintf("t%d", want) {
			t.Fatalf("FIFO order broken: got %q want t%d", task.Label, want)
		}
	}
	if q.len() != 30 {
		t.Fatalf("expected 30 remaining, got %d", q.len())
	}
	// The 70 popped head triggered compaction (head > 64 && head*2 >= len);
	// remaining items must still dequeue in order.
	for want := 70; want < 100; want++ {
		task, ok := q.pop()
		if !ok {
			t.Fatalf("queue drained early at %d", want)
		}
		if task.Label != fmt.Sprintf("t%d", want) {
			t.Fatalf("post-compaction order broken: got %q want t%d", task.Label, want)
		}
	}
	if _, ok := q.pop(); ok {
		t.Fatal("expected empty queue to report done")
	}
	if q.head != 0 || len(q.items) != 0 {
		t.Fatalf("expected fully drained queue reset, got head=%d len=%d", q.head, len(q.items))
	}

	// Reuse after reset must keep backing capacity without growth bugs.
	q.push(Task{Label: "reused", Run: func() {}})
	task, ok := q.pop()
	if !ok || task.Label != "reused" {
		t.Fatalf("expected reused task after reset, got ok=%v task=%+v", ok, task)
	}
}
