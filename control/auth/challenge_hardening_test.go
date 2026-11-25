// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func signTestEd25519JWT(t *testing.T, priv ed25519.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	hdrJSON, _ := json.Marshal(map[string]any{"alg": "EdDSA", "kid": kid, "typ": "JWT"})
	payJSON, _ := json.Marshal(claims)
	h := base64.RawURLEncoding.EncodeToString(hdrJSON)
	p := base64.RawURLEncoding.EncodeToString(payJSON)
	sig := ed25519.Sign(priv, []byte(h+"."+p))
	return h + "." + p + "." + base64.RawURLEncoding.EncodeToString(sig)
}

type fakeLockableStore struct {
	data map[string][]byte
}

func (f *fakeLockableStore) Name() string    { return "Fake Keychain" }
func (f *fakeLockableStore) Available() bool { return true }
func (f *fakeLockableStore) GetSecret(_ context.Context, service, account string) ([]byte, error) {
	v, ok := f.data[service+":"+account]
	if !ok {
		return nil, ErrKeychainNotFound
	}
	return append([]byte(nil), v...), nil
}
func (f *fakeLockableStore) SetSecret(_ context.Context, service, account string, secret []byte) error {
	if f.data == nil {
		f.data = make(map[string][]byte)
	}
	f.data[service+":"+account] = append([]byte(nil), secret...)
	return nil
}
func (f *fakeLockableStore) DeleteSecret(_ context.Context, service, account string) error {
	delete(f.data, service+":"+account)
	return nil
}

func TestChallengeC1MultiAudienceRequiresAZP(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ks := &JWKSKeySet{Keys: []JWK{{KeyID: "k1", KeyType: "OKP", Algorithm: "EdDSA", PublicKey: pub}}}
	now := time.Now().UTC()

	// Multi-audience token missing azp MUST be rejected (OIDC Core §3.1.3.7).
	tokNoAzp := signTestEd25519JWT(t, priv, "k1", map[string]any{
		"iss": "https://idp.example.com",
		"sub": "user-1",
		"aud": []string{"client-a", "client-b"},
		"exp": now.Add(time.Hour).Unix(),
	})
	_, err = VerifyIDToken(tokNoAzp, ks, IDTokenVerifyConfig{
		ExpectedIssuer:   "https://idp.example.com",
		ExpectedAudience: "client-a",
		Now:              func() time.Time { return now },
	})
	if !errors.Is(err, ErrAZPMismatch) {
		t.Fatalf("expected ErrAZPMismatch for multi-aud token without azp, got %v", err)
	}

	// Multi-audience token with matching azp MUST succeed.
	tokGoodAzp := signTestEd25519JWT(t, priv, "k1", map[string]any{
		"iss": "https://idp.example.com",
		"sub": "user-1",
		"aud": []string{"client-a", "client-b"},
		"azp": "client-a",
		"exp": now.Add(time.Hour).Unix(),
	})
	claims, err := VerifyIDToken(tokGoodAzp, ks, IDTokenVerifyConfig{
		ExpectedIssuer:   "https://idp.example.com",
		ExpectedAudience: "client-a",
		Now:              func() time.Time { return now },
	})
	if err != nil || claims.AuthorizedParty != "client-a" {
		t.Fatalf("expected valid multi-aud token with matching azp, got claims=%+v err=%v", claims, err)
	}
}

func TestChallengeC1UserInfoSubjectMismatchRejected(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jwks":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]any{{
					"kty": "OKP", "crv": "Ed25519", "kid": "k1", "alg": "EdDSA", "use": "sig",
					"x": base64.RawURLEncoding.EncodeToString(pub),
				}},
			})
		case "/token":
			idTok := signTestEd25519JWT(t, priv, "k1", map[string]any{
				"iss":   srv.URL,
				"sub":   "alice-sub",
				"aud":   "marlin-cli",
				"nonce": "nonce-123",
				"exp":   now.Add(time.Hour).Unix(),
			})
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "at-1",
				"id_token":     idTok,
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		case "/userinfo":
			// Attacker substitutes a different subject at UserInfo!
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sub":   "mallory-sub",
				"email": "mallory@example.com",
			})
		}
	}))
	defer srv.Close()

	ex := NewHTTPOAuthExchanger(srv.Client())
	cfg := ProviderConfig{
		ID:            ProviderOIDC,
		DisplayName:   "Test OIDC",
		Method:        MethodOAuthPKCE,
		AuthURL:       srv.URL + "/auth",
		TokenURL:      srv.URL + "/token",
		JWKSURL:       srv.URL + "/jwks",
		UserInfoURL:   srv.URL + "/userinfo",
		Issuer:        srv.URL,
		ClientID:      "marlin-cli",
		AllowLoopback: true,
	}
	_, _, err = ex.ExchangeCode(context.Background(), cfg, "code-1", "verifier-1", "http://127.0.0.1/cb", "nonce-123")
	if !errors.Is(err, ErrSubjectMismatch) {
		t.Fatalf("expected ErrSubjectMismatch when UserInfo sub != ID token sub, got %v", err)
	}
}

