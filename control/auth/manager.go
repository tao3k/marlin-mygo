// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ManagerConfig configures an AuthManager instance.
type ManagerConfig struct {
	Vault        CredentialVault   // defaults to NewMemoryVault()
	Exchanger    TokenExchanger    // defaults to HTTPOAuthExchanger
	DeviceFlow   DeviceFlowBackend // optional RFC 8628 backend
	EmailBackend EmailAuthBackend  // optional passwordless Email OTP / Magic Link backend
	OpenBrowser  BrowserOpener     // optional OS browser launcher for OAuth PKCE
	AttemptTTL   time.Duration     // defaults to 5m
	RefreshSkew  time.Duration     // defaults to 60s
	Now          func() time.Time  // injectable clock for deterministic security tests
}

type refreshFlight struct {
	done chan struct{}
	tok  TokenSet
	err  error
}

// AuthManager coordinates multi-provider, multi-account authentication,
// OAuth 2.1 PKCE S256 state machines, Email OTP/Magic Link challenges,
// singleflight token refresh, and redacted 120Hz UI snapshots.
type AuthManager struct {
	vault       CredentialVault
	exchanger   TokenExchanger
	deviceFlow  DeviceFlowBackend
	email       EmailAuthBackend
	openBrowser BrowserOpener
	attemptTTL  time.Duration
	refreshSkew time.Duration
	now         func() time.Time

	mu                sync.RWMutex
	providerOrder     []ProviderID
	providers         map[ProviderID]ProviderConfig
	accountOrder      []AccountID
	accounts          map[AccountID]AccountProfile
	activeAccountID   AccountID
	phase             AuthPhase
	lastError         string
	oauthAttempts     map[string]*oauthAttempt
	pendingOAuth      *PendingOAuthFlow
	pendingDeviceCode string
	pendingEmail      *PendingEmailChallenge
	flights           map[AccountID]*refreshFlight
	flowEpoch         uint64
	accountEpoch      map[AccountID]uint64
	nextSub           uint64
	subs              map[uint64]func(AuthSnapshot)
}

