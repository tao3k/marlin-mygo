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

// AuthzTicket binds a Cedar PDP decision to the exact accountEpoch, policyEpoch,
// tenant boundary, and optional RFC 8693 OBO delegated agent provenance observed
// at evaluation time (Zero Trust PEP/PDP separation).
type AuthzTicket struct {
	Allowed          bool
	AccountID        AccountID
	TenantID         string
	ResourceTenantID string
	Action           string
	Resource         string
	AccountEpoch     uint64
	PolicyEpoch      uint64
	TenantEpoch      uint64
	DelegatedAgentID string
	DelegatedScopes  []string
	Reasons          []string
	Errors           []string
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
		case "provider", "method", "email_verified", "scopes", "account_epoch", "policy_epoch",
			"tenant_id", "resource_tenant_id", "delegated", "actor_agent":
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

// EvaluateForTenant evaluates a tenant-scoped Cedar PDP decision, failing
// closed on unbound/foreign tenants or revoked tenant membership before
// emitting an AuthzTicket pinned to (TenantID, ResourceTenantID, TenantEpoch).
func (a *Authorizer) EvaluateForTenant(
	accountID AccountID,
	resourceTenantID string,
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
	if profile.TenantID == "" || resourceTenantID == "" {
		return AuthzTicket{}, ErrCrossTenantAccess
	}
	if profile.TenantID != resourceTenantID {
		return AuthzTicket{}, ErrCrossTenantAccess
	}
	tenEpoch, tenOK := a.mgr.tenantStanding(profile.TenantID)
	if !tenOK {
		return AuthzTicket{}, ErrTenantRevoked
	}

	return a.evaluateCore(profile, accEpoch, profile.TenantID, resourceTenantID, tenEpoch, "", profile.Scopes, action, resource, extraContext)
}

// EvaluateDelegated evaluates an RFC 8693 On-Behalf-Of (OBO) delegated agent
// authorization request: enforces monotonic scope attenuation against the
// delegating human's active scopes, preserves actor provenance, blocks
// delegated agents from human-only gates, and enforces tenant isolation when
// resourceTenantID is non-empty.
func (a *Authorizer) EvaluateDelegated(
	delegatorID AccountID,
	agentID string,
	resourceTenantID string,
	action string,
	resource string,
	requestedScopes []string,
	extraContext map[cedar.String]cedar.Value,
) (AuthzTicket, error) {
	if a.mgr == nil {
		return AuthzTicket{}, ErrAccountNotFound
	}
	if a.mgr.isVaultLocked() {
		return AuthzTicket{}, ErrVaultLocked
	}
	if agentID == "" {
		return AuthzTicket{}, ErrPolicyDenied
	}
	if action == "approve_gate" {
		return AuthzTicket{}, ErrDelegatedHumanGateForbidden
	}
	profile, accEpoch, ok := a.mgr.accountStanding(delegatorID)
	if !ok {
		return AuthzTicket{}, ErrAccountNotFound
	}
	granted := make(map[string]struct{}, len(profile.Scopes))
	for _, sc := range profile.Scopes {
		granted[sc] = struct{}{}
	}
	for _, reqSc := range requestedScopes {
		if _, allowed := granted[reqSc]; !allowed {
			return AuthzTicket{}, ErrDelegationScopeAmplified
		}
	}
	var tenEpoch uint64
	if resourceTenantID != "" {
		if profile.TenantID == "" || profile.TenantID != resourceTenantID {
			return AuthzTicket{}, ErrCrossTenantAccess
		}
		var tenOK bool
		tenEpoch, tenOK = a.mgr.tenantStanding(profile.TenantID)
		if !tenOK {
			return AuthzTicket{}, ErrTenantRevoked
		}
	}
	return a.evaluateCore(profile, accEpoch, profile.TenantID, resourceTenantID, tenEpoch, agentID, requestedScopes, action, resource, extraContext)
}

func (a *Authorizer) evaluateCore(
	profile AccountProfile,
	accEpoch uint64,
	tenantID string,
	resourceTenantID string,
	tenEpoch uint64,
	agentID string,
	effectiveScopes []string,
	action string,
	resource string,
	extraContext map[cedar.String]cedar.Value,
) (AuthzTicket, error) {
	a.mu.RLock()
	ps := a.policies
	polEpoch := a.policyEpoch
	a.mu.RUnlock()

	scopeVals := make([]cedar.Value, 0, len(effectiveScopes))
	for _, s := range effectiveScopes {
		scopeVals = append(scopeVals, cedar.String(s))
	}

	ctxMap := cedar.RecordMap{
		"provider":           cedar.String(profile.ProviderID),
		"method":             cedar.String(profile.Method),
		"email_verified":     cedar.Boolean(profile.EmailVerified),
		"scopes":             cedar.NewSet(scopeVals...),
		"account_epoch":      cedar.Long(accEpoch),
		"policy_epoch":       cedar.Long(polEpoch),
		"tenant_id":          cedar.String(tenantID),
		"resource_tenant_id": cedar.String(resourceTenantID),
		"delegated":          cedar.Boolean(agentID != ""),
		"actor_agent":        cedar.String(agentID),
	}
	for k, v := range extraContext {
		switch k {
		case "provider", "method", "email_verified", "scopes", "account_epoch", "policy_epoch",
			"tenant_id", "resource_tenant_id", "delegated", "actor_agent":
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
				"tenant_id":      cedar.String(tenantID),
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
		Allowed:          decision == cedar.Allow && len(diag.Errors) == 0,
		AccountID:        profile.ID,
		TenantID:         tenantID,
		ResourceTenantID: resourceTenantID,
		Action:           action,
		Resource:         resource,
		AccountEpoch:     accEpoch,
		PolicyEpoch:      polEpoch,
		TenantEpoch:      tenEpoch,
		DelegatedAgentID: agentID,
		DelegatedScopes:  append([]string(nil), effectiveScopes...),
		Reasons:          reasons,
		Errors:           diagErrs,
	}, nil
}

// Enforce performs the Zero Trust PEP final-use gate before executing an effect:
// verifies the ticket was allowed AND neither policyEpoch, accountEpoch, tenant
// boundary/revocation epoch, nor delegated actor restrictions were violated.
func (a *Authorizer) Enforce(ticket AuthzTicket) error {
	if !ticket.Allowed {
		return ErrPolicyDenied
	}
	if a.mgr != nil && a.mgr.isVaultLocked() {
		return ErrVaultLocked
	}
	if ticket.DelegatedAgentID != "" && ticket.Action == "approve_gate" {
		return ErrDelegatedHumanGateForbidden
	}
	if ticket.ResourceTenantID != "" && ticket.TenantID != ticket.ResourceTenantID {
		return ErrCrossTenantAccess
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
	if ticket.TenantID != "" && ticket.TenantEpoch > 0 && !a.mgr.isTenantEpochCurrent(ticket.TenantID, ticket.TenantEpoch) {
		return ErrTenantRevoked
	}
	return nil
}

// EnforceStepUp enforces ticket PLUS one-shot RFC 9470 fresh human step-up
// verification with verified email (never satisfiable by a delegated agent).
func (a *Authorizer) EnforceStepUp(ticket AuthzTicket) error {
	if ticket.DelegatedAgentID != "" {
		return ErrDelegatedHumanGateForbidden
	}
	if err := a.Enforce(ticket); err != nil {
		return err
	}
	if a.mgr == nil {
		return ErrAccountNotFound
	}
	return a.mgr.ConsumeStepUp(ticket.AccountID)
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
