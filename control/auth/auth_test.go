// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/control/auth"
)

func TestSecurityEndpointEmailAndRedactionInvariants(t *testing.T) {
	// 1. Remote HTTP OAuth endpoints MUST be rejected fail-closed!
	badProv := auth.ProviderConfig{
		ID:          "evil",
		DisplayName: "Evil HTTP",
		Method:      auth.MethodOAuthPKCE,
		AuthURL:     "http://accounts.example.com/auth",
		TokenURL:    "https://accounts.example.com/token",
		ClientID:    "cid",
	}
	if err := badProv.Validate(); !errors.Is(err, auth.ErrInsecureEndpoint) {
		t.Fatalf("expected ErrInsecureEndpoint for remote HTTP AuthURL, got %v", err)
	}
	if err := auth.GoogleOAuthProvider("google-client-123").Validate(); err != nil {
		t.Fatalf("expected canonical Google OAuth preset to validate, got %v", err)
	}

	// 2. Email validation rejects malformed/injection strings and normalizes valid addresses.
	for _, bad := range []string{"", "not-an-email", "a@b", "user@bad..com", ".lead@x.com", "a b@c.com", "a@b.com\r\nBcc:evil"} {
		if _, err := auth.NormalizeAndValidateEmail(bad); !errors.Is(err, auth.ErrInvalidEmail) {
			t.Fatalf("expected ErrInvalidEmail for %q, got %v", bad, err)
		}
	}
	norm, err := auth.NormalizeAndValidateEmail("  Alice.Dev@Example.COM ")
	if err != nil || norm != "alice.dev@example.com" {
		t.Fatalf("expected normalized email alice.dev@example.com, got %q err=%v", norm, err)
	}

	// 3. String()/GoString() MUST NEVER leak raw tokens or PKCE verifiers.
	tok := auth.TokenSet{
		AccessToken:  "ya29.super-secret-access-token",
		RefreshToken: "1//super-secret-refresh-token",
		IDToken:      "eyJhbGciOiJSUzI1NiJ9.secret",
		TokenType:    "Bearer",
	}
	if s := fmt.Sprintf("%s %#v", tok, tok); strings.Contains(s, "super-secret") || strings.Contains(s, "eyJhbGci") {
		t.Fatalf("TokenSet leaked secret material in String/GoString: %s", s)
	}
	pkce, err := auth.GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if s := pkce.String(); strings.Contains(s, pkce.Verifier) {
		t.Fatalf("PKCEPair.String() leaked verifier: %s", s)
	}
}

