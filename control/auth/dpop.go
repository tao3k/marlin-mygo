// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"sync"
	"time"
)

// RFC 9449 OAuth 2.0 Demonstrating Proof of Possession (DPoP) error sentinels.
var (
	ErrDPoPMalformedProof          = errors.New("auth: malformed DPoP proof JWT")
	ErrDPoPInvalidTyp              = errors.New("auth: DPoP proof header typ must be dpop+jwt")
	ErrDPoPUnsupportedAlg          = errors.New("auth: unsupported or unsafe DPoP signing algorithm")
	ErrDPoPPrivateJWK              = errors.New("auth: DPoP proof jwk header must not contain private key parameters")
	ErrDPoPInvalidSignature        = errors.New("auth: DPoP proof cryptographic signature verification failed")
	ErrDPoPThumbprintMismatch      = errors.New("auth: DPoP proof JWK thumbprint does not match bound cnf.jkt")
	ErrDPoPMethodMismatch          = errors.New("auth: DPoP proof htm claim does not match HTTP method")
	ErrDPoPURIMismatch             = errors.New("auth: DPoP proof htu claim does not match HTTP target URI")
	ErrDPoPAccessTokenHashMismatch = errors.New("auth: DPoP proof ath claim does not match access token hash")
	ErrDPoPNonceMismatch           = errors.New("auth: DPoP proof nonce claim mismatch")
	ErrDPoPStaleProof              = errors.New("auth: DPoP proof iat outside acceptable freshness window")
	ErrDPoPProofReplay             = errors.New("auth: DPoP proof jti replay detected")
)

// DPoPKeyPair holds an ephemeral or device-bound asymmetric signing key for
// generating RFC 9449 DPoP proofs (pure Go stdlib, zero CGO, WASM-compatible).
type DPoPKeyPair struct {
	alg    string
	ecKey  *ecdsa.PrivateKey
	edKey  ed25519.PrivateKey
	pubJWK map[string]string
	jkt    string
}

// GenerateDPoPKeyPair creates a fresh ES256 (ECDSA P-256) key pair for RFC 9449 DPoP.
func GenerateDPoPKeyPair() (*DPoPKeyPair, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("auth: generate DPoP ES256 key: %w", err)
	}
	xBytes := make([]byte, 32)
	yBytes := make([]byte, 32)
	priv.PublicKey.X.FillBytes(xBytes)
	priv.PublicKey.Y.FillBytes(yBytes)
	pubJWK := map[string]string{
		"kty": "EC",
		"crv": "P-256",
		"x":   base64.RawURLEncoding.EncodeToString(xBytes),
		"y":   base64.RawURLEncoding.EncodeToString(yBytes),
	}
	jkt, err := computeJWKThumbprint(pubJWK)
	if err != nil {
		return nil, err
	}
	return &DPoPKeyPair{
		alg:    "ES256",
		ecKey:  priv,
		pubJWK: pubJWK,
		jkt:    jkt,
	}, nil
}

// GenerateDPoPEd25519KeyPair creates a fresh EdDSA (Ed25519) key pair for RFC 9449 DPoP.
func GenerateDPoPEd25519KeyPair() (*DPoPKeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("auth: generate DPoP EdDSA key: %w", err)
	}
	pubJWK := map[string]string{
		"kty": "OKP",
		"crv": "Ed25519",
		"x":   base64.RawURLEncoding.EncodeToString(pub),
	}
	jkt, err := computeJWKThumbprint(pubJWK)
	if err != nil {
		return nil, err
	}
	return &DPoPKeyPair{
		alg:    "EdDSA",
		edKey:  priv,
		pubJWK: pubJWK,
		jkt:    jkt,
	}, nil
}

// Alg returns the JWS algorithm identifier ("ES256" or "EdDSA").
func (k *DPoPKeyPair) Alg() string {
	if k == nil {
		return ""
	}
	return k.alg
}

// Thumbprint returns the RFC 7638 SHA-256 JWK thumbprint (used as `cnf.jkt`).
func (k *DPoPKeyPair) Thumbprint() string {
	if k == nil {
		return ""
	}
	return k.jkt
}

// CreateProof signs a fresh RFC 9449 DPoP proof JWT bound to HTTP method htm,
// HTTP target URI htu (query and fragment stripped per §4.2), optional access
// token (hashed as `ath`), and optional server-issued `nonce`.
func (k *DPoPKeyPair) CreateProof(htm, htu, accessToken, nonce string, now time.Time) (string, string, error) {
	if k == nil || (k.ecKey == nil && len(k.edKey) == 0) {
		return "", "", ErrDPoPMalformedProof
	}
	jtiBytes := make([]byte, 16)
	if _, err := rand.Read(jtiBytes); err != nil {
		return "", "", fmt.Errorf("auth: generate DPoP jti: %w", err)
	}
	jti := base64.RawURLEncoding.EncodeToString(jtiBytes)
	proof, err := k.createProofWithJTI(jti, htm, htu, accessToken, nonce, now)
	if err != nil {
		return "", "", err
	}
	return proof, jti, nil
}

