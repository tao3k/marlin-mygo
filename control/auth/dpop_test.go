// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDPoPES256AndEdDSARoundTripAndSecurityGates(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	for _, tc := range []struct {
		name string
		gen  func() (*DPoPKeyPair, error)
		alg  string
	}{
		{"ES256", GenerateDPoPKeyPair, "ES256"},
		{"EdDSA", GenerateDPoPEd25519KeyPair, "EdDSA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			honestKey, err := tc.gen()
			if err != nil {
				t.Fatal(err)
			}
			attackerKey, err := tc.gen()
			if err != nil {
				t.Fatal(err)
			}
			if honestKey.Alg() != tc.alg {
				t.Fatalf("expected alg %s, got %s", tc.alg, honestKey.Alg())
			}

			verifier := NewDPoPVerifier(60*time.Second, 5*time.Minute)
			accessTok := "sender-constrained-access-token-123"
			htu := "https://api.example.com/v1/projects?limit=10#top"

			// 1. Fresh honest proof succeeds (query & fragment normalized per RFC 9449 §4.2).
			proof, jti, err := honestKey.CreateProof("post", htu, accessTok, "srv-nonce-1", now)
			if err != nil {
				t.Fatal(err)
			}
			res, err := verifier.VerifyProof(proof, DPoPVerifyRequest{
				ExpectedHTM:   "POST",
				ExpectedHTU:   "https://api.example.com/v1/projects",
				AccessToken:   accessTok,
				ExpectedJKT:   honestKey.Thumbprint(),
				ExpectedNonce: "srv-nonce-1",
				Now:           now,
			})
			if err != nil {
				t.Fatalf("valid DPoP proof rejected: %v", err)
			}
			if res.JTI != jti || res.Thumbprint != honestKey.Thumbprint() {
				t.Fatalf("unexpected verified proof metadata: %+v", res)
			}

			// 2. Single-use jti replay MUST fail closed.
			if _, err := verifier.VerifyProof(proof, DPoPVerifyRequest{
				ExpectedHTM:   "POST",
				ExpectedHTU:   "https://api.example.com/v1/projects",
				AccessToken:   accessTok,
				ExpectedJKT:   honestKey.Thumbprint(),
				ExpectedNonce: "srv-nonce-1",
				Now:           now,
			}); !errors.Is(err, ErrDPoPProofReplay) {
				t.Fatalf("expected ErrDPoPProofReplay, got %v", err)
			}

			// 3. Stolen access token + attacker's own key MUST fail cnf.jkt binding.
			stolenProof, _, err := attackerKey.CreateProof("POST", htu, accessTok, "srv-nonce-1", now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := verifier.VerifyProof(stolenProof, DPoPVerifyRequest{
				ExpectedHTM:   "POST",
				ExpectedHTU:   "https://api.example.com/v1/projects",
				AccessToken:   accessTok,
				ExpectedJKT:   honestKey.Thumbprint(),
				ExpectedNonce: "srv-nonce-1",
				Now:           now,
			}); !errors.Is(err, ErrDPoPThumbprintMismatch) {
				t.Fatalf("expected ErrDPoPThumbprintMismatch, got %v", err)
			}

			// 4. Wrong HTTP method / URI / ath / nonce / stale iat MUST fail closed.
			freshProof, _, _ := honestKey.CreateProof("POST", htu, accessTok, "srv-nonce-1", now)
			if _, err := verifier.VerifyProof(freshProof, DPoPVerifyRequest{
				ExpectedHTM: "DELETE", ExpectedHTU: htu, AccessToken: accessTok, ExpectedJKT: honestKey.Thumbprint(), ExpectedNonce: "srv-nonce-1", Now: now,
			}); !errors.Is(err, ErrDPoPMethodMismatch) {
				t.Fatalf("expected ErrDPoPMethodMismatch, got %v", err)
			}
			if _, err := verifier.VerifyProof(freshProof, DPoPVerifyRequest{
				ExpectedHTM: "POST", ExpectedHTU: "https://api.example.com/v1/other", AccessToken: accessTok, ExpectedJKT: honestKey.Thumbprint(), ExpectedNonce: "srv-nonce-1", Now: now,
			}); !errors.Is(err, ErrDPoPURIMismatch) {
				t.Fatalf("expected ErrDPoPURIMismatch, got %v", err)
			}
			if _, err := verifier.VerifyProof(freshProof, DPoPVerifyRequest{
				ExpectedHTM: "POST", ExpectedHTU: htu, AccessToken: "other-token", ExpectedJKT: honestKey.Thumbprint(), ExpectedNonce: "srv-nonce-1", Now: now,
			}); !errors.Is(err, ErrDPoPAccessTokenHashMismatch) {
				t.Fatalf("expected ErrDPoPAccessTokenHashMismatch, got %v", err)
			}
			if _, err := verifier.VerifyProof(freshProof, DPoPVerifyRequest{
				ExpectedHTM: "POST", ExpectedHTU: htu, AccessToken: accessTok, ExpectedJKT: honestKey.Thumbprint(), ExpectedNonce: "wrong-nonce", Now: now,
			}); !errors.Is(err, ErrDPoPNonceMismatch) {
				t.Fatalf("expected ErrDPoPNonceMismatch, got %v", err)
			}
			if _, err := verifier.VerifyProof(freshProof, DPoPVerifyRequest{
				ExpectedHTM: "POST", ExpectedHTU: htu, AccessToken: accessTok, ExpectedJKT: honestKey.Thumbprint(), ExpectedNonce: "srv-nonce-1", Now: now.Add(5 * time.Minute),
			}); !errors.Is(err, ErrDPoPStaleProof) {
				t.Fatalf("expected ErrDPoPStaleProof, got %v", err)
			}
		})
	}
}

