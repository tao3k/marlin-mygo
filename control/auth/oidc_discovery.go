// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OIDCDiscoveryDocument represents the validated subset of RFC 8414 / OpenID
// Connect Discovery 1.0 metadata returned from `/.well-known/openid-configuration`.
type OIDCDiscoveryDocument struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	JWKSURI                       string   `json:"jwks_uri"`
	UserInfoEndpoint              string   `json:"userinfo_endpoint"`
	RevocationEndpoint            string   `json:"revocation_endpoint"`
	DeviceAuthorizationEndpoint   string   `json:"device_authorization_endpoint"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
}

// DiscoverOIDCProvider fetches `<issuerURL>/.well-known/openid-configuration`,
// strictly enforces HTTPS and OIDC Discovery 1.0 Section 4.3 exact issuer match
// (preventing issuer substitution / MITM attacks), verifies PKCE S256 support,
// and returns a validated ProviderConfig ready for AuthManager.RegisterProvider.
func DiscoverOIDCProvider(
	ctx context.Context,
	client *http.Client,
	issuerURL string,
	clientID string,
	displayName string,
	allowLoopback bool,
) (ProviderConfig, error) {
	canonicalIssuer := strings.TrimRight(strings.TrimSpace(issuerURL), "/")
	if err := ValidateSecureURL(canonicalIssuer, allowLoopback); err != nil {
		return ProviderConfig{}, fmt.Errorf("auth: OIDC issuer URL: %w", err)
	}
	cid := strings.TrimSpace(clientID)
	if cid == "" {
		return ProviderConfig{}, fmt.Errorf("auth: Enterprise OIDC Client ID is required")
	}
	client = noRedirectHTTPClient(client, 10*time.Second)

	wellKnownURL := canonicalIssuer + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnownURL, nil)
	if err != nil {
		return ProviderConfig{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return ProviderConfig{}, fmt.Errorf("auth: fetch OIDC discovery %s: %w", wellKnownURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ProviderConfig{}, fmt.Errorf("auth: OIDC discovery returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return ProviderConfig{}, err
	}
	var doc OIDCDiscoveryDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return ProviderConfig{}, fmt.Errorf("auth: decode OIDC discovery JSON: %w", err)
	}

	// OIDC Discovery 1.0 Section 4.3: returned issuer MUST exactly match queried issuer.
	if strings.TrimRight(strings.TrimSpace(doc.Issuer), "/") != canonicalIssuer {
		return ProviderConfig{}, fmt.Errorf("%w: discovery returned %q for queried issuer %q", ErrIssuerMismatch, doc.Issuer, canonicalIssuer)
	}

	// If the IdP advertises supported PKCE methods, S256 MUST be among them.
	if len(doc.CodeChallengeMethodsSupported) > 0 {
		hasS256 := false
		for _, m := range doc.CodeChallengeMethodsSupported {
			if m == "S256" {
				hasS256 = true
				break
			}
		}
		if !hasS256 {
			return ProviderConfig{}, fmt.Errorf("auth: OIDC provider %s does not advertise mandatory PKCE S256", canonicalIssuer)
		}
	}

	name := strings.TrimSpace(displayName)
	if name == "" {
		name = "Enterprise SSO"
	}
	cfg := ProviderConfig{
		ID:              ProviderOIDC,
		DisplayName:     name,
		Method:          MethodOAuthPKCE,
		AuthURL:         strings.TrimSpace(doc.AuthorizationEndpoint),
		TokenURL:        strings.TrimSpace(doc.TokenEndpoint),
		DeviceAuthURL:   strings.TrimSpace(doc.DeviceAuthorizationEndpoint),
		UserInfoURL:     strings.TrimSpace(doc.UserInfoEndpoint),
		RevokeURL:       strings.TrimSpace(doc.RevocationEndpoint),
		Issuer:          doc.Issuer,
		JWKSURL:         strings.TrimSpace(doc.JWKSURI),
		ClientID:        cid,
		Scopes:          []string{"openid", "email", "profile"},
		AllowLoopback:   allowLoopback,
		SupportsRefresh: true,
	}
	if err := cfg.Validate(); err != nil {
		return ProviderConfig{}, err
	}
	return cfg, nil
}
