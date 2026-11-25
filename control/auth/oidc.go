// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidIDToken       = errors.New("auth: invalid OIDC ID token")
	ErrUnsupportedAlgorithm = errors.New("auth: unsupported or forbidden JWT signing algorithm")
	ErrInvalidSignature     = errors.New("auth: OIDC ID token signature verification failed")
	ErrIssuerMismatch       = errors.New("auth: OIDC ID token issuer mismatch")
	ErrAudienceMismatch     = errors.New("auth: OIDC ID token audience mismatch")
	ErrIDTokenExpired       = errors.New("auth: OIDC ID token expired")
	ErrKeyNotFound          = errors.New("auth: signing key not found in JWKS")
)

// IDTokenClaims represents the verified standard OIDC claims extracted from a
// cryptographically authenticated JWT id_token.
type IDTokenClaims struct {
	Issuer        string
	Subject       string
	Audience      []string
	ExpiresAt     time.Time
	IssuedAt      time.Time
	NotBefore     time.Time
	Nonce         string
	Email         string
	EmailVerified bool
	Name          string
	Picture       string
}

// IDTokenVerifyConfig specifies mandatory claim invariants for OIDC verification.
type IDTokenVerifyConfig struct {
	ExpectedIssuer   string
	ExpectedAudience string
	ExpectedNonce    string
	ClockSkew        time.Duration // defaults to 30s
	Now              func() time.Time
}

// JWK represents one RFC 7517 public signing key (RSA, EC P-256, or OKP Ed25519).
type JWK struct {
	KeyID     string
	KeyType   string // "RSA", "EC", "OKP"
	Algorithm string
	Use       string
	PublicKey crypto.PublicKey
}

// JWKSKeySet is an immutable parsed set of RFC 7517 public verification keys.
type JWKSKeySet struct {
	Keys []JWK
}

// ParseJWKS parses a standard RFC 7517 JSON Web Key Set (`{"keys":[...]}`).
func ParseJWKS(raw []byte) (*JWKSKeySet, error) {
	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Alg string `json:"alg"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
			Crv string `json:"crv"`
			X   string `json:"x"`
			Y   string `json:"y"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("auth: parse JWKS JSON: %w", err)
	}
	set := &JWKSKeySet{Keys: make([]JWK, 0, len(doc.Keys))}
	for _, k := range doc.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		var pub crypto.PublicKey
		switch k.Kty {
		case "RSA":
			nBytes, err1 := base64.RawURLEncoding.DecodeString(k.N)
			eBytes, err2 := base64.RawURLEncoding.DecodeString(k.E)
			if err1 != nil || err2 != nil || len(nBytes) == 0 || len(eBytes) == 0 {
				continue
			}
			eVal := new(big.Int).SetBytes(eBytes)
			if !eVal.IsInt64() || eVal.Int64() < 3 {
				continue
			}
			pub = &rsa.PublicKey{
				N: new(big.Int).SetBytes(nBytes),
				E: int(eVal.Int64()),
			}
		case "EC":
			if k.Crv != "P-256" {
				continue
			}
			xBytes, err1 := base64.RawURLEncoding.DecodeString(k.X)
			yBytes, err2 := base64.RawURLEncoding.DecodeString(k.Y)
			if err1 != nil || err2 != nil || len(xBytes) == 0 || len(yBytes) == 0 {
				continue
			}
			x := new(big.Int).SetBytes(xBytes)
			y := new(big.Int).SetBytes(yBytes)
			if !elliptic.P256().IsOnCurve(x, y) {
				continue
			}
			pub = &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
		case "OKP":
			if k.Crv != "Ed25519" {
				continue
			}
			xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
			if err != nil || len(xBytes) != ed25519.PublicKeySize {
				continue
			}
			pub = ed25519.PublicKey(xBytes)
		default:
			continue
		}
		set.Keys = append(set.Keys, JWK{
			KeyID:     k.Kid,
			KeyType:   k.Kty,
			Algorithm: k.Alg,
			Use:       k.Use,
			PublicKey: pub,
		})
	}
	return set, nil
}

// Lookup finds matching verification keys by kid (or returns all keys if kid is empty).
func (s *JWKSKeySet) Lookup(kid string) []JWK {
	if s == nil {
		return nil
	}
	if kid == "" {
		return append([]JWK(nil), s.Keys...)
	}
	var out []JWK
	for _, k := range s.Keys {
		if k.KeyID == kid {
			out = append(out, k)
		}
	}
	return out
}

