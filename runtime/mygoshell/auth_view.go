// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/auth"
)

// AuthController bridges control/auth.AuthManager to a Tier-0 Host GPU UI
// authentication & account center modal. All credential inputs and OAuth flows
// render exclusively inside the Tier-0 host layer above untrusted WebViews,
// and raw tokens never enter this controller or UI state.
type AuthController struct {
	Manager *auth.AuthManager

	// Optional platform hooks invoked by UI buttons.
	OnStartOAuth func(providerID auth.ProviderID) error
	OnCopyText   func(text string)
	OnRefresh    func(accountID auth.AccountID) error
	OnChanged    func()

	mu            sync.Mutex
	emailInput    string
	codeInput     string
	emailMode     auth.AuthMethod
	addingAccount bool
	statusMessage string
	statusError   string
}

// NewAuthController wraps an AuthManager for Tier-0 GPU UI rendering.
func NewAuthController(mgr *auth.AuthManager) *AuthController {
	return &AuthController{
		Manager:   mgr,
		emailMode: auth.MethodEmailOTP,
	}
}

// TopBarLabel returns the concise Account pill label for the top navigation bar.
func (ac *AuthController) TopBarLabel() string {
	if ac == nil || ac.Manager == nil {
		return "Sign In"
	}
	snap := ac.Manager.Snapshot()
	if prof, ok := snap.ActiveProfile(); ok {
		name := strings.TrimSpace(prof.DisplayName)
		if name == "" {
			name = prof.Email
		}
		if len(name) > 22 {
			name = name[:21] + "…"
		}
		return name
	}
	if snap.Phase == auth.PhaseAuthorizing || snap.Phase == auth.PhaseAwaitingCode {
		return "Signing In…"
	}
	return "Sign In"
}

// IsSignedIn reports whether an active authenticated account exists.
func (ac *AuthController) IsSignedIn() bool {
	if ac == nil || ac.Manager == nil {
		return false
	}
	_, ok := ac.Manager.Snapshot().ActiveProfile()
	return ok
}

// SetEmailInput sets the email form value (useful for programmatic/test flows).
func (ac *AuthController) SetEmailInput(v string) {
	ac.mu.Lock()
	ac.emailInput = v
	ac.mu.Unlock()
}

// SetCodeInput sets the OTP/magic-link code input value.
func (ac *AuthController) SetCodeInput(v string) {
	ac.mu.Lock()
	ac.codeInput = v
	ac.mu.Unlock()
}

// ClearSensitiveInputs scrubs any in-progress OTP/code entry from UI memory.
func (ac *AuthController) ClearSensitiveInputs() {
	ac.mu.Lock()
	ac.codeInput = ""
	ac.mu.Unlock()
}

