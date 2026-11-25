// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type stubExchanger struct {
	mu           sync.Mutex
	exchangeFn   func(ctx context.Context, cfg ProviderConfig, code, verifier, redirectURI, nonce string) (TokenSet, AccountProfile, error)
	refreshFn    func(ctx context.Context, cfg ProviderConfig, refreshToken string) (TokenSet, error)
	revoked      []string
}

func (s *stubExchanger) ExchangeCode(ctx context.Context, cfg ProviderConfig, code, verifier, redirectURI, nonce string) (TokenSet, AccountProfile, error) {
	if s.exchangeFn != nil {
		return s.exchangeFn(ctx, cfg, code, verifier, redirectURI, nonce)
	}
	return TokenSet{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		Scopes:       []string{"openid", "email"},
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}, AccountProfile{Subject: "u-1", Email: "u1@example.com", Scopes: []string{"openid", "email"}}, nil
}

func (s *stubExchanger) RefreshToken(ctx context.Context, cfg ProviderConfig, refreshToken string) (TokenSet, error) {
	if s.refreshFn != nil {
		return s.refreshFn(ctx, cfg, refreshToken)
	}
	return TokenSet{
		AccessToken:  "access-2",
		RefreshToken: "refresh-2",
		Scopes:       []string{"openid", "email"},
		ExpiresAt:    time.Now().Add(2 * time.Hour),
	}, nil
}

func (s *stubExchanger) RevokeToken(_ context.Context, _ ProviderConfig, token string) error {
	s.mu.Lock()
	s.revoked = append(s.revoked, token)
	s.mu.Unlock()
	return nil
}

func testProviderConfig() ProviderConfig {
	return ProviderConfig{
		ID:              ProviderOIDC,
		DisplayName:     "TestOIDC",
		Method:          MethodOAuthPKCE,
		AuthURL:         "https://idp.example.com/authorize",
		TokenURL:        "https://idp.example.com/token",
		RevokeURL:       "https://idp.example.com/revoke",
		ClientID:        "marlin-test",
		Scopes:          []string{"openid", "email"},
		RedirectURI:     "http://127.0.0.1:8888/oauth/callback",
		SupportsRefresh: true,
	}
}

func TestZombieRefreshBlockedAfterSignOut(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	refreshEntered := make(chan struct{})
	releaseRefresh := make(chan struct{})

	ex := &stubExchanger{
		exchangeFn: func(_ context.Context, _ ProviderConfig, _, _, _, _ string) (TokenSet, AccountProfile, error) {
			return TokenSet{
				AccessToken:  "expired-access",
				RefreshToken: "refresh-1",
				Scopes:       []string{"openid", "email"},
				ExpiresAt:    now.Add(-1 * time.Minute),
			}, AccountProfile{Subject: "sub-zombie", Email: "z@example.com", Scopes: []string{"openid", "email"}}, nil
		},
		refreshFn: func(_ context.Context, _ ProviderConfig, _ string) (TokenSet, error) {
			close(refreshEntered)
			<-releaseRefresh
			return TokenSet{
				AccessToken:  "zombie-access",
				RefreshToken: "zombie-refresh",
				Scopes:       []string{"openid", "email"},
				ExpiresAt:    now.Add(1 * time.Hour),
			}, nil
		},
	}
	vault := NewMemoryVault()
	mgr := NewManager(ManagerConfig{Vault: vault, Exchanger: ex, Now: func() time.Time { return now }})
	if err := mgr.RegisterProvider(testProviderConfig()); err != nil {
		t.Fatal(err)
	}
	flow, err := mgr.BeginOAuth(ProviderOIDC, "")
	if err != nil {
		t.Fatal(err)
	}
	mgr.mu.RLock()
	state := mgr.oauthAttempts[flow.AttemptID].state
	mgr.mu.RUnlock()

	acc, err := mgr.CompleteOAuthCallback(context.Background(), flow.AttemptID, LoopbackCallback{Code: "c1", State: state})
	if err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, rErr := mgr.ValidAccessToken(context.Background(), acc.ID)
		errCh <- rErr
	}()

	<-refreshEntered
	if err := mgr.SignOut(context.Background(), acc.ID); err != nil {
		t.Fatal(err)
	}
	close(releaseRefresh)

	if err := <-errCh; !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("expected ErrSessionRevoked, got %v", err)
	}
	if _, found, _ := vault.Load(context.Background(), acc.ID); found {
		t.Fatalf("vault resurrected token after SignOut")
	}
	if snap := mgr.Snapshot(); snap.Phase != PhaseSignedOut {
		t.Fatalf("expected PhaseSignedOut, got %s", snap.Phase)
	}
}

