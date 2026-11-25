// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package auth implements Marlin's Tier-0 Host authentication and authorization
// framework: OAuth 2.1 with mandatory PKCE S256 (Google, GitHub, Enterprise OIDC),
// RFC 8628 Device Authorization, passwordless Email OTP / Magic Link flows,
// singleflight token refresh, and pluggable encrypted credential vaults.
//
// Security invariant: raw access/refresh/ID tokens are strictly confined to the
// Tier-0 host boundary and are NEVER included in AuthSnapshot, String() output,
// logs, or untrusted plugin projections.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// AccountID is a typed identifier for one signed-in user account.
type AccountID string

// ProviderID is a typed identifier for one authentication provider.
type ProviderID string

const (
	ProviderGoogle ProviderID = "google"
	ProviderGitHub ProviderID = "github"
	ProviderApple  ProviderID = "apple"
	ProviderOIDC   ProviderID = "oidc"
	ProviderEmail  ProviderID = "email"
)

// AuthMethod classifies the cryptographic login protocol used by a provider.
type AuthMethod string

const (
	MethodOAuthPKCE      AuthMethod = "oauth2-pkce-s256"
	MethodDeviceCode     AuthMethod = "oauth2-device-code"
	MethodEmailOTP       AuthMethod = "email-otp"
	MethodEmailMagicLink AuthMethod = "email-magic-link"
)

// AuthPhase drives the Tier-0 Auth UI state machine.
type AuthPhase string

const (
	PhaseSignedOut    AuthPhase = "signed_out"
	PhaseAuthorizing  AuthPhase = "authorizing"
	PhaseAwaitingCode AuthPhase = "awaiting_code"
	PhaseExchanging   AuthPhase = "exchanging"
	PhaseSignedIn     AuthPhase = "signed_in"
	PhaseRefreshing   AuthPhase = "refreshing"
	PhaseError        AuthPhase = "error"
)

var (
	ErrProviderNotFound   = errors.New("auth: provider not registered")
	ErrAccountNotFound    = errors.New("auth: account not found")
	ErrInvalidEmail       = errors.New("auth: invalid email address")
	ErrInsecureEndpoint   = errors.New("auth: remote OAuth endpoint must use HTTPS")
	ErrInvalidState       = errors.New("auth: OAuth state mismatch or already consumed")
	ErrAttemptExpired     = errors.New("auth: authorization attempt expired")
	ErrCooldownActive     = errors.New("auth: verification resend cooldown active")
	ErrEmptyCode          = errors.New("auth: verification code is required")
	ErrRefreshUnavailable = errors.New("auth: no refresh token available for account")
	ErrNotSupported       = errors.New("auth: flow not supported on this platform or provider")
	ErrScopeEscalation    = errors.New("auth: token refresh attempted scope escalation")
	ErrNonceMismatch      = errors.New("auth: OIDC ID token nonce mismatch")
	ErrFlowCancelled      = errors.New("auth: authorization flow was cancelled or superseded")
	ErrSessionRevoked     = errors.New("auth: account session was signed out or revoked during refresh")
)

// ProviderConfig defines one registered authentication provider.
type ProviderConfig struct {
	ID              ProviderID
	DisplayName     string
	Method          AuthMethod
	AuthURL         string   // HTTPS authorization endpoint (OAuth 2.1 / OIDC)
	TokenURL        string   // HTTPS token exchange & refresh endpoint
	DeviceAuthURL   string   // optional RFC 8628 device authorization endpoint
	UserInfoURL     string   // optional OIDC UserInfo endpoint
	RevokeURL       string   // optional RFC 7009 token revocation endpoint
	Issuer          string   // optional OIDC expected `iss` claim for id_token verification
	JWKSURL         string   // optional RFC 7517 JWKS endpoint for cryptographic id_token verification
	ClientID        string   // public native client ID (PKCE requires no client secret)
	Scopes          []string // requested OAuth scopes
	RedirectURI     string   // optional fixed redirect URI (loopback chosen dynamically when empty)
	AllowLoopback   bool     // allow http://127.0.0.1 / http://localhost endpoints (for tests/local IdP)
	BadgeAccentHex  string   // optional UI hint
	SupportsRefresh bool
}

