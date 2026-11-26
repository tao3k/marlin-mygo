// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell_test

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/promptgate"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

func TestDesktopPromptGateAskOptionAndAuthModalEndToEnd(t *testing.T) {
	targets := transport.NewTargetRegistry()
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)

	var resolved []promptgate.PromptResponse
	app.PromptGate.OnResolve = func(_ promptgate.PromptRequest, resp promptgate.PromptResponse) {
		resolved = append(resolved, resp)
	}

	// Queue all 3 prompt families simultaneously (verifies FIFO queue + 1/3 PENDING badge).
	if err := app.ShowAskPrompt(promptgate.PromptRequest{
		ID:          "desk-ask-1",
		Title:       "Clarify Target",
		Prompt:      "Which environment should Marlin verify?",
		Placeholder: "production or staging",
	}); err != nil {
		t.Fatalf("ShowAskPrompt: %v", err)
	}
	if err := app.ShowOptionPrompt(promptgate.PromptRequest{
		ID:          "desk-opt-1",
		Title:       "Choose Test Scope",
		Prompt:      "Pick verification suites to execute",
		MultiSelect: true,
		AllowCustom: true,
		Choices: []promptgate.OptionChoice{
			{ID: "race", Label: "Go Race Suite", Description: "Concurrent engine & UI checks", Recommended: true},
			{ID: "wasm", Label: "WASM Build", Description: "GOOS=js GOARCH=wasm target"},
		},
	}); err != nil {
		t.Fatalf("ShowOptionPrompt: %v", err)
	}
	if err := app.ShowAuthPrompt(promptgate.PromptRequest{
		ID:             "desk-auth-1",
		Title:          "Authoritative Approval Gate",
		Prompt:         "Deploy signed package to production?",
		ToolName:       "deploy.release.publish",
		AuthFamily:     promptgate.AuthGraphHumanGate,
		Risk:           promptgate.RiskHigh,
		DetailsHeading: "Scope: production-us-east",
		DetailsSummary: "Uploads immutable artifact digest sha256:abcd1234",
	}); err != nil {
		t.Fatalf("ShowAuthPrompt: %v", err)
	}

	tt := ui.NewTester(app.View, 1200, 820)
	tt.Frame()

	// 1. Ask modal is at head with "1 / 3 PENDING"; empty submit fails closed with inline error.
	if !tt.HasText("ASK QUESTION") || !tt.HasText("1 / 3 PENDING") {
		t.Fatalf("expected Ask modal with 1 / 3 PENDING, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Submit Answer"); err != nil {
		t.Fatalf("click Submit Answer (empty): %v", err)
	}
	tt.Frame()
	if !tt.HasText("answer is required") || len(resolved) != 0 {
		t.Fatalf("expected fail-closed validation message on empty Ask submit, got %q", strings.Join(tt.Texts(), " | "))
	}
	app.PromptGate.SetTextInput("staging")
	tt.Frame()
	if err := tt.Click("Submit Answer"); err != nil {
		t.Fatalf("click Submit Answer (filled): %v", err)
	}
	tt.Frame()
	if len(resolved) != 1 || resolved[0].TextInput != "staging" || resolved[0].Action != promptgate.ActionSubmit {
		t.Fatalf("expected Ask resolved with 'staging', got %+v", resolved)
	}

	// 2. Option modal advances automatically to head ("1 / 2 PENDING"): pick both options + confirm!
	if !tt.HasText("SELECT OPTION") || !tt.HasText("MULTI-SELECT") || !tt.HasText("Recommended") || !tt.HasText("1 / 2 PENDING") {
		t.Fatalf("expected Option modal at head, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Go Race Suite"); err != nil {
		t.Fatalf("click Go Race Suite: %v", err)
	}
	if err := tt.Click("WASM Build"); err != nil {
		t.Fatalf("click WASM Build: %v", err)
	}
	if err := tt.Click("Confirm Choice"); err != nil {
		t.Fatalf("click Confirm Choice: %v", err)
	}
	tt.Frame()
	if len(resolved) != 2 || len(resolved[1].SelectedIDs) != 2 || resolved[1].SelectedIDs[0] != "race" || resolved[1].SelectedIDs[1] != "wasm" {
		t.Fatalf("expected multi-select Option resolved with [race wasm], got %+v", resolved)
	}

	// 3. Tier-0 Authorization modal advances to head: ESC MUST NOT dismiss it; Request Patch with note resolves!
	if !tt.HasText("TIER-0 AUTHORIZATION") || !tt.HasText("HIGH RISK") || !tt.HasText("GRAPH HUMAN GATE") || !tt.HasText("deploy.release.publish") {
		t.Fatalf("expected Tier-0 Authorization modal at head, got %q", strings.Join(tt.Texts(), " | "))
	}
	if app.DismissTopOverlay() {
		t.Fatal("security violation: DismissTopOverlay must NEVER dismiss Tier-0 Authorization prompt")
	}
	if !app.PromptGate.HasPending() {
		t.Fatal("expected Tier-0 Authorization gate still pending after ESC")
	}
	app.PromptGate.SetTextInput("Add canary verification step")
	tt.Frame()
	if err := tt.Click("Request Patch"); err != nil {
		t.Fatalf("click Request Patch: %v", err)
	}
	tt.Frame()
	if len(resolved) != 3 || resolved[2].Action != promptgate.ActionRequestPatch || resolved[2].TextInput != "Add canary verification step" {
		t.Fatalf("expected Auth gate resolved with RequestPatch, got %+v", resolved)
	}
	if app.PromptGate.HasPending() {
		t.Fatal("expected prompt queue empty after resolving all 3 prompts")
	}
}

func TestMobilePromptGateBottomSheetsZeroOverflowAcrossIOSAndAndroid(t *testing.T) {
	for _, tc := range []struct {
		plat mygoshell.MobilePlatform
		dev  string
		w, h int
	}{
		{mygoshell.MobilePlatformIOS, "iphone16", 393, 852},
		{mygoshell.MobilePlatformAndroid, "pixel9", 412, 915},
	} {
		mob := mygoshell.NewMobileCompanionAppForPlatform(tc.plat, tc.dev, "")
		var resolved []promptgate.PromptResponse
		mob.PromptGate.OnResolve = func(_ promptgate.PromptRequest, resp promptgate.PromptResponse) {
			resolved = append(resolved, resp)
		}

		// Enqueue Option + Authorization bottom sheets while user is on Chat tab!
		_ = mob.ShowOptionPrompt(promptgate.PromptRequest{
			ID:     "mob-opt-1",
			Title:  "Select Model Route",
			Prompt: "Choose inference endpoint for this turn",
			Choices: []promptgate.OptionChoice{
				{ID: "local", Label: "Local Sidecar", Description: "Low-latency Apple Silicon host", Recommended: true},
				{ID: "cloud", Label: "Remote H100", Description: "mTLS cluster node"},
			},
		})
		_ = mob.ShowAuthPrompt(promptgate.PromptRequest{
			ID:             "mob-auth-1",
			Prompt:         "Allow Marlin to send calendar reminder?",
			ToolName:       "device.ios.notify.send",
			AuthFamily:     promptgate.AuthDeviceCapability,
			Risk:           promptgate.RiskHigh,
			DetailsHeading: "One-shot GateReceipt protected",
		})

		tt := ui.NewTester(mob.View, tc.w, tc.h)
		tt.Frame()

		// Option bottom sheet floats immediately over Chat tab without navigating away!
		if !tt.HasText("SELECT OPTION") || !tt.HasText("Local Sidecar") || !tt.HasText("1 / 2 PENDING") {
			t.Fatalf("expected mobile Option bottom sheet (%s), got %q", tc.plat, strings.Join(tt.Texts(), " | "))
		}
		if err := tt.Click("Local Sidecar"); err != nil {
			t.Fatalf("click Local Sidecar (%s): %v", tc.plat, err)
		}
		if err := tt.Click("Confirm Choice"); err != nil {
			t.Fatalf("click Confirm Choice (%s): %v", tc.plat, err)
		}
		tt.Frame()
		if len(resolved) != 1 || len(resolved[0].SelectedIDs) != 1 || resolved[0].SelectedIDs[0] != "local" {
			t.Fatalf("expected mobile Option resolved with [local] (%s), got %+v", tc.plat, resolved)
		}

		// Authorization bottom sheet immediately surfaces next!
		if !tt.HasText("TIER-0 AUTHORIZATION") || !tt.HasText("HIGH RISK") || !tt.HasText("Approve Once") {
			t.Fatalf("expected mobile Auth bottom sheet (%s), got %q", tc.plat, strings.Join(tt.Texts(), " | "))
		}
		if err := tt.Click("Approve Once"); err != nil {
			t.Fatalf("click Approve Once (%s): %v", tc.plat, err)
		}
		tt.Frame()
		if len(resolved) != 2 || resolved[1].Action != promptgate.ActionApproveOnce || mob.PromptGate.HasPending() {
			t.Fatalf("expected mobile Auth approved and queue empty (%s), got %+v", tc.plat, resolved)
		}
	}

	// Verify AdaptiveShell shares the exact same PromptGate controller across Desktop & Mobile.
	as := mygoshell.NewAdaptiveShell(mygoshell.AdaptiveShellConfig{BrandTitle: "Marlin"})
	if as.Desktop.PromptGate == nil || as.Desktop.PromptGate != as.Mobile.PromptGate {
		t.Fatal("expected AdaptiveShell Desktop and Mobile to share the same PromptGateController")
	}
}
