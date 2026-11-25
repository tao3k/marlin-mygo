// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/auth"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

type uiMockKeychain struct {
	secrets map[string][]byte
}

func (m *uiMockKeychain) Name() string    { return "macOS Keychain" }
func (m *uiMockKeychain) Available() bool { return true }
func (m *uiMockKeychain) GetSecret(_ context.Context, s, a string) ([]byte, error) {
	v, ok := m.secrets[s+"/"+a]
	if !ok {
		return nil, auth.ErrKeychainNotFound
	}
	return append([]byte(nil), v...), nil
}
func (m *uiMockKeychain) SetSecret(_ context.Context, s, a string, sec []byte) error {
	if m.secrets == nil {
		m.secrets = make(map[string][]byte)
	}
	m.secrets[s+"/"+a] = append([]byte(nil), sec...)
	return nil
}
func (m *uiMockKeychain) DeleteSecret(_ context.Context, s, a string) error {
	delete(m.secrets, s+"/"+a)
	return nil
}

func TestDedicatedLoginWindowDesktopCompactAndOSKeychainLockToggle(t *testing.T) {
	ctx := context.Background()
	vault, vaultInfo := auth.OpenNativeVault(ctx, auth.NativeVaultConfig{
		Store:     &uiMockKeychain{},
		VaultPath: filepath.Join(t.TempDir(), "login-window.enc"),
	})
	osVault, ok := vault.(*auth.OSProtectedVault)
	if !ok {
		t.Fatalf("expected *OSProtectedVault, got %T", vault)
	}

	mgr := auth.NewManager(auth.ManagerConfig{
		Vault:        vault,
		EmailBackend: uiTestEmailBackend{},
	})
	_ = mgr.RegisterProvider(auth.GoogleOAuthProvider("marlin-login-win"))
	_ = mgr.RegisterProvider(auth.GitHubOAuthProvider("marlin-login-win"))
	_ = mgr.RegisterProvider(auth.EmailOTPProvider())

	authCtrl := mygoshell.NewAuthController(mgr)
	authCtrl.Vault = osVault
	authCtrl.VaultInfo = vaultInfo

	var launchedProfile auth.AccountProfile
	offlineClicked := false

	loginWin := mygoshell.NewLoginWindowApp(mygoshell.LoginWindowConfig{
		BrandTitle:       "Marlin Studio",
		InitialThemeID:   "midnight",
		Auth:             authCtrl,
		AllowOfflineMode: true,
		OnAuthenticated: func(p auth.AccountProfile) {
			launchedProfile = p
		},
		OnContinueOffline: func() {
			offlineClicked = true
		},
	})

	// 1. Desktop two-column Login Window renders Brand Hero, OS Keychain status, and SSO + Email options.
	tt := ui.NewTester(loginWin.View, 920, 620)
	for _, want := range []string{
		"TIER-0 LOGIN WINDOW",
		"Welcome to Marlin Studio",
		"OAuth 2.1 PKCE S256 + OIDC JWKS",
		"macOS Keychain",
		"OS KEYCHAIN",
		"Continue with Google",
		"Continue with GitHub",
		"Continue Offline",
	} {
		if !tt.HasText(want) {
			t.Fatalf("expected LoginWindow desktop frame to contain %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}

	// 2. Interactive OS Keychain Lock/Unlock button toggles live vault state!
	if err := tt.Click("Lock Vault"); err != nil {
		t.Fatalf("click Lock Vault failed: %v", err)
	}
	tt.Frame()
	if !osVault.SecurityInfo().Locked || !tt.HasText("LOCKED") || !tt.HasText("Unlock Vault") {
		t.Fatalf("expected vault to enter LOCKED state, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Unlock Vault"); err != nil {
		t.Fatalf("click Unlock Vault failed: %v", err)
	}
	tt.Frame()
	if osVault.SecurityInfo().Locked || !tt.HasText("Lock Vault") {
		t.Fatalf("expected vault to unlock cleanly, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 3. Complete Email OTP sign-in inside the dedicated Login Window -> Launch Workspace CTA!
	authCtrl.SetEmailInput("architect@marlin.dev")
	if err := tt.Click("Send Verification Code"); err != nil {
		t.Fatalf("click Send Verification Code failed: %v", err)
	}
	tt.Frame()
	authCtrl.SetCodeInput("123456")
	if err := tt.Click("Verify & Sign In"); err != nil {
		t.Fatalf("click Verify & Sign In failed: %v", err)
	}
	tt.Frame()

	if !tt.HasText("Launch Workspace") || !tt.HasText("Guangtao Dev") {
		t.Fatalf("expected Launch Workspace CTA after sign-in, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Launch Workspace"); err != nil {
		t.Fatalf("click Launch Workspace failed: %v", err)
	}
	if launchedProfile.Email != "architect@marlin.dev" {
		t.Fatalf("expected OnAuthenticated with architect@marlin.dev, got %+v", launchedProfile)
	}

	// 4. Continue Offline callback works cleanly.
	if err := tt.Click("Continue Offline"); err != nil {
		t.Fatalf("click Continue Offline failed: %v", err)
	}
	if !offlineClicked {
		t.Fatal("expected OnContinueOffline callback to fire")
	}

	// 5. Compact (<720px) phone viewport renders cleanly without overflow or missing interactive controls.
	compactTester := ui.NewTester(loginWin.View, 393, 852)
	if !compactTester.HasText("TIER-0 LOGIN WINDOW") || !compactTester.HasText("Guangtao Dev") {
		t.Fatalf("expected compact LoginWindow to render cleanly, got %q", strings.Join(compactTester.Texts(), " | "))
	}

	// 6. Enterprise OIDC Discovery UI panel inside Sign-In view captures Issuer + Client ID cleanly!
	_ = mgr.SignOutAll(ctx)
	var gotIssuer, gotClient string
	authCtrl.OnConfigureOIDC = func(issuer, clientID string) error {
		gotIssuer = issuer
		gotClient = clientID
		return nil
	}
	tt.Frame()
	if err := tt.Click("+ Enterprise SSO"); err != nil {
		t.Fatalf("click + Enterprise SSO failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("ENTERPRISE OIDC DISCOVERY") {
		t.Fatalf("expected Enterprise OIDC Discovery form, got %q", strings.Join(tt.Texts(), " | "))
	}
	authCtrl.SetEnterpriseOIDCInputs("https://sso.acme.dev", "acme-marlin-native")
	if err := tt.Click("Connect Enterprise SSO"); err != nil {
		t.Fatalf("click Connect Enterprise SSO failed: %v", err)
	}
	if gotIssuer != "https://sso.acme.dev" || gotClient != "acme-marlin-native" {
		t.Fatalf("unexpected Enterprise OIDC submission: issuer=%q client=%q", gotIssuer, gotClient)
	}
}
