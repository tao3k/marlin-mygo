// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package scheduler

import (
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

func TestFrameSchedulerCircularRingPreservesFIFOWraparoundAndResize(t *testing.T) {
	sched := NewFrameScheduler(32)
	var got []int
	// Push 10, drain 6 (advancing head past 0), then push 40 (forcing wraparound + resize).
	for i := 0; i < 10; i++ {
		v := i
		sched.Enqueue(Task{Priority: PriorityActive, Run: func() { got = append(got, v) }})
	}
	for i := 0; i < 6; i++ {
		tk, ok := sched.pop(PriorityActive)
		if !ok {
			t.Fatalf("expected pop %d", i)
		}
		tk.Run()
	}
	for i := 10; i < 50; i++ {
		v := i
		sched.Enqueue(Task{Priority: PriorityActive, Run: func() { got = append(got, v) }})
	}
	rep := sched.RunFrame(time.Second)
	if rep.ExecutedP1 != 44 || len(got) != 50 {
		t.Fatalf("expected 44 drained (50 total), got rep=%+v len=%d", rep, len(got))
	}
	for i := 0; i < 50; i++ {
		if got[i] != i {
			t.Fatalf("FIFO order broken at index %d: got %d", i, got[i])
		}
	}
}
