// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package pluginhost implements the zero-authority, hot-pluggable plugin host
// mediating between isolated client runtimes (Untrusted WebView, wazero WASM-IR,
// Declarative packs) and Marlin's marlin.plugin.v1 Scoped Panel gRPC boundary.
package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

var (
	ErrActionNotDeclared  = errors.New("pluginhost: action not declared in plugin manifest")
	ErrScopeNotAuthorized = errors.New("pluginhost: topic outside plugin read_scopes")
	ErrPluginNotActive    = errors.New("pluginhost: plugin is not active")
	ErrStaleGeneration    = errors.New("pluginhost: stale plugin activation generation")
	ErrProxyRevoked       = errors.New("pluginhost: plugin proxy revoked after deactivation or hot-reload")
)

// ScopedEvent mirrors marlin.plugin.v1.ScopedPanelEvent.
type ScopedEvent struct {
	EventID              string          `json:"eventId"`
	PluginID             schema.PluginID `json:"pluginId"`
	ActivationGeneration uint64          `json:"activationGeneration,string"`
	Topic                string          `json:"topic"`
	Payload              json.RawMessage `json:"payload"`
}

// ScopedActionRequest mirrors marlin.plugin.v1.SubmitScopedPanelActionRequest.
type ScopedActionRequest struct {
	PluginID             schema.PluginID       `json:"pluginId"`
	ContributionID       schema.ContributionID `json:"contributionId"`
	ActivationGeneration uint64                `json:"activationGeneration,string"`
	ActionName           string                `json:"actionName"`
	Args                 json.RawMessage       `json:"args"`
	IdempotencyKey       string                `json:"idempotencyKey"`
}

// ScopedActionReceipt mirrors marlin.plugin.v1.ScopedPanelActionReceipt.
type ScopedActionReceipt struct {
	Admitted  bool            `json:"admitted"`
	Result    json.RawMessage `json:"result,omitempty"`
	ReceiptID string          `json:"receiptId,omitempty"`
	Denial    string          `json:"denial,omitempty"`
}

// ActionDispatcher forwards validated plugin actions to marlin.plugin.v1.
type ActionDispatcher interface {
	SubmitScopedPanelAction(ctx context.Context, req ScopedActionRequest) (ScopedActionReceipt, error)
}

// ScopedScope binds one running plugin instance to its exact manifest allowlists.
type ScopedScope struct {
	PluginID             schema.PluginID
	ContributionID       schema.ContributionID
	ActivationGeneration uint64
	Runtime              schema.PanelRuntimeKind
	SlotID               schema.SlotID
	SlotKey              string
	EntryPath            string
	ReadScopes           []string
	DeclaredActions      []string
	Assets               fs.FS
}

func (s ScopedScope) AllowsAction(name string) bool {
	return slices.Contains(s.DeclaredActions, name)
}

func (s ScopedScope) AllowsTopic(topic string) bool {
	return slices.Contains(s.ReadScopes, topic)
}

// Instance represents one live, hot-pluggable plugin mounted into the shell.
// Implementations must ensure Scope() returns an immutable snapshot without
// acquiring locks that could deadlock with Host synchronization.
type Instance interface {
	Scope() ScopedScope
	Renderer() shell.SlotRenderer
	DeliverEvent(ev ScopedEvent) error
	Close() error
}

// RuntimeFactory constructs a sandboxed Instance for a specific PanelRuntimeKind.
type RuntimeFactory interface {
	Kind() schema.PanelRuntimeKind
	Instantiate(ctx context.Context, scope ScopedScope, proxy *ScopedProxy) (Instance, error)
}

// ScopedProxy enforces manifest allowlists, generation pinning, and atomic
// revocation before any plugin action crosses into Marlin's Rust gRPC boundary.
type ScopedProxy struct {
	scope      ScopedScope
	dispatcher ActionDispatcher
	revoked    atomic.Bool
}

