// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package pluginhost

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// PluginScheme is registered with mygo.Protocol.Handle(PluginScheme, handler)
// to serve Untrusted WebView plugins at "marlin-plugin://<plugin-id>/".
const (
	PluginScheme       = "marlin-plugin"
	ActionEndpointPath = "/__marlin/action"
	MaxActionBodyBytes = 64 << 10 // 64 KiB cap matching mygo Untrusted recommendations
)

// CallerResolver extracts the calling plugin identity from the request context.
// In a live MyGo app, wire this to map mygo.CallerPage(ctx) -> schema.PluginID.
type CallerResolver func(ctx context.Context) (schema.PluginID, bool)

type webViewBinding struct {
	assets fs.FS
	proxy  *ScopedProxy
}

// WebViewProtocolHandler serves isolated static assets and scoped action posts
// for PageOptions{Untrusted: true} WebView plugins (both TS/Web and Go js/wasm).
type WebViewProtocolHandler struct {
	resolveCaller CallerResolver
	mu            sync.RWMutex
	bindings      map[schema.PluginID]webViewBinding
}

func NewWebViewProtocolHandler(resolve CallerResolver) *WebViewProtocolHandler {
	return &WebViewProtocolHandler{
		resolveCaller: resolve,
		bindings:      make(map[schema.PluginID]webViewBinding),
	}
}

func (h *WebViewProtocolHandler) Bind(id schema.PluginID, assets fs.FS, proxy *ScopedProxy) {
	h.mu.Lock()
	h.bindings[id] = webViewBinding{assets: assets, proxy: proxy}
	h.mu.Unlock()
}

func (h *WebViewProtocolHandler) Unbind(id schema.PluginID) {
	h.mu.Lock()
	delete(h.bindings, id)
	h.mu.Unlock()
}

type actionPostBody struct {
	Action         string          `json:"action"`
	Args           json.RawMessage `json:"args"`
	IdempotencyKey string          `json:"idempotencyKey"`
}

// ServeHTTP enforces CallerPage-to-Host origin matching before serving assets
// or forwarding scoped actions, preventing cross-plugin spoofing.
func (h *WebViewProtocolHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.resolveCaller == nil {
		http.Error(w, "caller resolver not configured", http.StatusInternalServerError)
		return
	}
	callerID, ok := h.resolveCaller(r.Context())
	if !ok || callerID == "" {
		http.Error(w, "unidentified plugin page", http.StatusForbidden)
		return
	}
	targetHost := strings.ToLower(r.URL.Host)
	if targetHost == "" {
		targetHost = strings.ToLower(r.Host)
	}
	if strings.Contains(targetHost, ":") || strings.ToLower(string(callerID)) != targetHost {
		http.Error(w, "cross-plugin protocol access denied", http.StatusForbidden)
		return
	}

	h.mu.RLock()
	binding, exists := h.bindings[callerID]
	h.mu.RUnlock()
	if !exists {
		http.NotFound(w, r)
		return
	}

	cleanPath := path.Clean("/" + r.URL.Path)
	if r.Method == http.MethodPost && cleanPath == ActionEndpointPath {
		h.handleAction(w, r, binding.proxy)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if binding.assets == nil {
		http.NotFound(w, r)
		return
	}
	http.FileServerFS(binding.assets).ServeHTTP(w, r)
}

func (h *WebViewProtocolHandler) handleAction(w http.ResponseWriter, r *http.Request, proxy *ScopedProxy) {
	if proxy == nil {
		http.Error(w, "plugin action proxy unavailable", http.StatusForbidden)
		return
	}
	limited := http.MaxBytesReader(w, r.Body, MaxActionBodyBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		http.Error(w, "action payload too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	var body actionPostBody
	if err := json.Unmarshal(raw, &body); err != nil || body.Action == "" {
		http.Error(w, "invalid action JSON envelope", http.StatusBadRequest)
		return
	}
	receipt, err := proxy.SubmitAction(r.Context(), body.Action, body.Args, body.IdempotencyKey)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
	}
	_ = json.NewEncoder(w).Encode(receipt)
}
