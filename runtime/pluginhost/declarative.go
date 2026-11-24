// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package pluginhost

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// DeclarativeRuntimeFactory mounts zero-guest-code declarative packs (themes,
// profile overlays, and App Store–safe iOS/Android panels) into a SlotRegistry.
type DeclarativeRuntimeFactory struct {
	BuildRenderer func(scope ScopedScope, entryContent string) shell.SlotRenderer
}

func (f *DeclarativeRuntimeFactory) Kind() schema.PanelRuntimeKind {
	return schema.RuntimeDeclarative
}

func (f *DeclarativeRuntimeFactory) Instantiate(_ context.Context, scope ScopedScope, _ *ScopedProxy) (Instance, error) {
	entryPath := scope.EntryPath
	if entryPath == "" {
		entryPath = "overlay.toml"
	}
	var content string
	if scope.Assets != nil {
		if raw, err := fs.ReadFile(scope.Assets, entryPath); err == nil {
			content = string(raw)
		} else if matches, err := fs.Glob(scope.Assets, "*.toml"); err == nil && len(matches) > 0 {
			if raw, err := fs.ReadFile(scope.Assets, matches[0]); err == nil {
				content = string(raw)
			}
		}
	}
	var renderer shell.SlotRenderer
	if f.BuildRenderer != nil {
		renderer = f.BuildRenderer(scope, content)
	} else {
		renderer = &declarativeSlotRenderer{
			id:      string(scope.PluginID),
			summary: strings.TrimSpace(content),
		}
	}
	return &declarativeInstance{
		scope:    scope,
		renderer: renderer,
	}, nil
}

type declarativeSlotRenderer struct {
	id      string
	summary string
	mu      sync.Mutex
	events  []ScopedEvent
}

func (r *declarativeSlotRenderer) ID() string                        { return r.id }
func (r *declarativeSlotRenderer) Trusted() bool                     { return false }
func (r *declarativeSlotRenderer) Render(shell.RenderContext)        {}
func (r *declarativeSlotRenderer) HandleEvent(shell.HostEvent) error { return nil }
func (r *declarativeSlotRenderer) Dispose()                          {}

type declarativeInstance struct {
	scope    ScopedScope
	renderer shell.SlotRenderer
	mu       sync.Mutex
	closed   bool
	events   []ScopedEvent
}

func (i *declarativeInstance) Scope() ScopedScope           { return i.scope }
func (i *declarativeInstance) Renderer() shell.SlotRenderer { return i.renderer }
func (i *declarativeInstance) Close() error {
	i.mu.Lock()
	i.closed = true
	i.mu.Unlock()
	return nil
}

func (i *declarativeInstance) DeliverEvent(ev ScopedEvent) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return fmt.Errorf("%w: declarative instance closed", ErrPluginNotActive)
	}
	i.events = append(i.events, ev)
	return nil
}
