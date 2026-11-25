// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RunLoopbackOAuth executes the full RFC 8252 native desktop OAuth 2.1 PKCE S256
// flow end-to-end: binds an ephemeral 127.0.0.1:0 callback server, generates
// PKCE+state+nonce, opens the system browser via OpenBrowser, waits for the
// single-use redirect, and exchanges the authorization code.
func (m *AuthManager) RunLoopbackOAuth(ctx context.Context, providerID ProviderID) (AccountProfile, error) {
	recv, err := StartLoopbackReceiver()
	if err != nil {
		m.setError(err)
		return AccountProfile{}, err
	}
	defer recv.Close()

	flow, err := m.BeginOAuth(providerID, recv.RedirectURI())
	if err != nil {
		m.setError(err)
		return AccountProfile{}, err
	}
	m.mu.RLock()
	if att, ok := m.oauthAttempts[flow.AttemptID]; ok {
		recv.SetExpectedState(att.state)
	}
	m.mu.RUnlock()
	if m.openBrowser != nil {
		if err := m.openBrowser(ctx, flow.AuthorizeURL); err != nil {
			m.CancelPending()
			m.setError(err)
			return AccountProfile{}, err
		}
	}
	cb, err := recv.Wait(ctx)
	if err != nil {
		m.CancelPending()
		m.setError(err)
		return AccountProfile{}, err
	}
	return m.CompleteOAuthCallback(ctx, flow.AttemptID, cb)
}

// BeginDeviceAuth initiates an RFC 8628 Device Authorization flow (for headless
// remote workstations, SSH, CLI, or WebAssembly environments).
func (m *AuthManager) BeginDeviceAuth(ctx context.Context, providerID ProviderID) (PendingOAuthFlow, string, error) {
	if m.deviceFlow == nil {
		return PendingOAuthFlow{}, "", ErrNotSupported
	}
	m.mu.RLock()
	cfg, ok := m.providers[providerID]
	m.mu.RUnlock()
	if !ok {
		return PendingOAuthFlow{}, "", fmt.Errorf("%w: %s", ErrProviderNotFound, providerID)
	}
	resp, err := m.deviceFlow.RequestDeviceCode(ctx, cfg)
	if err != nil {
		m.setError(err)
		return PendingOAuthFlow{}, "", err
	}
	if err := ValidateSecureURL(resp.VerificationURI, cfg.AllowLoopback); err != nil {
		m.setError(err)
		return PendingOAuthFlow{}, "", err
	}
	ttl := resp.ExpiresIn
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	flow := PendingOAuthFlow{
		ProviderID:      providerID,
		ProviderName:    cfg.DisplayName,
		Method:          MethodDeviceCode,
		UserCode:        resp.UserCode,
		VerificationURI: resp.VerificationURI,
		AuthorizeURL:    resp.VerificationURI,
		ExpiresAt:       m.now().Add(ttl),
	}
	m.mu.Lock()
	m.flowEpoch++
	m.pendingOAuth = &flow
	m.pendingDeviceCode = resp.DeviceCode
	m.phase = PhaseAuthorizing
	m.lastError = ""
	m.mu.Unlock()
	m.emit()
	return flow, resp.DeviceCode, nil
}

// CompleteDeviceAuth polls once (or finishes) a pending RFC 8628 device flow,
// enforcing active flow binding, expiry, and cancellation epoch currency.
func (m *AuthManager) CompleteDeviceAuth(ctx context.Context, providerID ProviderID, deviceCode string) (AccountProfile, bool, error) {
	if m.deviceFlow == nil {
		return AccountProfile{}, false, ErrNotSupported
	}
	m.mu.Lock()
	cfg, ok := m.providers[providerID]
	if !ok {
		m.mu.Unlock()
		return AccountProfile{}, false, fmt.Errorf("%w: %s", ErrProviderNotFound, providerID)
	}
	if m.pendingOAuth == nil || m.pendingOAuth.Method != MethodDeviceCode || m.pendingOAuth.ProviderID != providerID {
		m.mu.Unlock()
		return AccountProfile{}, false, ErrFlowCancelled
	}
	if !ConstantTimeTokenEqual(m.pendingDeviceCode, deviceCode) {
		m.mu.Unlock()
		return AccountProfile{}, false, ErrInvalidState
	}
	if m.now().After(m.pendingOAuth.ExpiresAt) {
		m.pendingOAuth = nil
		m.pendingDeviceCode = ""
		m.phase = PhaseError
		m.lastError = ErrAttemptExpired.Error()
		m.mu.Unlock()
		m.emit()
		return AccountProfile{}, false, ErrAttemptExpired
	}
	startFlowEpoch := m.flowEpoch
	m.mu.Unlock()

	tokens, profile, ready, err := m.deviceFlow.PollDeviceToken(ctx, cfg, deviceCode)
	if err != nil {
		m.setError(err)
		return AccountProfile{}, false, err
	}
	if !ready {
		return AccountProfile{}, false, nil
	}
	acc, err := m.admitSignedInAccountIfCurrent(ctx, cfg, tokens, profile, startFlowEpoch)
	if err != nil {
		return AccountProfile{}, false, err
	}
	m.mu.Lock()
	if m.flowEpoch == startFlowEpoch {
		m.pendingDeviceCode = ""
	}
	m.mu.Unlock()
	return acc, true, nil
}

