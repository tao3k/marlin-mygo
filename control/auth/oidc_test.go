// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/control/auth"
)

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func signTestJWT(t *testing.T, header map[string]any, claims map[string]any, signer any) string {
	t.Helper()
	hb, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signingInput := b64url(hb) + "." + b64url(cb)
	var sig []byte
	switch k := signer.(type) {
	case *rsa.PrivateKey:
		sum := sha256.Sum256([]byte(signingInput))
		sig, err = rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, sum[:])
		if err != nil {
			t.Fatal(err)
		}
	case *ecdsa.PrivateKey:
		sum := sha256.Sum256([]byte(signingInput))
		r, s, err := ecdsa.Sign(rand.Reader, k, sum[:])
		if err != nil {
			t.Fatal(err)
		}
		sig = make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
	case ed25519.PrivateKey:
		sig = ed25519.Sign(k, []byte(signingInput))
	case []byte:
		sig = k
	default:
		t.Fatalf("unsupported test signer %T", signer)
	}
	return signingInput + "." + b64url(sig)
}

func makeJWKSJSON(t *testing.T, keys []map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"keys": keys})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOIDCIDTokenVerificationAlgorithmsAndAttackDefenses(t *testing.T) {
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	edPub, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	jwksRaw := makeJWKSJSON(t, []map[string]any{
		{
			"kty": "RSA", "kid": "rsa-1", "alg": "RS256", "use": "sig",
			"n": b64url(rsaPriv.N.Bytes()),
			"e": b64url(big.NewInt(int64(rsaPriv.E)).Bytes()),
		},
		{
			"kty": "EC", "kid": "ec-1", "alg": "ES256", "use": "sig", "crv": "P-256",
			"x": b64url(ecPriv.X.Bytes()),
			"y": b64url(ecPriv.Y.Bytes()),
		},
		{
			"kty": "OKP", "kid": "ed-1", "alg": "EdDSA", "use": "sig", "crv": "Ed25519",
			"x": b64url(edPub),
		},
	})
	keySet, err := auth.ParseJWKS(jwksRaw)
	if err != nil || len(keySet.Keys) != 3 {
		t.Fatalf("ParseJWKS failed: err=%v count=%d", err, len(keySet.Keys))
	}

	now := time.Date(2026, 11, 25, 22, 0, 0, 0, time.UTC)
	baseClaims := func() map[string]any {
		return map[string]any{
			"iss":            "https://accounts.google.com",
			"sub":            "sub-oidc-1",
			"aud":            "marlin-client-id",
			"exp":            now.Add(10 * time.Minute).Unix(),
			"iat":            now.Unix(),
			"nonce":          "fresh-nonce-xyz",
			"email":          "security@marlin.dev",
			"email_verified": true,
			"name":           "Security Architect",
		}
	}
	verifyCfg := auth.IDTokenVerifyConfig{
		ExpectedIssuer:   "https://accounts.google.com",
		ExpectedAudience: "marlin-client-id",
		ExpectedNonce:    "fresh-nonce-xyz",
		Now:              func() time.Time { return now },
	}

	// 1. All 3 asymmetric algorithms (RS256, ES256, EdDSA) MUST verify cleanly.
	for _, tc := range []struct {
		alg, kid string
		key      any
	}{
		{"RS256", "rsa-1", rsaPriv},
		{"ES256", "ec-1", ecPriv},
		{"EdDSA", "ed-1", edPriv},
	} {
		jwt := signTestJWT(t, map[string]any{"alg": tc.alg, "kid": tc.kid, "typ": "JWT"}, baseClaims(), tc.key)
		claims, err := auth.VerifyIDToken(jwt, keySet, verifyCfg)
		if err != nil {
			t.Fatalf("expected valid %s token to verify, got %v", tc.alg, err)
		}
		if claims.Subject != "sub-oidc-1" || claims.Email != "security@marlin.dev" || !claims.EmailVerified {
			t.Fatalf("unexpected %s claims: %+v", tc.alg, claims)
		}
	}

	// 2. Algorithm downgrade (`alg: none`) & confusion (`alg: HS256`) MUST fail-closed!
	noneJWT := signTestJWT(t, map[string]any{"alg": "none"}, baseClaims(), []byte("ignored"))
	if _, err := auth.VerifyIDToken(noneJWT, keySet, verifyCfg); !errors.Is(err, auth.ErrUnsupportedAlgorithm) {
		t.Fatalf("expected ErrUnsupportedAlgorithm for alg=none, got %v", err)
	}
	hsJWT := signTestJWT(t, map[string]any{"alg": "HS256", "kid": "rsa-1"}, baseClaims(), []byte("fake-hmac"))
	if _, err := auth.VerifyIDToken(hsJWT, keySet, verifyCfg); !errors.Is(err, auth.ErrUnsupportedAlgorithm) {
		t.Fatalf("expected ErrUnsupportedAlgorithm for HS256 confusion, got %v", err)
	}

	// 3. Unhandled `crit` header MUST be rejected!
	critJWT := signTestJWT(t, map[string]any{"alg": "EdDSA", "kid": "ed-1", "crit": []string{"exp-custom"}}, baseClaims(), edPriv)
	if _, err := auth.VerifyIDToken(critJWT, keySet, verifyCfg); !errors.Is(err, auth.ErrInvalidIDToken) {
		t.Fatalf("expected ErrInvalidIDToken for unhandled crit header, got %v", err)
	}

	// 4. Tampered signature, wrong nonce, wrong issuer, wrong audience, expired token MUST all fail!
	validJWT := signTestJWT(t, map[string]any{"alg": "EdDSA", "kid": "ed-1"}, baseClaims(), edPriv)
	tampered := validJWT[:len(validJWT)-2] + "AA"
	if _, err := auth.VerifyIDToken(tampered, keySet, verifyCfg); !errors.Is(err, auth.ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for tampered JWT, got %v", err)
	}

	badNonce := baseClaims()
	badNonce["nonce"] = "replayed-nonce"
	if _, err := auth.VerifyIDToken(signTestJWT(t, map[string]any{"alg": "EdDSA", "kid": "ed-1"}, badNonce, edPriv), keySet, verifyCfg); !errors.Is(err, auth.ErrNonceMismatch) {
		t.Fatalf("expected ErrNonceMismatch, got %v", err)
	}

	badIss := baseClaims()
	badIss["iss"] = "https://evil.idp.example"
	if _, err := auth.VerifyIDToken(signTestJWT(t, map[string]any{"alg": "EdDSA", "kid": "ed-1"}, badIss, edPriv), keySet, verifyCfg); !errors.Is(err, auth.ErrIssuerMismatch) {
		t.Fatalf("expected ErrIssuerMismatch, got %v", err)
	}

	badAud := baseClaims()
	badAud["aud"] = "different-client-id"
	if _, err := auth.VerifyIDToken(signTestJWT(t, map[string]any{"alg": "EdDSA", "kid": "ed-1"}, badAud, edPriv), keySet, verifyCfg); !errors.Is(err, auth.ErrAudienceMismatch) {
		t.Fatalf("expected ErrAudienceMismatch, got %v", err)
	}

	expired := baseClaims()
	expired["exp"] = now.Add(-2 * time.Minute).Unix()
	if _, err := auth.VerifyIDToken(signTestJWT(t, map[string]any{"alg": "EdDSA", "kid": "ed-1"}, expired, edPriv), keySet, verifyCfg); !errors.Is(err, auth.ErrIDTokenExpired) {
		t.Fatalf("expected ErrIDTokenExpired, got %v", err)
	}
}