func TestOAuthPKCES256LoopbackEndToEndAndReplayProtection(t *testing.T) {
	var (
		mu              sync.Mutex
		savedChallenge  string
		revokedTokens   []string
		callbackHeaders http.Header
	)

	// Mock OAuth 2.1 / OIDC Identity Provider verifying PKCE S256 server-side!
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			verifier := r.FormValue("code_verifier")
			sum := sha256.Sum256([]byte(verifier))
			gotChallenge := base64.RawURLEncoding.EncodeToString(sum[:])

			mu.Lock()
			wantChallenge := savedChallenge
			mu.Unlock()
			if gotChallenge == "" || gotChallenge != wantChallenge {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "PKCE S256 mismatch"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "live-google-access-token-1",
				"refresh_token": "live-google-refresh-token-1",
				"token_type":    "Bearer",
				"scope":         "openid email profile",
				"expires_in":    3600,
			})
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer live-google-access-token-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sub":            "google-sub-42",
				"email":          "ada@marlin.dev",
				"name":           "Ada Lovelace",
				"email_verified": true,
			})
		case "/revoke":
			_ = r.ParseForm()
			mu.Lock()
			revokedTokens = append(revokedTokens, r.FormValue("token"))
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer idp.Close()

	vault := auth.NewMemoryVault()
	mgr := auth.NewManager(auth.ManagerConfig{
		Vault: vault,
		OpenBrowser: func(ctx context.Context, authorizeURL string) error {
			u, err := url.Parse(authorizeURL)
			if err != nil {
				return err
			}
			q := u.Query()
			if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("nonce") == "" {
				return fmt.Errorf("missing mandatory OAuth 2.1 PKCE/state/nonce params: %s", authorizeURL)
			}
			mu.Lock()
			savedChallenge = q.Get("code_challenge")
			mu.Unlock()

			// Simulate system browser redirecting back to Marlin's ephemeral 127.0.0.1 callback!
			cbURL := fmt.Sprintf("%s?code=auth-code-99&state=%s", q.Get("redirect_uri"), url.QueryEscape(q.Get("state")))
			go func() {
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, cbURL, nil)
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					mu.Lock()
					callbackHeaders = resp.Header.Clone()
					mu.Unlock()
					_ = resp.Body.Close()
				}
			}()
			return nil
		},
	})

	if err := mgr.RegisterProvider(auth.ProviderConfig{
		ID:              auth.ProviderGoogle,
		DisplayName:     "Google",
		Method:          auth.MethodOAuthPKCE,
		AuthURL:         idp.URL + "/authorize",
		TokenURL:        idp.URL + "/token",
		UserInfoURL:     idp.URL + "/userinfo",
		RevokeURL:       idp.URL + "/revoke",
		ClientID:        "marlin-native-client",
		Scopes:          []string{"openid", "email", "profile"},
		AllowLoopback:   true,
		SupportsRefresh: true,
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	prof, err := mgr.RunLoopbackOAuth(ctx, auth.ProviderGoogle)
	if err != nil {
		t.Fatalf("RunLoopbackOAuth failed: %v", err)
	}
	if prof.Email != "ada@marlin.dev" || prof.DisplayName != "Ada Lovelace" || !prof.EmailVerified {
		t.Fatalf("unexpected OAuth profile: %+v", prof)
	}

	// Verify loopback receiver enforced browser hardening headers!
	mu.Lock()
	hdrs := callbackHeaders
	mu.Unlock()
	if hdrs.Get("X-Frame-Options") != "DENY" || !strings.Contains(hdrs.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("expected hardened callback security headers, got %+v", hdrs)
	}

	// Verify AuthorizationHeader works and AuthSnapshot NEVER exposes raw tokens!
	hdr, err := mgr.AuthorizationHeader(ctx)
	if err != nil || hdr != "Bearer live-google-access-token-1" {
		t.Fatalf("unexpected AuthorizationHeader: %q err=%v", hdr, err)
	}
	snapJSON, _ := json.Marshal(mgr.Snapshot())
	if strings.Contains(string(snapJSON), "live-google-access-token-1") || strings.Contains(string(snapJSON), "live-google-refresh-token-1") {
		t.Fatalf("AuthSnapshot leaked vaulted secret token: %s", string(snapJSON))
	}

	// Verify CSRF wrong-state & replay rejection!
	flow2, err := mgr.BeginOAuth(auth.ProviderGoogle, "http://127.0.0.1:9999/oauth/callback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.CompleteOAuthCallback(ctx, flow2.AttemptID, auth.LoopbackCallback{
		Code:  "forged",
		State: "wrong-csrf-state",
	}); !errors.Is(err, auth.ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState for forged CSRF state, got %v", err)
	}
	// Attempt must be consumed even after failed state check (no replay probing).
	if _, err := mgr.CompleteOAuthCallback(ctx, flow2.AttemptID, auth.LoopbackCallback{
		Code:  "forged",
		State: "still-consumed",
	}); !errors.Is(err, auth.ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState on consumed attempt replay, got %v", err)
	}

	// SignOut MUST invoke RevokeURL and scrub credentials from vault!
	if err := mgr.SignOut(ctx, prof.ID); err != nil {
		t.Fatalf("SignOut failed: %v", err)
	}
	mu.Lock()
	revoked := append([]string(nil), revokedTokens...)
	mu.Unlock()
	if len(revoked) != 1 || revoked[0] != "live-google-refresh-token-1" {
		t.Fatalf("expected refresh token revocation on SignOut, got %v", revoked)
	}
	if _, found, _ := vault.Load(ctx, prof.ID); found {
		t.Fatal("expected vault to scrub deleted account tokens on SignOut")
	}
}