// Render paints the Tier-0 Host Authentication & Account Center inside a Modal.
func (ac *AuthController) Render(c *ui.Context, t *ui.Theme, dt DerivedTheme, onClose func()) {
	if ac == nil || ac.Manager == nil {
		ui.Text(c, "Auth framework unavailable.").TextColor(toColor(dt.InkMuted))
		return
	}
	snap := ac.Manager.Snapshot()

	ac.mu.Lock()
	adding := ac.addingAccount
	statusMsg := ac.statusMessage
	statusErr := ac.statusError
	if statusErr == "" && snap.LastError != "" {
		statusErr = snap.LastError
	}
	emailMode := ac.emailMode
	if emailMode != auth.MethodEmailOTP && emailMode != auth.MethodEmailMagicLink {
		emailMode = auth.MethodEmailOTP
		ac.emailMode = emailMode
	}
	ac.mu.Unlock()

	// Header + Tier-0 Security Shield Banner.
	ui.Column(c).Width(520).Gap(12).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			AppleBadgePill(c, "TIER-0 HOST AUTH", dt.Accent, dt)
			ui.Text(c, "Account & Authorization").FontSize(16.5).FontWeight(700).TextColor(toColor(dt.Ink))
			ui.Spacer(c)
			if onClose != nil {
				if ApplePillButton(c, "auth-close", "Close", BtnGlass, dt, svgIconXMark, true).Clicked() {
					ac.ClearSensitiveInputs()
					onClose()
				}
			}
		})

		ui.Row(c).Padding(8, 12).Gap(8).Radius(10).AlignItems(ui.Center).
			Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
			ui.Icon(c, svgIconShield).Size(14, 14).TextColor(toColor(dt.Success))
			ui.Text(c, "OAuth 2.1 PKCE S256 · Encrypted Host Vault · Zero Untrusted Plugin Token Access").
				FontSize(11).TextColor(toColor(dt.InkSecondary))
		})

		if statusErr != "" {
			ui.Row(c).Padding(8, 12).Radius(10).
				Background(rgbaFromRGB(dt.Danger, 0.14)).
				Border(1, rgbaFromRGB(dt.Danger, 0.45)).Children(func() {
				ui.Text(c, statusErr).FontSize(12).FontWeight(600).TextColor(toColor(dt.Danger))
			})
		} else if statusMsg != "" {
			ui.Row(c).Padding(8, 12).Radius(10).
				Background(rgbaFromRGB(dt.Success, 0.12)).
				Border(1, rgbaFromRGB(dt.Success, 0.35)).Children(func() {
				ui.Text(c, statusMsg).FontSize(12).TextColor(toColor(dt.Success))
			})
		}

		// Route between: (1) In-progress OAuth/Device flow, (2) Pending Email OTP/Link verification,
		// (3) Signed-In Account Center, or (4) Sign-In / Add Account selector.
		if snap.PendingOAuth != nil {
			ac.renderPendingOAuth(c, dt, snap.PendingOAuth)
			return
		}
		if snap.PendingEmail != nil {
			ac.renderPendingEmail(c, dt, snap.PendingEmail)
			return
		}
		if active, signedIn := snap.ActiveProfile(); signedIn && !adding {
			ac.renderAccountCenter(c, dt, snap, active)
			return
		}
		ac.renderSignInOptions(c, dt, snap, emailMode, len(snap.Accounts) > 0)
	})
}

