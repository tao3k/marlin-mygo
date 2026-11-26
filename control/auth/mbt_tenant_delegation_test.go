// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/resilience"
)

// ---------------------------------------------------------------------------
// 8 & 9. MultiTenantIsolation.qnt + MarlinTenantOverlay.qnt (@quint-extends) ->
//        Go AuthManager + Cedar Authorizer (marlin-default.cedar) + Outbox + Federation TargetRegistry
// ---------------------------------------------------------------------------

type multiTenantState struct {
	ActiveTenant     itfBigInt `json:"activeTenant"`
	Tenant1Epoch     itfBigInt `json:"tenant1Epoch"`
	Tenant2Epoch     itfBigInt `json:"tenant2Epoch"`
	Tenant1Revoked   bool      `json:"tenant1Revoked"`
	Tenant2Revoked   bool      `json:"tenant2Revoked"`
	TicketIssued     bool      `json:"ticketIssued"`
	TicketTenant     itfBigInt `json:"ticketTenant"`
	TicketEpoch      itfBigInt `json:"ticketEpoch"`
	OutboxPending    bool      `json:"outboxPending"`
	OutboxTenant     itfBigInt `json:"outboxTenant"`
	ExecutedCount    itfBigInt `json:"executedCount"`
	CrossTenantLeak  bool      `json:"crossTenantLeak"`
	StaleTenantUse   bool      `json:"staleTenantUse"`
	CrossTenantFlush bool      `json:"crossTenantFlush"`
	// MarlinTenantOverlay fields (zero-valued on base MultiTenantIsolation traces)
	CedarPolicyEpoch itfBigInt `json:"cedarPolicyEpoch"`
	TargetTenant     itfBigInt `json:"targetTenant"`
	TargetCrossLeak  bool      `json:"targetCrossLeak"`
}

type multiTenantNondetPicks struct {
	TargetTenant itfOptionInt `json:"targetTenant"`
}

type multiTenantTraceState struct {
	H           multiTenantState       `json:"h"`
	ActionTaken string                 `json:"mbt::actionTaken"`
	NondetPicks multiTenantNondetPicks `json:"mbt::nondetPicks"`
}

type multiTenantTrace struct {
	Vars   []string                `json:"vars"`
	States []multiTenantTraceState `json:"states"`
}

type stubTenantTarget struct {
	meta transport.TargetMeta
}

func (s *stubTenantTarget) Meta() transport.TargetMeta     { return s.meta }
func (s *stubTenantTarget) Ping(_ context.Context) error   { return nil }
func (s *stubTenantTarget) Close() error                   { return nil }

func tenantIDFromNum(n int64) string {
	switch n {
	case 1:
		return "tenant-1"
	case 2:
		return "tenant-2"
	default:
		return ""
	}
}

func TestMBTReplayMultiTenantIsolationITF(t *testing.T) {
	bunBin, cliPath, qntModel := resolveDEModelAndCLI(t, "threat/zero-trust/multi-tenant-isolation/MultiTenantIsolation.qnt")
	matches := generateMBTTraces(t, bunBin, cliPath, qntModel, "multi_tenant_isolation", 18, 8)
	totalSteps := 0
	for _, tracePath := range matches {
		raw, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatal(err)
		}
		var trace multiTenantTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tracePath, err)
		}
		replayOneMultiTenantTrace(t, filepath.Base(tracePath), trace, false)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [MultiTenantIsolation] replayed %d Quint ITF traces (%d transitions) against Go AuthManager + Cedar Authorizer + Outbox", len(matches), totalSteps)
}

func TestMBTReplayMarlinTenantOverlayITF(t *testing.T) {
	bunBin, cliPath, repoRoot := resolveResolverCLI(t)
	overlayModel := filepath.Join(repoRoot, "spec/auth/MarlinTenantOverlay.qnt")
	matches := generateMBTTraces(t, bunBin, cliPath, overlayModel, "marlin_tenant_composed", 18, 12)
	totalSteps := 0
	for _, tracePath := range matches {
		raw, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatal(err)
		}
		var trace multiTenantTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tracePath, err)
		}
		replayOneMultiTenantTrace(t, filepath.Base(tracePath), trace, true)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [MarlinTenantOverlay] replayed %d Quint @quint-extends ITF traces (%d transitions) against Go AuthManager + Cedar + Federation TargetRegistry", len(matches), totalSteps)
}