type fakeEmailBackend struct {
	expectedCode string
	requests     int
}

func (b *fakeEmailBackend) RequestChallenge(_ context.Context, _ string, _ auth.AuthMethod) (auth.EmailChallengeResponse, error) {
	b.requests++
	return auth.EmailChallengeResponse{
		ChallengeID: fmt.Sprintf("ch-%d", b.requests),
		ExpiresIn:   5 * time.Minute,
		Cooldown:    30 * time.Second,
		MaxAttempts: 3,
	}, nil
}

func (b *fakeEmailBackend) VerifyChallenge(_ context.Context, _, email, code string, _ auth.AuthMethod) (auth.TokenSet, auth.AccountProfile, error) {
	if code != b.expectedCode {
		return auth.TokenSet{}, auth.AccountProfile{}, errors.New("invalid verification code")
	}
	return auth.TokenSet{
			AccessToken:  "email-access-token",
			RefreshToken: "email-refresh-token",
			TokenType:    "Bearer",
			ExpiresAt:    time.Now().Add(time.Hour),
		}, auth.AccountProfile{
			Email:         email,
			DisplayName:   "Email User",
			EmailVerified: true,
		}, nil
}

func TestEmailOTPAndMagicLinkSecurityInvariants(t *testing.T) {
	now := time.Date(2026, 11, 25, 22, 0, 0, 0, time.UTC)
	backend := &fakeEmailBackend{expectedCode: "482910"}
	mgr := auth.NewManager(auth.ManagerConfig{
		EmailBackend: backend,
		Now:          func() time.Time { return now },
	})
	_ = mgr.RegisterProvider(auth.EmailOTPProvider())

	ctx := context.Background()
	ch, err := mgr.RequestEmailLogin(ctx, "Builder@Marlin.Dev", auth.MethodEmailOTP)
	if err != nil || ch.Email != "builder@marlin.dev" {
		t.Fatalf("RequestEmailLogin failed: ch=%+v err=%v", ch, err)
	}

	// Immediate resend MUST be blocked by cooldown!
	if _, err := mgr.RequestEmailLogin(ctx, "builder@marlin.dev", auth.MethodEmailOTP); !errors.Is(err, auth.ErrCooldownActive) {
		t.Fatalf("expected ErrCooldownActive on rapid resend, got %v", err)
	}

	// Brute-force lockout after MaxAttempts (3) wrong codes!
	for i := 0; i < 3; i++ {
		if _, err := mgr.VerifyEmailLogin(ctx, "000000"); err == nil {
			t.Fatalf("expected wrong OTP #%d to fail", i+1)
		}
	}
	if mgr.Snapshot().PendingEmail != nil {
		t.Fatal("expected pending email challenge to be invalidated after max attempts")
	}

	// Advance past cooldown -> request Magic Link -> verify cleanly!
	now = now.Add(35 * time.Second)
	if _, err := mgr.RequestEmailLogin(ctx, "builder@marlin.dev", auth.MethodEmailMagicLink); err != nil {
		t.Fatalf("RequestEmailLogin(magic link) after cooldown failed: %v", err)
	}
	prof, err := mgr.VerifyEmailLogin(ctx, "482910")
	if err != nil || !prof.EmailVerified || prof.Method != auth.MethodEmailMagicLink {
		t.Fatalf("VerifyEmailLogin failed: prof=%+v err=%v", prof, err)
	}
}

type countingExchanger struct {
	refreshCalls atomic.Int32
}