// VerifyIDToken cryptographically verifies a compact JWS OIDC id_token against
// keySet and validates all mandatory claims (iss, aud, exp, nbf, nonce).
//
// Security invariants:
//   - Strictly allows ONLY asymmetric algorithms RS256, ES256, and EdDSA.
//   - Explicitly rejects alg=none, symmetric HS* (algorithm-confusion attacks),
//     and unhandled JWT `crit` headers.
//   - Compares OIDC `nonce` in constant time.
func VerifyIDToken(rawJWT string, keySet *JWKSKeySet, cfg IDTokenVerifyConfig) (IDTokenClaims, error) {
	parts := strings.Split(strings.TrimSpace(rawJWT), ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return IDTokenClaims{}, fmt.Errorf("%w: malformed compact JWS", ErrInvalidIDToken)
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return IDTokenClaims{}, fmt.Errorf("%w: invalid header base64url", ErrInvalidIDToken)
	}
	var hdr struct {
		Alg  string   `json:"alg"`
		Kid  string   `json:"kid"`
		Crit []string `json:"crit"`
	}
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return IDTokenClaims{}, fmt.Errorf("%w: invalid header JSON", ErrInvalidIDToken)
	}
	if len(hdr.Crit) > 0 {
		return IDTokenClaims{}, fmt.Errorf("%w: unsupported critical JWT header extensions", ErrInvalidIDToken)
	}
	switch hdr.Alg {
	case "RS256", "ES256", "EdDSA":
	default:
		return IDTokenClaims{}, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, hdr.Alg)
	}

	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return IDTokenClaims{}, fmt.Errorf("%w: invalid signature base64url", ErrInvalidIDToken)
	}
	signingInput := []byte(parts[0] + "." + parts[1])

	candidates := keySet.Lookup(hdr.Kid)
	if len(candidates) == 0 {
		return IDTokenClaims{}, fmt.Errorf("%w: kid=%q", ErrKeyNotFound, hdr.Kid)
	}
	verified := false
	for _, jwk := range candidates {
		if jwk.Algorithm != "" && jwk.Algorithm != hdr.Alg {
			continue
		}
		if verifyJWSSignature(hdr.Alg, jwk.PublicKey, signingInput, sigBytes) {
			verified = true
			break
		}
	}
	if !verified {
		return IDTokenClaims{}, ErrInvalidSignature
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return IDTokenClaims{}, fmt.Errorf("%w: invalid payload base64url", ErrInvalidIDToken)
	}
	var rawClaims struct {
		Iss           string          `json:"iss"`
		Sub           string          `json:"sub"`
		Aud           json.RawMessage `json:"aud"`
		Exp           int64           `json:"exp"`
		Iat           int64           `json:"iat"`
		Nbf           int64           `json:"nbf"`
		Nonce         string          `json:"nonce"`
		Email         string          `json:"email"`
		EmailVerified bool            `json:"email_verified"`
		Name          string          `json:"name"`
		Picture       string          `json:"picture"`
	}
	if err := json.Unmarshal(payloadBytes, &rawClaims); err != nil {
		return IDTokenClaims{}, fmt.Errorf("%w: invalid claims JSON", ErrInvalidIDToken)
	}

	auds, err := parseJWTAudience(rawClaims.Aud)
	if err != nil {
		return IDTokenClaims{}, fmt.Errorf("%w: invalid aud claim", ErrInvalidIDToken)
	}

	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()
	skew := cfg.ClockSkew
	if skew <= 0 {
		skew = 30 * time.Second
	}

	if rawClaims.Sub == "" {
		return IDTokenClaims{}, fmt.Errorf("%w: missing sub claim", ErrInvalidIDToken)
	}
	if cfg.ExpectedIssuer != "" && rawClaims.Iss != cfg.ExpectedIssuer {
		return IDTokenClaims{}, fmt.Errorf("%w: got %q want %q", ErrIssuerMismatch, rawClaims.Iss, cfg.ExpectedIssuer)
	}
	if cfg.ExpectedAudience != "" {
		audOK := false
		for _, a := range auds {
			if a == cfg.ExpectedAudience {
				audOK = true
				break
			}
		}
		if !audOK {
			return IDTokenClaims{}, fmt.Errorf("%w: expected %q", ErrAudienceMismatch, cfg.ExpectedAudience)
		}
	}
	if rawClaims.Exp == 0 {
		return IDTokenClaims{}, fmt.Errorf("%w: missing exp claim", ErrInvalidIDToken)
	}
	expTime := time.Unix(rawClaims.Exp, 0).UTC()
	if now.After(expTime.Add(skew)) {
		return IDTokenClaims{}, ErrIDTokenExpired
	}
	var nbfTime time.Time
	if rawClaims.Nbf > 0 {
		nbfTime = time.Unix(rawClaims.Nbf, 0).UTC()
		if now.Add(skew).Before(nbfTime) {
			return IDTokenClaims{}, fmt.Errorf("%w: token not yet valid", ErrInvalidIDToken)
		}
	}
	if cfg.ExpectedNonce != "" && !ConstantTimeTokenEqual(rawClaims.Nonce, cfg.ExpectedNonce) {
		return IDTokenClaims{}, ErrNonceMismatch
	}

	claims := IDTokenClaims{
		Issuer:        rawClaims.Iss,
		Subject:       rawClaims.Sub,
		Audience:      auds,
		ExpiresAt:     expTime,
		NotBefore:     nbfTime,
		Nonce:         rawClaims.Nonce,
		Email:         rawClaims.Email,
		EmailVerified: rawClaims.EmailVerified,
		Name:          rawClaims.Name,
		Picture:       rawClaims.Picture,
	}
	if rawClaims.Iat > 0 {
		claims.IssuedAt = time.Unix(rawClaims.Iat, 0).UTC()
	}
	return claims, nil
}