// RequestEmailLogin validates the email address, enforces resend cooldown,
// and triggers a passwordless Email OTP or Magic Link challenge.
func (m *AuthManager) RequestEmailLogin(ctx context.Context, rawEmail string, method AuthMethod) (PendingEmailChallenge, error) {
	if m.email == nil {
		return PendingEmailChallenge{}, ErrNotSupported
	}
	if method != MethodEmailOTP && method != MethodEmailMagicLink {
		method = MethodEmailOTP
	}
	email, err := NormalizeAndValidateEmail(rawEmail)
	if err != nil {
		m.setError(err)
		return PendingEmailChallenge{}, err
	}

	now := m.now()
	m.mu.RLock()
	prev := m.pendingEmail
	m.mu.RUnlock()
	if prev != nil && prev.Email == email && now.Before(prev.CooldownUntil) {
		return PendingEmailChallenge{}, ErrCooldownActive
	}

	resp, err := m.email.RequestChallenge(ctx, email, method)
	if err != nil {
		m.setError(err)
		return PendingEmailChallenge{}, err
	}
	ttl := resp.ExpiresIn
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	cooldown := resp.Cooldown
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	maxAttempts := resp.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	ch := PendingEmailChallenge{
		ChallengeID:   resp.ChallengeID,
		Email:         email,
		Method:        method,
		ExpiresAt:     now.Add(ttl),
		CooldownUntil: now.Add(cooldown),
		Attempts:      0,
		MaxAttempts:   maxAttempts,
	}
	m.mu.Lock()
	m.pendingEmail = &ch
	m.pendingOAuth = nil
	m.phase = PhaseAwaitingCode
	m.lastError = ""
	m.mu.Unlock()
	m.emit()
	return ch, nil
}

// VerifyEmailLogin submits the user's OTP code or Magic Link token to complete
// passwordless Email sign-in, enforcing expiry and max-attempt lockouts.
func (m *AuthManager) VerifyEmailLogin(ctx context.Context, codeOrToken string) (AccountProfile, error) {
	if m.email == nil {
		return AccountProfile{}, ErrNotSupported
	}
	code := strings.TrimSpace(codeOrToken)
	if code == "" {
		m.setError(ErrEmptyCode)
		return AccountProfile{}, ErrEmptyCode
	}
	now := m.now()
	m.mu.Lock()
	ch := m.pendingEmail
	if ch == nil {
		m.mu.Unlock()
		return AccountProfile{}, ErrAttemptExpired
	}
	if now.After(ch.ExpiresAt) {
		m.pendingEmail = nil
		m.phase = PhaseError
		m.lastError = ErrAttemptExpired.Error()
		m.mu.Unlock()
		m.emit()
		return AccountProfile{}, ErrAttemptExpired
	}
	if ch.Attempts >= ch.MaxAttempts {
		m.pendingEmail = nil
		err := errors.New("auth: maximum email verification attempts exceeded")
		m.phase = PhaseError
		m.lastError = err.Error()
		m.mu.Unlock()
		m.emit()
		return AccountProfile{}, err
	}
	ch.Attempts++
	challengeCopy := *ch
	cfg, hasCfg := m.providers[ProviderEmail]
	if !hasCfg {
		cfg = EmailOTPProvider()
	}
	cfg.Method = challengeCopy.Method
	startFlowEpoch := m.flowEpoch
	m.phase = PhaseExchanging
	m.lastError = ""
	m.mu.Unlock()
	m.emit()

	tokens, profile, err := m.email.VerifyChallenge(ctx, challengeCopy.ChallengeID, challengeCopy.Email, code, challengeCopy.Method)
	if err != nil {
		m.mu.Lock()
		if m.flowEpoch == startFlowEpoch {
			if m.pendingEmail != nil && m.pendingEmail.Attempts >= m.pendingEmail.MaxAttempts {
				m.pendingEmail = nil
				m.phase = PhaseError
			} else {
				m.phase = PhaseAwaitingCode
			}
			m.lastError = err.Error()
		}
		m.mu.Unlock()
		m.emit()
		return AccountProfile{}, err
	}
	if profile.Email == "" {
		profile.Email = challengeCopy.Email
	}
	profile.EmailVerified = true
	return m.admitSignedInAccountIfCurrent(ctx, cfg, tokens, profile, startFlowEpoch)
}