func NewScopedProxy(scope ScopedScope, dispatcher ActionDispatcher) *ScopedProxy {
	return &ScopedProxy{scope: scope, dispatcher: dispatcher}
}

// Revoke permanently disables this generation's proxy so stale WebView/WASM
// callbacks cannot emit actions after deactivation, breaker trip, or hot-reload.
func (p *ScopedProxy) Revoke() {
	p.revoked.Store(true)
}

func (p *ScopedProxy) SubmitAction(ctx context.Context, actionName string, args json.RawMessage, idempotencyKey string) (ScopedActionReceipt, error) {
	if p.revoked.Load() {
		return ScopedActionReceipt{
			Admitted: false,
			Denial:   ErrProxyRevoked.Error(),
		}, fmt.Errorf("%w: plugin %q gen %d", ErrProxyRevoked, p.scope.PluginID, p.scope.ActivationGeneration)
	}
	if !p.scope.AllowsAction(actionName) {
		return ScopedActionReceipt{
			Admitted: false,
			Denial:   ErrActionNotDeclared.Error(),
		}, fmt.Errorf("%w: %q for plugin %q", ErrActionNotDeclared, actionName, p.scope.PluginID)
	}
	if p.dispatcher == nil {
		return ScopedActionReceipt{Admitted: false, Denial: "no dispatcher attached"}, errors.New("pluginhost: nil dispatcher")
	}
	return p.dispatcher.SubmitScopedPanelAction(ctx, ScopedActionRequest{
		PluginID:             p.scope.PluginID,
		ContributionID:       p.scope.ContributionID,
		ActivationGeneration: p.scope.ActivationGeneration,
		ActionName:           actionName,
		Args:                 args,
		IdempotencyKey:       idempotencyKey,
	})
}

// Host coordinates hot-plug activation, slot mounting, event routing, and teardown.
type Host struct {
	mu         sync.RWMutex
	slots      *shell.SlotRegistry
	dispatcher ActionDispatcher
	factories  map[schema.PanelRuntimeKind]RuntimeFactory
	active     map[schema.PluginID]activeEntry
}

type activeEntry struct {
	scope   ScopedScope // immutable snapshot cached at Activate (never calls inst.Scope() under h.mu)
	inst    Instance
	proxy   *ScopedProxy
	unmount func()
}

func NewHost(slots *shell.SlotRegistry, dispatcher ActionDispatcher) *Host {
	return &Host{
		slots:      slots,
		dispatcher: dispatcher,
		factories:  make(map[schema.PanelRuntimeKind]RuntimeFactory),
		active:     make(map[schema.PluginID]activeEntry),
	}
}

func (h *Host) RegisterRuntime(f RuntimeFactory) {
	h.mu.Lock()
	h.factories[f.Kind()] = f
	h.mu.Unlock()
}

// Activate hot-loads a verified plugin contribution and mounts it into its declared slot.
// Re-activating an existing plugin atomically replaces the previous generation and revokes its proxy.
func (h *Host) Activate(ctx context.Context, scope ScopedScope) (Instance, error) {
	if _, err := schema.ParsePluginID(string(scope.PluginID)); err != nil {
		return nil, err
	}
	if schema.IsRestrictedSlot(scope.SlotID) {
		return nil, fmt.Errorf("%w: plugin %q cannot mount restricted slot %q", shell.ErrUntrustedForbidden, scope.PluginID, scope.SlotID)
	}
	if scope.ActivationGeneration == 0 {
		return nil, fmt.Errorf("%w: activation generation must be > 0", ErrStaleGeneration)
	}
	h.mu.RLock()
	factory, ok := h.factories[scope.Runtime]
	h.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("pluginhost: no runtime registered for %q", scope.Runtime)
	}

	proxy := NewScopedProxy(scope, h.dispatcher)
	inst, err := factory.Instantiate(ctx, scope, proxy)
	if err != nil {
		proxy.Revoke()
		return nil, err
	}
	unmount, err := h.slots.Mount(scope.SlotID, scope.SlotKey, inst.Renderer())
	if err != nil {
		proxy.Revoke()
		_ = inst.Close()
		return nil, err
	}

	h.mu.Lock()
	prev, hadPrev := h.active[scope.PluginID]
	if hadPrev && scope.ActivationGeneration <= prev.scope.ActivationGeneration {
		h.mu.Unlock()
		proxy.Revoke()
		unmount()
		_ = inst.Close()
		return nil, fmt.Errorf("%w: activation gen %d <= current %d", ErrStaleGeneration, scope.ActivationGeneration, prev.scope.ActivationGeneration)
	}
	h.active[scope.PluginID] = activeEntry{scope: scope, inst: inst, proxy: proxy, unmount: unmount}
	h.mu.Unlock()

	if hadPrev {
		prev.proxy.Revoke()
		prev.unmount()
		_ = prev.inst.Close()
	}
	return inst, nil
}

