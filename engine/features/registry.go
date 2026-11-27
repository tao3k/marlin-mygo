// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package features implements Marlin's Rust-Cargo-inspired opt-in frontend
// feature registry and Tier-0 UI Governance admission plane.
//
// Optional interaction paradigms (such as Slock-inspired Chat Room, T3-style
// Multi-Model Compare, or live Artifact Canvas) compile only when selected via
// Go build tags (`marlin_feat_*` / `marlin_full`) and activate at runtime only
// when permitted by both the user's `-features` profile and GovernancePolicy.
package features

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/engine/shell"
)

var (
	ErrFeatureInvalid    = errors.New("features: feature ID and name are required")
	ErrFeatureDuplicate  = errors.New("features: feature ID already registered")
	ErrFeatureNotFound   = errors.New("features: feature not compiled into this binary")
	ErrFeatureDenied     = errors.New("features: activation denied by UI governance policy")
)

// FeatureID uniquely identifies an opt-in frontend interaction module.
type FeatureID string

const (
	FeatureChatRoom   FeatureID = "chatroom"
	FeatureCompare    FeatureID = "compare"
	FeatureCanvas     FeatureID = "canvas"
	FeatureGovernance FeatureID = "governance"
)

// TrustTier classifies the execution isolation level of a UI module.
type TrustTier string

const (
	Tier0HostNative       TrustTier = "tier0-native"
	Tier1SandboxWASI      TrustTier = "tier1-wasi"
	Tier2UntrustedWebView TrustTier = "tier2-webview"
)

// FeatureMeta describes a compiled feature module for governance review and UI display.
type FeatureMeta struct {
	ID                   FeatureID
	Name                 string
	Description          string
	Category             string // e.g. "collaboration", "reasoning", "workspace", "security"
	TrustTier            TrustTier
	RequiredCapabilities []string
	DefaultEnabled       bool
}

// BufferRenderFn renders a feature-owned buffer into the host GPU surface.
// Parameters use `any` at the engine boundary so `engine/features` never imports UI toolkits.
type BufferRenderFn func(surface any, theme any)

// Host collects the buffers, commands, and render hooks contributed by enabled features.
type Host struct {
	mu        sync.RWMutex
	buffers   []shell.PaneBuffer
	commands  []input.CommandEntry
	renderers map[string]BufferRenderFn
}

// NewHost constructs an empty feature registration host.
func NewHost() *Host {
	return &Host{
		renderers: make(map[string]BufferRenderFn),
	}
}

// AddBuffer registers a composable Emacs WindowTree buffer owned by a feature.
func (h *Host) AddBuffer(buf shell.PaneBuffer, render BufferRenderFn) {
	if h == nil || buf.BufferID == "" {
		return
	}
	h.mu.Lock()
	replaced := false
	for i := range h.buffers {
		if h.buffers[i].BufferID == buf.BufferID {
			h.buffers[i] = buf
			replaced = true
			break
		}
	}
	if !replaced {
		h.buffers = append(h.buffers, buf)
	}
	if render != nil {
		h.renderers[buf.BufferID] = render
	}
	h.mu.Unlock()
}

// AddCommand registers a Command Palette entry contributed by a feature.
func (h *Host) AddCommand(cmd input.CommandEntry) {
	if h == nil || strings.TrimSpace(cmd.Name) == "" {
		return
	}
	h.mu.Lock()
	for i := range h.commands {
		if h.commands[i].Name == cmd.Name {
			h.commands[i] = cmd
			h.mu.Unlock()
			return
		}
	}
	h.commands = append(h.commands, cmd)
	h.mu.Unlock()
}

// Buffers returns all buffers contributed by active features.
func (h *Host) Buffers() []shell.PaneBuffer {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return append([]shell.PaneBuffer(nil), h.buffers...)
}

// Commands returns all Command Palette entries contributed by active features.
func (h *Host) Commands() []input.CommandEntry {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return append([]input.CommandEntry(nil), h.commands...)
}

