// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package async

import (
	"context"
	"errors"
	"sync"
)

// ErrPoolOverloaded indicates the bounded backpressure admission or queue
// ceiling has been reached.
var ErrPoolOverloaded = errors.New("async: bounded permit pool overloaded")

// PermitState tracks disjoint ownership states matching BackpressureOwners.qnt.
type PermitState uint8

const (
	PermitQueued PermitState = iota
	PermitRunning
	PermitReturned
	PermitCleanup
	PermitDone
)

// PermitPool conserves disjoint queued/running/returned/cleanup permits and
// retains capacity across non-abortable cancellation cleanup until Drain.
type PermitPool struct {
	mu         sync.Mutex
	limit      int
	queueLimit int
	states     map[uint64]PermitState
	queued     int
	nextID     uint64
}

// NewPermitPool creates a bounded PermitPool with explicit admission and queue limits.
func NewPermitPool(limit, queueLimit int) *PermitPool {
	if limit <= 0 {
		limit = 1
	}
	if queueLimit <= 0 {
		queueLimit = 1
	}
	return &PermitPool{
		limit:      limit,
		queueLimit: queueLimit,
		states:     make(map[uint64]PermitState, limit),
	}
}

// Admit acquires a Queued permit when below both admission and queue ceilings.
func (p *PermitPool) Admit() (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.states) >= p.limit || p.queued >= p.queueLimit {
		return 0, ErrPoolOverloaded
	}
	p.nextID++
	id := p.nextID
	p.states[id] = PermitQueued
	p.queued++
	return id, nil
}

// Start transitions a Queued permit to Running.
func (p *PermitPool) Start(id uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if st, ok := p.states[id]; !ok || st != PermitQueued {
		return false
	}
	p.states[id] = PermitRunning
	p.queued--
	return true
}

// Complete transitions a Running permit to Returned.
func (p *PermitPool) Complete(id uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if st, ok := p.states[id]; !ok || st != PermitRunning {
		return false
	}
	p.states[id] = PermitReturned
	return true
}

// BeginCancelCleanup transitions a Running permit to Cleanup while retaining
// its capacity permit until Drain.
func (p *PermitPool) BeginCancelCleanup(id uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if st, ok := p.states[id]; !ok || st != PermitRunning {
		return false
	}
	p.states[id] = PermitCleanup
	return true
}

// Drain releases a Returned or Cleanup permit cleanly into Done.
func (p *PermitPool) Drain(id uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.states[id]
	if !ok || (st != PermitReturned && st != PermitCleanup) {
		return false
	}
	delete(p.states, id)
	return true
}

// Stats returns active (admitted) and queued counts.
func (p *PermitPool) Stats() (admitted, queued int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.states), p.queued
}

// StructuredScope enforces structured concurrency quiescence (zero fire-and-
// forget goroutines; parent exit blocks until all children drain).
type StructuredScope struct {
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	closing  bool
	wg       sync.WaitGroup
	children int
}

// NewStructuredScope creates a structured concurrency nursery derived from parent.
func NewStructuredScope(parent context.Context) *StructuredScope {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &StructuredScope{ctx: ctx, cancel: cancel}
}

// Go spawns a tracked child goroutine bound to the scope. Returns false once
// CloseAndDrain has been initiated.
func (s *StructuredScope) Go(fn func(ctx context.Context)) bool {
	if fn == nil {
		return false
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return false
	}
	s.children++
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			s.children--
			s.mu.Unlock()
			s.wg.Done()
		}()
		fn(s.ctx)
	}()
	return true
}

// ActiveChildren reports currently running or draining child goroutines.
func (s *StructuredScope) ActiveChildren() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.children
}

// CloseAndDrain cancels the scope context and waits until all children drain.
func (s *StructuredScope) CloseAndDrain() {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
}