// Deactivate unmounts the plugin (restoring the Scheme/native fallback),
// revokes its action proxy, and releases resources.
func (h *Host) Deactivate(id schema.PluginID) error {
	h.mu.Lock()
	entry, ok := h.active[id]
	if ok {
		delete(h.active, id)
	}
	h.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrPluginNotActive, id)
	}
	entry.proxy.Revoke()
	entry.unmount()
	return entry.inst.Close()
}

// ActiveScope returns the currently mounted ScopedScope for one active plugin.
func (h *Host) ActiveScope(id schema.PluginID) (ScopedScope, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	entry, ok := h.active[id]
	if !ok {
		return ScopedScope{}, false
	}
	return entry.scope, true
}

// ActiveScopes returns a snapshot of all currently active plugin scopes.
func (h *Host) ActiveScopes() []ScopedScope {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]ScopedScope, 0, len(h.active))
	for _, entry := range h.active {
		out = append(out, entry.scope)
	}
	return out
}

// RouteEvent verifies generation and read_scopes before delivering a host event to one plugin.
func (h *Host) RouteEvent(ev ScopedEvent) error {
	h.mu.RLock()
	entry, ok := h.active[ev.PluginID]
	h.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrPluginNotActive, ev.PluginID)
	}
	scope := entry.scope
	if ev.ActivationGeneration != scope.ActivationGeneration {
		return fmt.Errorf("%w: event gen %d != active %d", ErrStaleGeneration, ev.ActivationGeneration, scope.ActivationGeneration)
	}
	if !scope.AllowsTopic(ev.Topic) {
		return fmt.Errorf("%w: topic %q for %s", ErrScopeNotAuthorized, ev.Topic, ev.PluginID)
	}
	return entry.inst.DeliverEvent(ev)
}

// PublishTopic fans out a host domain projection strictly to active plugins
// whose manifest read_scopes authorize topic, stamping each delivery with that
// plugin's PluginID and current ActivationGeneration.
func (h *Host) PublishTopic(eventID, topic string, payload json.RawMessage) ([]schema.PluginID, map[schema.PluginID]error) {
	h.mu.RLock()
	type target struct {
		scope ScopedScope
		inst  Instance
	}
	var targets []target
	for _, entry := range h.active {
		sc := entry.scope
		if sc.AllowsTopic(topic) {
			targets = append(targets, target{scope: sc, inst: entry.inst})
		}
	}
	h.mu.RUnlock()

	var delivered []schema.PluginID
	var errs map[schema.PluginID]error
	for _, t := range targets {
		ev := ScopedEvent{
			EventID:              eventID,
			PluginID:             t.scope.PluginID,
			ActivationGeneration: t.scope.ActivationGeneration,
			Topic:                topic,
			Payload:              payload,
		}
		if err := t.inst.DeliverEvent(ev); err != nil {
			if errs == nil {
				errs = make(map[schema.PluginID]error)
			}
			errs[t.scope.PluginID] = err
		} else {
			delivered = append(delivered, t.scope.PluginID)
		}
	}
	slices.Sort(delivered)
	return delivered, errs
}