func TestChallengeC2JWKSForceRefreshCooldown(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{}})
	}))
	defer srv.Close()

	cache := NewHTTPJWKSCache(srv.Client(), 15*time.Minute)
	cache.RefreshCooldown = time.Minute

	if _, err := cache.Get(context.Background(), srv.URL, true, true); err != nil {
		t.Fatalf("first force refresh should succeed, got %v", err)
	}
	if _, err := cache.Get(context.Background(), srv.URL, true, true); !errors.Is(err, ErrJWKSRefreshCooldown) {
		t.Fatalf("second immediate force refresh must fail closed with ErrJWKSRefreshCooldown, got %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected exactly 1 upstream JWKS hit, got %d", hits.Load())
	}
}

func TestChallengeC3AuthorizerFailsClosedWhenVaultLocked(t *testing.T) {
	ctx := context.Background()
	store := &fakeLockableStore{}
	v, _ := OpenNativeVault(ctx, NativeVaultConfig{
		VaultPath: filepath.Join(t.TempDir(), "vault.enc"),
		Store:     store,
	})
	osVault, ok := v.(*OSProtectedVault)
	if !ok {
		t.Fatalf("expected *OSProtectedVault, got %T", v)
	}

	mgr := NewManager(ManagerConfig{Vault: osVault})
	cfg := ProviderConfig{
		ID:          ProviderGoogle,
		DisplayName: "Google",
		Method:      MethodOAuthPKCE,
		AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:    "https://oauth2.googleapis.com/token",
		ClientID:    "marlin-client",
		Scopes:      []string{"openid"},
	}
	if err := mgr.RegisterProvider(cfg); err != nil {
		t.Fatal(err)
	}
	prof, err := mgr.admitSignedInAccount(ctx, cfg, TokenSet{AccessToken: "tok-1"}, AccountProfile{
		Subject:       "u1",
		Email:         "u1@example.com",
		EmailVerified: true,
		Scopes:        []string{"openid"},
	})
	if err != nil {
		t.Fatal(err)
	}

	authz := NewAuthorizer(mgr)
	policy := []byte(`permit(principal is Marlin::Account, action == Marlin::Action::"invoke_grpc", resource is Marlin::Resource) when { context.scopes.contains("openid") };`)
	if _, err := authz.LoadPolicyBytes("test.cedar", policy); err != nil {
		t.Fatal(err)
	}

	ticket, err := authz.Authorize(prof.ID, "invoke_grpc", "rpc/ping", nil)
	if err != nil || !ticket.Allowed {
		t.Fatalf("expected Allow while vault unlocked, got ticket=%+v err=%v", ticket, err)
	}

	// Lock the vault: BOTH Enforce(existingTicket) AND new Evaluate MUST fail closed!
	osVault.Lock()
	if err := authz.Enforce(ticket); !errors.Is(err, ErrVaultLocked) {
		t.Fatalf("expected ErrVaultLocked on Enforce after Lock, got %v", err)
	}
	if _, err := authz.Authorize(prof.ID, "invoke_grpc", "rpc/ping", nil); !errors.Is(err, ErrVaultLocked) {
		t.Fatalf("expected ErrVaultLocked on Authorize after Lock, got %v", err)
	}
}