func (c *countingExchanger) ExchangeCode(context.Context, auth.ProviderConfig, string, string, string, string) (auth.TokenSet, auth.AccountProfile, error) {
	return auth.TokenSet{}, auth.AccountProfile{}, nil
}

func (c *countingExchanger) RefreshToken(_ context.Context, _ auth.ProviderConfig, refreshToken string) (auth.TokenSet, error) {
	c.refreshCalls.Add(1)
	time.Sleep(25 * time.Millisecond)
	return auth.TokenSet{
		AccessToken:  "rotated-access-token",
		RefreshToken: refreshToken + "-v2",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(time.Hour),
	}, nil
}

func (c *countingExchanger) RevokeToken(context.Context, auth.ProviderConfig, string) error {
	return nil
}

func TestSingleflightTokenRefreshCoalescesConcurrentStorm(t *testing.T) {
	ex := &countingExchanger{}
	now := time.Now()
	vault := auth.NewMemoryVault()
	mgr := auth.NewManager(auth.ManagerConfig{
		Vault:        vault,
		Exchanger:    ex,
		EmailBackend: &fakeEmailBackend{expectedCode: "111111"},
		Now:          func() time.Time { return now },
	})
	_ = mgr.RegisterProvider(auth.EmailOTPProvider())

	ctx := context.Background()
	_, _ = mgr.RequestEmailLogin(ctx, "ops@marlin.dev", auth.MethodEmailOTP)
	prof, err := mgr.VerifyEmailLogin(ctx, "111111")
	if err != nil {
		t.Fatal(err)
	}
	// Replace vaulted token with an already-expired access token + valid refresh token.
	_ = vault.Store(ctx, prof.ID, auth.TokenSet{
		AccessToken:  "stale-expired-token",
		RefreshToken: "valid-refresh-token",
		ExpiresAt:    now.Add(-time.Minute),
	})

	// 16 concurrent gRPC callers request ValidAccessToken simultaneously -> MUST refresh ONCE!
	const callers = 16
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := mgr.ValidAccessToken(ctx, prof.ID)
			if err != nil {
				errs <- err
				return
			}
			if tok != "rotated-access-token" {
				errs <- fmt.Errorf("unexpected token %q", tok)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got := ex.refreshCalls.Load(); got != 1 {
		t.Fatalf("expected singleflight refresh to invoke RefreshToken exactly 1 time across %d callers, got %d", callers, got)
	}
}

func TestEncryptedFileVaultAES256GCMTamperAndZeroPlaintext(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "credentials.vault")
	masterKey := []byte("0123456789abcdef0123456789abcdef")

	vault, err := auth.NewEncryptedFileVault(vaultPath, masterKey)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	secretAccess := "gho_verySecretGitHubToken998877"
	if err := vault.Store(ctx, "github:alice", auth.TokenSet{
		AccessToken:  secretAccess,
		RefreshToken: "ghr_verySecretRefreshToken",
		TokenType:    "Bearer",
	}); err != nil {
		t.Fatal(err)
	}

	// File permissions MUST be 0600 and ciphertext MUST contain ZERO plaintext secret!
	info, err := os.Stat(vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 vault file permissions, got %o", info.Mode().Perm())
	}
	raw, err := os.ReadFile(vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secretAccess)) {
		t.Fatal("plaintext access token leaked into encrypted vault file!")
	}

	// Valid decrypt roundtrip.
	loaded, ok, err := vault.Load(ctx, "github:alice")
	if err != nil || !ok || loaded.AccessToken != secretAccess {
		t.Fatalf("vault.Load failed: ok=%v err=%v", ok, err)
	}

	// Wrong master key MUST fail authentication!
	wrongVault, _ := auth.NewEncryptedFileVault(vaultPath, []byte("wrong-master-key-000000000000000"))
	if _, _, err := wrongVault.Load(ctx, "github:alice"); err == nil {
		t.Fatal("expected GCM auth failure with wrong master key")
	}

	// Flipping 1 ciphertext byte MUST fail GCM integrity check!
	raw[len(raw)-1] ^= 0xFF
	if err := os.WriteFile(vaultPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := vault.Load(ctx, "github:alice"); err == nil {
		t.Fatal("expected GCM authentication error after 1-byte ciphertext tamper")
	}
}

