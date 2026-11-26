// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package compare_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/resilience"
	"github.com/tao3k/marlin-mygo/features/compare"
)

func TestCompareDesktopParallelArenaPromoteAndBranchTree(t *testing.T) {
	ledger := features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	mod := compare.NewModule(ledger)
	view := mod.View

	var promoted compare.ModelLane
	view.Ctrl.OnPromoteWinner = func(ln compare.ModelLane) { promoted = ln }

	theme := ui.DarkTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		view.Render(c, theme, false)
	}, 1220, 820)
	tt.Frame()

	for _, want := range []string{"MODEL COMPARE", "Parallel Multi-Model Arena", "Marlin Core", "Marlin Fast", "64 tok/s", "118 tok/s"} {
		if !tt.HasText(want) {
			t.Fatalf("expected desktop Compare Arena to render %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}

	// Promote Marlin Fast as winner -> updates badge & writes GovernanceLedger receipt!
	if err := tt.Click("Promote Marlin Fast"); err != nil {
		t.Fatalf("click Promote Marlin Fast: %v", err)
	}
	tt.Frame()
	if promoted.ModelID != "marlin-fast" || !tt.HasText("Promoted") || len(ledger.AuditTrail()) == 0 {
		t.Fatalf("expected Marlin Fast promoted with audit receipt, promoted=%+v", promoted)
	}

	// Fork a new non-destructive branch from Marlin Core lane and inspect Branch Tree!
	if err := tt.Click("Branch Marlin Core"); err != nil {
		t.Fatalf("click Branch Marlin Core: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Branches (3)"); err != nil {
		t.Fatalf("click Branches (3): %v", err)
	}
	tt.Frame()
	if !tt.HasText("Marlin Core Fork") || !tt.HasText("NON-DESTRUCTIVE CONVERSATION BRANCH TREE") {
		t.Fatalf("expected forked branch in Branch Tree, got %q", strings.Join(tt.Texts(), " | "))
	}

	// Dispatch a fresh parallel compare prompt across selected models.
	if err := tt.Click("Arena (2)"); err != nil {
		t.Fatalf("click Arena (2): %v", err)
	}
	tt.Frame()
	view.SetPromptDraft("Benchmark zero-CGO WASM latency")
	tt.Frame()
	if err := tt.Click("Compare"); err != nil {
		t.Fatalf("click Compare: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Benchmark zero-CGO WASM latency") || !tt.HasText("STREAMING") {
		t.Fatalf("expected parallel STREAMING lanes after Compare, got %q", strings.Join(tt.Texts(), " | "))
	}
}

func TestCompareMobileCompactLaneSwitcherZeroOverflow(t *testing.T) {
	view := compare.NewView(compare.NewController(nil))
	theme := ui.DarkTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		view.Render(c, theme, true)
	}, 393, 852)
	tt.Frame()

	if !tt.HasText("MODEL COMPARE") || !tt.HasText("Marlin Core") {
		t.Fatalf("expected mobile Compare Arena, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Branches (2)"); err != nil {
		t.Fatalf("mobile click Branches (2): %v", err)
	}
	tt.Frame()
	if !tt.HasText("Fast Latency Path") {
		t.Fatalf("expected mobile Branches list, got %q", strings.Join(tt.Texts(), " | "))
	}
}

func TestComparePowerLossPromptDraftRecoveryAndOfflineBanner(t *testing.T) {
	recoveryFile := filepath.Join(t.TempDir(), "compare-recovery.json")

	res1 := resilience.NewSessionResilience()
	mod1 := compare.NewModule(nil)
	mod1.SetResilience(res1, nil)
	mod1.View.SetPromptDraft("Compare cold-start latency across 3 quantized models")
	if err := res1.SaveFile(recoveryFile); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	// Cold restart -> restore draft into brand-new Compare module & show OFFLINE banner.
	res2 := resilience.NewSessionResilience()
	if err := res2.LoadFile(recoveryFile); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	res2.MarkOffline("network disconnected")
	mod2 := compare.NewModule(nil)
	mod2.SetResilience(res2, nil)
	if got := mod2.View.PromptDraft(); got != "Compare cold-start latency across 3 quantized models" {
		t.Fatalf("expected restored compare draft, got %q", got)
	}

	theme := ui.DarkTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		mod2.View.Render(c, theme, false)
	}, 1100, 720)
	tt.Frame()
	if !tt.HasText("OFFLINE") {
		t.Fatalf("expected OFFLINE banner in Compare view, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Compare"); err != nil {
		t.Fatalf("click Compare on restored draft: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Compare cold-start latency across 3 quantized models") {
		t.Fatalf("expected restored draft submitted to Compare arena, got %q", strings.Join(tt.Texts(), " | "))
	}
}