// Validate enforces fail-closed transport security on provider endpoints.
func (c ProviderConfig) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("auth: ProviderConfig.ID is required")
	}
	if c.DisplayName == "" {
		return fmt.Errorf("auth: ProviderConfig.DisplayName is required")
	}
	switch c.Method {
	case MethodOAuthPKCE:
		if err := ValidateSecureURL(c.AuthURL, c.AllowLoopback); err != nil {
			return fmt.Errorf("auth: AuthURL: %w", err)
		}
		if err := ValidateSecureURL(c.TokenURL, c.AllowLoopback); err != nil {
			return fmt.Errorf("auth: TokenURL: %w", err)
		}
		if c.ClientID == "" {
			return fmt.Errorf("auth: ClientID is required for OAuth provider %q", c.ID)
		}
	case MethodDeviceCode:
		if err := ValidateSecureURL(c.DeviceAuthURL, c.AllowLoopback); err != nil {
			return fmt.Errorf("auth: DeviceAuthURL: %w", err)
		}
		if err := ValidateSecureURL(c.TokenURL, c.AllowLoopback); err != nil {
			return fmt.Errorf("auth: TokenURL: %w", err)
		}
		if c.ClientID == "" {
			return fmt.Errorf("auth: ClientID is required for device flow provider %q", c.ID)
		}
	case MethodEmailOTP, MethodEmailMagicLink:
		// Handled through EmailAuthBackend (Rust App Server or HTTPS identity gateway).
	default:
		return fmt.Errorf("auth: unsupported auth method %q", c.Method)
	}
	if c.UserInfoURL != "" {
		if err := ValidateSecureURL(c.UserInfoURL, c.AllowLoopback); err != nil {
			return fmt.Errorf("auth: UserInfoURL: %w", err)
		}
	}
	if c.RevokeURL != "" {
		if err := ValidateSecureURL(c.RevokeURL, c.AllowLoopback); err != nil {
			return fmt.Errorf("auth: RevokeURL: %w", err)
		}
	}
	if c.JWKSURL != "" {
		if err := ValidateSecureURL(c.JWKSURL, c.AllowLoopback); err != nil {
			return fmt.Errorf("auth: JWKSURL: %w", err)
		}
	}
	return nil
}

// GoogleOAuthProvider returns the canonical Google OIDC / OAuth 2.1 PKCE S256 preset.
func GoogleOAuthProvider(clientID string, extraScopes ...string) ProviderConfig {
	scopes := []string{"openid", "email", "profile"}
	scopes = append(scopes, extraScopes...)
	return ProviderConfig{
		ID:              ProviderGoogle,
		DisplayName:     "Google",
		Method:          MethodOAuthPKCE,
		AuthURL:         "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:        "https://oauth2.googleapis.com/token",
		UserInfoURL:     "https://openidconnect.googleapis.com/v1/userinfo",
		RevokeURL:       "https://oauth2.googleapis.com/revoke",
		Issuer:          "https://accounts.google.com",
		JWKSURL:         "https://www.googleapis.com/oauth2/v3/certs",
		ClientID:        strings.TrimSpace(clientID),
		Scopes:          scopes,
		SupportsRefresh: true,
	}
}

// GitHubOAuthProvider returns the canonical GitHub OAuth 2.1 PKCE S256 / Device preset.
func GitHubOAuthProvider(clientID string, extraScopes ...string) ProviderConfig {
	scopes := []string{"read:user", "user:email"}
	scopes = append(scopes, extraScopes...)
	return ProviderConfig{
		ID:              ProviderGitHub,
		DisplayName:     "GitHub",
		Method:          MethodOAuthPKCE,
		AuthURL:         "https://github.com/login/oauth/authorize",
		TokenURL:        "https://github.com/login/oauth/access_token",
		DeviceAuthURL:   "https://github.com/login/device/code",
		UserInfoURL:     "https://api.github.com/user",
		ClientID:        strings.TrimSpace(clientID),
		Scopes:          scopes,
		SupportsRefresh: true,
	}
}

// EmailOTPProvider returns the canonical passwordless Email OTP / Magic Link preset.
func EmailOTPProvider() ProviderConfig {
	return ProviderConfig{
		ID:              ProviderEmail,
		DisplayName:     "Email",
		Method:          MethodEmailOTP,
		Scopes:          []string{"openid", "email"},
		SupportsRefresh: true,
	}
}

// TokenSet holds sensitive OAuth/session credentials inside the Tier-0 vault.
// String() and GoString() ALWAYS redact secret material so accidental logging
// can never expose tokens.
type TokenSet struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	TokenType    string // usually "Bearer"
	Scopes       []string
	IssuedAt     time.Time
	ExpiresAt    time.Time
}

// String implements fmt.Stringer with mandatory secret redaction.
func (t TokenSet) String() string {
	return fmt.Sprintf("TokenSet{type=%q scopes=%d expiresAt=%s access=[REDACTED] refresh=[REDACTED]}",
		t.TokenType, len(t.Scopes), t.ExpiresAt.UTC().Format(time.RFC3339))
}

// GoString implements fmt.GoStringer with mandatory secret redaction.
func (t TokenSet) GoString() string { return t.String() }

// IsExpired reports whether the access token is expired or within skew of expiry.
func (t TokenSet) IsExpired(now time.Time, skew time.Duration) bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return !now.Add(skew).Before(t.ExpiresAt)
}

// Clone returns an isolated copy so callers cannot mutate vaulted slices.
func (t TokenSet) Clone() TokenSet {
	cp := t
	if len(t.Scopes) > 0 {
		cp.Scopes = append([]string(nil), t.Scopes...)
	}
	return cp
}

// AccountProfile is the secret-free identity metadata safe to display in Tier-0 UI.
type AccountProfile struct {
	ID            AccountID
	ProviderID    ProviderID
	ProviderName  string
	Method        AuthMethod
	Subject       string
	Email         string
	DisplayName   string
	AvatarURL     string
	EmailVerified bool
	Scopes        []string
	SignedInAt    time.Time
	ExpiresAt     time.Time
	HasRefresh    bool
}

