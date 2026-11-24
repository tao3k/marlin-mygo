// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package mygoshell

import (
	"context"
	"fmt"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

// PanelEvent delivers host-scoped plugin events strictly via EmitPage to an
// Untrusted WebView plugin. Broadcast and window-wide Emit never reach it.
var PanelEvent = mygo.NewEvent[pluginhost.ScopedEvent]("marlin:panel-event")

// WebViewRuntime implements pluginhost.RuntimeFactory for both TS/Web plugins
// (webview-untrusted) and Go GOOS=js/wasm MyGo-UI plugins (mygo-js-wasm).
type WebViewRuntime struct {
	kind     schema.PanelRuntimeKind
	win      *mygo.Window
	protocol *pluginhost.WebViewProtocolHandler

	mu         sync.RWMutex
	pageOwners map[*mygo.Page]schema.PluginID
}

// NewWebViewRuntime wires a WebViewProtocolHandler using mygo.CallerPage(ctx)
// and registers the "marlin-plugin://" custom protocol scheme with MyGo.
// Call RegisterProtocol() before creating the host window.
func NewWebViewRuntime(kind schema.PanelRuntimeKind) *WebViewRuntime {
	rt := &WebViewRuntime{
		kind:       kind,
		pageOwners: make(map[*mygo.Page]schema.PluginID),
	}
	rt.protocol = pluginhost.NewWebViewProtocolHandler(func(ctx context.Context) (schema.PluginID, bool) {
		p := mygo.CallerPage(ctx)
		if p == nil {
			return "", false
		}
		rt.mu.RLock()
		id, ok := rt.pageOwners[p]
		rt.mu.RUnlock()
		return id, ok
	})
	return rt
}

// RegisterProtocol installs the marlin-plugin:// scheme handler before App.Run / NewWindow.
func (r *WebViewRuntime) RegisterProtocol() error {
	return mygo.Protocol.Handle(pluginhost.PluginScheme, r.protocol)
}

// AttachWindow binds the host native-UI window once created inside App.WhenReady.
func (r *WebViewRuntime) AttachWindow(win *mygo.Window) {
	r.mu.Lock()
	r.win = win
	r.mu.Unlock()
}

func (r *WebViewRuntime) Kind() schema.PanelRuntimeKind { return r.kind }

func (r *WebViewRuntime) Instantiate(_ context.Context, scope pluginhost.ScopedScope, proxy *pluginhost.ScopedProxy) (pluginhost.Instance, error) {
	r.mu.RLock()
	win := r.win
	r.mu.RUnlock()
	if win == nil {
		return nil, fmt.Errorf("mygoshell: host window not attached before activating %q", scope.PluginID)
	}

	r.protocol.Bind(scope.PluginID, scope.Assets, proxy)
	wv, err := win.NewWebView(mygo.WebViewOptions{
		URL: fmt.Sprintf("%s://%s/", pluginhost.PluginScheme, scope.PluginID),
		Page: mygo.PageOptions{
			Untrusted: true,
		},
	})
	if err != nil {
		r.protocol.Unbind(scope.PluginID)
		return nil, fmt.Errorf("mygoshell: create untrusted webview for %q: %w", scope.PluginID, err)
	}

	page := wv.Page()
	r.mu.Lock()
	r.pageOwners[page] = scope.PluginID
	r.mu.Unlock()
	win.Invalidate()

	inst := &webViewInstance{
		rt:    r,
		scope: scope,
		wv:    wv,
		page:  page,
	}
	inst.renderer = &webViewSlotRenderer{inst: inst}
	return inst, nil
}

type webViewInstance struct {
	rt       *WebViewRuntime
	scope    pluginhost.ScopedScope
	wv       *mygo.WebView
	page     *mygo.Page
	renderer *webViewSlotRenderer
	once     sync.Once
}

func (i *webViewInstance) Scope() pluginhost.ScopedScope { return i.scope }
func (i *webViewInstance) Renderer() shell.SlotRenderer  { return i.renderer }
func (i *webViewInstance) DeliverEvent(ev pluginhost.ScopedEvent) error {
	return PanelEvent.EmitPage(i.page, ev)
}

func (i *webViewInstance) Close() error {
	i.once.Do(func() {
		i.rt.mu.Lock()
		delete(i.rt.pageOwners, i.page)
		win := i.rt.win
		i.rt.mu.Unlock()
		i.rt.protocol.Unbind(i.scope.PluginID)
		i.wv.Destroy()
		if win != nil {
			win.Invalidate()
		}
	})
	return nil
}

type webViewSlotRenderer struct {
	inst *webViewInstance
}

func (r *webViewSlotRenderer) ID() string                        { return string(r.inst.scope.PluginID) }
func (r *webViewSlotRenderer) Trusted() bool                     { return false }
func (r *webViewSlotRenderer) HandleEvent(shell.HostEvent) error { return nil }
func (r *webViewSlotRenderer) Dispose()                          { _ = r.inst.Close() }

func (r *webViewSlotRenderer) Render(ctx shell.RenderContext) {
	c, ok := ctx.Surface.(*ui.Context)
	if !ok || c == nil || r.inst.wv == nil || r.inst.wv.IsDestroyed() {
		return
	}
	t := c.Theme()
	ui.WebView(c, r.inst.wv).Grow(1).Radius(8).Border(1, t.Border)
}
