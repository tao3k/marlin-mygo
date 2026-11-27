// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

type callerCtxKey struct{}

type stubDispatcher struct {
	last ScopedActionRequest
}

func (d *stubDispatcher) SubmitScopedPanelAction(_ context.Context, req ScopedActionRequest) (ScopedActionReceipt, error) {
	d.last = req
	return ScopedActionReceipt{Admitted: true, ReceiptID: "rcpt-001"}, nil
}

func TestWebViewProtocolEnforcesCallerIsolationAndActionAllowlist(t *testing.T) {
	disp := &stubDispatcher{}
	scope := ScopedScope{
		PluginID:             "tao3k.org-kanban",
		ContributionID:       "kanban-panel",
		ActivationGeneration: 3,
		Runtime:              schema.RuntimeWebViewUntrusted,
		DeclaredActions:      []string{"org.task.move"},
	}
	assets := fstest.MapFS{
		"index.html": {Data: []byte("<h1>Kanban</h1>")},
	}
	h := NewWebViewProtocolHandler(func(ctx context.Context) (schema.PluginID, bool) {
		id, ok := ctx.Value(callerCtxKey{}).(schema.PluginID)
		return id, ok
	})
	h.Bind(scope.PluginID, assets, NewScopedProxy(scope, disp))

	// 1. Cross-plugin spoofing must be forbidden.
	spoofReq := httptest.NewRequest(http.MethodGet, "marlin-plugin://tao3k.org-kanban/index.html", nil)
	spoofReq = spoofReq.WithContext(context.WithValue(spoofReq.Context(), callerCtxKey{}, schema.PluginID("evil.other")))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, spoofReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on cross-plugin spoofing, got %d", rec.Code)
	}

	// 2. Undeclared action must be rejected before reaching dispatcher.
	badBody, _ := json.Marshal(actionPostBody{Action: "shell.exec", IdempotencyKey: "k1"})
	badReq := httptest.NewRequest(http.MethodPost, "marlin-plugin://tao3k.org-kanban/__marlin/action", bytes.NewReader(badBody))
	badReq = badReq.WithContext(context.WithValue(badReq.Context(), callerCtxKey{}, scope.PluginID))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, badReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on undeclared action, got %d", rec.Code)
	}
	if disp.last.ActionName != "" {
		t.Fatalf("undeclared action leaked to dispatcher: %+v", disp.last)
	}

	// 3. Declared action succeeds and carries exact generation provenance.
	okBody, _ := json.Marshal(actionPostBody{Action: "org.task.move", Args: json.RawMessage(`{"id":"T-1"}`), IdempotencyKey: "k2"})
	okReq := httptest.NewRequest(http.MethodPost, "marlin-plugin://tao3k.org-kanban/__marlin/action", bytes.NewReader(okBody))
	okReq = okReq.WithContext(context.WithValue(okReq.Context(), callerCtxKey{}, scope.PluginID))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, okReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on declared action, got %d (%s)", rec.Code, rec.Body.String())
	}
	if disp.last.ActivationGeneration != 3 || disp.last.ActionName != "org.task.move" {
		t.Fatalf("unexpected dispatched request: %+v", disp.last)
	}
}