func (k *DPoPKeyPair) createProofWithJTI(jti, htm, htu, accessToken, nonce string, now time.Time) (string, error) {
	normHTU, err := normalizeDPoPHTU(htu)
	if err != nil {
		return "", err
	}
	method := strings.ToUpper(strings.TrimSpace(htm))
	if method == "" {
		return "", fmt.Errorf("%w: empty htm", ErrDPoPMethodMismatch)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	header := map[string]any{
		"typ": "dpop+jwt",
		"alg": k.alg,
		"jwk": k.pubJWK,
	}
	claims := map[string]any{
		"jti": jti,
		"htm": method,
		"htu": normHTU,
		"iat": now.Unix(),
	}
	if accessToken != "" {
		claims["ath"] = computeDPoPAccessTokenHash(accessToken)
	}
	if nonce != "" {
		claims["nonce"] = nonce
	}

	hdrBytes, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	clmBytes, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(hdrBytes) + "." +
		base64.RawURLEncoding.EncodeToString(clmBytes)

	var sig []byte
	switch k.alg {
	case "ES256":
		digest := sha256.Sum256([]byte(signingInput))
		r, s, err := ecdsa.Sign(rand.Reader, k.ecKey, digest[:])
		if err != nil {
			return "", fmt.Errorf("auth: sign ES256 DPoP proof: %w", err)
		}
		sig = make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
	case "EdDSA":
		sig = ed25519.Sign(k.edKey, []byte(signingInput))
	default:
		return "", ErrDPoPUnsupportedAlg
	}

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// DPoPVerifyRequest describes the expected HTTP request and bound token context
// for RFC 9449 §4.3 resource/authorization server proof verification.
type DPoPVerifyRequest struct {
	ExpectedHTM   string    // e.g. "GET", "POST"
	ExpectedHTU   string    // full request URL (query & fragment ignored per §4.2)
	AccessToken   string    // when non-empty, `ath` MUST match SHA-256(AccessToken)
	ExpectedJKT   string    // when non-empty, proof JWK thumbprint MUST match token `cnf.jkt`
	ExpectedNonce string    // when non-empty, proof `nonce` MUST match
	Now           time.Time // defaults to time.Now().UTC()
}

// DPoPVerifiedProof carries verified metadata extracted from an accepted DPoP proof.
type DPoPVerifiedProof struct {
	Alg        string
	Thumbprint string
	JTI        string
	HTM        string
	HTU        string
	IssuedAt   time.Time
}

// DPoPVerifier validates RFC 9449 DPoP proofs with strict asymmetric algorithm
// allowlisting, cnf.jkt sender-constraining, ath token binding, iat freshness,
// and single-use jti replay protection.
type DPoPVerifier struct {
	mu             sync.Mutex
	maxSkew        time.Duration
	replayTTL      time.Duration
	seenJTIs       map[string]time.Time
	totalAccepted  int
	acceptedByKey  map[string]int
}

// NewDPoPVerifier creates a verifier with default ±60s clock skew and 5m jti cache TTL.
func NewDPoPVerifier(maxSkew, replayTTL time.Duration) *DPoPVerifier {
	if maxSkew <= 0 {
		maxSkew = 60 * time.Second
	}
	if replayTTL <= 0 {
		replayTTL = 5 * time.Minute
	}
	return &DPoPVerifier{
		maxSkew:       maxSkew,
		replayTTL:     replayTTL,
		seenJTIs:      make(map[string]time.Time),
		acceptedByKey: make(map[string]int),
	}
}

type dpopHeader struct {
	Typ string            `json:"typ"`
	Alg string            `json:"alg"`
	JWK map[string]any    `json:"jwk"`
}

type dpopClaims struct {
	JTI   string `json:"jti"`
	HTM   string `json:"htm"`
	HTU   string `json:"htu"`
	IAT   *int64 `json:"iat"`
	ATH   string `json:"ath"`
	Nonce string `json:"nonce"`
}

// VerifyProof validates a raw DPoP JWT against req and enforces single-use jti replay protection.
func (v *DPoPVerifier) VerifyProof(proofJWT string, req DPoPVerifyRequest) (DPoPVerifiedProof, error) {
	parts := strings.Split(strings.TrimSpace(proofJWT), ".")
	if len(parts) != 3 {
		return DPoPVerifiedProof{}, ErrDPoPMalformedProof
	}
	hdrBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return DPoPVerifiedProof{}, fmt.Errorf("%w: bad header b64", ErrDPoPMalformedProof)
	}
	clmBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return DPoPVerifiedProof{}, fmt.Errorf("%w: bad claims b64", ErrDPoPMalformedProof)
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return DPoPVerifiedProof{}, fmt.Errorf("%w: bad sig b64", ErrDPoPMalformedProof)
	}

	var hdr dpopHeader
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil {
		return DPoPVerifiedProof{}, fmt.Errorf("%w: invalid header JSON", ErrDPoPMalformedProof)
	}
	if hdr.Typ != "dpop+jwt" {
		return DPoPVerifiedProof{}, ErrDPoPInvalidTyp
	}
	if hdr.Alg != "ES256" && hdr.Alg != "EdDSA" {
		return DPoPVerifiedProof{}, fmt.Errorf("%w: %q", ErrDPoPUnsupportedAlg, hdr.Alg)
	}
	if len(hdr.JWK) == 0 {
		return DPoPVerifiedProof{}, fmt.Errorf("%w: missing jwk header", ErrDPoPMalformedProof)
	}
	// RFC 9449 §4.3: embedded JWK MUST NOT contain private key material.
	for _, forbidden := range []string{"d", "p", "q", "dp", "dq", "qi", "oth", "k"} {
		if _, exists := hdr.JWK[forbidden]; exists {
			return DPoPVerifiedProof{}, ErrDPoPPrivateJWK
		}
	}

	jwkStrings := make(map[string]string, len(hdr.JWK))
	for k, val := range hdr.JWK {
		if s, ok := val.(string); ok {
			jwkStrings[k] = s
		}
	}

	signingInput := parts[0] + "." + parts[1]
	if err := verifyDPoPSignature(hdr.Alg, jwkStrings, []byte(signingInput), sigBytes); err != nil {
		return DPoPVerifiedProof{}, err
	}

	jkt, err := computeJWKThumbprint(jwkStrings)
	if err != nil {
		return DPoPVerifiedProof{}, fmt.Errorf("%w: %v", ErrDPoPMalformedProof, err)
	}
	if req.ExpectedJKT != "" && subtle.ConstantTimeCompare([]byte(jkt), []byte(req.ExpectedJKT)) != 1 {
		return DPoPVerifiedProof{}, ErrDPoPThumbprintMismatch
	}

	var clm dpopClaims
	if err := json.Unmarshal(clmBytes, &clm); err != nil {
		return DPoPVerifiedProof{}, fmt.Errorf("%w: invalid claims JSON", ErrDPoPMalformedProof)
	}
	if strings.TrimSpace(clm.JTI) == "" || clm.IAT == nil {
		return DPoPVerifiedProof{}, fmt.Errorf("%w: missing jti or iat", ErrDPoPMalformedProof)
	}
	if strings.ToUpper(strings.TrimSpace(clm.HTM)) != strings.ToUpper(strings.TrimSpace(req.ExpectedHTM)) {
		return DPoPVerifiedProof{}, ErrDPoPMethodMismatch
	}
	wantHTU, err := normalizeDPoPHTU(req.ExpectedHTU)
	if err != nil {
		return DPoPVerifiedProof{}, ErrDPoPURIMismatch
	}
	gotHTU, err := normalizeDPoPHTU(clm.HTU)
	if err != nil || gotHTU != wantHTU {
		return DPoPVerifiedProof{}, ErrDPoPURIMismatch
	}
	if req.AccessToken != "" {
		wantATH := computeDPoPAccessTokenHash(req.AccessToken)
		if subtle.ConstantTimeCompare([]byte(clm.ATH), []byte(wantATH)) != 1 {
			return DPoPVerifiedProof{}, ErrDPoPAccessTokenHashMismatch
		}
	}
	if req.ExpectedNonce != "" && subtle.ConstantTimeCompare([]byte(clm.Nonce), []byte(req.ExpectedNonce)) != 1 {
		return DPoPVerifiedProof{}, ErrDPoPNonceMismatch
	}

	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	iat := time.Unix(*clm.IAT, 0).UTC()
	delta := now.Sub(iat)
	if delta < -v.maxSkew || delta > v.maxSkew {
		return DPoPVerifiedProof{}, ErrDPoPStaleProof
	}

	// Single-use jti replay check AFTER cryptographic verification so unauthenticated
	// callers cannot poison the replay cache.
	replayKey := jkt + "\x00" + clm.JTI
	v.mu.Lock()
	for k, exp := range v.seenJTIs {
		if now.After(exp) {
			delete(v.seenJTIs, k)
		}
	}
	if exp, seen := v.seenJTIs[replayKey]; seen && !now.After(exp) {
		v.mu.Unlock()
		return DPoPVerifiedProof{}, ErrDPoPProofReplay
	}
	v.seenJTIs[replayKey] = now.Add(v.replayTTL)
	v.totalAccepted++
	v.acceptedByKey[jkt]++
	v.mu.Unlock()

	return DPoPVerifiedProof{
		Alg:        hdr.Alg,
		Thumbprint: jkt,
		JTI:        clm.JTI,
		HTM:        clm.HTM,
		HTU:        gotHTU,
		IssuedAt:   iat,
	}, nil
}

