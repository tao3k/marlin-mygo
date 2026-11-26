// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/auth"
)

// Pre-parsed Tier-0 Auth vector icons (parsed once at init; zero per-frame alloc).
var (
	svgIconLock = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>`,
	))
	svgIconUnlock = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 9.9-1"/></svg>`,
	))
	svgIconKey = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="7.5" cy="15.5" r="5.5"/><path d="m21 2-9.6 9.6"/><path d="m15.5 7.5 3 3L22 7l-3-3"/></svg>`,
	))
	svgIconMail = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="4" width="20" height="16" rx="2"/><path d="m22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7"/></svg>`,
	))
	svgIconGlobe = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>`,
	))
)

// AuthSecurityBanner renders a quiet, restrained credential storage status line.
func AuthSecurityBanner(c *ui.Context, dt DerivedTheme, vaultInfo auth.VaultSecurityInfo) {
	vaultLabel := "Encrypted Host Vault"
	if vaultInfo.BackendName != "" {
		vaultLabel = vaultInfo.BackendName
	}
	ui.Row(c).Padding(4, 2).Gap(7).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, svgIconShield).Size(13, 13).TextColor(toColor(dt.Success))
		ui.Textf(c, "Credentials stored in %s", vaultLabel).
			FontSize(11.5).TextColor(toColor(dt.InkMuted))
	})
}

// AuthVaultStatusCard renders an interactive OS Native Keychain & AES-256-GCM
// vault security card with optional Lock/Unlock controls.
func AuthVaultStatusCard(
	c *ui.Context,
	dt DerivedTheme,
	vaultInfo auth.VaultSecurityInfo,
	canToggleLock bool,
	onToggleLock func(),
) {
	if vaultInfo.BackendName == "" {
		vaultInfo = auth.VaultSecurityInfo{
			BackendName: "Memory Vault (Ephemeral)",
			CipherSuite: "In-RAM Scrubbed",
		}
	}
	tone := dt.Info
	badgeText := "EPHEMERAL"
	icon := svgIconLock
	if vaultInfo.OSProtected {
		tone = dt.Success
		badgeText = "OS KEYCHAIN"
	}
	if vaultInfo.Locked {
		tone = dt.Warning
		badgeText = "LOCKED"
		icon = svgIconLock
	} else if vaultInfo.OSProtected {
		icon = svgIconUnlock
	}

	ui.Column(c).Padding(10, 12).Gap(6).Radius(12).
		Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icon).Size(14, 14).TextColor(toColor(tone))
			ui.Text(c, vaultInfo.BackendName).FontSize(12.5).FontWeight(700).TextColor(toColor(dt.Ink))
			AppleBadgePill(c, badgeText, tone, dt)
			ui.Spacer(c)
			if canToggleLock && onToggleLock != nil {
				btnLbl := "Lock Vault"
				btnVar := BtnGlass
				if vaultInfo.Locked {
					btnLbl = "Unlock Vault"
					btnVar = BtnPrimary
				}
				if ApplePillButton(c, "auth-vault-lock-toggle", btnLbl, btnVar, dt, icon, true).Clicked() {
					onToggleLock()
				}
			}
		})
		detail := vaultInfo.CipherSuite
		if vaultInfo.VaultPath != "" {
			detail = fmt.Sprintf("%s · %s", vaultInfo.CipherSuite, vaultInfo.VaultPath)
		} else if vaultInfo.FallbackReason != "" {
			detail = fmt.Sprintf("%s (%s)", vaultInfo.CipherSuite, vaultInfo.FallbackReason)
		}
		if detail != "" {
			ui.Text(c, detail).FontSize(10.5).TextColor(toColor(dt.InkMuted))
		}
	})
}

// AuthOAuthProviderButton renders a clean, full-width, neutral sign-in button
// with crisp 8px corners and zero protocol-jargon badge clutter.
func AuthOAuthProviderButton(
	c *ui.Context,
	dt DerivedTheme,
	provider auth.ProviderSummary,
	onClick func(),
) {
	icon := svgIconGlobe
	switch provider.ID {
	case auth.ProviderGoogle:
		icon = svgIconShield
	case auth.ProviderGitHub:
		icon = svgIconKey
	case auth.ProviderOIDC:
		icon = svgIconLock
	}
	label := fmt.Sprintf("Continue with %s", provider.DisplayName)
	btn := ui.ButtonBase(c.Key("auth-oauth-"+string(provider.ID))).Label(label).
		Padding(9, 12).Radius(RadiusControl).
		AlignItems(ui.Center).Justify(ui.Start).Gap(10)
	fill := toColor(dt.SurfaceElevated)
	borderCol := toColor(dt.Border)
	if btn.Hovered() {
		fill = toColor(dt.SurfaceHover)
		borderCol = toColor(dt.BorderStrong)
	} else if dt.Appearance == AppearanceLight {
		fill = ui.RGB(255, 255, 255)
	}
	btn.Background(fill).Border(1, borderCol)
	btn.Children(func() {
		ui.Icon(c, icon).Size(15, 15).TextColor(toColor(dt.InkSecondary))
		ui.Text(c, label).FontSize(13).FontWeight(500).TextColor(toColor(dt.Ink))
		ui.Spacer(c)
		ui.Text(c, "Browser").FontSize(11).TextColor(toColor(dt.InkMuted))
	})
	if btn.Clicked() && onClick != nil {
		onClick()
	}
}

// AuthAccountAvatarPill renders a compact identity capsule with monogram badge,
// live status dot, and account display label for top bars and mobile headers.
func AuthAccountAvatarPill(
	c *ui.Context,
	dt DerivedTheme,
	key string,
	label string,
	signedIn bool,
) ui.Element {
	variant := BtnGlass
	if signedIn {
		variant = BtnTinted
	}
	return ApplePillButton(c, key, label, variant, dt, svgIconShield, true)
}

// AuthScopeChips renders granted OAuth/OIDC scopes as crisp bounded pills.
func AuthScopeChips(c *ui.Context, dt DerivedTheme, scopes []string) {
	if len(scopes) == 0 {
		return
	}
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "SCOPES:").FontSize(10).FontWeight(700).TextColor(toColor(dt.InkMuted))
		limit := min(len(scopes), 5)
		for i := 0; i < limit; i++ {
			AppleBadgePill(c, scopes[i], dt.Info, dt)
		}
	})
}

// AuthEmailInputRow renders a crisp recessed 8px input row for email/OTP fields.
func AuthEmailInputRow(
	c *ui.Context,
	dt DerivedTheme,
	key string,
	icon *ui.SVG,
	value *string,
	placeholder string,
	autoFocus bool,
) {
	ui.Row(c).Padding(7, 10).Gap(8).Radius(RadiusControl).AlignItems(ui.Center).
		Background(toColor(dt.SurfaceRecessed)).Border(1, toColor(dt.Border)).Children(func() {
		if icon != nil {
			ui.Icon(c, icon).Size(14, 14).TextColor(toColor(dt.InkMuted))
		}
		inp := ui.TextInputBase(c.Key(key), value).
			Placeholder(placeholder).
			FontSize(13).TextColor(toColor(dt.Ink)).Grow(1)
		if autoFocus {
			inp.AutoFocus()
		}
	})
}

// AuthTrustFeatureRow renders one security architecture guarantee bullet inside
// the dedicated Login Interface Hero Panel.
func AuthTrustFeatureRow(c *ui.Context, dt DerivedTheme, icon *ui.SVG, title, subtitle string) {
	ui.Row(c).Padding(10, 12).Gap(10).Radius(12).AlignItems(ui.Center).
		Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
		ui.Row(c).Padding(8).Radius(10).Background(rgbaFromRGB(dt.Accent, 0.14)).Children(func() {
			if icon != nil {
				ui.Icon(c, icon).Size(16, 16).TextColor(toColor(dt.Accent))
			}
		})
		ui.Column(c).Gap(2).Grow(1).Children(func() {
			ui.Text(c, title).FontSize(12.5).FontWeight(700).TextColor(toColor(dt.Ink))
			ui.Text(c, subtitle).FontSize(11).TextColor(toColor(dt.InkSecondary))
		})
	})
}