func TestDPoPRejectsAlgNoneAndEmbeddedPrivateJWK(t *testing.T) {
	verifier := NewDPoPVerifier(60*time.Second, 5*time.Minute)
	key, err := GenerateDPoPKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	// alg=none forgery attempt.
	badHdr, _ := json.Marshal(map[string]any{"typ": "dpop+jwt", "alg": "none", "jwk": key.pubJWK})
	clm, _ := json.Marshal(map[string]any{"jti": "j1", "htm": "GET", "htu": "https://api.example.com/x", "iat": time.Now().Unix()})
	algNoneJWT := base64.RawURLEncoding.EncodeToString(badHdr) + "." + base64.RawURLEncoding.EncodeToString(clm) + "."
	if _, err := verifier.VerifyProof(algNoneJWT, DPoPVerifyRequest{
		ExpectedHTM: "GET", ExpectedHTU: "https://api.example.com/x",
	}); !errors.Is(err, ErrDPoPUnsupportedAlg) {
		t.Fatalf("expected ErrDPoPUnsupportedAlg for alg=none, got %v", err)
	}

	// Embedded private parameter `d` in JWK header MUST be rejected (RFC 9449 §4.3).
	privJWK := map[string]any{"kty": "EC", "crv": "P-256", "x": key.pubJWK["x"], "y": key.pubJWK["y"], "d": "secret-private-scalar"}
	privHdr, _ := json.Marshal(map[string]any{"typ": "dpop+jwt", "alg": "ES256", "jwk": privJWK})
	privJWT := base64.RawURLEncoding.EncodeToString(privHdr) + "." + base64.RawURLEncoding.EncodeToString(clm) + ".AAAA"
	if _, err := verifier.VerifyProof(privJWT, DPoPVerifyRequest{
		ExpectedHTM: "GET", ExpectedHTU: "https://api.example.com/x",
	}); !errors.Is(err, ErrDPoPPrivateJWK) {
		t.Fatalf("expected ErrDPoPPrivateJWK, got %v", err)
	}
}

func TestAuthorizerAuthorizedDPoPHeadersIntegration(t *testing.T) {
	ctx := context.Background()
	vault := NewMemoryVault()
	ex := &mbtExchanger{refreshStart: make(chan struct{}, 1), refreshResume: make(chan int64, 1)}
	ex.nextPkceOk = true
	ex.nextNonceOk = true
	mgr := NewManager(ManagerConfig{Vault: vault, Exchanger: ex})
	_ = mgr.RegisterProvider(ProviderConfig{
		ID: ProviderOIDC, DisplayName: "OIDC", Method: MethodOAuthPKCE,
		AuthURL: "https://idp.example.com/auth", TokenURL: "https://idp.example.com/token",
		ClientID: "c1", Scopes: []string{"openid"},
	})
	flow, _ := mgr.BeginOAuth(ProviderOIDC, "http://127.0.0.1:9999/cb")
	mgr.mu.RLock()
	st := mgr.oauthAttempts[flow.AttemptID].state
	mgr.mu.RUnlock()
	if _, err := mgr.CompleteOAuthCallback(ctx, flow.AttemptID, LoopbackCallback{Code: "c", State: st}); err != nil {
		t.Fatal(err)
	}
	// Replace vaulted token with non-expired access token for direct header minting.
	_ = vault.Store(ctx, "oidc:mbt-user", TokenSet{AccessToken: "live-dpop-at", ExpiresAt: time.Now().Add(time.Hour)})

	authz := NewAuthorizer(mgr)
	if _, err := authz.LoadPolicyBytes("permit", []byte(`permit(principal, action, resource);`)); err != nil {
		t.Fatal(err)
	}
	ticket, err := authz.Evaluate("oidc:mbt-user", "invoke", "api", nil)
	if err != nil || !ticket.Allowed {
		t.Fatalf("expected allowed ticket, got %+v err=%v", ticket, err)
	}

	dpopKey, err := GenerateDPoPKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	authHdr, proof, err := authz.AuthorizedDPoPHeaders(ctx, ticket, dpopKey, "POST", "https://api.example.com/exec", "n-1")
	if err != nil {
		t.Fatalf("AuthorizedDPoPHeaders failed: %v", err)
	}
	if !strings.HasPrefix(authHdr, "DPoP ") {
		t.Fatalf("expected DPoP auth scheme, got %q", authHdr)
	}
	tok := strings.TrimPrefix(authHdr, "DPoP ")
	verifier := NewDPoPVerifier(60*time.Second, 5*time.Minute)
	if _, err := verifier.VerifyProof(proof, DPoPVerifyRequest{
		ExpectedHTM: "POST", ExpectedHTU: "https://api.example.com/exec",
		AccessToken: tok, ExpectedJKT: dpopKey.Thumbprint(), ExpectedNonce: "n-1",
	}); err != nil {
		t.Fatalf("Authorizer-issued DPoP proof failed verification: %v", err)
	}
}