// Stats returns total accepted proofs and unique jti count (used by conformance checks).
func (v *DPoPVerifier) Stats() (totalAccepted int, uniqueJTIs int, acceptedByKey map[string]int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	cp := make(map[string]int, len(v.acceptedByKey))
	for k, c := range v.acceptedByKey {
		cp[k] = c
	}
	return v.totalAccepted, len(v.seenJTIs), cp
}

// AuthorizedDPoPHeaders enforces the Zero Trust Cedar PEP ticket at final-use time
// and returns sender-constrained `Authorization: DPoP <tok>` and `DPoP: <proof>` headers.
func (a *Authorizer) AuthorizedDPoPHeaders(
	ctx context.Context,
	ticket AuthzTicket,
	dpopKey *DPoPKeyPair,
	htm, htu, nonce string,
) (authHeader string, dpopProof string, err error) {
	if err := a.Enforce(ticket); err != nil {
		return "", "", err
	}
	tok, err := a.mgr.ValidAccessToken(ctx, ticket.AccountID)
	if err != nil {
		return "", "", err
	}
	if err := a.Enforce(ticket); err != nil {
		return "", "", err
	}
	proof, _, err := dpopKey.CreateProof(htm, htu, tok, nonce, time.Now().UTC())
	if err != nil {
		return "", "", err
	}
	return "DPoP " + tok, proof, nil
}