// PendingOAuthFlow describes an active browser/device authorization waiting for completion.
type PendingOAuthFlow struct {
	AttemptID       string
	ProviderID      ProviderID
	ProviderName    string
	Method          AuthMethod
	AuthorizeURL    string
	RedirectURI     string
	UserCode        string // populated for RFC 8628 Device Flow
	VerificationURI string // populated for RFC 8628 Device Flow
	ExpiresAt       time.Time
}

// PendingEmailChallenge describes an active passwordless email OTP / Magic Link challenge.
type PendingEmailChallenge struct {
	ChallengeID   string
	Email         string
	Method        AuthMethod
	ExpiresAt     time.Time
	CooldownUntil time.Time
	Attempts      int
	MaxAttempts   int
}

// AuthSnapshot is an immutable, 100% secret-free view for 120Hz Tier-0 UI rendering.
type AuthSnapshot struct {
	Phase           AuthPhase
	ActiveAccountID AccountID
	Accounts        []AccountProfile
	Providers       []ProviderSummary
	PendingOAuth    *PendingOAuthFlow
	PendingEmail    *PendingEmailChallenge
	LastError       string
}

// ProviderSummary is a secret-free descriptor of one configured login provider.
type ProviderSummary struct {
	ID          ProviderID
	DisplayName string
	Method      AuthMethod
	Scopes      []string
}

// ActiveProfile returns the active AccountProfile if signed in.
func (s AuthSnapshot) ActiveProfile() (AccountProfile, bool) {
	for _, acc := range s.Accounts {
		if acc.ID == s.ActiveAccountID {
			return acc, true
		}
	}
	return AccountProfile{}, false
}

// TokenExchanger abstracts HTTP/gRPC OAuth token exchange, refresh, revocation,
// and profile lookup so callers can route through Go HTTPS directly or delegate
// to the Rust App Server authority.
type TokenExchanger interface {
	ExchangeCode(ctx context.Context, cfg ProviderConfig, code, codeVerifier, redirectURI, expectedNonce string) (TokenSet, AccountProfile, error)
	RefreshToken(ctx context.Context, cfg ProviderConfig, refreshToken string) (TokenSet, error)
	RevokeToken(ctx context.Context, cfg ProviderConfig, token string) error
}

// DeviceFlowBackend abstracts RFC 8628 device authorization start & poll steps.
type DeviceFlowBackend interface {
	RequestDeviceCode(ctx context.Context, cfg ProviderConfig) (DeviceCodeResponse, error)
	PollDeviceToken(ctx context.Context, cfg ProviderConfig, deviceCode string) (TokenSet, AccountProfile, bool, error)
}

// DeviceCodeResponse holds the RFC 8628 device authorization response.
type DeviceCodeResponse struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	ExpiresIn       time.Duration
	Interval        time.Duration
}

// EmailAuthBackend abstracts passwordless Email OTP / Magic Link challenge
// issuance and verification (owned by Rust App Server or HTTPS identity provider).
type EmailAuthBackend interface {
	RequestChallenge(ctx context.Context, email string, method AuthMethod) (EmailChallengeResponse, error)
	VerifyChallenge(ctx context.Context, challengeID, email, codeOrToken string, method AuthMethod) (TokenSet, AccountProfile, error)
}

// EmailChallengeResponse holds metadata returned when an email challenge is sent.
type EmailChallengeResponse struct {
	ChallengeID string
	ExpiresIn   time.Duration
	Cooldown    time.Duration
	MaxAttempts int
}

// BrowserOpener opens an HTTPS authorization URL in the user's system browser
// (RFC 8252: external user-agent, NEVER an embedded untrusted WebView).
type BrowserOpener func(ctx context.Context, authorizeURL string) error

// ValidateSecureURL enforces HTTPS for all non-loopback OAuth/OIDC URLs.
func ValidateSecureURL(raw string, allowLoopback bool) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("%w: empty URL", ErrInsecureEndpoint)
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: malformed URL %q", ErrInsecureEndpoint, trimmed)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "https" {
		return nil
	}
	if scheme == "http" && allowLoopback && isLoopbackHost(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrInsecureEndpoint, trimmed)
}

// NormalizeAndValidateEmail validates and canonicalizes an email address.
func NormalizeAndValidateEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if len(email) < 5 || len(email) > 254 {
		return "", ErrInvalidEmail
	}
	if strings.ContainsAny(email, " \t\r\n<>\"'\\") {
		return "", ErrInvalidEmail
	}
	at := strings.IndexByte(email, '@')
	if at <= 0 || at != strings.LastIndexByte(email, '@') {
		return "", ErrInvalidEmail
	}
	local, domain := email[:at], email[at+1:]
	if len(local) == 0 || len(local) > 64 || len(domain) < 3 {
		return "", ErrInvalidEmail
	}
	if strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, "..") {
		return "", ErrInvalidEmail
	}
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || strings.Contains(domain, "..") || !strings.Contains(domain, ".") {
		return "", ErrInvalidEmail
	}
	return email, nil
}

func isLoopbackHost(host string) bool {
	h := strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