// Renderer returns the registered render function for bufferID, if any.
func (h *Host) Renderer(bufferID string) (BufferRenderFn, bool) {
	if h == nil {
		return nil, false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	fn, ok := h.renderers[bufferID]
	return fn, ok
}

// FeatureModule is implemented by each opt-in interaction paradigm package.
type FeatureModule interface {
	Meta() FeatureMeta
	Register(host *Host) error
}

// SelectionMode controls how a CLI `-features` spec selects compiled modules.
type SelectionMode string

const (
	SelectDefault  SelectionMode = "default"
	SelectAll      SelectionMode = "all"
	SelectNone     SelectionMode = "none"
	SelectExplicit SelectionMode = "explicit"
)

// FeatureSelection is a parsed runtime `-features` profile.
type FeatureSelection struct {
	Mode    SelectionMode
	Include map[FeatureID]bool
	Exclude map[FeatureID]bool
}

// ParseFeatureSpec parses a Cargo-like feature specification:
// "", "default" -> DefaultEnabled modules; "all" -> all compiled; "none" -> zero optional;
// "chatroom,compare" -> explicit set; "+chatroom,-canvas" -> delta over defaults.
func ParseFeatureSpec(spec string) FeatureSelection {
	raw := strings.TrimSpace(spec)
	sel := FeatureSelection{
		Mode:    SelectDefault,
		Include: make(map[FeatureID]bool),
		Exclude: make(map[FeatureID]bool),
	}
	if raw == "" || strings.EqualFold(raw, "default") {
		return sel
	}
	if strings.EqualFold(raw, "all") {
		sel.Mode = SelectAll
		return sel
	}
	if strings.EqualFold(raw, "none") || strings.EqualFold(raw, "minimal") {
		sel.Mode = SelectNone
		return sel
	}

	hasDelta := false
	hasExplicit := false
	for _, part := range strings.Split(raw, ",") {
		tok := strings.TrimSpace(part)
		if tok == "" {
			continue
		}
		switch {
		case strings.HasPrefix(tok, "+"):
			hasDelta = true
			id := FeatureID(strings.ToLower(strings.TrimSpace(tok[1:])))
			if id != "" {
				sel.Include[id] = true
			}
		case strings.HasPrefix(tok, "-"):
			hasDelta = true
			id := FeatureID(strings.ToLower(strings.TrimSpace(tok[1:])))
			if id != "" {
				sel.Exclude[id] = true
			}
		default:
			hasExplicit = true
			id := FeatureID(strings.ToLower(tok))
			if id != "" {
				sel.Include[id] = true
			}
		}
	}
	if hasExplicit && !hasDelta {
		sel.Mode = SelectExplicit
	}
	return sel
}

// Want reports whether id is requested by sel given its DefaultEnabled flag.
func (s FeatureSelection) Want(id FeatureID, defaultEnabled bool) bool {
	if s.Exclude[id] {
		return false
	}
	switch s.Mode {
	case SelectAll:
		return true
	case SelectNone:
		return false
	case SelectExplicit:
		return s.Include[id]
	default:
		if s.Include[id] {
			return true
		}
		return defaultEnabled
	}
}

// FeatureStatus is an immutable snapshot row displayed in the UI Governance Console.
type FeatureStatus struct {
	Meta         FeatureMeta
	Enabled      bool
	Denied       bool
	DenialReason string
}

// Registry tracks all compiled FeatureModules and their governed runtime states.
type Registry struct {
	mu      sync.RWMutex
	modules map[FeatureID]FeatureModule
	enabled map[FeatureID]bool
	denied  map[FeatureID]string
}

// NewRegistry creates an empty Feature Registry.
func NewRegistry() *Registry {
	return &Registry{
		modules: make(map[FeatureID]FeatureModule),
		enabled: make(map[FeatureID]bool),
		denied:  make(map[FeatureID]string),
	}
}

// RegisterCompiled adds a build-tag-compiled FeatureModule into the registry.
func (r *Registry) RegisterCompiled(mod FeatureModule) error {
	if mod == nil {
		return ErrFeatureInvalid
	}
	meta := mod.Meta()
	meta.ID = FeatureID(strings.ToLower(strings.TrimSpace(string(meta.ID))))
	if meta.ID == "" || strings.TrimSpace(meta.Name) == "" {
		return ErrFeatureInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.modules[meta.ID]; exists {
		return fmt.Errorf("%w: %s", ErrFeatureDuplicate, meta.ID)
	}
	r.modules[meta.ID] = mod
	return nil
}

// Activate evaluates sel against GovernanceLedger (fail-closed) and registers
// every admitted feature onto host.
func (r *Registry) Activate(sel FeatureSelection, ledger *GovernanceLedger, host *Host) []FeatureStatus {
	r.mu.Lock()
	ids := make([]FeatureID, 0, len(r.modules))
	for id := range r.modules {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	r.enabled = make(map[FeatureID]bool)
	r.denied = make(map[FeatureID]string)

	for _, id := range ids {
		mod := r.modules[id]
		meta := mod.Meta()
		if !sel.Want(id, meta.DefaultEnabled) {
			continue
		}
		if ledger != nil {
			if err := ledger.AdmitFeature(meta); err != nil {
				r.denied[id] = err.Error()
				continue
			}
		}
		if host != nil {
			if err := mod.Register(host); err != nil {
				r.denied[id] = err.Error()
				continue
			}
		}
		r.enabled[id] = true
	}
	r.mu.Unlock()
	return r.Snapshot()
}

// SetEnabled dynamically toggles a compiled feature at runtime, enforcing
// GovernanceLedger admission fail-closed on enable.
func (r *Registry) SetEnabled(id FeatureID, enable bool, ledger *GovernanceLedger, host *Host) error {
	r.mu.Lock()
	mod, ok := r.modules[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrFeatureNotFound, id)
	}
	meta := mod.Meta()
	if !enable {
		delete(r.enabled, id)
		delete(r.denied, id)
		r.mu.Unlock()
		if ledger != nil {
			ledger.RecordAudit("user", string(id), "feature.disable", AuditAdmitted, "disabled at runtime")
		}
		return nil
	}
	r.mu.Unlock()

	if ledger != nil {
		if err := ledger.AdmitFeature(meta); err != nil {
			r.mu.Lock()
			delete(r.enabled, id)
			r.denied[id] = err.Error()
			r.mu.Unlock()
			return err
		}
	}
	if host != nil {
		if err := mod.Register(host); err != nil {
			r.mu.Lock()
			delete(r.enabled, id)
			r.denied[id] = err.Error()
			r.mu.Unlock()
			return err
		}
	}
	r.mu.Lock()
	r.enabled[id] = true
	delete(r.denied, id)
	r.mu.Unlock()
	return nil
}

// IsEnabled reports whether feature id is compiled AND currently active.
func (r *Registry) IsEnabled(id FeatureID) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.enabled[id]
}

// Snapshot returns a deterministic sorted view of all compiled features.
func (r *Registry) Snapshot() []FeatureStatus {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]FeatureID, 0, len(r.modules))
	for id := range r.modules {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]FeatureStatus, 0, len(ids))
	for _, id := range ids {
		reason := r.denied[id]
		out = append(out, FeatureStatus{
			Meta:         r.modules[id].Meta(),
			Enabled:      r.enabled[id],
			Denied:       reason != "",
			DenialReason: reason,
		})
	}
	return out
}
