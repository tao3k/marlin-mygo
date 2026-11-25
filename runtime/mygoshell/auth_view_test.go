// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/auth"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

type uiTestEmailBackend struct{}

func (uiTestEmailBackend) RequestChallenge(_ context.Context, _ string, _ auth.AuthMethod) (auth.EmailChallengeResponse, error) {
	return auth.EmailChallengeResponse{
		ChallengeID: "ui-ch-1",
		ExpiresIn:   5 * time.Minute,
		Cooldown:    30 * time.Second,
		MaxAttempts: 5,
	}, nil
}

func (uiTestEmailBackend) VerifyChallenge(_ context.Context, _, email, code string, _ auth.AuthMethod) (auth.TokenSet, auth.AccountProfile, error) {
	if code != "123456" {
		return auth.TokenSet{}, auth.AccountProfile{}, errors.New("invalid OTP code")
	}
	return auth.TokenSet{
			AccessToken:  "SECRET_UI_TOKEN_NEVER_RENDER",
			RefreshToken: "SECRET_UI_REFRESH_NEVER_RENDER",
			TokenType:    "Bearer",
			ExpiresAt:    time.Now().Add(time.Hour),
		}, auth.AccountProfile{
			Subject:       "usr-1",
			Email:         email,
			DisplayName:   "Guangtao Dev",
			EmailVerified: true,
		}, nil
}

type authUITestTarget struct{ meta transport.TargetMeta }

func (t *authUITestTarget) Meta() transport.TargetMeta { return t.meta }
func (t *authUITestTarget) Ping(context.Context) error { return nil }
func (t *authUITestTarget) Close() error               { return nil }

func TestTier0AuthModalOAuthEmailAndZeroTokenExposure(t *testing.T) {
	targets := transport.NewTargetRegistry()
	_ = targets.Register(&authUITestTarget{meta: transport.TargetMeta{
		ID: "local-app-server", DisplayName: "Marlin App Server", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Address: "127.0.0.1:50051", Phase: transport.PhaseReady,
	}})
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)

	authMgr := auth.NewManager(auth.ManagerConfig{
		EmailBackend: uiTestEmailBackend{},
	})
	_ = authMgr.RegisterProvider(auth.GoogleOAuthProvider("marlin-google-ui"))
	_ = authMgr.RegisterProvider(auth.GitHubOAuthProvider("marlin-github-ui"))
	_ = authMgr.RegisterProvider(auth.EmailOTPProvider())

	var copiedURL string
	authCtrl := mygoshell.NewAuthController(authMgr)
	authCtrl.OnCopyText = func(text string) { copiedURL = text }
	app.Auth = authCtrl

	tt := ui.NewTester(app.View, 1280, 800)

	// 1. Top bar displays Sign In pill when signed out -> clicking opens Tier-0 Auth Modal.
	if !tt.HasText("Sign In") {
		t.Fatalf("expected top bar 'Sign In' pill, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Sign In"); err != nil {
		t.Fatalf("click Sign In failed: %v", err)
	}
	tt.Frame()
	if !app.IsAuthOpen() || !tt.HasText("TIER-0 HOST AUTH") || !tt.HasText("Continue with Google") || !tt.HasText("Continue with GitHub") {
		t.Fatalf("expected Tier-0 Auth modal with Google & GitHub OAuth buttons, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 2. Clicking Continue with Google starts PKCE S256 authorization flow & allows copying URL + Cancel.
	if err := tt.Click("Continue with Google"); err != nil {
		t.Fatalf("click Continue with Google failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("AUTHORIZING") || !tt.HasText("Signing in with Google") {
		t.Fatalf("expected AUTHORIZING state for Google OAuth, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Copy Sign-In URL"); err != nil {
		t.Fatalf("click Copy Sign-In URL failed: %v", err)
	}
	if !strings.Contains(copiedURL, "https://accounts.google.com/o/oauth2/v2/auth") || !strings.Contains(copiedURL, "code_challenge_method=S256") {
		t.Fatalf("expected copied Google PKCE S256 URL, got %q", copiedURL)
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("click Cancel failed: %v", err)
	}
	tt.Frame()

	// 3. Complete passwordless Email OTP flow end-to-end inside Tier-0 UI!
	authCtrl.SetEmailInput("guangtao@marlin.dev")
	if err := tt.Click("Send Verification Code"); err != nil {
		t.Fatalf("click Send Verification Code failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("CHECK EMAIL") || !tt.HasText("guangtao@marlin.dev") {
		t.Fatalf("expected CHECK EMAIL verification screen, got %q", strings.Join(tt.Texts(), " | "))
	}

	authCtrl.SetCodeInput("123456")
	if err := tt.Click("Verify & Sign In"); err != nil {
		t.Fatalf("click Verify & Sign In failed: %v", err)
	}
	tt.Frame()

	if !authCtrl.IsSignedIn() || !tt.HasText("Guangtao Dev") || !tt.HasText("VERIFIED") {
		t.Fatalf("expected signed-in Account Center for Guangtao Dev, got %q", strings.Join(tt.Texts(), " | "))
	}

	// Security invariant: raw access/refresh tokens MUST NEVER appear in any rendered GPU text!
	allText := strings.Join(tt.Texts(), " ")
	if strings.Contains(allText, "SECRET_UI_TOKEN_NEVER_RENDER") || strings.Contains(allText, "SECRET_UI_REFRESH_NEVER_RENDER") {
		t.Fatalf("SECURITY VIOLATION: raw auth token rendered in UI frame: %s", allText)
	}

	// 4. Sign Out scrubs session and restores top-bar Sign In state!
	if err := tt.Click("Sign Out"); err != nil {
		t.Fatalf("click Sign Out failed: %v", err)
	}
	tt.Frame()
	if authCtrl.IsSignedIn() || !tt.HasText("Continue with Google") {
		t.Fatalf("expected signed-out UI after Sign Out, got %q", strings.Join(tt.Texts(), " | "))
	}
}
