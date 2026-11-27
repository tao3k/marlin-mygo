// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPOAuthExchanger implements standard RFC 6749 + RFC 7636 PKCE + RFC 7009
// OAuth 2.1 HTTP form-encoded token exchange, refresh, revocation, and
// cryptographic OIDC JWT id_token verification via JWKS.
type HTTPOAuthExchanger struct {
	Client *http.Client
	JWKS   *HTTPJWKSCache
}

func NewHTTPOAuthExchanger(client *http.Client) *HTTPOAuthExchanger {
	safeClient := noRedirectHTTPClient(client, 15*time.Second)
	return &HTTPOAuthExchanger{
		Client: safeClient,
		JWKS:   NewHTTPJWKSCache(safeClient, 15*time.Minute),
	}
}

func (e *HTTPOAuthExchanger) ExchangeCode(
	ctx context.Context,
	cfg ProviderConfig,
	code, codeVerifier, redirectURI, expectedNonce string,
) (TokenSet, AccountProfile, error) {
	if err := ValidateSecureURL(cfg.TokenURL, cfg.AllowLoopback); err != nil {
		return TokenSet{}, AccountProfile{}, err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", cfg.ClientID)
	form.Set("code", code)
	form.Set("code_verifier", codeVerifier)
	form.Set("redirect_uri", redirectURI)

	tok, err := e.postTokenForm(ctx, cfg.TokenURL, form)
	if err != nil {
		return TokenSet{}, AccountProfile{}, err
	}
	profile := AccountProfile{
		ProviderID:   cfg.ID,
		ProviderName: cfg.DisplayName,
		Method:       cfg.Method,
		Scopes:       tok.Scopes,
	}
	if strings.TrimSpace(tok.IDToken) != "" {
		if cfg.JWKSURL != "" {
			if e.JWKS == nil {
				e.JWKS = NewHTTPJWKSCache(e.Client, 15*time.Minute)
			}
			verifyCfg := IDTokenVerifyConfig{
				ExpectedIssuer:   cfg.Issuer,
				ExpectedAudience: cfg.ClientID,
				ExpectedNonce:    expectedNonce,
			}
			keySet, kErr := e.JWKS.Get(ctx, cfg.JWKSURL, cfg.AllowLoopback, false)
			if kErr != nil {
				return TokenSet{}, AccountProfile{}, fmt.Errorf("auth: fetch OIDC JWKS: %w", kErr)
			}
			claims, vErr := VerifyIDToken(tok.IDToken, keySet, verifyCfg)
			if errors.Is(vErr, ErrKeyNotFound) {
				if refreshedSet, rErr := e.JWKS.Get(ctx, cfg.JWKSURL, cfg.AllowLoopback, true); rErr == nil {
					claims, vErr = VerifyIDToken(tok.IDToken, refreshedSet, verifyCfg)
				}
			}
			if vErr != nil {
				return TokenSet{}, AccountProfile{}, vErr
			}
			profile.Subject = claims.Subject
			profile.Email = claims.Email
			profile.DisplayName = claims.Name
			profile.AvatarURL = claims.Picture
			profile.EmailVerified = claims.EmailVerified
		} else if expectedNonce != "" {
			if err := verifyIDTokenNonce(tok.IDToken, expectedNonce); err != nil {
				return TokenSet{}, AccountProfile{}, err
			}
		}
	}
	if cfg.UserInfoURL != "" {
		if err := ValidateSecureURL(cfg.UserInfoURL, cfg.AllowLoopback); err != nil {
			return TokenSet{}, AccountProfile{}, err
		}
		if uiProf, uErr := e.fetchUserInfo(ctx, cfg.UserInfoURL, tok.AccessToken); uErr == nil {
			// OIDC Core 1.0 Section 5.3.4: UserInfo sub MUST match verified ID token sub.
			if profile.Subject != "" && uiProf.Subject != "" && uiProf.Subject != profile.Subject {
				return TokenSet{}, AccountProfile{}, fmt.Errorf("%w: id_token=%q userinfo=%q", ErrSubjectMismatch, profile.Subject, uiProf.Subject)
			}
			if profile.Subject == "" {
				profile.Subject = uiProf.Subject
			}
			if profile.Email == "" {
				profile.Email = uiProf.Email
			}
			if profile.DisplayName == "" {
				profile.DisplayName = uiProf.DisplayName
			}
			if profile.AvatarURL == "" {
				profile.AvatarURL = uiProf.AvatarURL
			}
			if uiProf.EmailVerified {
				profile.EmailVerified = true
			}
		}
	}
	return tok, profile, nil
}

func (e *HTTPOAuthExchanger) RefreshToken(ctx context.Context, cfg ProviderConfig, refreshToken string) (TokenSet, error) {
	if err := ValidateSecureURL(cfg.TokenURL, cfg.AllowLoopback); err != nil {
		return TokenSet{}, err
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", cfg.ClientID)
	form.Set("refresh_token", refreshToken)
	return e.postTokenForm(ctx, cfg.TokenURL, form)
}

func (e *HTTPOAuthExchanger) RevokeToken(ctx context.Context, cfg ProviderConfig, token string) error {
	if cfg.RevokeURL == "" {
		return nil
	}
	if err := ValidateSecureURL(cfg.RevokeURL, cfg.AllowLoopback); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("token", token)
	if cfg.ClientID != "" {
		form.Set("client_id", cfg.ClientID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.RevokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := e.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return nil
}

func (e *HTTPOAuthExchanger) postTokenForm(ctx context.Context, endpoint string, form url.Values) (TokenSet, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenSet{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := e.Client.Do(req)
	if err != nil {
		return TokenSet{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return TokenSet{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return TokenSet{}, fmt.Errorf("auth: token endpoint returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return TokenSet{}, fmt.Errorf("auth: decode token response: %w", err)
	}
	if payload.Error != "" {
		return TokenSet{}, fmt.Errorf("auth: token exchange rejected (%s: %s)", payload.Error, payload.ErrorDesc)
	}
	now := time.Now()
	tok := TokenSet{
		AccessToken:  payload.AccessToken,
		RefreshToken: payload.RefreshToken,
		IDToken:      payload.IDToken,
		TokenType:    payload.TokenType,
		IssuedAt:     now,
	}
	if payload.ExpiresIn > 0 {
		tok.ExpiresAt = now.Add(time.Duration(payload.ExpiresIn) * time.Second)
	}
	if payload.Scope != "" {
		tok.Scopes = strings.Fields(payload.Scope)
	}
	return tok, nil
}

func (e *HTTPOAuthExchanger) fetchUserInfo(ctx context.Context, endpoint, accessToken string) (AccountProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return AccountProfile{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := e.Client.Do(req)
	if err != nil {
		return AccountProfile{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AccountProfile{}, fmt.Errorf("auth: userinfo returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if err != nil {
		return AccountProfile{}, err
	}
	var payload struct {
		Sub           string `json:"sub"`
		ID            any    `json:"id"`
		Email         string `json:"email"`
		Name          string `json:"name"`
		Login         string `json:"login"`
		Picture       string `json:"picture"`
		AvatarURL     string `json:"avatar_url"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return AccountProfile{}, err
	}
	sub := payload.Sub
	if sub == "" && payload.ID != nil {
		sub = fmt.Sprint(payload.ID)
	}
	name := payload.Name
	if name == "" {
		name = payload.Login
	}
	avatar := payload.Picture
	if avatar == "" {
		avatar = payload.AvatarURL
	}
	return AccountProfile{
		Subject:       sub,
		Email:         payload.Email,
		DisplayName:   name,
		AvatarURL:     avatar,
		EmailVerified: payload.EmailVerified,
	}, nil
}

func verifyIDTokenNonce(idToken, expectedNonce string) error {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return ErrNonceMismatch
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ErrNonceMismatch
	}
	var claims struct {
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return ErrNonceMismatch
	}
	if !ConstantTimeTokenEqual(claims.Nonce, expectedNonce) {
		return ErrNonceMismatch
	}
	return nil
}
