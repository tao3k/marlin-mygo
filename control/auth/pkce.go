// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// PKCEPair holds a fresh RFC 7636 S256 verifier and derived challenge.
// Verifier is strictly confidential and never serialized to URLs or logs.
type PKCEPair struct {
	Verifier  string
	Challenge string
	Method    string // always "S256"
}

// String redacts the PKCE verifier.
func (p PKCEPair) String() string {
	return fmt.Sprintf("PKCEPair{method=%s challenge=%s verifier=[REDACTED]}", p.Method, p.Challenge)
}

// GeneratePKCE produces a 32-byte cryptographically random RFC 7636 S256 pair
// (43-character base64url verifier + SHA-256 base64url challenge).
func GeneratePKCE() (PKCEPair, error) {
	verifier, err := GenerateRandomToken(32)
	if err != nil {
		return PKCEPair{}, fmt.Errorf("auth: generate PKCE verifier: %w", err)
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return PKCEPair{
		Verifier:  verifier,
		Challenge: challenge,
		Method:    "S256",
	}, nil
}

// GenerateRandomToken returns a base64url-encoded (no padding) secret derived
// from nBytes of crypto/rand entropy (minimum 16 bytes enforced).
func GenerateRandomToken(nBytes int) (string, error) {
	if nBytes < 16 {
		nBytes = 16
	}
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ConstantTimeTokenEqual compares two opaque tokens/states in constant time.
func ConstantTimeTokenEqual(a, b string) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// BuildAuthorizeURL constructs an OAuth 2.1 / OIDC authorization URL enforcing
// response_type=code, PKCE S256, CSRF state, and OIDC nonce.
func BuildAuthorizeURL(cfg ProviderConfig, redirectURI, state, nonce string, pkce PKCEPair) (string, error) {
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	if strings.TrimSpace(redirectURI) == "" {
		return "", fmt.Errorf("auth: redirectURI is required")
	}
	if strings.TrimSpace(state) == "" || pkce.Challenge == "" || pkce.Method != "S256" {
		return "", fmt.Errorf("auth: PKCE S256 and CSRF state are mandatory")
	}
	u, err := url.Parse(cfg.AuthURL)
	if err != nil {
		return "", fmt.Errorf("auth: parse AuthURL: %w", err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", pkce.Challenge)
	q.Set("code_challenge_method", "S256")
	if len(cfg.Scopes) > 0 {
		q.Set("scope", strings.Join(cfg.Scopes, " "))
	}
	if nonce != "" {
		q.Set("nonce", nonce)
	}
	if cfg.ID == ProviderGoogle && cfg.SupportsRefresh {
		q.Set("access_type", "offline")
		q.Set("prompt", "consent")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

type oauthAttempt struct {
	id           string
	providerID   ProviderID
	redirectURI  string
	state        string
	nonce        string
	codeVerifier string
	expiresAt    time.Time
	consumed     bool
}

// validate checks expiry, single-use consumption, and constant-time state match.
func (a *oauthAttempt) validate(now time.Time, state, code, oauthErr, oauthErrDesc string) error {
	if a == nil || a.consumed {
		return ErrInvalidState
	}
	if now.After(a.expiresAt) {
		return ErrAttemptExpired
	}
	if !ConstantTimeTokenEqual(a.state, state) {
		return ErrInvalidState
	}
	if strings.TrimSpace(oauthErr) != "" {
		if strings.TrimSpace(oauthErrDesc) != "" {
			return fmt.Errorf("auth: provider denied authorization (%s: %s)", oauthErr, oauthErrDesc)
		}
		return fmt.Errorf("auth: provider denied authorization (%s)", oauthErr)
	}
	if strings.TrimSpace(code) == "" {
		return fmt.Errorf("auth: callback missing authorization code")
	}
	return nil
}
