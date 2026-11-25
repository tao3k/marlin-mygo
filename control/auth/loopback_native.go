// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package auth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// LoopbackCallback holds the parsed query parameters from an RFC 8252 loopback redirect.
type LoopbackCallback struct {
	Code             string
	State            string
	Error            string
	ErrorDescription string
}

// LoopbackReceiver binds an ephemeral 127.0.0.1:0 HTTP listener strictly for
// one single-use OAuth 2.1 authorization redirect, enforcing strict browser
// security headers and shutting down immediately after delivery or cancellation.
type LoopbackReceiver struct {
	redirectURI   string
	srv           *http.Server
	resultCh      chan LoopbackCallback
	once          sync.Once
	closeOnce     sync.Once
	mu            sync.Mutex
	expectedState string
}

// SetExpectedState binds the receiver to a specific CSRF state so stray or
// adversarial loopback hits with a wrong/missing state cannot consume the
// single-use callback slot.
func (r *LoopbackReceiver) SetExpectedState(state string) {
	r.mu.Lock()
	r.expectedState = state
	r.mu.Unlock()
}

// StartLoopbackReceiver starts a single-use RFC 8252 loopback callback server.
func StartLoopbackReceiver() (*LoopbackReceiver, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("auth: listen loopback callback: %w", err)
	}
	r := &LoopbackReceiver{
		redirectURI: fmt.Sprintf("http://%s/oauth/callback", ln.Addr().String()),
		resultCh:    make(chan LoopbackCallback, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/callback", r.handleCallback)
	r.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = r.srv.Serve(ln)
	}()
	return r, nil
}

// RedirectURI returns the bound loopback redirect URI (`http://127.0.0.1:<port>/oauth/callback`).
func (r *LoopbackReceiver) RedirectURI() string { return r.redirectURI }

// Wait blocks until the single callback arrives or ctx is cancelled.
func (r *LoopbackReceiver) Wait(ctx context.Context) (LoopbackCallback, error) {
	defer r.Close()
	select {
	case cb := <-r.resultCh:
		return cb, nil
	case <-ctx.Done():
		return LoopbackCallback{}, ctx.Err()
	}
}

// Close shuts down the loopback listener cleanly.
func (r *LoopbackReceiver) Close() {
	r.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = r.srv.Shutdown(ctx)
	})
}

func (r *LoopbackReceiver) handleCallback(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")

	if req.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := req.URL.Query()
	cb := LoopbackCallback{
		Code:             q.Get("code"),
		State:            q.Get("state"),
		Error:            q.Get("error"),
		ErrorDescription: q.Get("error_description"),
	}
	r.mu.Lock()
	expected := r.expectedState
	r.mu.Unlock()
	if expected != "" && !ConstantTimeTokenEqual(expected, cb.State) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("<!doctype html><title>Marlin Auth</title><p>Invalid OAuth state.</p>"))
		return
	}
	delivered := false
	r.once.Do(func() {
		r.resultCh <- cb
		delivered = true
	})
	if !delivered {
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte("<!doctype html><title>Marlin Auth</title><p>Callback already consumed. You may close this tab.</p>"))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("<!doctype html><title>Marlin Sign-In</title><body style='font-family:system-ui;padding:32px;background:#0d1117;color:#e6edf3'><h2>Marlin authentication received</h2><p>You can close this browser tab and return to Marlin Studio.</p></body>"))
}
