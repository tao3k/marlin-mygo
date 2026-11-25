// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth

import (
	"context"
	"fmt"
	"sync"

	"github.com/cedar-policy/cedar-go"
)

// AuthzTicket binds a Cedar PDP decision to the exact accountEpoch and
// policyEpoch observed at evaluation time (Zero Trust PEP/PDP separation).
type AuthzTicket struct {
	Allowed      bool
	AccountID    AccountID
	Action       string
	Resource     string
	AccountEpoch uint64
	PolicyEpoch  uint64
	Reasons      []string
	Errors       []string
}

// Authorizer is Marlin's Tier-0 local Cedar PDP + Zero Trust PEP bridge.
// It consumes authenticated, secret-free AccountProfile state directly from
// AuthManager so callers can never forge identity, scopes, or epoch context.
type Authorizer struct {
	mgr         *AuthManager
	mu          sync.RWMutex
	policies    *cedar.PolicySet
	policyEpoch uint64
}

// NewAuthorizer creates a fail-closed Cedar Authorizer backed by mgr.
// Until a PolicySet is loaded via LoadPolicyBytes, every request is denied.
func NewAuthorizer(mgr *AuthManager) *Authorizer {
	return &Authorizer{
		mgr:      mgr,
		policies: cedar.NewPolicySet(),
	}
}

// PolicyEpoch returns the current monotonic policy version.
func (a *Authorizer) PolicyEpoch() uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.policyEpoch
}

// LoadPolicyBytes compiles a Cedar policy document and atomically installs it,
// advancing policyEpoch so any previously issued AuthzTicket becomes stale.
func (a *Authorizer) LoadPolicyBytes(name string, document []byte) (uint64, error) {
	ps, err := cedar.NewPolicySetFromBytes(name, document)
	if err != nil {
		return 0, fmt.Errorf("auth: compile Cedar policy %q: %w", name, err)
	}
	a.mu.Lock()
	a.policies = ps
	a.policyEpoch++
	epoch := a.policyEpoch
	a.mu.Unlock()
	return epoch, nil
}

// Evaluate queries the local Cedar PDP for (accountID, action, resource) using
// tamper-proof context derived directly from AuthManager (zero raw tokens).
func (a *Authorizer) Evaluate(
	accountID AccountID,
	action string,
	resource string,
	extraContext map[cedar.String]cedar.Value,
) (AuthzTicket, error) {
	if a.mgr == nil {
		return AuthzTicket{}, ErrAccountNotFound
	}
	if a.mgr.isVaultLocked() {
		return AuthzTicket{}, ErrVaultLocked
	}
	profile, accEpoch, ok := a.mgr.accountStanding(accountID)
	if !ok {
		return AuthzTicket{}, ErrAccountNotFound
	}

	a.mu.RLock()
	ps := a.policies
	polEpoch := a.policyEpoch
	a.mu.RUnlock()

	scopeVals := make([]cedar.Value, 0, len(profile.Scopes))
	for _, s := range profile.Scopes {
		scopeVals = append(scopeVals, cedar.String(s))
	}

	ctxMap := cedar.RecordMap{
		"provider":       cedar.String(profile.ProviderID),
		"method":         cedar.String(profile.Method),
		"email_verified": cedar.Boolean(profile.EmailVerified),
		"scopes":         cedar.NewSet(scopeVals...),
		"account_epoch":  cedar.Long(accEpoch),
		"policy_epoch":   cedar.Long(polEpoch),
	}
	for k, v := range extraContext {
		// Prevent callers from overwriting host-owned security attributes.
		switch k {
		case "provider", "method", "email_verified", "scopes", "account_epoch", "policy_epoch":
			continue
		default:
			ctxMap[k] = v
		}
	}

	principalUID := cedar.NewEntityUID("Marlin::Account", cedar.String(profile.ID))
	actionUID := cedar.NewEntityUID("Marlin::Action", cedar.String(action))
	resourceUID := cedar.NewEntityUID("Marlin::Resource", cedar.String(resource))

	entities := cedar.EntityMap{
		principalUID: cedar.Entity{
			UID: principalUID,
			Attributes: cedar.NewRecord(cedar.RecordMap{
				"provider":       cedar.String(profile.ProviderID),
				"email_verified": cedar.Boolean(profile.EmailVerified),
			}),
		},
	}
	req := cedar.Request{
		Principal: principalUID,
		Action:    actionUID,
		Resource:  resourceUID,
		Context:   cedar.NewRecord(ctxMap),
	}

	decision, diag := cedar.Authorize(ps, entities, req)
	reasons := make([]string, 0, len(diag.Reasons))
	for _, r := range diag.Reasons {
		reasons = append(reasons, string(r.PolicyID))
	}
	diagErrs := make([]string, 0, len(diag.Errors))
	for _, e := range diag.Errors {
		diagErrs = append(diagErrs, fmt.Sprintf("%s: %s", e.PolicyID, e.Message))
	}

	return AuthzTicket{
		Allowed:      decision == cedar.Allow && len(diag.Errors) == 0,
		AccountID:    profile.ID,
		Action:       action,
		Resource:     resource,
		AccountEpoch: accEpoch,
		PolicyEpoch:  polEpoch,
		Reasons:      reasons,
		Errors:       diagErrs,
	}, nil
}

// Enforce performs the Zero Trust PEP final-use gate before executing an effect:
// verifies the ticket was allowed AND neither policyEpoch nor accountEpoch drifted.
func (a *Authorizer) Enforce(ticket AuthzTicket) error {
	if !ticket.Allowed {
		return ErrPolicyDenied
	}
	if a.mgr != nil && a.mgr.isVaultLocked() {
		return ErrVaultLocked
	}
	a.mu.RLock()
	curPolEpoch := a.policyEpoch
	a.mu.RUnlock()
	if curPolEpoch != ticket.PolicyEpoch {
		return ErrStalePolicyDecision
	}
	if a.mgr == nil || !a.mgr.isAccountEpochCurrent(ticket.AccountID, ticket.AccountEpoch) {
		return ErrSessionRevoked
	}
	return nil
}

// Authorize evaluates the Cedar PDP and immediately enforces the PEP gate.
func (a *Authorizer) Authorize(
	accountID AccountID,
	action string,
	resource string,
	extraContext map[cedar.String]cedar.Value,
) (AuthzTicket, error) {
	ticket, err := a.Evaluate(accountID, action, resource, extraContext)
	if err != nil {
		return ticket, err
	}
	if err := a.Enforce(ticket); err != nil {
		return ticket, err
	}
	return ticket, nil
}

// AuthorizedBearerHeader re-enforces ticket at final-use time (verifying vault
// unlock, policyEpoch, and accountEpoch) and returns a Bearer header strictly
// bound to ticket.AccountID — preventing cross-account token confusion when
// SwitchAccount runs concurrently.
func (a *Authorizer) AuthorizedBearerHeader(ctx context.Context, ticket AuthzTicket) (string, error) {
	if err := a.Enforce(ticket); err != nil {
		return "", err
	}
	tok, err := a.mgr.ValidAccessToken(ctx, ticket.AccountID)
	if err != nil {
		return "", err
	}
	// Re-check PEP standing after token fetch/refresh before releasing header.
	if err := a.Enforce(ticket); err != nil {
		return "", err
	}
	return "Bearer " + tok, nil
}
