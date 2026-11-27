// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cedar-policy/cedar-go"
)

func signInTestAccount(t *testing.T, mgr *AuthManager, subject string, verified bool, scopes []string) AccountProfile {
	t.Helper()
	cfg := testProviderConfig()
	cfg.Scopes = scopes
	if err := mgr.RegisterProvider(cfg); err != nil {
		t.Fatal(err)
	}
	flow, err := mgr.BeginOAuth(ProviderOIDC, "")
	if err != nil {
		t.Fatal(err)
	}
	mgr.mu.RLock()
	state := mgr.oauthAttempts[flow.AttemptID].state
	mgr.mu.RUnlock()
	acc, err := mgr.CompleteOAuthCallback(context.Background(), flow.AttemptID, LoopbackCallback{Code: "c1", State: state})
	if err != nil {
		t.Fatal(err)
	}
	return acc
}

func loadDefaultMarlinPolicy(t *testing.T, authz *Authorizer) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "spec", "auth", "marlin-default.cedar"))
	if err != nil {
		t.Fatalf("read marlin-default.cedar: %v", err)
	}
	if _, err := authz.LoadPolicyBytes("marlin-default.cedar", raw); err != nil {
		t.Fatalf("load marlin-default.cedar: %v", err)
	}
}

func TestCedarAuthorizerPermitDenyAndAntiSpoofing(t *testing.T) {
	ex := &stubExchanger{
		exchangeFn: func(_ context.Context, _ ProviderConfig, _, _, _, _ string) (TokenSet, AccountProfile, error) {
			return TokenSet{
				AccessToken: "tok-1",
				Scopes:      []string{"openid", "plugin:install"},
				ExpiresAt:   time.Now().Add(time.Hour),
			}, AccountProfile{Subject: "alice", Email: "alice@example.com", EmailVerified: false, Scopes: []string{"openid", "plugin:install"}}, nil
		},
	}
	mgr := NewManager(ManagerConfig{Exchanger: ex})
	acc := signInTestAccount(t, mgr, "alice", false, []string{"openid", "plugin:install"})

	authz := NewAuthorizer(mgr)
	// Fail-closed before any policy is loaded.
	if _, err := authz.Authorize(acc.ID, "invoke_grpc", "sidecar:main", nil); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected fail-closed ErrPolicyDenied before policy load, got %v", err)
	}

	loadDefaultMarlinPolicy(t, authz)

	// invoke_grpc allowed with openid scope.
	if _, err := authz.Authorize(acc.ID, "invoke_grpc", "sidecar:main", nil); err != nil {
		t.Fatalf("expected invoke_grpc allowed, got %v", err)
	}
	// install_plugin denied because email_verified == false, even if caller tries to spoof context!
	spoof := map[cedar.String]cedar.Value{"email_verified": cedar.Boolean(true)}
	if _, err := authz.Authorize(acc.ID, "install_plugin", "plugin:foo", spoof); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied for unverified email even with spoofed context, got %v", err)
	}
}

func TestCedarAuthorizerStalePolicyEpochAndSignOutRevocation(t *testing.T) {
	ex := &stubExchanger{
		exchangeFn: func(_ context.Context, _ ProviderConfig, _, _, _, _ string) (TokenSet, AccountProfile, error) {
			return TokenSet{
				AccessToken: "tok-2",
				Scopes:      []string{"openid", "plugin:install"},
				ExpiresAt:   time.Now().Add(time.Hour),
			}, AccountProfile{Subject: "bob", Email: "bob@example.com", EmailVerified: true, Scopes: []string{"openid", "plugin:install"}}, nil
		},
	}
	mgr := NewManager(ManagerConfig{Exchanger: ex})
	acc := signInTestAccount(t, mgr, "bob", true, []string{"openid", "plugin:install"})

	authz := NewAuthorizer(mgr)
	loadDefaultMarlinPolicy(t, authz)

	// Issue a valid PDP ticket at policyEpoch 1.
	ticket, err := authz.Evaluate(acc.ID, "install_plugin", "plugin:bar", nil)
	if err != nil || !ticket.Allowed {
		t.Fatalf("expected allowed ticket, got %+v, err=%v", ticket, err)
	}

	// Reload policy (advances policyEpoch to 2): previously issued ticket MUST be rejected by PEP!
	if _, err := authz.LoadPolicyBytes("revoked.cedar", []byte(`forbid (principal, action, resource);`)); err != nil {
		t.Fatal(err)
	}
	if err := authz.Enforce(ticket); !errors.Is(err, ErrStalePolicyDecision) {
		t.Fatalf("expected ErrStalePolicyDecision after policy update, got %v", err)
	}

	// Reload permissive policy, issue fresh ticket, then SignOut: PEP MUST reject with ErrSessionRevoked!
	loadDefaultMarlinPolicy(t, authz)
	freshTicket, err := authz.Evaluate(acc.ID, "install_plugin", "plugin:bar", nil)
	if err != nil || !freshTicket.Allowed {
		t.Fatalf("expected fresh allowed ticket, got %+v, err=%v", freshTicket, err)
	}
	if err := mgr.SignOut(context.Background(), acc.ID); err != nil {
		t.Fatal(err)
	}
	if err := authz.Enforce(freshTicket); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("expected ErrSessionRevoked after SignOut, got %v", err)
	}
}