func TestChallengeC4EncryptedVaultDetectsRollback(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	vaultPath := filepath.Join(dir, "auth-vault.enc")
	key := []byte("0123456789abcdef0123456789abcdef")

	v, err := NewEncryptedFileVault(vaultPath, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Store(ctx, "acct-1", TokenSet{AccessToken: "old-token"}); err != nil {
		t.Fatal(err)
	}
	rev1 := v.Revision()
	snap1, err := os.ReadFile(vaultPath)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate SignOut (revision advances to 2).
	if err := v.Delete(ctx, "acct-1"); err != nil {
		t.Fatal(err)
	}
	rev2 := v.Revision()
	if rev2 <= rev1 {
		t.Fatalf("expected monotonic revision increase: rev1=%d rev2=%d", rev1, rev2)
	}

	// Attacker replays old vault snapshot from rev1 onto disk!
	if err := os.WriteFile(vaultPath, snap1, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := v.Load(ctx, "acct-1"); !errors.Is(err, ErrVaultRollback) {
		t.Fatalf("expected ErrVaultRollback on replayed snapshot, got %v", err)
	}
}

type stubDeviceFlowBackend struct {
	resp        DeviceCodeResponse
	pollTokens  TokenSet
	pollProfile AccountProfile
	pollReady   bool
	onPoll      func()
}

func (s *stubDeviceFlowBackend) RequestDeviceCode(_ context.Context, _ ProviderConfig) (DeviceCodeResponse, error) {
	return s.resp, nil
}
func (s *stubDeviceFlowBackend) PollDeviceToken(_ context.Context, _ ProviderConfig, _ string) (TokenSet, AccountProfile, bool, error) {
	if s.onPoll != nil {
		s.onPoll()
	}
	return s.pollTokens, s.pollProfile, s.pollReady, nil
}

func TestChallengeC6DeviceFlowCancelAndExpiryEnforced(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	var mgr *AuthManager
	devBackend := &stubDeviceFlowBackend{
		resp: DeviceCodeResponse{
			DeviceCode:      "dev-secret-1",
			UserCode:        "ABCD-1234",
			VerificationURI: "https://github.com/login/device",
			ExpiresIn:       5 * time.Minute,
		},
		pollTokens:  TokenSet{AccessToken: "dev-at-1"},
		pollProfile: AccountProfile{Subject: "dev-u1", Email: "dev@example.com"},
		pollReady:   true,
	}
	mgr = NewManager(ManagerConfig{
		DeviceFlow: devBackend,
		Now:        func() time.Time { return now },
	})
	cfg := ProviderConfig{
		ID:            ProviderGitHub,
		DisplayName:   "GitHub",
		Method:        MethodDeviceCode,
		DeviceAuthURL: "https://github.com/login/device/code",
		TokenURL:      "https://github.com/login/oauth/access_token",
		ClientID:      "marlin-cli",
	}
	if err := mgr.RegisterProvider(cfg); err != nil {
		t.Fatal(err)
	}

	// 1. In-flight CancelPending during PollDeviceToken MUST reject admission with ErrFlowCancelled.
	_, devCode, err := mgr.BeginDeviceAuth(ctx, ProviderGitHub)
	if err != nil {
		t.Fatal(err)
	}
	devBackend.onPoll = func() { mgr.CancelPending() }
	if _, _, err := mgr.CompleteDeviceAuth(ctx, ProviderGitHub, devCode); !errors.Is(err, ErrFlowCancelled) {
		t.Fatalf("expected ErrFlowCancelled when cancelled during device poll, got %v", err)
	}
	if len(mgr.Snapshot().Accounts) != 0 {
		t.Fatalf("cancelled device flow must not admit account")
	}

	// 2. Expired device code MUST be rejected with ErrAttemptExpired.
	devBackend.onPoll = nil
	_, devCode2, err := mgr.BeginDeviceAuth(ctx, ProviderGitHub)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Minute)
	if _, _, err := mgr.CompleteDeviceAuth(ctx, ProviderGitHub, devCode2); !errors.Is(err, ErrAttemptExpired) {
		t.Fatalf("expected ErrAttemptExpired for expired device code, got %v", err)
	}

	// 3. Insecure http:// VerificationURI MUST be rejected fail-closed.
	devBackend.resp.VerificationURI = "http://evil.example.com/phish"
	if _, _, err := mgr.BeginDeviceAuth(ctx, ProviderGitHub); !errors.Is(err, ErrInsecureEndpoint) {
		t.Fatalf("expected ErrInsecureEndpoint for non-HTTPS VerificationURI, got %v", err)
	}
}

func TestChallengeC7AuthorizedBearerHeaderPreventsCrossAccountConfusion(t *testing.T) {
	ctx := context.Background()
	mgr := NewManager(ManagerConfig{})
	cfg := ProviderConfig{
		ID:          ProviderGoogle,
		DisplayName: "Google",
		Method:      MethodOAuthPKCE,
		AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:    "https://oauth2.googleapis.com/token",
		ClientID:    "marlin-cli",
		Scopes:      []string{"openid"},
	}
	if err := mgr.RegisterProvider(cfg); err != nil {
		t.Fatal(err)
	}
	accA, err := mgr.admitSignedInAccount(ctx, cfg, TokenSet{AccessToken: "token-account-A"}, AccountProfile{
		Subject: "user-A", Email: "a@example.com", EmailVerified: true, Scopes: []string{"openid"},
	})
	if err != nil {
		t.Fatal(err)
	}
	accB, err := mgr.admitSignedInAccount(ctx, cfg, TokenSet{AccessToken: "token-account-B"}, AccountProfile{
		Subject: "user-B", Email: "b@example.com", EmailVerified: true, Scopes: []string{"openid"},
	})
	if err != nil {
		t.Fatal(err)
	}

	authz := NewAuthorizer(mgr)
	policy := []byte(`permit(principal is Marlin::Account, action == Marlin::Action::"invoke_grpc", resource is Marlin::Resource) when { context.scopes.contains("openid") };`)
	if _, err := authz.LoadPolicyBytes("test.cedar", policy); err != nil {
		t.Fatal(err)
	}

	// Issue authorization ticket specifically for Account A.
	ticketA, err := authz.Authorize(accA.ID, "invoke_grpc", "rpc/run", nil)
	if err != nil {
		t.Fatal(err)
	}
	// User switches active account to Account B before gRPC call attaches Bearer header!
	if err := mgr.SwitchAccount(accB.ID); err != nil {
		t.Fatal(err)
	}
	hdr, err := authz.AuthorizedBearerHeader(ctx, ticketA)
	if err != nil {
		t.Fatal(err)
	}
	if hdr != "Bearer token-account-A" {
		t.Fatalf("cross-account token leak! expected Bearer token-account-A, got %q", hdr)
	}
}

func TestChallengeC8BrowserURLRejectsShellMetachars(t *testing.T) {
	if err := OpenSystemBrowser(context.Background(), "https://idp.example.com/auth?a=1&b=2\"|calc"); !errors.Is(err, ErrInsecureEndpoint) {
		t.Fatalf("expected ErrInsecureEndpoint for URL with quote/pipe metachars, got %v", err)
	}
}

func TestChallengeC9CrossRestartVaultRollbackBlockedByKeychainWatermark(t *testing.T) {
	ctx := context.Background()
	store := &fakeLockableStore{}
	vaultPath := filepath.Join(t.TempDir(), "auth-vault.enc")

	// Session 1: open native vault, store token (rev 1), snapshot disk file, then SignOut/Delete (rev 2).
	v1, _ := OpenNativeVault(ctx, NativeVaultConfig{VaultPath: vaultPath, Store: store})
	if err := v1.Store(ctx, "acct-1", TokenSet{AccessToken: "secret-rev1"}); err != nil {
		t.Fatal(err)
	}
	oldDiskSnap, err := os.ReadFile(vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := v1.Delete(ctx, "acct-1"); err != nil {
		t.Fatal(err)
	}

	// Offline host attacker rolls back auth-vault.enc on disk while app is closed!
	if err := os.WriteFile(vaultPath, oldDiskSnap, 0o600); err != nil {
		t.Fatal(err)
	}

	// Session 2 (fresh process restart): OpenNativeVault loads revision watermark from OS Keychain
	// and MUST reject the rolled-back disk file with ErrVaultRollback!
	v2, _ := OpenNativeVault(ctx, NativeVaultConfig{VaultPath: vaultPath, Store: store})
	if _, _, err := v2.Load(ctx, "acct-1"); !errors.Is(err, ErrVaultRollback) {
		t.Fatalf("expected ErrVaultRollback across process restart, got %v", err)
	}
}

func TestChallengeC10OAuthAndOIDCEndpointsBlockRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://attacker.example.com/steal", http.StatusFound)
	}))
	defer srv.Close()

	// 1. Token exchange endpoint redirect MUST be blocked fail-closed.
	ex := NewHTTPOAuthExchanger(srv.Client())
	cfg := ProviderConfig{
		ID:            ProviderOIDC,
		DisplayName:   "Test",
		Method:        MethodOAuthPKCE,
		AuthURL:       srv.URL + "/auth",
		TokenURL:      srv.URL + "/token",
		ClientID:      "cli",
		AllowLoopback: true,
	}
	if _, _, err := ex.ExchangeCode(context.Background(), cfg, "code", "verifier", "http://127.0.0.1/cb", "nonce"); !errors.Is(err, ErrRedirectBlocked) {
		t.Fatalf("expected ErrRedirectBlocked on token endpoint redirect, got %v", err)
	}

	// 2. OIDC Discovery redirect MUST be blocked fail-closed.
	if _, err := DiscoverOIDCProvider(context.Background(), srv.Client(), srv.URL, "cli", "Test", true); !errors.Is(err, ErrRedirectBlocked) {
		t.Fatalf("expected ErrRedirectBlocked on OIDC discovery redirect, got %v", err)
	}

	// 3. JWKS endpoint redirect MUST be blocked fail-closed.
	cache := NewHTTPJWKSCache(srv.Client(), time.Minute)
	if _, err := cache.Get(context.Background(), srv.URL+"/jwks", true, true); !errors.Is(err, ErrRedirectBlocked) {
		t.Fatalf("expected ErrRedirectBlocked on JWKS redirect, got %v", err)
	}
}