type fakeDeviceFlow struct {
	ready bool
}

func (f *fakeDeviceFlow) RequestDeviceCode(context.Context, auth.ProviderConfig) (auth.DeviceCodeResponse, error) {
	return auth.DeviceCodeResponse{
		DeviceCode:      "dev-code-1",
		UserCode:        "WDJB-MJHT",
		VerificationURI: "https://github.com/login/device",
		ExpiresIn:       10 * time.Minute,
		Interval:        5 * time.Second,
	}, nil
}

func (f *fakeDeviceFlow) PollDeviceToken(context.Context, auth.ProviderConfig, string) (auth.TokenSet, auth.AccountProfile, bool, error) {
	if !f.ready {
		return auth.TokenSet{}, auth.AccountProfile{}, false, nil
	}
	return auth.TokenSet{
			AccessToken: "gh-device-access-token",
			TokenType:   "Bearer",
		}, auth.AccountProfile{
			Subject:     "octocat",
			Email:       "octocat@github.com",
			DisplayName: "Octocat",
		}, true, nil
}

func TestDeviceAuthorizationFlowAndMultiAccountSwitch(t *testing.T) {
	devFlow := &fakeDeviceFlow{}
	mgr := auth.NewManager(auth.ManagerConfig{
		DeviceFlow:   devFlow,
		EmailBackend: &fakeEmailBackend{expectedCode: "654321"},
	})
	_ = mgr.RegisterProvider(auth.GitHubOAuthProvider("gh-client"))
	_ = mgr.RegisterProvider(auth.EmailOTPProvider())

	ctx := context.Background()
	flow, devCode, err := mgr.BeginDeviceAuth(ctx, auth.ProviderGitHub)
	if err != nil || flow.UserCode != "WDJB-MJHT" {
		t.Fatalf("BeginDeviceAuth failed: flow=%+v err=%v", flow, err)
	}
	if _, ready, err := mgr.CompleteDeviceAuth(ctx, auth.ProviderGitHub, devCode); err != nil || ready {
		t.Fatalf("expected authorization_pending before user approves, got ready=%v err=%v", ready, err)
	}
	devFlow.ready = true
	ghProf, ready, err := mgr.CompleteDeviceAuth(ctx, auth.ProviderGitHub, devCode)
	if err != nil || !ready || ghProf.DisplayName != "Octocat" {
		t.Fatalf("CompleteDeviceAuth failed: prof=%+v ready=%v err=%v", ghProf, ready, err)
	}

	// Sign in second account (Email) and test multi-account switching + SignOutAll!
	_, _ = mgr.RequestEmailLogin(ctx, "work@marlin.dev", auth.MethodEmailOTP)
	emailProf, err := mgr.VerifyEmailLogin(ctx, "654321")
	if err != nil {
		t.Fatal(err)
	}
	if got := mgr.Snapshot().ActiveAccountID; got != emailProf.ID {
		t.Fatalf("expected newest account %s active, got %s", emailProf.ID, got)
	}
	if err := mgr.SwitchAccount(ghProf.ID); err != nil {
		t.Fatal(err)
	}
	if got := mgr.Snapshot().ActiveAccountID; got != ghProf.ID {
		t.Fatalf("expected switched active account %s, got %s", ghProf.ID, got)
	}
	if err := mgr.SignOutAll(ctx); err != nil {
		t.Fatal(err)
	}
	if snap := mgr.Snapshot(); snap.Phase != auth.PhaseSignedOut || len(snap.Accounts) != 0 {
		t.Fatalf("expected PhaseSignedOut with 0 accounts after SignOutAll, got %+v", snap)
	}
}