func TestOIDCJWKSKeyRotationEndToEnd(t *testing.T) {
	pub1, _, _ := ed25519.GenerateKey(rand.Reader)
	pub2, priv2, _ := ed25519.GenerateKey(rand.Reader)

	var jwksFetches atomic.Int32
	var capturedNonce atomic.Value

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jwks":
			// First fetch returns old key k1; second fetch (after rotation) serves new key k2!
			if jwksFetches.Add(1) == 1 {
				_, _ = w.Write(makeJWKSJSON(t, []map[string]any{
					{"kty": "OKP", "kid": "k1", "alg": "EdDSA", "use": "sig", "crv": "Ed25519", "x": b64url(pub1)},
				}))
				return
			}
			_, _ = w.Write(makeJWKSJSON(t, []map[string]any{
				{"kty": "OKP", "kid": "k2", "alg": "EdDSA", "use": "sig", "crv": "Ed25519", "x": b64url(pub2)},
			}))
		case "/token":
			nonce, _ := capturedNonce.Load().(string)
			idToken := signTestJWT(t,
				map[string]any{"alg": "EdDSA", "kid": "k2"},
				map[string]any{
					"iss":            srv.URL,
					"sub":            "oidc-rotated-sub",
					"aud":            "marlin-oidc-client",
					"exp":            time.Now().Add(time.Hour).Unix(),
					"nonce":          nonce,
					"email":          "rotated@marlin.dev",
					"email_verified": true,
					"name":           "Rotated Key User",
				},
				priv2,
			)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "oidc-access-1",
				"id_token":     idToken,
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		}
	}))
	defer srv.Close()

	ex := auth.NewHTTPOAuthExchanger(srv.Client())
	// Pre-warm JWKS cache with stale k1 so ExchangeCode MUST detect k2 miss & auto-rotate!
	if _, err := ex.JWKS.Get(context.Background(), srv.URL+"/jwks", true, false); err != nil {
		t.Fatal(err)
	}

	mgr := auth.NewManager(auth.ManagerConfig{
		Exchanger: ex,
		OpenBrowser: func(ctx context.Context, authorizeURL string) error {
			u, _ := url.Parse(authorizeURL)
			q := u.Query()
			capturedNonce.Store(q.Get("nonce"))
			cbURL := fmt.Sprintf("%s?code=rot-code&state=%s", q.Get("redirect_uri"), url.QueryEscape(q.Get("state")))
			go func() {
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, cbURL, nil)
				if resp, err := http.DefaultClient.Do(req); err == nil {
					_ = resp.Body.Close()
				}
			}()
			return nil
		},
	})
	if err := mgr.RegisterProvider(auth.ProviderConfig{
		ID:            auth.ProviderOIDC,
		DisplayName:   "Enterprise OIDC",
		Method:        auth.MethodOAuthPKCE,
		AuthURL:       srv.URL + "/authorize",
		TokenURL:      srv.URL + "/token",
		Issuer:        srv.URL,
		JWKSURL:       srv.URL + "/jwks",
		ClientID:      "marlin-oidc-client",
		Scopes:        []string{"openid", "email", "profile"},
		AllowLoopback: true,
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prof, err := mgr.RunLoopbackOAuth(ctx, auth.ProviderOIDC)
	if err != nil {
		t.Fatalf("RunLoopbackOAuth with JWKS key rotation failed: %v", err)
	}
	if prof.Email != "rotated@marlin.dev" || prof.DisplayName != "Rotated Key User" || !prof.EmailVerified {
		t.Fatalf("unexpected OIDC verified profile: %+v", prof)
	}
	if jwksFetches.Load() != 2 {
		t.Fatalf("expected 2 JWKS fetches (initial + rotation refresh), got %d", jwksFetches.Load())
	}
}

func TestOpenSystemBrowserRejectsUnsafeURLs(t *testing.T) {
	ctx := context.Background()
	for _, bad := range []string{
		"http://accounts.google.com/auth",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"-a Calculator",
		"",
	} {
		if err := auth.OpenSystemBrowser(ctx, bad); !errors.Is(err, auth.ErrInsecureEndpoint) {
			t.Fatalf("expected ErrInsecureEndpoint for unsafe browser URL %q, got %v", bad, err)
		}
	}
	// Standard RFC 3986 HTTPS OAuth PKCE URLs with '&' query separators MUST pass validation.
	for _, good := range []string{
		"https://github.com/login/oauth/authorize?client_id=marlin&redirect_uri=http%3A%2F%2F127.0.0.1%3A9999%2Fcb&code_challenge=abc&code_challenge_method=S256&state=xyz",
		"https://accounts.google.com/o/oauth2/v2/auth?client_id=marlin&response_type=code&scope=openid%20email",
	} {
		if err := auth.ValidateBrowserURL(good); err != nil {
			t.Fatalf("expected valid OAuth URL %q to pass ValidateBrowserURL, got %v", good, err)
		}
	}
}

func TestDiscoverOIDCProviderValidatesIssuerAndPKCES256(t *testing.T) {
	var mode string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch mode {
		case "wrong-issuer":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 "https://evil-substituted-issuer.example",
				"authorization_endpoint": srv.URL + "/auth",
				"token_endpoint":         srv.URL + "/token",
				"jwks_uri":               srv.URL + "/jwks",
			})
		case "no-s256":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                           srv.URL,
				"authorization_endpoint":           srv.URL + "/auth",
				"token_endpoint":                   srv.URL + "/token",
				"jwks_uri":                         srv.URL + "/jwks",
				"code_challenge_methods_supported": []string{"plain"},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                           srv.URL,
				"authorization_endpoint":           srv.URL + "/auth",
				"token_endpoint":                   srv.URL + "/token",
				"jwks_uri":                         srv.URL + "/jwks",
				"userinfo_endpoint":                srv.URL + "/userinfo",
				"revocation_endpoint":              srv.URL + "/revoke",
				"code_challenge_methods_supported": []string{"S256"},
			})
		}
	}))
	defer srv.Close()

	ctx := context.Background()

	// 1. Valid OIDC Discovery succeeds and wires all endpoints + JWKS + Issuer.
	mode = "ok"
	cfg, err := auth.DiscoverOIDCProvider(ctx, srv.Client(), srv.URL, "ent-client-1", "Acme SSO", true)
	if err != nil {
		t.Fatalf("DiscoverOIDCProvider failed: %v", err)
	}
	if cfg.Issuer != srv.URL || cfg.JWKSURL != srv.URL+"/jwks" || cfg.DisplayName != "Acme SSO" {
		t.Fatalf("unexpected discovered ProviderConfig: %+v", cfg)
	}

	// 2. Issuer substitution attack MUST fail-closed with ErrIssuerMismatch!
	mode = "wrong-issuer"
	if _, err := auth.DiscoverOIDCProvider(ctx, srv.Client(), srv.URL, "ent-client-1", "Acme", true); !errors.Is(err, auth.ErrIssuerMismatch) {
		t.Fatalf("expected ErrIssuerMismatch on issuer substitution, got %v", err)
	}

	// 3. Provider advertising only `plain` PKCE (no S256) MUST be rejected!
	mode = "no-s256"
	if _, err := auth.DiscoverOIDCProvider(ctx, srv.Client(), srv.URL, "ent-client-1", "Acme", true); err == nil {
		t.Fatal("expected rejection when IdP does not support PKCE S256")
	}
}
