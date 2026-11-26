// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"context"
	"sync"

	"github.com/tao3k/marlin-mygo/control/transport"
)

// SwitchableTargetProvider multiplexes multiple registered gRPC targets behind
// one atomic TargetProvider + swapNotifier handle. Calling SwitchTo or
// ResolveFromRegistry increments Generation() and wakes every long-lived stream
// pump (SessionDriver, ProjectionStreamBridge, TurnStreamController) cleanly.
type SwitchableTargetProvider struct {
	mu         sync.RWMutex
	providers  map[string]TargetProvider
	activeID   string
	generation uint64
	swapCh     chan struct{}
}

// NewSwitchableTargetProvider constructs a SwitchableTargetProvider with an
// optional initial target provider.
func NewSwitchableTargetProvider(initialID string, initial TargetProvider) *SwitchableTargetProvider {
	p := &SwitchableTargetProvider{
		providers: make(map[string]TargetProvider),
		swapCh:    make(chan struct{}),
	}
	if initialID != "" && initial != nil {
		p.providers[initialID] = initial
		p.activeID = initialID
		p.generation = 1
	}
	return p
}

// Register binds a named TargetProvider so Federation Cockpit can switch to it.
func (s *SwitchableTargetProvider) Register(id string, p TargetProvider) {
	if s == nil || id == "" || p == nil {
		return
	}
	s.mu.Lock()
	s.providers[id] = p
	if s.activeID == "" {
		s.activeID = id
		s.bumpGenerationLocked()
	}
	s.mu.Unlock()
}

// SwitchTo atomically changes the active target ID and notifies waiting streams.
func (s *SwitchableTargetProvider) SwitchTo(id string) (bool, uint64) {
	if s == nil || id == "" {
		return false, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.providers[id]; !ok {
		return false, s.generation
	}
	s.activeID = id
	s.bumpGenerationLocked()
	return true, s.generation
}

// ResolveFromRegistry looks up id in reg, registers it if it implements
// TargetProvider (such as *grpcwire.Target or *ResilientSidecarTarget), and
// switches the active stream provider to it atomically.
func (s *SwitchableTargetProvider) ResolveFromRegistry(reg *transport.TargetRegistry, id string) (bool, uint64) {
	if s == nil || reg == nil || id == "" {
		return false, 0
	}
	t, err := reg.Switch(id)
	if err != nil {
		return false, s.Generation()
	}
	tp, ok := t.(TargetProvider)
	if !ok {
		return false, s.Generation()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providers[id] = tp
	s.activeID = id
	s.bumpGenerationLocked()
	return true, s.generation
}

// ReconnectActive bumps the generation without changing activeID, signaling
// resilient stream pumps to drop and re-establish streams immediately.
func (s *SwitchableTargetProvider) ReconnectActive() uint64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bumpGenerationLocked()
	return s.generation
}

// ActiveID returns the currently selected target ID.
func (s *SwitchableTargetProvider) ActiveID() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeID
}

// CurrentTarget satisfies TargetProvider for the currently active target.
func (s *SwitchableTargetProvider) CurrentTarget() *Target {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	p := s.providers[s.activeID]
	s.mu.RUnlock()
	if p == nil {
		return nil
	}
	return p.CurrentTarget()
}

// Generation returns the monotonic swap generation counter.
func (s *SwitchableTargetProvider) Generation() uint64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.generation
}

// WaitSwap blocks until a target switch/reconnect increments generation strictly
// above afterGen, or until ctx is cancelled.
func (s *SwitchableTargetProvider) WaitSwap(ctx context.Context, afterGen uint64) (uint64, bool) {
	if s == nil {
		return afterGen, false
	}
	for {
		s.mu.RLock()
		gen := s.generation
		ch := s.swapCh
		s.mu.RUnlock()
		if gen > afterGen {
			return gen, true
		}
		select {
		case <-ctx.Done():
			return gen, false
		case <-ch:
		}
	}
}

func (s *SwitchableTargetProvider) bumpGenerationLocked() {
	s.generation++
	close(s.swapCh)
	s.swapCh = make(chan struct{})
}
