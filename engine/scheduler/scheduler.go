// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package scheduler enforces MAR-UI-R03 frame-budget priority scheduling:
// P0 control input and approval gates always execute immediately; P1 active
// viewport streaming runs within the frame budget; P2 background plugins defer
// cleanly when 80% of the frame budget is consumed, preserving 120Hz responsiveness.
package scheduler

import (
	"slices"
	"sync"
	"time"
)

type Priority uint8

const (
	PriorityCritical   Priority = 0 // Keystrokes, Stop, Approval Gate
	PriorityActive     Priority = 1 // Active viewport streaming / visible slot render
	PriorityBackground Priority = 2 // Offscreen / background Marketplace plugins
)

type Task struct {
	Label    string
	Priority Priority
	Run      func()
}

type FrameReport struct {
	ExecutedP0 int
	ExecutedP1 int
	ExecutedP2 int
	DeferredP1 int
	DeferredP2 int
	Duration   time.Duration
	OverBudget bool
}

type Percentiles struct {
	Samples int
	P50     time.Duration
	P95     time.Duration
	P99     time.Duration
}

// FrameScheduler executes priority-tiered work inside a fixed frame budget
// (e.g. 8.33ms for 120Hz or 16.67ms for 60Hz) and tracks rolling frame latency.
type FrameScheduler struct {
	mu      sync.Mutex
	queues  [3][]Task
	history []time.Duration
	histPos int
	histLen int
}

func NewFrameScheduler(historyWindow int) *FrameScheduler {
	if historyWindow < 16 {
		historyWindow = 256
	}
	return &FrameScheduler{
		history: make([]time.Duration, historyWindow),
	}
}

func (s *FrameScheduler) Enqueue(t Task) {
	if t.Run == nil {
		return
	}
	if t.Priority > PriorityBackground {
		t.Priority = PriorityBackground
	}
	s.mu.Lock()
	s.queues[t.Priority] = append(s.queues[t.Priority], t)
	s.mu.Unlock()
}

// RunFrame drains all P0 tasks unconditionally, P1 tasks up to budget, and P2
// tasks only within 80% of budget so GPU submission retains headroom.
func (s *FrameScheduler) RunFrame(budget time.Duration) FrameReport {
	start := time.Now()
	p2Cutoff := (budget * 4) / 5

	var rep FrameReport

	// 1. P0 Critical: drain unconditionally (never starved by slow plugins).
	for {
		t, ok := s.pop(PriorityCritical)
		if !ok {
			break
		}
		t.Run()
		rep.ExecutedP0++
	}

	// 2. P1 Active: drain while within frame budget.
	for time.Since(start) < budget {
		t, ok := s.pop(PriorityActive)
		if !ok {
			break
		}
		t.Run()
		rep.ExecutedP1++
	}

	// 3. P2 Background: drain only while P0/P1 empty and inside 80% headroom.
	for time.Since(start) < p2Cutoff {
		t, ok := s.pop(PriorityBackground)
		if !ok {
			break
		}
		t.Run()
		rep.ExecutedP2++
	}

	rep.Duration = time.Since(start)
	rep.OverBudget = rep.Duration > budget

	s.mu.Lock()
	rep.DeferredP1 = len(s.queues[PriorityActive])
	rep.DeferredP2 = len(s.queues[PriorityBackground])
	s.history[s.histPos] = rep.Duration
	s.histPos = (s.histPos + 1) % len(s.history)
	if s.histLen < len(s.history) {
		s.histLen++
	}
	s.mu.Unlock()
	return rep
}

func (s *FrameScheduler) pop(p Priority) (Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queues[p]
	if len(q) == 0 {
		return Task{}, false
	}
	t := q[0]
	copy(q, q[1:])
	q[len(q)-1] = Task{}
	s.queues[p] = q[:len(q)-1]
	return t, true
}

func (s *FrameScheduler) Percentiles() Percentiles {
	s.mu.Lock()
	n := s.histLen
	if n == 0 {
		s.mu.Unlock()
		return Percentiles{}
	}
	cp := make([]time.Duration, n)
	copy(cp, s.history[:n])
	s.mu.Unlock()

	slices.Sort(cp)
	at := func(pct float64) time.Duration {
		idx := int(float64(n-1) * pct)
		return cp[idx]
	}
	return Percentiles{
		Samples: n,
		P50:     at(0.50),
		P95:     at(0.95),
		P99:     at(0.99),
	}
}
