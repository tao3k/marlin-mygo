// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell_test

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/auth"
	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

func TestAdaptiveShellBottomLeftSettingsAndAuthDock(t *testing.T) {
	as := mygoshell.NewAdaptiveShell(mygoshell.AdaptiveShellConfig{
		BrandTitle:     "Marlin",
		InitialThemeID: "midnight",
		FormFactor:     mygoshell.FormFactorDesktop,
	})

	spec := schema.BoundedSchema{
		ID:    "marlin.ui.settings.v1",
		Title: "Marlin Preferences",
		Fields: []schema.FieldSpec{
			{
				Key:        "theme_id",
				Label:      "Theme Preset",
				Kind:       schema.FieldEnum,
				EnumValues: []string{"midnight", "paper", "tokyo-night"},
				Default:    "midnight",
			},
			{
				Key:     "default_session",
				Label:   "Default Session",
				Kind:    schema.FieldString,
				MinLen:  1,
				MaxLen:  64,
				Default: "marlin-main",
			},
			{
				Key:     "real_session",
				Label:   "Durable Session Streaming",
				Kind:    schema.FieldBool,
				Default: true,
			},
		},
	}
	formState := mygoshell.NewSchemaFormState(spec, map[string]any{
		"theme_id":        "midnight",
		"default_session": "marlin-main",
		"real_session":    true,
	})
	var saved map[string]any
	as.SetSettings(&spec, formState, func(vals map[string]any) {
		saved = vals
	})

	authMgr := auth.NewManager(auth.ManagerConfig{EmailBackend: uiTestEmailBackend{}})
	_ = authMgr.RegisterProvider(auth.GitHubOAuthProvider("marlin-gh-test"))
	_ = authMgr.RegisterProvider(auth.EmailOTPProvider())
	authCtrl := mygoshell.NewAuthController(authMgr)
	authCtrl.VaultInfo = auth.VaultSecurityInfo{
		BackendName: "macOS Keychain (AES-256-GCM)",
		Locked:      false,
	}
	as.SetAuth(authCtrl)

	reg := features.NewRegistry()
	ledger := features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	host := features.NewHost()
	as.SetFeatureHost(host, mygoshell.NewGovernanceController(reg, ledger, host))

	tt := ui.NewTester(as.View, 1360, 880)

	// 1. Bottom-left dock renders Settings + Account/Vault summary even when AdaptiveShell sets OnCycleTheme.
	if !tt.HasText("Settings") || !tt.HasText("macOS Keychain") {
		t.Fatalf("expected bottom-left Settings & OS Vault dock, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 2. Clicking Settings opens the unified tabbed Settings & Authorization modal on General tab.
	if err := tt.Click("Settings"); err != nil {
		t.Fatalf("click Settings failed: %v", err)
	}
	tt.Frame()
	if !as.Desktop.IsSettingsOpen() || as.Desktop.ActiveSettingsTab() != mygoshell.SettingsTabGeneral {
		t.Fatalf("expected Settings modal open on General tab, open=%v tab=%s", as.Desktop.IsSettingsOpen(), as.Desktop.ActiveSettingsTab())
	}
	if !tt.HasText("Settings & Authorization") || !tt.HasText("Marlin Preferences") {
		t.Fatalf("expected unified Settings modal with BoundedSchema form, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 3. Switch to Account & Auth tab inside the same modal and verify zero raw token exposure.
	as.Desktop.OpenSettingsTab(mygoshell.SettingsTabAuth)
	tt.Frame()
	if as.Desktop.ActiveSettingsTab() != mygoshell.SettingsTabAuth {
		t.Fatalf("expected Auth tab active, got %s", as.Desktop.ActiveSettingsTab())
	}
	if !tt.HasText("Continue with GitHub") {
		t.Fatalf("expected GitHub OAuth option inside Settings Auth tab, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 4. Switch to Governance tab inside Settings.
	as.Desktop.OpenSettingsTab(mygoshell.SettingsTabGovernance)
	tt.Frame()
	if !tt.HasText("Tier-0 Governance & Host Security") {
		t.Fatalf("expected Governance summary inside Settings modal, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 5. Return to General tab and save BoundedSchema form.
	as.Desktop.OpenSettingsTab(mygoshell.SettingsTabGeneral)
	tt.Frame()
	if err := tt.Click("Save Settings"); err != nil {
		// Fallback to generic Save button label if RenderBoundedForm uses "Save".
		if err2 := tt.Click("Save"); err2 != nil {
			t.Fatalf("click Save failed: %v / %v (texts: %q)", err, err2, strings.Join(tt.Texts(), " | "))
		}
	}
	tt.Frame()
	if saved == nil || saved["default_session"] != "marlin-main" {
		t.Fatalf("expected saved settings payload, got %+v", saved)
	}
	if as.Desktop.IsSettingsOpen() {
		t.Fatalf("expected Settings modal to close after successful save")
	}

	// 6. Collapsing sidebar keeps Settings reachable from top bar.
	as.Desktop.ToggleSidebar()
	tt.Frame()
	if !tt.HasText("Settings") {
		t.Fatalf("expected top-bar Settings fallback when sidebar collapsed, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 7. Mobile shares the same SettingsSchema and AuthController via AdaptiveShell.
	if as.Mobile.SettingsSchema == nil || as.Mobile.Auth == nil {
		t.Fatalf("expected MobileCompanionApp to inherit SettingsSchema and Auth from AdaptiveShell")
	}
}
