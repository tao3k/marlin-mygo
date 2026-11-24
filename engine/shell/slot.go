// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package shell implements the modular MyGo workbench shell:
// Scheme-declared SlotRegistry (single/list/keyed), generation-gated workbench
// state, and automatic native fallback when Marketplace plugins unmount.
package shell

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

var (
	ErrSlotNotDeclared    = errors.New("shell: slot is not declared")
	ErrUntrustedForbidden = errors.New("shell: slot does not allow untrusted plugins")
	ErrStaleGeneration    = errors.New("shell: rejected stale workbench generation")
)

// RenderContext carries frame-scoped presentation data into a SlotRenderer.
// Host adapters wrap mygo's *ui.Context inside Surface.
type RenderContext struct {
	Surface   any
	Theme     schema.UITheme
	TargetID  string
	SessionID string
	Facts     map[string]bool
	Bindings  map[string]string
}

// HostEvent represents a mediated UI event routed from the shell to a slot.
type HostEvent struct {
	Kind    string
	Command string
	Payload []byte
}

// SlotRenderer is the uniform visual contract implemented by native MyGo views,
// the Scheme UI IR interpreter, Untrusted WebView hosts, and wazero WASM-IR hosts.
type SlotRenderer interface {
	ID() string
	Trusted() bool
	Render(ctx RenderContext)
	HandleEvent(ev HostEvent) error
	Dispose()
}

type slotEntry struct {
	decl     schema.SlotDeclaration
	fallback SlotRenderer
	single   SlotRenderer
	list     []SlotRenderer
	keyed    map[string]SlotRenderer
	keyOrder []string
}

// SlotRegistry manages hot-pluggable UI slots with deterministic ordering and
// guaranteed fallback to the Scheme/native renderer.
type SlotRegistry struct {
	mu    sync.RWMutex
	slots map[schema.SlotID]*slotEntry
}

func NewSlotRegistry() *SlotRegistry {
	return &SlotRegistry{slots: make(map[schema.SlotID]*slotEntry)}
}

// Declare registers or refreshes a Scheme-declared slot with its native fallback.
func (s *SlotRegistry) Declare(decl schema.SlotDeclaration, fallback SlotRenderer) error {
	if err := schema.ValidateDotIdentifier(string(decl.SlotID)); err != nil {
		return err
	}
	if fallback == nil {
		return fmt.Errorf("shell: slot %q requires a non-nil fallback renderer", decl.SlotID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.slots[decl.SlotID]; ok {
		existing.decl = decl
		existing.fallback = fallback
		return nil
	}
	s.slots[decl.SlotID] = &slotEntry{
		decl:     decl,
		fallback: fallback,
		keyed:    make(map[string]SlotRenderer),
	}
	return nil
}

// Mount hot-plugs a renderer into a declared slot and returns an idempotent unmount hook.
func (s *SlotRegistry) Mount(slotID schema.SlotID, key string, r SlotRenderer) (func(), error) {
	if r == nil {
		return nil, fmt.Errorf("shell: cannot mount nil renderer")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.slots[slotID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSlotNotDeclared, slotID)
	}
	if !r.Trusted() && !entry.decl.AllowUntrustedPlugin {
		return nil, fmt.Errorf("%w: %s", ErrUntrustedForbidden, slotID)
	}

	switch entry.decl.Cardinality {
	case schema.SlotSingle:
		if prev := entry.single; prev != nil {
			prev.Dispose()
		}
		entry.single = r
		return func() {
			s.mu.Lock()
			if e, ok := s.slots[slotID]; ok && e.single == r {
				e.single = nil
				r.Dispose()
			}
			s.mu.Unlock()
		}, nil

	case schema.SlotList:
		entry.list = append(entry.list, r)
		return func() {
			s.mu.Lock()
			if e, ok := s.slots[slotID]; ok {
				e.list = slices.DeleteFunc(e.list, func(x SlotRenderer) bool { return x == r })
				r.Dispose()
			}
			s.mu.Unlock()
		}, nil

	case schema.SlotKeyed:
		if key == "" {
			return nil, fmt.Errorf("shell: keyed slot %q requires a non-empty key", slotID)
		}
		if prev, exists := entry.keyed[key]; exists {
			prev.Dispose()
		} else {
			entry.keyOrder = append(entry.keyOrder, key)
		}
		entry.keyed[key] = r
		return func() {
			s.mu.Lock()
			if e, ok := s.slots[slotID]; ok && e.keyed[key] == r {
				delete(e.keyed, key)
				e.keyOrder = slices.DeleteFunc(e.keyOrder, func(k string) bool { return k == key })
				r.Dispose()
			}
			s.mu.Unlock()
		}, nil

	default:
		return nil, fmt.Errorf("shell: unknown slot cardinality %q", entry.decl.Cardinality)
	}
}

// Resolve returns active renderers for a slot, or the native fallback if empty.
func (s *SlotRegistry) Resolve(slotID schema.SlotID) []SlotRenderer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.slots[slotID]
	if !ok {
		return nil
	}
	switch entry.decl.Cardinality {
	case schema.SlotSingle:
		if entry.single != nil {
			return []SlotRenderer{entry.single}
		}
		return []SlotRenderer{entry.fallback}
	case schema.SlotList:
		if len(entry.list) > 0 {
			return slices.Clone(entry.list)
		}
		return []SlotRenderer{entry.fallback}
	case schema.SlotKeyed:
		if len(entry.keyOrder) == 0 {
			return []SlotRenderer{entry.fallback}
		}
		out := make([]SlotRenderer, 0, len(entry.keyOrder))
		for _, k := range entry.keyOrder {
			if r := entry.keyed[k]; r != nil {
				out = append(out, r)
			}
		}
		return out
	default:
		return []SlotRenderer{entry.fallback}
	}
}

// ResolveKey looks up a keyed slot entry (e.g. "tool-card" + key "diff"),
// falling back to the slot's default renderer when no override is mounted.
func (s *SlotRegistry) ResolveKey(slotID schema.SlotID, key string) SlotRenderer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.slots[slotID]
	if !ok {
		return nil
	}
	if r := entry.keyed[key]; r != nil {
		return r
	}
	return entry.fallback
}
