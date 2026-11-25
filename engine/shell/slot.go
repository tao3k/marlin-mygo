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
	OnAction  func(action, argsJSON string)
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
	decl       schema.SlotDeclaration
	fallback   SlotRenderer
	single     SlotRenderer
	mounted    map[string]SlotRenderer // warm resident renderers keyed by renderer.ID() for O(1) tab focus
	mountOrder []string
	list       []SlotRenderer
	keyed      map[string]SlotRenderer
	keyOrder   []string
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
		mounted:  make(map[string]SlotRenderer),
		keyed:    make(map[string]SlotRenderer),
	}
	return nil
}

// Mount hot-plugs a renderer into a declared slot and returns an idempotent unmount hook.
// Any superseded renderer's Dispose() is invoked strictly outside s.mu so OS main-thread
// teardown (such as macOS WKWebView.Destroy) can never contend with Resolve() frames.
//
// For SlotSingle, distinct mounted renderers remain warm in the slot deck so Focus()
// can switch active tabs in O(1) without destroying and recreating native WebViews;
// mounting a new generation with the same renderer.ID() replaces and disposes the old one.
func (s *SlotRegistry) Mount(slotID schema.SlotID, key string, r SlotRenderer) (func(), error) {
	if r == nil {
		return nil, fmt.Errorf("shell: cannot mount nil renderer")
	}
	var toDispose SlotRenderer
	var unmount func()

	s.mu.Lock()
	entry, ok := s.slots[slotID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrSlotNotDeclared, slotID)
	}
	if !r.Trusted() && !entry.decl.AllowUntrustedPlugin {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrUntrustedForbidden, slotID)
	}

	switch entry.decl.Cardinality {
	case schema.SlotSingle:
		if entry.mounted == nil {
			entry.mounted = make(map[string]SlotRenderer)
		}
		rid := r.ID()
		if rid != "" {
			if prevSame, exists := entry.mounted[rid]; exists && prevSame != r {
				toDispose = prevSame
			} else if !exists {
				entry.mountOrder = append(entry.mountOrder, rid)
			}
			entry.mounted[rid] = r
		} else if prev := entry.single; prev != nil && prev != r {
			toDispose = prev
		}
		entry.single = r
		unmount = func() {
			var disposeNow SlotRenderer
			s.mu.Lock()
			if e, ok := s.slots[slotID]; ok {
				if rid != "" && e.mounted[rid] == r {
					delete(e.mounted, rid)
					e.mountOrder = slices.DeleteFunc(e.mountOrder, func(id string) bool { return id == rid })
					disposeNow = r
				}
				if e.single == r {
					disposeNow = r
					e.single = nil
					for i := len(e.mountOrder) - 1; i >= 0; i-- {
						if cand := e.mounted[e.mountOrder[i]]; cand != nil {
							e.single = cand
							break
						}
					}
				}
			}
			s.mu.Unlock()
			if disposeNow != nil {
				disposeNow.Dispose()
			}
		}

	case schema.SlotList:
		entry.list = append(entry.list, r)
		unmount = func() {
			var disposeNow SlotRenderer
			s.mu.Lock()
			if e, ok := s.slots[slotID]; ok {
				before := len(e.list)
				e.list = slices.DeleteFunc(e.list, func(x SlotRenderer) bool { return x == r })
				if len(e.list) != before {
					disposeNow = r
				}
			}
			s.mu.Unlock()
			if disposeNow != nil {
				disposeNow.Dispose()
			}
		}

	case schema.SlotKeyed:
		if key == "" {
			s.mu.Unlock()
			return nil, fmt.Errorf("shell: keyed slot %q requires a non-empty key", slotID)
		}
		if prev, exists := entry.keyed[key]; exists && prev != r {
			toDispose = prev
		} else if !exists {
			entry.keyOrder = append(entry.keyOrder, key)
		}
		entry.keyed[key] = r
		unmount = func() {
			var disposeNow SlotRenderer
			s.mu.Lock()
			if e, ok := s.slots[slotID]; ok && e.keyed[key] == r {
				delete(e.keyed, key)
				e.keyOrder = slices.DeleteFunc(e.keyOrder, func(k string) bool { return k == key })
				disposeNow = r
			}
			s.mu.Unlock()
			if disposeNow != nil {
				disposeNow.Dispose()
			}
		}

	default:
		s.mu.Unlock()
		return nil, fmt.Errorf("shell: unknown slot cardinality %q", entry.decl.Cardinality)
	}
	s.mu.Unlock()

	if toDispose != nil {
		toDispose.Dispose()
	}
	return unmount, nil
}

// Focus switches the visible SlotSingle renderer to an already-mounted warm renderer
// in O(1) without destroying or recreating native views. Returns true if rendererID
// was already warm in slotID and is now focused.
func (s *SlotRegistry) Focus(slotID schema.SlotID, rendererID string) bool {
	if rendererID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.slots[slotID]
	if !ok || entry.decl.Cardinality != schema.SlotSingle {
		return false
	}
	r, exists := entry.mounted[rendererID]
	if !exists || r == nil {
		return false
	}
	entry.single = r
	return true
}

// ActiveRendererID returns the ID of the currently focused SlotSingle renderer,
// or empty string if the slot is showing its native fallback.
func (s *SlotRegistry) ActiveRendererID(slotID schema.SlotID) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.slots[slotID]
	if !ok || entry.single == nil {
		return ""
	}
	return entry.single.ID()
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
