// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/auth"
)

// LoginWindowConfig configures a dedicated standalone Tier-0 Login &
// Authorization window (suitable for mygo.NewWindow or full-screen auth gate).
type LoginWindowConfig struct {
	BrandTitle        string
	BrandSubtitle     string
	InitialThemeID    string
	Auth              *AuthController
	AllowOfflineMode  bool
	OnAuthenticated   func(profile auth.AccountProfile)
	OnContinueOffline func()
	OnThemeChange     func(DerivedTheme)
}

// LoginWindowApp renders Marlin's dedicated standalone Sign-In & Security Window.
// On desktop viewports (>= 720px), it renders a split two-column Liquid Glass
// experience (left Brand & Security Trust Panel + right Interactive Auth Card).
// On compact viewports (< 720px), it adapts cleanly to a single-column layout.
type LoginWindowApp struct {
	BrandTitle        string
	BrandSubtitle     string
	Auth              *AuthController
	AllowOfflineMode  bool
	OnAuthenticated   func(profile auth.AccountProfile)
	OnContinueOffline func()
	OnThemeChange     func(DerivedTheme)

	mu    sync.RWMutex
	theme ThemePreset
}

// NewLoginWindowApp creates a standalone Tier-0 Login Interface window view.
func NewLoginWindowApp(cfg LoginWindowConfig) *LoginWindowApp {
	title := cfg.BrandTitle
	if title == "" {
		title = "Marlin Studio"
	}
	subtitle := cfg.BrandSubtitle
	if subtitle == "" {
		subtitle = "Tier-0 Host Authentication & Multi-Machine Security"
	}
	return &LoginWindowApp{
		BrandTitle:        title,
		BrandSubtitle:     subtitle,
		Auth:              cfg.Auth,
		AllowOfflineMode:  cfg.AllowOfflineMode,
		OnAuthenticated:   cfg.OnAuthenticated,
		OnContinueOffline: cfg.OnContinueOffline,
		OnThemeChange:     cfg.OnThemeChange,
		theme:             ThemeByID(cfg.InitialThemeID),
	}
}

// CycleTheme advances the Login Window across Marlin's 5 curated themes.
func (w *LoginWindowApp) CycleTheme() DerivedTheme {
	w.mu.Lock()
	w.theme = NextThemePreset(w.theme.ID)
	preset := w.theme
	cb := w.OnThemeChange
	w.mu.Unlock()
	dt := DeriveTheme(preset.ID, preset.Name, preset.Base)
	if cb != nil {
		cb(dt)
	}
	return dt
}

// View is the ui.View callback passed directly to mygo.NewWindow or ui.NewTester.
func (w *LoginWindowApp) View(c *ui.Context) {
	w.mu.RLock()
	preset := w.theme
	w.mu.RUnlock()

	dt := DeriveTheme(preset.ID, preset.Name, preset.Base)
	t := ApplyMarlinTheme(c, preset.Base)

	vw, _ := c.Size()
	compact := vw > 0 && vw < DefaultCompactBreakpoint

	ui.Column(c.Key("login-window-root")).Fill().Padding(20).Gap(16).
		Background(toColor(dt.Canvas)).AlignItems(ui.Stretch).Children(func() {
		// Top window bar: Brand mark + Live Theme Switcher + Offline escape hatch.
		ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
			AppleBadgePill(c, "TIER-0 LOGIN WINDOW", dt.Accent, dt)
			ui.Text(c, w.BrandTitle).FontSize(15).FontWeight(700).TextColor(toColor(dt.Ink))
			ui.Spacer(c)
			if AppleSwatchPill(c, "login-cycle-theme", preset.Name, dt.Accent, false, dt).Clicked() {
				w.CycleTheme()
			}
			if w.AllowOfflineMode && w.OnContinueOffline != nil {
				if ApplePillButton(c, "login-offline", "Continue Offline", BtnGlass, dt, nil, true).Clicked() {
					w.OnContinueOffline()
				}
			}
		})

		if compact {
			ui.Column(c).Grow(1).Gap(14).AlignItems(ui.Stretch).Children(func() {
				w.renderInteractiveAuthCard(c, t, dt)
			})
			return
		}

		// Desktop two-column split Login Interface.
		ui.Row(c).Grow(1).Gap(20).AlignItems(ui.Stretch).Children(func() {
			// Left column: Brand Welcome + Live OS Keychain & Security Trust Guarantees.
			ui.Column(c).Width(330).Padding(20).Gap(14).Radius(16).
				Background(toColor(dt.Surface)).Border(1, glassBorderColor(dt)).
				AlignItems(ui.Stretch).Children(func() {
				ui.Text(c, "Welcome to "+w.BrandTitle).
					FontSize(22).FontWeight(700).LetterSpacing(-0.4).TextColor(toColor(dt.Ink))
				ui.Text(c, w.BrandSubtitle).
					FontSize(12.5).LineHeight(1.4).TextColor(toColor(dt.InkSecondary))

				AuthTrustFeatureRow(c, dt, svgIconShield,
					"OAuth 2.1 PKCE S256 + OIDC JWKS",
					"External system browser only; RS256/ES256/EdDSA verified with CSRF & nonce binding.")
				AuthTrustFeatureRow(c, dt, svgIconLock,
					"OS Native Keychain + AES-256-GCM",
					"Master wrapping key sealed in macOS Keychain / Linux Secret Service; zero plaintext disk tokens.")
				AuthTrustFeatureRow(c, dt, svgIconKey,
					"Zero Untrusted Plugin Exposure",
					"Tokens stay inside Tier-0 Host memory; plugins receive scoped receipts only.")

				ui.Spacer(c)
				if w.Auth != nil {
					vaultInfo := w.Auth.CurrentVaultInfo()
					AuthVaultStatusCard(c, dt, vaultInfo, w.Auth.CanToggleVaultLock(), w.Auth.ToggleVaultLock)
				}
			})

			// Right column: Interactive OAuth / Email / Multi-Account Auth Card.
			ui.Column(c).Grow(1).MinWidth(0).AlignItems(ui.Stretch).Children(func() {
				w.renderInteractiveAuthCard(c, t, dt)
			})
		})
	})
}

func (w *LoginWindowApp) renderInteractiveAuthCard(c *ui.Context, t *ui.Theme, dt DerivedTheme) {
	ui.Column(c).Grow(1).Padding(20).Gap(12).Radius(16).
		Background(toColor(dt.SurfaceElevated)).Border(1, glassBorderColor(dt)).
		AlignItems(ui.Stretch).Children(func() {
		if w.Auth == nil {
			ui.Text(c, "Authentication controller not configured.").TextColor(toColor(dt.InkMuted))
			return
		}
		w.Auth.RenderBody(c, t, dt)

		if prof, signedIn := w.Auth.Manager.Snapshot().ActiveProfile(); signedIn && w.OnAuthenticated != nil {
			ui.Spacer(c)
			ui.Row(c).Justify(ui.End).Gap(10).Children(func() {
				if ApplePillButton(c, "login-launch-workspace", "Launch Workspace", BtnPrimary, dt, svgIconArrowUp, false).Clicked() {
					w.OnAuthenticated(prof)
				}
			})
		}
	})
}