// NewManager creates an AuthManager with safe security defaults.
func NewManager(cfg ManagerConfig) *AuthManager {
	if cfg.Vault == nil {
		cfg.Vault = NewMemoryVault()
	}
	if cfg.Exchanger == nil {
		cfg.Exchanger = NewHTTPOAuthExchanger(nil)
	}
	if cfg.AttemptTTL <= 0 {
		cfg.AttemptTTL = 5 * time.Minute
	}
	if cfg.RefreshSkew <= 0 {
		cfg.RefreshSkew = 60 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &AuthManager{
		vault:         cfg.Vault,
		exchanger:     cfg.Exchanger,
		deviceFlow:    cfg.DeviceFlow,
		email:         cfg.EmailBackend,
		openBrowser:   cfg.OpenBrowser,
		attemptTTL:    cfg.AttemptTTL,
		refreshSkew:   cfg.RefreshSkew,
		now:           cfg.Now,
		providers:     make(map[ProviderID]ProviderConfig),
		accounts:      make(map[AccountID]AccountProfile),
		oauthAttempts: make(map[string]*oauthAttempt),
		flights:       make(map[AccountID]*refreshFlight),
		accountEpoch:  make(map[AccountID]uint64),
		phase:         PhaseSignedOut,
		subs:          make(map[uint64]func(AuthSnapshot)),
	}
}

// RegisterProvider validates and registers one auth provider (e.g. Google, GitHub, Email, OIDC).
func (m *AuthManager) RegisterProvider(cfg ProviderConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	if _, exists := m.providers[cfg.ID]; !exists {
		m.providerOrder = append(m.providerOrder, cfg.ID)
	}
	m.providers[cfg.ID] = cfg
	m.mu.Unlock()
	m.emit()
	return nil
}

// BeginOAuth initiates an OAuth 2.1 PKCE S256 authorization flow, generating
// cryptographic state, nonce, and S256 code_verifier, and returning the
// PendingOAuthFlow whose AuthorizeURL should be opened in the system browser.
func (m *AuthManager) BeginOAuth(providerID ProviderID, redirectURI string) (PendingOAuthFlow, error) {
	m.mu.Lock()
	cfg, ok := m.providers[providerID]
	m.mu.Unlock()
	if !ok {
		return PendingOAuthFlow{}, fmt.Errorf("%w: %s", ErrProviderNotFound, providerID)
	}
	if cfg.Method != MethodOAuthPKCE {
		return PendingOAuthFlow{}, fmt.Errorf("%w: provider %s uses %s", ErrNotSupported, providerID, cfg.Method)
	}
	if strings.TrimSpace(redirectURI) == "" {
		redirectURI = cfg.RedirectURI
	}
	pkce, err := GeneratePKCE()
	if err != nil {
		return PendingOAuthFlow{}, err
	}
	state, err := GenerateRandomToken(32)
	if err != nil {
		return PendingOAuthFlow{}, err
	}
	nonce, err := GenerateRandomToken(24)
	if err != nil {
		return PendingOAuthFlow{}, err
	}
	attemptID, err := GenerateRandomToken(16)
	if err != nil {
		return PendingOAuthFlow{}, err
	}
	authURL, err := BuildAuthorizeURL(cfg, redirectURI, state, nonce, pkce)
	if err != nil {
		return PendingOAuthFlow{}, err
	}

	expiresAt := m.now().Add(m.attemptTTL)
	att := &oauthAttempt{
		id:           attemptID,
		providerID:   providerID,
		redirectURI:  redirectURI,
		state:        state,
		nonce:        nonce,
		codeVerifier: pkce.Verifier,
		expiresAt:    expiresAt,
	}
	flow := PendingOAuthFlow{
		AttemptID:    attemptID,
		ProviderID:   providerID,
		ProviderName: cfg.DisplayName,
		Method:       cfg.Method,
		AuthorizeURL: authURL,
		RedirectURI:  redirectURI,
		ExpiresAt:    expiresAt,
	}

	m.mu.Lock()
	m.pruneExpiredAttemptsLocked(m.now())
	m.oauthAttempts[attemptID] = att
	m.pendingOAuth = &flow
	m.phase = PhaseAuthorizing
	m.lastError = ""
	m.mu.Unlock()
	m.emit()
	return flow, nil
}

// CompleteOAuthCallback verifies CSRF state (constant-time), enforces single-use
// and TTL expiry, exchanges the authorization code with the PKCE code_verifier,
// stores tokens inside the Tier-0 CredentialVault, and activates the account.
func (m *AuthManager) CompleteOAuthCallback(
	ctx context.Context,
	attemptID string,
	cb LoopbackCallback,
) (AccountProfile, error) {
	m.mu.Lock()
	att, ok := m.oauthAttempts[attemptID]
	if !ok {
		m.phase = PhaseError
		m.lastError = ErrInvalidState.Error()
		m.mu.Unlock()
		m.emit()
		return AccountProfile{}, ErrInvalidState
	}
	if err := att.validate(m.now(), cb.State, cb.Code, cb.Error, cb.ErrorDescription); err != nil {
		att.consumed = true
		delete(m.oauthAttempts, attemptID)
		m.pendingOAuth = nil
		m.phase = PhaseError
		m.lastError = err.Error()
		m.mu.Unlock()
		m.emit()
		return AccountProfile{}, err
	}
	// Single-use: mark consumed immediately before any network I/O!
	att.consumed = true
	delete(m.oauthAttempts, attemptID)
	cfg := m.providers[att.providerID]
	startFlowEpoch := m.flowEpoch
	m.phase = PhaseExchanging
	m.lastError = ""
	m.mu.Unlock()
	m.emit()

	tokens, profile, err := m.exchanger.ExchangeCode(ctx, cfg, cb.Code, att.codeVerifier, att.redirectURI, att.nonce)
	if err != nil {
		m.setError(err)
		return AccountProfile{}, err
	}
	return m.admitSignedInAccountIfCurrent(ctx, cfg, tokens, profile, startFlowEpoch)
}

// CancelPending aborts any in-flight OAuth or Email login challenge cleanly.
func (m *AuthManager) CancelPending() {
	m.mu.Lock()
	m.flowEpoch++
	for id, att := range m.oauthAttempts {
		att.consumed = true
		delete(m.oauthAttempts, id)
	}
	m.pendingOAuth = nil
	m.pendingDeviceCode = ""
	m.pendingEmail = nil
	m.lastError = ""
	if m.activeAccountID != "" {
		m.phase = PhaseSignedIn
	} else {
		m.phase = PhaseSignedOut
	}
	m.mu.Unlock()
	m.emit()
}

// ValidAccessToken returns a live non-expired Bearer access token for accountID
// (or the active account when accountID == ""), using singleflight coalescing so
// concurrent gRPC streams trigger at most ONE refresh call on expiry.
func (m *AuthManager) ValidAccessToken(ctx context.Context, accountID AccountID) (string, error) {
	m.mu.RLock()
	if accountID == "" {
		accountID = m.activeAccountID
	}
	acc, ok := m.accounts[accountID]
	cfg := m.providers[acc.ProviderID]
	m.mu.RUnlock()
	if !ok {
		return "", ErrAccountNotFound
	}

	tok, found, err := m.vault.Load(ctx, accountID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", ErrAccountNotFound
	}
	if !tok.IsExpired(m.now(), m.refreshSkew) {
		return tok.AccessToken, nil
	}
	if tok.RefreshToken == "" {
		return "", ErrRefreshUnavailable
	}

	// Singleflight refresh per accountID to prevent concurrent refresh-token rotation storms.
	m.mu.Lock()
	if fl, inFlight := m.flights[accountID]; inFlight {
		m.mu.Unlock()
		select {
		case <-fl.done:
			return fl.tok.AccessToken, fl.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if _, stillSignedIn := m.accounts[accountID]; !stillSignedIn {
		m.mu.Unlock()
		return "", ErrAccountNotFound
	}
	startEpoch := m.accountEpoch[accountID]
	grantedScopes := append([]string(nil), m.accounts[accountID].Scopes...)
	fl := &refreshFlight{done: make(chan struct{})}
	m.flights[accountID] = fl
	m.phase = PhaseRefreshing
	m.mu.Unlock()
	m.emit()

	refreshed, rErr := m.exchanger.RefreshToken(ctx, cfg, tok.RefreshToken)
	if rErr == nil {
		if refreshed.RefreshToken == "" {
			refreshed.RefreshToken = tok.RefreshToken
		}
		if len(refreshed.Scopes) == 0 {
			refreshed.Scopes = tok.Scopes
		}
		if len(grantedScopes) > 0 && len(refreshed.Scopes) > 0 && !scopesSubset(refreshed.Scopes, grantedScopes) {
			rErr = ErrScopeEscalation
			refreshed = TokenSet{}
			m.mu.Lock()
			m.phase = PhaseError
			m.lastError = rErr.Error()
			m.mu.Unlock()
		} else {
			// Guard against zombie token resurrection if SignOut ran while refresh was in-flight.
			m.mu.Lock()
			_, stillSignedIn := m.accounts[accountID]
			epochCurrent := m.accountEpoch[accountID] == startEpoch
			if !stillSignedIn || !epochCurrent {
				m.mu.Unlock()
				if m.exchanger != nil && cfg.RevokeURL != "" && refreshed.RefreshToken != "" {
					_ = m.exchanger.RevokeToken(ctx, cfg, refreshed.RefreshToken)
				}
				rErr = ErrSessionRevoked
				refreshed = TokenSet{}
			} else {
				m.mu.Unlock()
				if storeErr := m.vault.Store(ctx, accountID, refreshed); storeErr != nil {
					rErr = storeErr
				} else {
					m.mu.Lock()
					if cur, exists := m.accounts[accountID]; exists && m.accountEpoch[accountID] == startEpoch {
						cur.ExpiresAt = refreshed.ExpiresAt
						cur.HasRefresh = refreshed.RefreshToken != ""
						if len(refreshed.Scopes) > 0 {
							cur.Scopes = append([]string(nil), refreshed.Scopes...)
						}
						m.accounts[accountID] = cur
						m.phase = PhaseSignedIn
						m.lastError = ""
						m.mu.Unlock()
					} else {
						m.mu.Unlock()
						_ = m.vault.Delete(ctx, accountID)
						rErr = ErrSessionRevoked
						refreshed = TokenSet{}
					}
				}
			}
		}
	} else {
		m.mu.Lock()
		m.phase = PhaseError
		m.lastError = rErr.Error()
		m.mu.Unlock()
	}

	fl.tok = refreshed
	fl.err = rErr
	close(fl.done)

	m.mu.Lock()
	delete(m.flights, accountID)
	m.mu.Unlock()
	m.emit()
	return refreshed.AccessToken, rErr
}

// AuthorizationHeader returns `"Bearer <token>"` for the active account, suitable
// for attaching to remote Rust App Server gRPC calls.
func (m *AuthManager) AuthorizationHeader(ctx context.Context) (string, error) {
	tok, err := m.ValidAccessToken(ctx, "")
	if err != nil {
		return "", err
	}
	return "Bearer " + tok, nil
}

// SwitchAccount changes the active account among signed-in profiles.
func (m *AuthManager) SwitchAccount(accountID AccountID) error {
	m.mu.Lock()
	if _, ok := m.accounts[accountID]; !ok {
		m.mu.Unlock()
		return ErrAccountNotFound
	}
	m.activeAccountID = accountID
	m.phase = PhaseSignedIn
	m.lastError = ""
	m.mu.Unlock()
	if pv, ok := m.vault.(ProfileVault); ok {
		_ = pv.SetActiveAccount(context.Background(), accountID)
	}
	m.emit()
	return nil
}

// RestoreFromVault rehydrates persisted signed-in AccountProfiles from an
// OS-Keychain-protected ProfileVault on application startup. Only accounts
// that still have valid vaulted credentials are restored.
func (m *AuthManager) RestoreFromVault(ctx context.Context) (int, error) {
	pv, ok := m.vault.(ProfileVault)
	if !ok {
		return 0, nil
	}
	profiles, savedActive, err := pv.LoadProfiles(ctx)
	if err != nil {
		return 0, err
	}
	restored := make([]AccountProfile, 0, len(profiles))
	for _, prof := range profiles {
		if prof.ID == "" {
			continue
		}
		tok, found, loadErr := m.vault.Load(ctx, prof.ID)
		if loadErr != nil || !found || strings.TrimSpace(tok.AccessToken) == "" {
			continue
		}
		prof.ExpiresAt = tok.ExpiresAt
		prof.HasRefresh = tok.RefreshToken != ""
		restored = append(restored, prof)
	}
	if len(restored) == 0 {
		return 0, nil
	}

	m.mu.Lock()
	for _, prof := range restored {
		if _, exists := m.accounts[prof.ID]; !exists {
			m.accountOrder = append(m.accountOrder, prof.ID)
		}
		m.accountEpoch[prof.ID]++
		m.accounts[prof.ID] = prof
	}
	if savedActive != "" {
		if _, exists := m.accounts[savedActive]; exists {
			m.activeAccountID = savedActive
		}
	}
	if m.activeAccountID == "" && len(m.accountOrder) > 0 {
		m.activeAccountID = m.accountOrder[0]
	}
	if m.activeAccountID != "" {
		m.phase = PhaseSignedIn
	}
	m.mu.Unlock()
	m.emit()
	return len(restored), nil
}

// SignOut revokes the account's refresh/access token if supported, scrubs its
// credentials from the Tier-0 CredentialVault, and switches to another signed-in
// account or PhaseSignedOut.
func (m *AuthManager) SignOut(ctx context.Context, accountID AccountID) error {
	m.mu.Lock()
	if accountID == "" {
		accountID = m.activeAccountID
	}
	acc, ok := m.accounts[accountID]
	cfg := m.providers[acc.ProviderID]
	if ok {
		// Advance account epoch immediately so any in-flight refresh cannot resurrect the vault entry.
		m.accountEpoch[accountID]++
		delete(m.accounts, accountID)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}

	if tok, found, err := m.vault.Load(ctx, accountID); err == nil && found && m.exchanger != nil && cfg.RevokeURL != "" {
		revokeTarget := tok.RefreshToken
		if revokeTarget == "" {
			revokeTarget = tok.AccessToken
		}
		if revokeTarget != "" {
			_ = m.exchanger.RevokeToken(ctx, cfg, revokeTarget)
		}
	}
	vaultErr := m.vault.Delete(ctx, accountID)

	m.mu.Lock()
	delete(m.accounts, accountID)
	nextOrder := make([]AccountID, 0, len(m.accountOrder))
	for _, id := range m.accountOrder {
		if id != accountID {
			nextOrder = append(nextOrder, id)
		}
	}
	m.accountOrder = nextOrder
	if m.activeAccountID == accountID {
		if len(m.accountOrder) > 0 {
			m.activeAccountID = m.accountOrder[0]
			m.phase = PhaseSignedIn
		} else {
			m.activeAccountID = ""
			m.phase = PhaseSignedOut
		}
	}
	m.lastError = ""
	m.mu.Unlock()
	m.emit()
	return vaultErr
}

// SignOutAll revokes and scrubs every signed-in account.
func (m *AuthManager) SignOutAll(ctx context.Context) error {
	m.mu.RLock()
	ids := append([]AccountID(nil), m.accountOrder...)
	m.mu.RUnlock()
	var firstErr error
	for _, id := range ids {
		if err := m.SignOut(ctx, id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Snapshot returns an immutable, secret-free AuthSnapshot safe for 120Hz UI reads.
func (m *AuthManager) Snapshot() AuthSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	accounts := make([]AccountProfile, 0, len(m.accountOrder))
	for _, id := range m.accountOrder {
		if acc, ok := m.accounts[id]; ok {
			cp := acc
			if len(acc.Scopes) > 0 {
				cp.Scopes = append([]string(nil), acc.Scopes...)
			}
			accounts = append(accounts, cp)
		}
	}
	providers := make([]ProviderSummary, 0, len(m.providerOrder))
	for _, id := range m.providerOrder {
		if p, ok := m.providers[id]; ok {
			providers = append(providers, ProviderSummary{
				ID:          p.ID,
				DisplayName: p.DisplayName,
				Method:      p.Method,
				Scopes:      append([]string(nil), p.Scopes...),
			})
		}
	}
	var po *PendingOAuthFlow
	if m.pendingOAuth != nil {
		cp := *m.pendingOAuth
		po = &cp
	}
	var pe *PendingEmailChallenge
	if m.pendingEmail != nil {
		cp := *m.pendingEmail
		pe = &cp
	}
	return AuthSnapshot{
		Phase:           m.phase,
		ActiveAccountID: m.activeAccountID,
		Accounts:        accounts,
		Providers:       providers,
		PendingOAuth:    po,
		PendingEmail:    pe,
		LastError:       m.lastError,
	}
}

// Subscribe registers a listener notified on every auth state change.
func (m *AuthManager) Subscribe(fn func(AuthSnapshot)) func() {
	if fn == nil {
		return func() {}
	}
	m.mu.Lock()
	m.nextSub++
	id := m.nextSub
	m.subs[id] = fn
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.subs, id)
		m.mu.Unlock()
	}
}

func (m *AuthManager) admitSignedInAccountIfCurrent(
	ctx context.Context,
	cfg ProviderConfig,
	tokens TokenSet,
	profile AccountProfile,
	expectedFlowEpoch uint64,
) (AccountProfile, error) {
	m.mu.RLock()
	current := m.flowEpoch == expectedFlowEpoch
	m.mu.RUnlock()
	if !current {
		if m.exchanger != nil && cfg.RevokeURL != "" && tokens.RefreshToken != "" {
			_ = m.exchanger.RevokeToken(ctx, cfg, tokens.RefreshToken)
		}
		return AccountProfile{}, ErrFlowCancelled
	}
	return m.admitSignedInAccount(ctx, cfg, tokens, profile)
}

func scopesSubset(candidate, allowed []string) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, s := range allowed {
		set[s] = struct{}{}
	}
	for _, s := range candidate {
		if _, ok := set[s]; !ok {
			return false
		}
	}
	return true
}

func (m *AuthManager) admitSignedInAccount(
	ctx context.Context,
	cfg ProviderConfig,
	tokens TokenSet,
	profile AccountProfile,
) (AccountProfile, error) {
	if strings.TrimSpace(tokens.AccessToken) == "" {
		err := errors.New("auth: token exchange returned empty access token")
		m.setError(err)
		return AccountProfile{}, err
	}
	if tokens.TokenType == "" {
		tokens.TokenType = "Bearer"
	}
	if tokens.IssuedAt.IsZero() {
		tokens.IssuedAt = m.now()
	}
	profile.ProviderID = cfg.ID
	if profile.ProviderName == "" {
		profile.ProviderName = cfg.DisplayName
	}
	profile.Method = cfg.Method
	if profile.ID == "" {
		ident := profile.Subject
		if ident == "" {
			ident = profile.Email
		}
		if ident == "" {
			ident = fmt.Sprintf("user-%d", m.now().UnixNano())
		}
		profile.ID = AccountID(fmt.Sprintf("%s:%s", cfg.ID, ident))
	}
	if profile.DisplayName == "" {
		if profile.Email != "" {
			profile.DisplayName = profile.Email
		} else {
			profile.DisplayName = string(profile.ID)
		}
	}
	if len(profile.Scopes) == 0 {
		if len(tokens.Scopes) > 0 {
			profile.Scopes = append([]string(nil), tokens.Scopes...)
		} else {
			profile.Scopes = append([]string(nil), cfg.Scopes...)
		}
	}
	profile.SignedInAt = m.now()
	profile.ExpiresAt = tokens.ExpiresAt
	profile.HasRefresh = tokens.RefreshToken != ""

	if err := m.vault.Store(ctx, profile.ID, tokens); err != nil {
		m.setError(err)
		return AccountProfile{}, err
	}
	if pv, ok := m.vault.(ProfileVault); ok {
		_ = pv.StoreProfile(ctx, profile)
	}

	m.mu.Lock()
	if _, exists := m.accounts[profile.ID]; !exists {
		m.accountOrder = append(m.accountOrder, profile.ID)
	}
	m.accountEpoch[profile.ID]++
	m.accounts[profile.ID] = profile
	m.activeAccountID = profile.ID
	m.pendingOAuth = nil
	m.pendingDeviceCode = ""
	m.pendingEmail = nil
	m.phase = PhaseSignedIn
	m.lastError = ""
	m.mu.Unlock()
	m.emit()
	return profile, nil
}

func (m *AuthManager) accountStanding(accountID AccountID) (AccountProfile, uint64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if accountID == "" {
		accountID = m.activeAccountID
	}
	acc, ok := m.accounts[accountID]
	if !ok {
		return AccountProfile{}, 0, false
	}
	cp := acc
	if len(acc.Scopes) > 0 {
		cp.Scopes = append([]string(nil), acc.Scopes...)
	}
	return cp, m.accountEpoch[accountID], true
}

func (m *AuthManager) isAccountEpochCurrent(accountID AccountID, epoch uint64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.accounts[accountID]
	return ok && m.accountEpoch[accountID] == epoch
}

func (m *AuthManager) isVaultLocked() bool {
	if lv, ok := m.vault.(LockableVault); ok {
		return lv.IsLocked()
	}
	return false
}

func (m *AuthManager) setError(err error) {
	m.mu.Lock()
	m.phase = PhaseError
	if err != nil {
		m.lastError = err.Error()
	}
	m.mu.Unlock()
	m.emit()
}

func (m *AuthManager) pruneExpiredAttemptsLocked(now time.Time) {
	for id, att := range m.oauthAttempts {
		if att.consumed || now.After(att.expiresAt) {
			delete(m.oauthAttempts, id)
		}
	}
}

func (m *AuthManager) emit() {
	snap := m.Snapshot()
	m.mu.RLock()
	listeners := make([]func(AuthSnapshot), 0, len(m.subs))
	for _, fn := range m.subs {
		listeners = append(listeners, fn)
	}
	m.mu.RUnlock()
	for _, fn := range listeners {
		fn(snap)
	}
}