func (ac *AuthController) renderSignInOptions(
	c *ui.Context,
	dt DerivedTheme,
	snap auth.AuthSnapshot,
	emailMode auth.AuthMethod,
	hasExistingAccounts bool,
) {
	if hasExistingAccounts {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "Add Another Account").FontSize(13.5).FontWeight(700).TextColor(toColor(dt.Ink))
			ui.Spacer(c)
			if ApplePillButton(c, "auth-back-accounts", "Back to Accounts", BtnGlass, dt, nil, true).Clicked() {
				ac.mu.Lock()
				ac.addingAccount = false
				ac.statusError = ""
				ac.statusMessage = ""
				ac.mu.Unlock()
			}
		})
	}

	// OAuth / SSO Provider Cards (Google, GitHub, Enterprise OIDC).
	var oauthProviders []auth.ProviderSummary
	hasEmailProvider := false
	for _, p := range snap.Providers {
		if p.Method == auth.MethodEmailOTP || p.Method == auth.MethodEmailMagicLink {
			hasEmailProvider = true
			continue
		}
		oauthProviders = append(oauthProviders, p)
	}

	if len(oauthProviders) > 0 {
		ui.Text(c, "SINGLE SIGN-ON (EXTERNAL BROWSER · PKCE S256)").
			FontSize(10.5).FontWeight(700).TextColor(toColor(dt.InkMuted))
		ui.Column(c).Gap(8).AlignItems(ui.Stretch).Children(func() {
			for _, p := range oauthProviders {
				prov := p
				variant := BtnPrimary
				if prov.ID != auth.ProviderGoogle {
					variant = BtnTinted
				}
				label := fmt.Sprintf("Continue with %s", prov.DisplayName)
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					if ApplePillButton(c, "auth-oauth-"+string(prov.ID), label, variant, dt, svgIconShield, false).Clicked() {
						ac.startOAuthFlow(prov.ID)
					}
					AppleBadgePill(c, strings.ToUpper(string(prov.Method)), dt.Info, dt)
				})
			}
		})
	}

	// Passwordless Email Login (OTP Code or Magic Link).
	if hasEmailProvider || len(snap.Providers) == 0 {
		ui.Box(c).Height(1).Background(toColor(dt.Border))
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "EMAIL LOGIN (PASSWORDLESS)").FontSize(10.5).FontWeight(700).TextColor(toColor(dt.InkMuted))
			ui.Spacer(c)
			if chosen := AppleSegmentedControl(c, "auth-email-mode", []SegmentedItem{
				{ID: string(auth.MethodEmailOTP), Label: "6-Digit Code"},
				{ID: string(auth.MethodEmailMagicLink), Label: "Magic Link"},
			}, string(emailMode), dt, true); chosen != "" {
				ac.mu.Lock()
				ac.emailMode = auth.AuthMethod(chosen)
				ac.mu.Unlock()
			}
		})

		ui.Row(c).Padding(8, 12).Radius(12).
			Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
			ui.TextInputBase(c.Key("auth-email-input"), &ac.emailInput).
				Placeholder("you@company.com").
				FontSize(13.5).TextColor(toColor(dt.Ink)).Grow(1)
		})

		sendLabel := "Send Verification Code"
		if emailMode == auth.MethodEmailMagicLink {
			sendLabel = "Send Magic Link"
		}
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			if ApplePillButton(c, "auth-email-send", sendLabel, BtnPrimary, dt, svgIconArrowUp, false).Clicked() {
				ac.mu.Lock()
				email := ac.emailInput
				mode := ac.emailMode
				ac.mu.Unlock()
				if _, err := ac.Manager.RequestEmailLogin(context.Background(), email, mode); err != nil {
					ac.setStatus("", err.Error())
				} else {
					ac.setStatus("Verification challenge dispatched to "+strings.TrimSpace(email), "")
				}
			}
		})
	}
}

func (ac *AuthController) renderPendingOAuth(c *ui.Context, dt DerivedTheme, pending *auth.PendingOAuthFlow) {
	ui.Column(c).Padding(12).Gap(10).Radius(12).
		Background(toColor(dt.SurfaceElevated)).Border(1, glassBorderColor(dt)).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			AppleBadgePill(c, "AUTHORIZING", dt.Warning, dt)
			ui.Textf(c, "Signing in with %s…", pending.ProviderName).
				FontSize(14).FontWeight(700).TextColor(toColor(dt.Ink))
		})

		if pending.UserCode != "" {
			ui.Text(c, "Enter this one-time device code in your browser:").
				FontSize(12).TextColor(toColor(dt.InkSecondary))
			ui.Row(c).Padding(10, 14).Radius(10).AlignItems(ui.Center).Gap(10).
				Background(toColor(dt.SurfaceRecessed)).Border(1, rgbaFromRGB(dt.Accent, 0.4)).Children(func() {
				ui.Text(c, pending.UserCode).FontSize(18).FontWeight(700).TextColor(toColor(dt.Accent))
				ui.Spacer(c)
				if pending.VerificationURI != "" {
					ui.Text(c, pending.VerificationURI).FontSize(11.5).TextColor(toColor(dt.InkMuted))
				}
			})
		} else {
			ui.Text(c, "Complete sign-in in your system browser. Marlin listens on a single-use 127.0.0.1 loopback callback.").
				FontSize(12).TextColor(toColor(dt.InkSecondary))
		}

		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if pending.AuthorizeURL != "" {
				if ApplePillButton(c, "auth-copy-url", "Copy Sign-In URL", BtnGlass, dt, nil, true).Clicked() {
					if ac.OnCopyText != nil {
						ac.OnCopyText(pending.AuthorizeURL)
					}
					ac.setStatus("Copied authorization URL to clipboard.", "")
				}
			}
			if ApplePillButton(c, "auth-cancel-oauth", "Cancel", BtnDestructive, dt, svgIconXMark, true).Clicked() {
				ac.Manager.CancelPending()
				ac.setStatus("", "")
			}
		})
	})
}

