// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package pluginhost

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestArtifactCASIntegrityEvictionAndMIMEAllowlist(t *testing.T) {
	cas := NewArtifactCAS(64, 100)

	// Unsafe active web content MIME MUST be rejected fail-closed.
	if _, err := cas.Put("text/html", []byte("<script>alert(1)</script>")); !errors.Is(err, ErrArtifactMIMEBlocked) {
		t.Fatalf("expected ErrArtifactMIMEBlocked for text/html, got %v", err)
	}
	if _, err := cas.Put("application/javascript", []byte("fetch('https://evil')")); !errors.Is(err, ErrArtifactMIMEBlocked) {
		t.Fatalf("expected ErrArtifactMIMEBlocked for javascript, got %v", err)
	}

	// Oversized payload rejected.
	if _, err := cas.Put("image/png", make([]byte, 65)); !errors.Is(err, ErrArtifactTooLarge) {
		t.Fatalf("expected ErrArtifactTooLarge, got %v", err)
	}

	e1, err := cas.Put("image/png", bytes.Repeat([]byte("A"), 60))
	if err != nil {
		t.Fatalf("Put e1: %v", err)
	}
	// Forged digest MUST fail PutVerified.
	if _, err := cas.PutVerified("0000000000000000000000000000000000000000000000000000000000000000", "image/png", []byte("hello")); !errors.Is(err, ErrArtifactDigestBad) {
		t.Fatalf("expected ErrArtifactDigestBad, got %v", err)
	}

	// Second 60-byte artifact exceeds 100-byte total cap -> evicts e1 FIFO.
	e2, err := cas.Put("text/x-diff", bytes.Repeat([]byte("B"), 60))
	if err != nil {
		t.Fatalf("Put e2: %v", err)
	}
	if _, err := cas.Get(e1.SHA256Hex); !errors.Is(err, ErrArtifactNotFound) {
		t.Fatalf("expected e1 evicted, got err=%v", err)
	}
	got2, err := cas.Get(e2.SHA256Hex)
	if err != nil || got2.MIMEType != "text/plain; charset=utf-8" || cas.TotalBytes() != 60 {
		t.Fatalf("unexpected e2 state: got=%+v total=%d err=%v", got2, cas.TotalBytes(), err)
	}
}

func TestArtifactProtocolHandlerEnforcesPerPluginGrantIsolation(t *testing.T) {
	cas := NewArtifactCAS(1024, 4096)
	entry, err := cas.Put("image/png", []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a})
	if err != nil {
		t.Fatal(err)
	}
	h := NewArtifactProtocolHandler(ArtifactCallerFromContext, cas)
	pluginA := schema.PluginID("tao3k.org-agenda")
	pluginB := schema.PluginID("evil.other-plugin")
	if err := h.Grant(pluginA, entry.SHA256Hex); err != nil {
		t.Fatal(err)
	}

	uri, err := ArtifactURL(entry.SHA256Hex)
	if err != nil {
		t.Fatal(err)
	}

	// Granted plugin A receives 200 + nosniff + immutable + exact bytes.
	reqA := httptest.NewRequest(http.MethodGet, uri, nil).WithContext(WithArtifactCaller(context.Background(), pluginA))
	recA := httptest.NewRecorder()
	h.ServeHTTP(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Fatalf("expected 200 for granted plugin A, got %d (%s)", recA.Code, recA.Body.String())
	}
	if recA.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing nosniff header")
	}
	if !bytes.Equal(recA.Body.Bytes(), entry.Data) {
		t.Fatalf("artifact body mismatch")
	}

	// Ungranted plugin B MUST receive 403 Forbidden (zero cross-plugin leakage).
	reqB := httptest.NewRequest(http.MethodGet, uri, nil).WithContext(WithArtifactCaller(context.Background(), pluginB))
	recB := httptest.NewRecorder()
	h.ServeHTTP(recB, reqB)
	if recB.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for ungranted plugin B, got %d", recB.Code)
	}

	// Unidentified caller MUST receive 403 Forbidden.
	reqAnon := httptest.NewRequest(http.MethodGet, uri, nil)
	recAnon := httptest.NewRecorder()
	h.ServeHTTP(recAnon, reqAnon)
	if recAnon.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for anonymous caller, got %d", recAnon.Code)
	}

	// Revoking plugin A immediately blocks subsequent fetches.
	h.RevokePlugin(pluginA)
	recAfter := httptest.NewRecorder()
	h.ServeHTTP(recAfter, reqA)
	if recAfter.Code != http.StatusForbidden {
		t.Fatalf("expected 403 after RevokePlugin, got %d", recAfter.Code)
	}

	// Concurrent reads and grants must remain race-free under -race.
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = h.Grant(pluginA, entry.SHA256Hex)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, reqA)
		}()
	}
	wg.Wait()
}

func FuzzArtifactProtocolHTTP(f *testing.F) {
	cas := NewArtifactCAS(1024, 4096)
	entry, _ := cas.Put("text/plain", []byte("bounded log line"))
	h := NewArtifactProtocolHandler(ArtifactCallerFromContext, cas)
	pluginA := schema.PluginID("tao3k.plugin-a")
	_ = h.Grant(pluginA, entry.SHA256Hex)

	f.Add(http.MethodGet, entry.SHA256Hex, string(pluginA))
	f.Add(http.MethodPost, entry.SHA256Hex, string(pluginA))
	f.Add(http.MethodGet, "../../etc/passwd", string(pluginA))
	f.Add(http.MethodGet, entry.SHA256Hex, "tao3k.plugin-b")
	f.Add(http.MethodGet, entry.SHA256Hex+":8080", string(pluginA))

	f.Fuzz(func(t *testing.T, method, host, caller string) {
		if method == "" {
			method = http.MethodGet
		}
		req := httptest.NewRequest(http.MethodGet, "marlin-artifact://placeholder/", nil)
		req.Method = method
		req.URL.Host = host
		req.Host = host
		if caller != "" {
			req = req.WithContext(WithArtifactCaller(context.Background(), schema.PluginID(caller)))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if caller != string(pluginA) && rec.Code == http.StatusOK {
			t.Fatalf("cross-plugin artifact leak for caller=%q host=%q", caller, host)
		}
	})
}