func verifyJWSSignature(alg string, pub crypto.PublicKey, signingInput, sig []byte) bool {
	switch alg {
	case "RS256":
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok || rsaPub.N.BitLen() < 2048 {
			return false
		}
		digest := sha256.Sum256(signingInput)
		return rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, digest[:], sig) == nil
	case "ES256":
		ecPub, ok := pub.(*ecdsa.PublicKey)
		if !ok || ecPub.Curve != elliptic.P256() || len(sig) != 64 {
			return false
		}
		r := new(big.Int).SetBytes(sig[:32])
		s := new(big.Int).SetBytes(sig[32:])
		digest := sha256.Sum256(signingInput)
		return ecdsa.Verify(ecPub, digest[:], r, s)
	case "EdDSA":
		edPub, ok := pub.(ed25519.PublicKey)
		if !ok || len(sig) != ed25519.SignatureSize {
			return false
		}
		return ed25519.Verify(edPub, signingInput, sig)
	default:
		return false
	}
}

func parseJWTAudience(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	var multi []string
	if err := json.Unmarshal(raw, &multi); err == nil {
		return multi, nil
	}
	return nil, errors.New("aud must be string or string array")
}

// HTTPJWKSCache caches fetched JWKS documents with bounded TTL and supports
// force-refresh on signing key rotation (`ErrKeyNotFound`).
type HTTPJWKSCache struct {
	Client *http.Client
	TTL    time.Duration

	mu      sync.RWMutex
	entries map[string]jwksCacheEntry
}

type jwksCacheEntry struct {
	set       *JWKSKeySet
	fetchedAt time.Time
}

func NewHTTPJWKSCache(client *http.Client, ttl time.Duration) *HTTPJWKSCache {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return &HTTPJWKSCache{
		Client:  client,
		TTL:     ttl,
		entries: make(map[string]jwksCacheEntry),
	}
}

// Get returns a cached JWKSKeySet or fetches it over HTTPS (or loopback when allowed).
func (c *HTTPJWKSCache) Get(ctx context.Context, jwksURL string, allowLoopback, forceRefresh bool) (*JWKSKeySet, error) {
	if err := ValidateSecureURL(jwksURL, allowLoopback); err != nil {
		return nil, err
	}
	if !forceRefresh {
		c.mu.RLock()
		ent, ok := c.entries[jwksURL]
		c.mu.RUnlock()
		if ok && time.Since(ent.fetchedAt) < c.TTL {
			return ent.set, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("auth: JWKS endpoint returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, err
	}
	set, err := ParseJWKS(body)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.entries[jwksURL] = jwksCacheEntry{set: set, fetchedAt: time.Now()}
	c.mu.Unlock()
	return set, nil
}