func (ac *AuthController) renderPendingEmail(c *ui.Context, dt DerivedTheme, pending *auth.PendingEmailChallenge) {
	ui.Column(c).Padding(12).Gap(10).Radius(12).
		Background(toColor(dt.SurfaceElevated)).Border(1, glassBorderColor(dt)).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			AppleBadgePill(c, "CHECK EMAIL", dt.Info, dt)
			ui.Textf(c, "Verification sent to %s", pending.Email).
				FontSize(13.5).FontWeight(700).TextColor(toColor(dt.Ink))
		})

		placeholder := "Enter 6-digit verification code..."
		if pending.Method == auth.MethodEmailMagicLink {
			placeholder = "Paste magic link verification token..."
		}
		ui.Row(c).Padding(8, 12).Radius(12).
			Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
			ui.TextInputBase(c.Key("auth-code-input"), &ac.codeInput).
				Placeholder(placeholder).
				FontSize(14).TextColor(toColor(dt.Ink)).Grow(1).AutoFocus()
		})

		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Textf(c, "Attempts %d/%d", pending.Attempts, pending.MaxAttempts).
				FontSize(11).TextColor(toColor(dt.InkMuted))
			ui.Spacer(c)
			if ApplePillButton(c, "auth-cancel-email", "Cancel", BtnGlass, dt, svgIconXMark, true).Clicked() {
				ac.ClearSensitiveInputs()
				ac.Manager.CancelPending()
				ac.setStatus("", "")
			}
			if ApplePillButton(c, "auth-verify-email", "Verify & Sign In", BtnPrimary, dt, svgIconCheck, false).Clicked() {
				ac.mu.Lock()
				code := ac.codeInput
				ac.codeInput = "" // scrub OTP immediately from UI memory
				ac.mu.Unlock()
				if _, err := ac.Manager.VerifyEmailLogin(context.Background(), code); err != nil {
					ac.setStatus("", err.Error())
				} else {
					ac.mu.Lock()
					ac.addingAccount = false
					ac.mu.Unlock()
					ac.setStatus("Signed in securely via verified email.", "")
				}
			}
		})
	})
}