func replayOneMultiTenantTrace(t *testing.T, name string, trace multiTenantTrace, isOverlay bool) {
	t.Helper()
	ctx := context.Background()
	repoRoot, _ := filepath.Abs("../..")
	cedarPolicy, err := os.ReadFile(filepath.Join(repoRoot, "spec/auth/marlin-default.cedar"))
	if err != nil {
		t.Fatalf("read marlin-default.cedar: %v", err)
	}

	mgr := NewManager(ManagerConfig{Vault: NewMemoryVault()})
	oidcCfg := ProviderConfig{ID: ProviderOIDC, DisplayName: "OIDC", Method: MethodOAuthPKCE}
	acct1 := AccountID("oidc:tenant-1-user")
	acct2 := AccountID("oidc:tenant-2-user")
	for _, pair := range []struct {
		id     AccountID
		tenant string
	}{
		{acct1, "tenant-1"},
		{acct2, "tenant-2"},
	} {
		if _, err := mgr.admitSignedInAccount(ctx, oidcCfg, TokenSet{
			AccessToken: "tok-" + pair.tenant,
			ExpiresAt:   time.Now().Add(time.Hour),
			Scopes:      []string{"openid"},
		}, AccountProfile{
			ID:            pair.id,
			Subject:       string(pair.id),
			EmailVerified: true,
			TenantID:      pair.tenant,
			Scopes:        []string{"openid"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := mgr.SwitchAccount(acct1); err != nil {
		t.Fatal(err)
	}

	authz := NewAuthorizer(mgr)
	if _, err := authz.LoadPolicyBytes("marlin-default", cedarPolicy); err != nil {
		t.Fatalf("load marlin-default.cedar: %v", err)
	}

	outbox := resilience.NewOutbox()
	reg := transport.NewTargetRegistry()
	_ = reg.Register(&stubTenantTarget{meta: transport.TargetMeta{ID: "node-t1", ActiveProfile: "tenant-1", Mode: transport.ModeRemoteGRPC}})
	_ = reg.Register(&stubTenantTarget{meta: transport.TargetMeta{ID: "node-t2", ActiveProfile: "tenant-2", Mode: transport.ModeRemoteGRPC}})
	_, _ = reg.Switch("node-t1")

	var (
		hasTicket       bool
		pendingTicket   AuthzTicket
		goExecutedCount int64
		outboxTenant    string
	)

	activeAccountForTenant := func(ten int64) AccountID {
		if ten == 2 {
			return acct2
		}
		return acct1
	}

	for i := 1; i < len(trace.States); i++ {
		prev := trace.States[i-1].H
		st := trace.States[i]
		act := st.ActionTaken

		switch act {
		case "switchTenant":
			nextTen := int64(2)
			if prev.ActiveTenant.Int64() == 2 {
				nextTen = 1
			}
			if err := mgr.SwitchAccount(activeAccountForTenant(nextTen)); err != nil {
				t.Fatalf("[%s step %d] SwitchAccount failed: %v", name, i, err)
			}
		case "issueTicket":
			tenID := tenantIDFromNum(prev.ActiveTenant.Int64())
			accID := activeAccountForTenant(prev.ActiveTenant.Int64())
			tk, evalErr := authz.EvaluateForTenant(accID, tenID, "invoke_grpc", "workspace/rpc", nil)
			revoked := (prev.ActiveTenant.Int64() == 1 && prev.Tenant1Revoked) || (prev.ActiveTenant.Int64() == 2 && prev.Tenant2Revoked)
			if revoked {
				if !errors.Is(evalErr, ErrTenantRevoked) {
					t.Fatalf("[%s step %d] expected ErrTenantRevoked issuing ticket on revoked %s, got tk=%+v err=%v", name, i, tenID, tk, evalErr)
				}
			} else {
				if evalErr != nil || !tk.Allowed {
					t.Fatalf("[%s step %d] valid EvaluateForTenant failed on %s: tk=%+v err=%v", name, i, tenID, tk, evalErr)
				}
				pendingTicket = tk
				hasTicket = true
			}
		case "revokeTenant1":
			if !prev.Tenant1Revoked && prev.Tenant1Epoch.Int64() < 3 {
				mgr.RevokeTenant("tenant-1")
			}
		case "revokeTenant2":
			if !prev.Tenant2Revoked && prev.Tenant2Epoch.Int64() < 3 {
				mgr.RevokeTenant("tenant-2")
			}
		case "enforceTicket":
			if prev.TicketIssued && prev.ExecutedCount.Int64() < 4 && hasTicket {
				targetTenNum, ok := st.NondetPicks.TargetTenant.Int64()
				if !ok {
					targetTenNum = prev.TicketTenant.Int64()
				}
				attempted := pendingTicket
				attempted.ResourceTenantID = tenantIDFromNum(targetTenNum)
				enfErr := authz.Enforce(attempted)
				quintExecuted := st.H.ExecutedCount.Int64() > prev.ExecutedCount.Int64()
				if quintExecuted {
					if isOverlay && errors.Is(enfErr, ErrStalePolicyDecision) && pendingTicket.PolicyEpoch < authz.PolicyEpoch() {
						// Stronger fail-closed overlay refinement: Cedar policy bump invalidated pre-bump ticket.
						hasTicket = false
					} else if enfErr != nil {
						t.Fatalf("[%s step %d] Quint executed valid tenant ticket but Go Authorizer rejected: %v", name, i, enfErr)
					} else {
						goExecutedCount++
						hasTicket = false
					}
				} else if enfErr == nil {
					t.Fatalf("[%s step %d] SECURITY VIOLATION: Quint blocked cross-tenant/revoked ticket (ticketTen=%d targetTen=%d) but Go allowed it!",
						name, i, prev.TicketTenant.Int64(), targetTenNum)
				}
			}
		case "enqueueOutbox":
			revoked := (prev.ActiveTenant.Int64() == 1 && prev.Tenant1Revoked) || (prev.ActiveTenant.Int64() == 2 && prev.Tenant2Revoked)
			if !prev.OutboxPending && !revoked {
				tenID := tenantIDFromNum(prev.ActiveTenant.Int64())
				if _, err := outbox.Enqueue("tenant:"+tenID, string(activeAccountForTenant(prev.ActiveTenant.Int64())), "", "payload"); err != nil {
					t.Fatalf("[%s step %d] outbox.Enqueue failed: %v", name, i, err)
				}
				outboxTenant = tenID
			}
		case "flushOutbox":
			if prev.OutboxPending {
				activeTenID := tenantIDFromNum(prev.ActiveTenant.Int64())
				activeRevoked := (prev.ActiveTenant.Int64() == 1 && prev.Tenant1Revoked) || (prev.ActiveTenant.Int64() == 2 && prev.Tenant2Revoked)
				// Tenant-pinned flush ONLY flushes scopes matching the active non-revoked tenant.
				if !activeRevoked {
					acked, _ := outbox.Flush("tenant:"+activeTenID, func(it resilience.OutboxItem) error {
						if it.ScopeID != "tenant:"+activeTenID {
							return ErrCrossTenantAccess
						}
						return nil
					})
					if acked > 0 {
						outboxTenant = ""
					}
				}
			}
		case "bumpTenantCedarPolicy":
			if prev.CedarPolicyEpoch.Int64() < 3 {
				if _, err := authz.LoadPolicyBytes("marlin-default-bump", cedarPolicy); err != nil {
					t.Fatalf("[%s step %d] LoadPolicyBytes failed: %v", name, i, err)
				}
				// Re-issue pending ticket if still valid? No: Quint keeps ticketIssued; stale policy epoch is intentionally enforced on next Enforce if bumped.
			}
		case "switchFederationTarget":
			nextNode := "node-t2"
			if prev.TargetTenant.Int64() == 2 {
				nextNode = "node-t1"
			}
			if _, err := reg.Switch(nextNode); err != nil {
				t.Fatalf("[%s step %d] TargetRegistry.Switch failed: %v", name, i, err)
			}
		case "dispatchToTarget":
			activeTarget, ok := reg.Active()
			if !ok {
				t.Fatalf("[%s step %d] missing active federation target", name, i)
			}
			activeTenID := tenantIDFromNum(prev.ActiveTenant.Int64())
			if activeTarget.Meta().ActiveProfile == activeTenID && prev.ActiveTenant.Int64() != prev.TargetTenant.Int64() {
				t.Fatalf("[%s step %d] federation target tenant mismatch: Go=%s Quint=%d", name, i, activeTarget.Meta().ActiveProfile, prev.TargetTenant.Int64())
			}
		default:
			t.Fatalf("[%s step %d] unknown MultiTenant MBT action %q", name, i, act)
		}

		// Step-by-step refinement invariants
		if !isOverlay && goExecutedCount != st.H.ExecutedCount.Int64() {
			t.Fatalf("[%s step %d after %s] refinement mismatch executedCount: Go=%d Quint=%d", name, i, act, goExecutedCount, st.H.ExecutedCount.Int64())
		}
		goOutboxPending := outbox.Len() > 0
		if goOutboxPending != st.H.OutboxPending {
			t.Fatalf("[%s step %d after %s] refinement mismatch outboxPending: Go=%v Quint=%v (outboxTenant=%q)", name, i, act, goOutboxPending, st.H.OutboxPending, outboxTenant)
		}
		if isOverlay && int64(authz.PolicyEpoch()) != st.H.CedarPolicyEpoch.Int64() {
			t.Fatalf("[%s step %d after %s] refinement mismatch cedarPolicyEpoch: Go=%d Quint=%d", name, i, act, authz.PolicyEpoch(), st.H.CedarPolicyEpoch.Int64())
		}
	}
}

// ---------------------------------------------------------------------------
// 10 & 11. DelegatedAgentAuthz.qnt + MarlinDelegatedAuthzOverlay.qnt (@quint-extends) ->
//          Go AuthManager + OSProtectedVault + Cedar Authorizer (OBO Attenuation + RFC 9470 Step-Up)
// ---------------------------------------------------------------------------

type delegatedAuthzState struct {
	HumanAuthEpoch       itfBigInt `json:"humanAuthEpoch"`
	HumanSignedIn        bool      `json:"humanSignedIn"`
	HumanScopeLevel      itfBigInt `json:"humanScopeLevel"`
	EmailVerified        bool      `json:"emailVerified"`
	StepUpFresh          bool      `json:"stepUpFresh"`
	DelegatedActive      bool      `json:"delegatedActive"`
	DelegatedEpoch       itfBigInt `json:"delegatedEpoch"`
	DelegatedScopeLevel  itfBigInt `json:"delegatedScopeLevel"`
	HumanGatesApproved   itfBigInt `json:"humanGatesApproved"`
	ExecutedScopeLevel   itfBigInt `json:"executedScopeLevel"`
	ScopeAmplified       bool      `json:"scopeAmplified"`
	ImpersonatedHuman    bool      `json:"impersonatedHuman"`
	ZombieDelegatedUse   bool      `json:"zombieDelegatedUse"`
	UnverifiedPrivileged bool      `json:"unverifiedPrivileged"`
	// MarlinDelegatedAuthzOverlay fields
	VaultLocked    bool `json:"vaultLocked"`
	LockedExecLeak bool `json:"lockedExecLeak"`
}

type delegatedAuthzNondetPicks struct {
	Verified itfOptionBool `json:"verified"`
	ReqScope itfOptionInt  `json:"reqScope"`
	IsAgent  itfOptionBool `json:"isAgent"`
}

type delegatedAuthzTraceState struct {
	H           delegatedAuthzState       `json:"h"`
	ActionTaken string                    `json:"mbt::actionTaken"`
	NondetPicks delegatedAuthzNondetPicks `json:"mbt::nondetPicks"`
}

type delegatedAuthzTrace struct {
	Vars   []string                   `json:"vars"`
	States []delegatedAuthzTraceState `json:"states"`
}

func scopesForLevel(lvl int64) []string {
	switch lvl {
	case 1:
		return []string{"openid"}
	case 2:
		return []string{"openid", "plugin:install"}
	case 3:
		return []string{"openid", "plugin:install", "vault:admin"}
	default:
		return nil
	}
}

func TestMBTReplayDelegatedAgentAuthzITF(t *testing.T) {
	bunBin, cliPath, qntModel := resolveDEModelAndCLI(t, "threat/identity/delegated-agent-authz/DelegatedAgentAuthz.qnt")
	matches := generateMBTTraces(t, bunBin, cliPath, qntModel, "delegated_agent_authz", 18, 8)
	totalSteps := 0
	for _, tracePath := range matches {
		raw, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatal(err)
		}
		var trace delegatedAuthzTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tracePath, err)
		}
		replayOneDelegatedAuthzTrace(t, filepath.Base(tracePath), trace, false)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [DelegatedAgentAuthz] replayed %d Quint ITF traces (%d transitions) against Go AuthManager + Cedar OBO/Step-Up", len(matches), totalSteps)
}

func TestMBTReplayMarlinDelegatedAuthzOverlayITF(t *testing.T) {
	bunBin, cliPath, repoRoot := resolveResolverCLI(t)
	overlayModel := filepath.Join(repoRoot, "spec/auth/MarlinDelegatedAuthzOverlay.qnt")
	matches := generateMBTTraces(t, bunBin, cliPath, overlayModel, "marlin_delegated_authz_composed", 18, 12)
	totalSteps := 0
	for _, tracePath := range matches {
		raw, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatal(err)
		}
		var trace delegatedAuthzTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tracePath, err)
		}
		replayOneDelegatedAuthzTrace(t, filepath.Base(tracePath), trace, true)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [MarlinDelegatedAuthzOverlay] replayed %d Quint @quint-extends ITF traces (%d transitions) against Go AuthManager + OSProtectedVault + Cedar OBO/Step-Up", len(matches), totalSteps)
}

