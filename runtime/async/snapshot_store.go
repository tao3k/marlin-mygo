// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package async provides deterministic, zero-CGO Go concurrency primitives for
// Marlin UI runtimes (Desktop, Mobile, Web WASM), refining the formal Quint
// concurrency models in @deterministic-engineering/quint-models:
//   - SnapshotStore[T]: lock-free RCU immutable UI snapshots (ui-mainthread-rcu-snapshot)
//   - FencedLane[Req, Res]: generation-fenced coalescing mailbox (fenced-coalescing-mailbox)
//   - PermitPool: disjoint bounded backpressure ownership (backpressure-memory)
//   - StructuredScope: lexical nursery with child-quiescence barrier (structured-cancellation)
//   - FramePacer & WebViewLifecycle: demand-driven 120Hz pacing & hybrid WebView isolation
package async

import (
	"sync"
	"sync/atomic"
)

// Snapshot carries an immutable UI state value paired with its monotonically
// published RCU generation number.
type Snapshot[T any] struct {
	Gen   uint64
	Value T
}

// SnapshotStore provides wait-free atomic.Pointer reads on the 120Hz GPU render
// main thread while serializing off-lock background staging and atomic swaps.
type SnapshotStore[T any] struct {
	ptr     atomic.Pointer[Snapshot[T]]
	writeMu sync.Mutex
}

// NewSnapshotStore initializes a SnapshotStore at generation 1.
func NewSnapshotStore[T any](initial T) *SnapshotStore[T] {
	s := &SnapshotStore[T]{}
	s.ptr.Store(&Snapshot[T]{Gen: 1, Value: initial})
	return s
}

// Load returns the latest published immutable snapshot without acquiring any
// mutex. Safe and O(1) on the 120Hz UI render thread.
func (s *SnapshotStore[T]) Load() *Snapshot[T] {
	return s.ptr.Load()
}

// Commit applies a fast pure state transition and atomically swaps the
// published snapshot pointer. Never perform disk or network I/O inside fn.
func (s *SnapshotStore[T]) Commit(fn func(prev T) T) *Snapshot[T] {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	cur := s.ptr.Load()
	var prev T
	var gen uint64
	if cur != nil {
		prev = cur.Value
		gen = cur.Gen
	}
	next := &Snapshot[T]{Gen: gen + 1, Value: fn(prev)}
	s.ptr.Store(next)
	return next
}

// StageAndPublish performs slow I/O or parsing completely off-lock in stage,
// then atomically merges the staged result into the latest published state.
func (s *SnapshotStore[T]) StageAndPublish(
	stage func(base T) (any, error),
	merge func(latest T, staged any) T,
) (*Snapshot[T], error) {
	baseSnap := s.ptr.Load()
	var base T
	if baseSnap != nil {
		base = baseSnap.Value
	}
	staged, err := stage(base)
	if err != nil {
		return baseSnap, err
	}
	return s.Commit(func(latest T) T {
		return merge(latest, staged)
	}), nil
}