func computeDPoPAccessTokenHash(accessToken string) string {
	sum := sha256.Sum256([]byte(accessToken))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// normalizeDPoPHTU strips query and fragment components per RFC 9449 §4.2.
func normalizeDPoPHTU(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("%w: invalid htu %q", ErrDPoPURIMismatch, raw)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.RawQuery = ""
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

// computeJWKThumbprint implements RFC 7638 canonical JSON SHA-256 thumbprint.
func computeJWKThumbprint(jwk map[string]string) (string, error) {
	var canonical string
	switch jwk["kty"] {
	case "EC":
		crv, x, y := jwk["crv"], jwk["x"], jwk["y"]
		if crv == "" || x == "" || y == "" {
			return "", errors.New("incomplete EC JWK")
		}
		// RFC 7638 §3.2: keys MUST be ordered lexicographically with no whitespace.
		canonical = fmt.Sprintf(`{"crv":%q,"kty":"EC","x":%q,"y":%q}`, crv, x, y)
	case "OKP":
		crv, x := jwk["crv"], jwk["x"]
		if crv == "" || x == "" {
			return "", errors.New("incomplete OKP JWK")
		}
		canonical = fmt.Sprintf(`{"crv":%q,"kty":"OKP","x":%q}`, crv, x)
	default:
		return "", fmt.Errorf("unsupported JWK kty %q", jwk["kty"])
	}
	sum := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func verifyDPoPSignature(alg string, jwk map[string]string, signingInput, sig []byte) error {
	switch alg {
	case "ES256":
		if jwk["kty"] != "EC" || jwk["crv"] != "P-256" || len(sig) != 64 {
			return ErrDPoPInvalidSignature
		}
		xb, err := base64.RawURLEncoding.DecodeString(jwk["x"])
		if err != nil || len(xb) != 32 {
			return ErrDPoPInvalidSignature
		}
		yb, err := base64.RawURLEncoding.DecodeString(jwk["y"])
		if err != nil || len(yb) != 32 {
			return ErrDPoPInvalidSignature
		}
		pub := &ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).SetBytes(xb),
			Y:     new(big.Int).SetBytes(yb),
		}
		if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
			return ErrDPoPInvalidSignature
		}
		r := new(big.Int).SetBytes(sig[:32])
		s := new(big.Int).SetBytes(sig[32:])
		digest := sha256.Sum256(signingInput)
		if !ecdsa.Verify(pub, digest[:], r, s) {
			return ErrDPoPInvalidSignature
		}
		return nil
	case "EdDSA":
		if jwk["kty"] != "OKP" || jwk["crv"] != "Ed25519" || len(sig) != ed25519.SignatureSize {
			return ErrDPoPInvalidSignature
		}
		xb, err := base64.RawURLEncoding.DecodeString(jwk["x"])
		if err != nil || len(xb) != ed25519.PublicKeySize {
			return ErrDPoPInvalidSignature
		}
		if !ed25519.Verify(ed25519.PublicKey(xb), signingInput, sig) {
			return ErrDPoPInvalidSignature
		}
		return nil
	default:
		return ErrDPoPUnsupportedAlg
	}
}