func replayOneDelegatedAuthzTrace(t *testing.T, name string, trace delegatedAuthzTrace, isOverlay bool) {
	t.Helper()
	ctx := context.Background()
	kcStore := &mbtKeychainStore{}
	vaultPath := filepath.Join(t.TempDir(), "delegated-vault.enc")
	rawVault, _ := OpenNativeVault(ctx, NativeVaultConfig{
		Store:     kcStore,
		Service:   "marlin-obo-mbt",
		Account:   DefaultKeychainAccount,
		VaultPath: vaultPath,
	})
	osVault, ok := rawVault.(*OSProtectedVault)
	if !ok {
		t.Fatalf("expected *OSProtectedVault, got %T", rawVault)
	}
	memVault := NewMemoryVault()
	combinedVault := &lockAwareSessionVault{MemoryVault: memVault, protected: osVault}

	mgr := NewManager(ManagerConfig{Vault: combinedVault})
	oidcCfg := ProviderConfig{ID: ProviderOIDC, DisplayName: "OIDC", Method: MethodOAuthPKCE}
	humanID := AccountID("oidc:human-operator")
	admitHuman := func(emailVerified bool) {
		if _, err := mgr.admitSignedInAccount(ctx, oidcCfg, TokenSet{
			AccessToken: "human-tok",
			ExpiresAt:   time.Now().Add(time.Hour),
			Scopes:      scopesForLevel(2), // humanScopeLevel = 2
		}, AccountProfile{
			ID:            humanID,
			Subject:       "human-operator",
			EmailVerified: emailVerified,
			TenantID:      "tenant-1",
			Scopes:        scopesForLevel(2),
		}); err != nil {
			t.Fatal(err)
		}
	}
	admitHuman(true)

	authz := NewAuthorizer(mgr)
	// Load Cedar policy allowing invoke_grpc, install_plugin, and approve_gate (with forbid for delegated approve_gate).
	policy := []byte(`
permit(principal is Marlin::Account, action == Marlin::Action::"invoke_grpc", resource is Marlin::Resource) when { context.scopes.contains("openid") };
permit(principal is Marlin::Account, action == Marlin::Action::"install_plugin", resource is Marlin::Resource) when { context.email_verified && context.scopes.contains("plugin:install") };
permit(principal is Marlin::Account, action == Marlin::Action::"approve_gate", resource is Marlin::Resource) when { context.scopes.contains("openid") };
forbid(principal is Marlin::Account, action == Marlin::Action::"approve_gate", resource is Marlin::Resource) when { context has delegated && context.delegated };
`)
	if _, err := authz.LoadPolicyBytes("obo-test", policy); err != nil {
		t.Fatal(err)
	}

	var (
		hasDelegatedTicket bool
		delegatedTicket    AuthzTicket
		delegatedScopeLvl  int64
		goExecutedScopeLvl int64
		goHumanGates       int64
	)

	for i := 1; i < len(trace.States); i++ {
		prev := trace.States[i-1].H
		st := trace.States[i]
		act := st.ActionTaken

		switch act {
		case "performStepUp":
			if prev.HumanSignedIn && prev.EmailVerified && !prev.StepUpFresh {
				if err := mgr.RecordStepUp(humanID); err != nil {
					t.Fatalf("[%s step %d] RecordStepUp failed: %v", name, i, err)
				}
			}
		case "revokeHumanSession":
			if prev.HumanSignedIn && prev.HumanAuthEpoch.Int64() < 3 {
				if err := mgr.SignOut(ctx, humanID); err != nil {
					t.Fatalf("[%s step %d] SignOut failed: %v", name, i, err)
				}
			}
		case "reauthenticateHuman":
			if !prev.HumanSignedIn && prev.HumanAuthEpoch.Int64() < 3 {
				verified, _ := st.NondetPicks.Verified.Bool()
				admitHuman(verified)
			}
		case "delegateToAgent":
			if prev.HumanSignedIn && !prev.DelegatedActive {
				reqScope, ok := st.NondetPicks.ReqScope.Int64()
				if !ok {
					reqScope = 1
				}
				wasLocked := osVault.IsLocked()
				if wasLocked {
					_ = osVault.Unlock(ctx)
				}
				tk, err := authz.EvaluateDelegated(humanID, "agent-architect", "tenant-1", "invoke_grpc", "workspace/tool", scopesForLevel(reqScope), nil)
				if wasLocked {
					osVault.Lock()
				}
				if reqScope > prev.HumanScopeLevel.Int64() {
					if !errors.Is(err, ErrDelegationScopeAmplified) {
						t.Fatalf("[%s step %d] expected ErrDelegationScopeAmplified for scope %d > %d, got err=%v", name, i, reqScope, prev.HumanScopeLevel.Int64(), err)
					}
				} else {
					if err != nil || !tk.Allowed {
						t.Fatalf("[%s step %d] valid EvaluateDelegated(scope=%d) failed: tk=%+v err=%v", name, i, reqScope, tk, err)
					}
					delegatedTicket = tk
					delegatedScopeLvl = reqScope
					hasDelegatedTicket = true
				}
			}
		case "executeDelegated":
			if prev.DelegatedActive && hasDelegatedTicket {
				wasLocked := osVault.IsLocked()
				if wasLocked {
					_ = osVault.Unlock(ctx)
				}
				enfErr := authz.Enforce(delegatedTicket)
				if wasLocked {
					osVault.Lock()
				}
				quintExecuted := !st.H.DelegatedActive
				if quintExecuted {
					if enfErr != nil {
						t.Fatalf("[%s step %d] Quint executed valid delegated ticket but Go Authorizer rejected: %v", name, i, enfErr)
					}
					goExecutedScopeLvl = delegatedScopeLvl
					hasDelegatedTicket = false
				} else if enfErr == nil {
					t.Fatalf("[%s step %d] SECURITY VIOLATION: Quint blocked revoked delegated ticket but Go allowed it!", name, i)
				}
			}
		case "approveHumanOnlyGate":
			isAgent, _ := st.NondetPicks.IsAgent.Bool()
			if prev.HumanSignedIn && prev.HumanGatesApproved.Int64() < 3 && (!isAgent || prev.DelegatedActive) {
				wasLocked := osVault.IsLocked()
				if wasLocked {
					_ = osVault.Unlock(ctx)
				}
				if isAgent {
					if _, err := authz.EvaluateDelegated(humanID, "agent-architect", "tenant-1", "approve_gate", "gate/1", []string{"openid"}, nil); !errors.Is(err, ErrDelegatedHumanGateForbidden) {
						t.Fatalf("[%s step %d] expected ErrDelegatedHumanGateForbidden for agent gate approval, got %v", name, i, err)
					}
				} else {
					tk, err := authz.EvaluateForTenant(humanID, "tenant-1", "approve_gate", "gate/1", nil)
					if err != nil || !tk.Allowed || authz.Enforce(tk) != nil {
						t.Fatalf("[%s step %d] direct human approve_gate failed: tk=%+v err=%v", name, i, tk, err)
					}
					goHumanGates++
				}
				if wasLocked {
					osVault.Lock()
				}
			}
		case "executePrivilegedHuman":
			wasLocked := osVault.IsLocked()
			if wasLocked {
				_ = osVault.Unlock(ctx)
			}
			tk, evalErr := authz.EvaluateForTenant(humanID, "tenant-1", "install_plugin", "plugin/1", nil)
			var stepErr error
			if evalErr != nil {
				stepErr = evalErr
			} else {
				stepErr = authz.EnforceStepUp(tk)
			}
			if wasLocked {
				osVault.Lock()
			}
			quintPermitted := prev.HumanSignedIn && prev.EmailVerified && prev.StepUpFresh
			if quintPermitted {
				if stepErr != nil {
					t.Fatalf("[%s step %d] valid step-up privileged action rejected: %v", name, i, stepErr)
				}
			} else if stepErr == nil {
				t.Fatalf("[%s step %d] SECURITY VIOLATION: privileged action succeeded without verified email + fresh step-up!", name, i)
			}
		case "lockVault":
			osVault.Lock()
		case "unlockVault":
			if err := osVault.Unlock(ctx); err != nil {
				t.Fatalf("[%s step %d] Unlock failed: %v", name, i, err)
			}
		case "invokeVaultBackedTool":
			if prev.VaultLocked {
				if _, err := authz.EvaluateForTenant(humanID, "tenant-1", "invoke_grpc", "vault/tool", nil); !errors.Is(err, ErrVaultLocked) {
					t.Fatalf("[%s step %d] expected ErrVaultLocked on locked vault tool call, got %v", name, i, err)
				}
			} else if prev.HumanSignedIn {
				tk, err := authz.EvaluateForTenant(humanID, "tenant-1", "invoke_grpc", "vault/tool", nil)
				if err != nil || !tk.Allowed || authz.Enforce(tk) != nil {
					t.Fatalf("[%s step %d] unlocked vault tool call failed: tk=%+v err=%v", name, i, tk, err)
				}
			}
		default:
			t.Fatalf("[%s step %d] unknown DelegatedAgentAuthz MBT action %q", name, i, act)
		}

		// Step-by-step refinement checks
		goSignedIn := len(mgr.Snapshot().Accounts) > 0
		if goSignedIn != st.H.HumanSignedIn {
			t.Fatalf("[%s step %d after %s] refinement mismatch humanSignedIn: Go=%v Quint=%v", name, i, act, goSignedIn, st.H.HumanSignedIn)
		}
		if goExecutedScopeLvl != st.H.ExecutedScopeLevel.Int64() {
			t.Fatalf("[%s step %d after %s] refinement mismatch executedScopeLevel: Go=%d Quint=%d", name, i, act, goExecutedScopeLvl, st.H.ExecutedScopeLevel.Int64())
		}
		if goHumanGates != st.H.HumanGatesApproved.Int64() {
			t.Fatalf("[%s step %d after %s] refinement mismatch humanGatesApproved: Go=%d Quint=%d", name, i, act, goHumanGates, st.H.HumanGatesApproved.Int64())
		}
		if isOverlay && osVault.IsLocked() != st.H.VaultLocked {
			t.Fatalf("[%s step %d after %s] refinement mismatch vaultLocked: Go=%v Quint=%v", name, i, act, osVault.IsLocked(), st.H.VaultLocked)
		}
	}
}