func (ac *AuthController) renderAccountCenter(
	c *ui.Context,
	dt DerivedTheme,
	snap auth.AuthSnapshot,
	active auth.AccountProfile,
) {
	// Active account security card.
	ui.Column(c).Padding(14).Gap(8).Radius(14).
		Background(toColor(dt.SurfaceElevated)).Border(1, rgbaFromRGB(dt.Accent, 0.35)).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Box(c).Size(9, 9).Radius(4.5).Background(toColor(dt.Success))
			ui.Text(c, active.DisplayName).FontSize(15).FontWeight(700).TextColor(toColor(dt.Ink))
			AppleBadgePill(c, strings.ToUpper(active.ProviderName), dt.Accent, dt)
			if active.EmailVerified {
				AppleBadgePill(c, "VERIFIED", dt.Success, dt)
			}
		})
		if active.Email != "" {
			ui.Text(c, active.Email).FontSize(12.5).TextColor(toColor(dt.InkSecondary))
		}
		expiryNote := "Active session"
		if !active.ExpiresAt.IsZero() {
			rem := time.Until(active.ExpiresAt).Truncate(time.Second)
			if rem > 0 {
				expiryNote = fmt.Sprintf("Token valid (%s remaining)", rem)
			} else if active.HasRefresh {
				expiryNote = "Auto-refresh armed on next RPC"
			} else {
				expiryNote = "Session expired — re-sign in required"
			}
		}
		ui.Text(c, expiryNote).FontSize(11.5).TextColor(toColor(dt.InkMuted))

		if len(active.Scopes) > 0 {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "SCOPES:").FontSize(10).FontWeight(700).TextColor(toColor(dt.InkMuted))
				limit := min(len(active.Scopes), 5)
				for i := 0; i < limit; i++ {
					AppleBadgePill(c, active.Scopes[i], dt.Info, dt)
				}
			})
		}

		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if active.HasRefresh {
				if ApplePillButton(c, "auth-refresh", "Refresh Token", BtnGlass, dt, nil, true).Clicked() {
					var err error
					if ac.OnRefresh != nil {
						err = ac.OnRefresh(active.ID)
					} else {
						_, err = ac.Manager.ValidAccessToken(context.Background(), active.ID)
					}
					if err != nil {
						ac.setStatus("", err.Error())
					} else {
						ac.setStatus("Session token verified/refreshed cleanly.", "")
					}
				}
			}
			if ApplePillButton(c, "auth-signout", "Sign Out", BtnDestructive, dt, svgIconXMark, true).Clicked() {
				if err := ac.Manager.SignOut(context.Background(), active.ID); err != nil {
					ac.setStatus("", err.Error())
				} else {
					ac.setStatus("Signed out and scrubbed vaulted credentials.", "")
				}
			}
		})
	})

	// Multi-account switcher when more than 1 account is connected.
	if len(snap.Accounts) > 1 {
		ui.Text(c, "CONNECTED ACCOUNTS").FontSize(10.5).FontWeight(700).TextColor(toColor(dt.InkMuted))
		ui.Column(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
			for _, acc := range snap.Accounts {
				item := acc
				isCurrent := item.ID == snap.ActiveAccountID
				ui.Row(c).Padding(8, 10).Radius(10).Gap(8).AlignItems(ui.Center).
					Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
					ui.Textf(c, "%s (%s)", item.DisplayName, item.ProviderName).
						FontSize(12.5).FontWeight(600).TextColor(toColor(dt.Ink))
					ui.Spacer(c)
					if isCurrent {
						AppleBadgePill(c, "ACTIVE", dt.Success, dt)
					} else {
						if ApplePillButton(c, "auth-switch-"+string(item.ID), "Switch", BtnTinted, dt, nil, true).Clicked() {
							_ = ac.Manager.SwitchAccount(item.ID)
							ac.setStatus("Switched active account to "+item.DisplayName, "")
						}
					}
				})
			}
		})
	}

	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if ApplePillButton(c, "auth-add-account", "+ Add Account", BtnTinted, dt, nil, true).Clicked() {
			ac.mu.Lock()
			ac.addingAccount = true
			ac.statusError = ""
			ac.statusMessage = ""
			ac.mu.Unlock()
		}
		ui.Spacer(c)
		if len(snap.Accounts) > 1 {
			if ApplePillButton(c, "auth-signout-all", "Sign Out All", BtnGlass, dt, nil, true).Clicked() {
				_ = ac.Manager.SignOutAll(context.Background())
				ac.setStatus("Signed out all accounts.", "")
			}
		}
	})
}

func (ac *AuthController) startOAuthFlow(providerID auth.ProviderID) {
	if ac.OnStartOAuth != nil {
		if err := ac.OnStartOAuth(providerID); err != nil {
			ac.setStatus("", err.Error())
		}
		return
	}
	flow, err := ac.Manager.BeginOAuth(providerID, "http://127.0.0.1:0/oauth/callback")
	if err != nil {
		ac.setStatus("", err.Error())
		return
	}
	ac.setStatus("Authorization URL ready for "+flow.ProviderName+".", "")
}

func (ac *AuthController) setStatus(msg, errMsg string) {
	ac.mu.Lock()
	ac.statusMessage = msg
	ac.statusError = errMsg
	ac.mu.Unlock()
	if ac.OnChanged != nil {
		ac.OnChanged()
	}
}