func TestCancelPendingBlocksInFlightOAuthAdmission(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	ex := &stubExchanger{
		exchangeFn: func(_ context.Context, _ ProviderConfig, _, _, _, _ string) (TokenSet, AccountProfile, error) {
			close(entered)
			<-release
			return TokenSet{
				AccessToken:  "late-access",
				RefreshToken: "late-refresh",
				Scopes:       []string{"openid"},
				ExpiresAt:    time.Now().Add(time.Hour),
			}, AccountProfile{Subject: "sub-cancel"}, nil
		},
	}
	vault := NewMemoryVault()
	mgr := NewManager(ManagerConfig{Vault: vault, Exchanger: ex})
	_ = mgr.RegisterProvider(testProviderConfig())
	flow, err := mgr.BeginOAuth(ProviderOIDC, "")
	if err != nil {
		t.Fatal(err)
	}
	mgr.mu.RLock()
	state := mgr.oauthAttempts[flow.AttemptID].state
	mgr.mu.RUnlock()

	errCh := make(chan error, 1)
	go func() {
		_, cErr := mgr.CompleteOAuthCallback(context.Background(), flow.AttemptID, LoopbackCallback{Code: "c1", State: state})
		errCh <- cErr
	}()

	<-entered
	mgr.CancelPending()
	close(release)

	if err := <-errCh; !errors.Is(err, ErrFlowCancelled) {
		t.Fatalf("expected ErrFlowCancelled, got %v", err)
	}
	if ids, _ := vault.ListAccountIDs(context.Background()); len(ids) != 0 {
		t.Fatalf("expected empty vault after CancelPending, got %v", ids)
	}
}

func TestRefreshScopeEscalationRejected(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	ex := &stubExchanger{
		exchangeFn: func(_ context.Context, _ ProviderConfig, _, _, _, _ string) (TokenSet, AccountProfile, error) {
			return TokenSet{
				AccessToken:  "expired-access",
				RefreshToken: "refresh-1",
				Scopes:       []string{"openid", "email"},
				ExpiresAt:    now.Add(-1 * time.Minute),
			}, AccountProfile{Subject: "sub-scope", Scopes: []string{"openid", "email"}}, nil
		},
		refreshFn: func(_ context.Context, _ ProviderConfig, _ string) (TokenSet, error) {
			return TokenSet{
				AccessToken:  "escalated-access",
				RefreshToken: "refresh-2",
				Scopes:       []string{"openid", "email", "admin:write"},
				ExpiresAt:    now.Add(1 * time.Hour),
			}, nil
		},
	}
	mgr := NewManager(ManagerConfig{Exchanger: ex, Now: func() time.Time { return now }})
	_ = mgr.RegisterProvider(testProviderConfig())
	flow, _ := mgr.BeginOAuth(ProviderOIDC, "")
	mgr.mu.RLock()
	state := mgr.oauthAttempts[flow.AttemptID].state
	mgr.mu.RUnlock()
	acc, err := mgr.CompleteOAuthCallback(context.Background(), flow.AttemptID, LoopbackCallback{Code: "c1", State: state})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.ValidAccessToken(context.Background(), acc.ID); !errors.Is(err, ErrScopeEscalation) {
		t.Fatalf("expected ErrScopeEscalation, got %v", err)
	}
}

func TestOIDCNonceMismatchRejected(t *testing.T) {
	makeJWT := func(nonce string) string {
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
		payloadBytes, _ := json.Marshal(map[string]string{"sub": "u1", "nonce": nonce})
		payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
		return header + "." + payload + ".sig"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-1",
			"id_token":     makeJWT("wrong-nonce"),
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	ex := NewHTTPOAuthExchanger(srv.Client())
	cfg := testProviderConfig()
	cfg.TokenURL = srv.URL
	cfg.AllowLoopback = true
	if _, _, err := ex.ExchangeCode(context.Background(), cfg, "code", "verifier", cfg.RedirectURI, "expected-nonce"); !errors.Is(err, ErrNonceMismatch) {
		t.Fatalf("expected ErrNonceMismatch, got %v", err)
	}
}

func TestLoopbackIgnoresWrongStateBeforeLegitimateCallback(t *testing.T) {
	recv, err := StartLoopbackReceiver()
	if err != nil {
		t.Fatal(err)
	}
	defer recv.Close()
	recv.SetExpectedState("good-state-123456")

	// Adversarial/prefetch request with wrong state must NOT consume the callback slot.
	resp, err := http.Get(recv.RedirectURI() + "?code=bad&state=wrong-state")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for wrong state, got %d", resp.StatusCode)
	}

	// Legitimate callback must still succeed!
	go func() {
		r, gErr := http.Get(recv.RedirectURI() + "?code=good-code&state=good-state-123456")
		if gErr == nil {
			_ = r.Body.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cb, err := recv.Wait(ctx)
	if err != nil {
		t.Fatalf("expected legitimate callback after wrong-state hit, got %v", err)
	}
	if cb.Code != "good-code" || cb.State != "good-state-123456" {
		t.Fatalf("unexpected callback: %+v", cb)
	}
}
