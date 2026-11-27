// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package canvas_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/resilience"
	"github.com/tao3k/marlin-mygo/features/canvas"
)

func TestCanvasDesktopRichDocVersionsDiffAndRevisionRequest(t *testing.T) {
	ledger := features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	mod := canvas.NewModule(ledger)
	view := mod.View

	var revDoc, revInst string
	var revVer int
	view.Ctrl.OnRequestRevision = func(docID string, version int, instruction string) {
		revDoc = docID
		revVer = version
		revInst = instruction
	}

	theme := ui.DarkTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		view.Render(c, theme, false)
	}, 1220, 820)
	tt.Frame()

	// 1. Renders pre-parsed Org-RZ1 Document IR at v2 (DONE) cleanly.
	for _, want := range []string{"LIVE CANVAS", "architecture.org", "DONE", "Feature-Gated Architecture Plan", "v2 · Reviewer"} {
		if !tt.HasText(want) {
			t.Fatalf("expected Canvas desktop view to render %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}

	// 2. Switch to v1 -> shows TODO snapshot!
	if err := tt.Click("v1"); err != nil {
		t.Fatalf("click v1: %v", err)
	}
	tt.Frame()
	if !tt.HasText("TODO") || !tt.HasText("v1 · Architect") {
		t.Fatalf("expected Canvas v1 TODO snapshot, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 3. Switch back to v2 & toggle Diff v1 -> v2!
	if err := tt.Click("v2"); err != nil {
		t.Fatalf("click v2: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Diff"); err != nil {
		t.Fatalf("click Diff: %v", err)
	}
	tt.Frame()
	if !tt.HasText("VERSION DIFF v1 -> v2") {
		t.Fatalf("expected version diff view, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 4. Switch to registry.go code artifact and submit an inline revision request!
	if err := tt.Click("registry.go"); err != nil {
		t.Fatalf("click registry.go: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Diff"); err != nil {
		t.Fatalf("click Diff off: %v", err)
	}
	tt.Frame()
	if !tt.HasText("1 | package features") {
		t.Fatalf("expected numbered Go code artifact, got %q", strings.Join(tt.Texts(), " | "))
	}

	view.SetRevisionDraft("Add mutex lock comment")
	tt.Frame()
	if err := tt.Click("Revise"); err != nil {
		t.Fatalf("click Revise: %v", err)
	}
	tt.Frame()
	if revDoc != "art-code" || revVer != 2 || revInst != "Add mutex lock comment" || len(ledger.AuditTrail()) == 0 {
		t.Fatalf("unexpected revision callback: doc=%q ver=%d inst=%q", revDoc, revVer, revInst)
	}
}

func TestCanvasMobileCompactViewZeroOverflow(t *testing.T) {
	view := canvas.NewView(canvas.NewController(nil))
	theme := ui.DarkTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		view.Render(c, theme, true)
	}, 393, 852)
	tt.Frame()

	if !tt.HasText("LIVE CANVAS") || !tt.HasText("architecture.org") {
		t.Fatalf("expected mobile Canvas view, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Diff"); err != nil {
		t.Fatalf("mobile click Diff: %v", err)
	}
	tt.Frame()
	if !tt.HasText("VERSION DIFF v1 -> v2") {
		t.Fatalf("expected mobile Canvas diff view, got %q", strings.Join(tt.Texts(), " | "))
	}
}

func TestCanvasPowerLossRevisionDraftRecoveryAndOfflineBanner(t *testing.T) {
	recoveryFile := filepath.Join(t.TempDir(), "canvas-recovery.json")

	res1 := resilience.NewSessionResilience()
	mod1 := canvas.NewModule(nil)
	mod1.SetResilience(res1, nil)
	mod1.View.SetRevisionDraft("Add retry backoff diagram to section 2")
	if err := res1.SaveFile(recoveryFile); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	// Cold restart -> restore draft into brand-new Canvas module & verify OFFLINE banner + Revise submit.
	res2 := resilience.NewSessionResilience()
	if err := res2.LoadFile(recoveryFile); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	res2.MarkOffline("socket closed")
	mod2 := canvas.NewModule(nil)
	mod2.SetResilience(res2, nil)
	if got := mod2.View.RevisionDraft(); got != "Add retry backoff diagram to section 2" {
		t.Fatalf("expected restored canvas draft, got %q", got)
	}

	var gotInst string
	mod2.SetRevisionHandler(func(_ string, _ int, inst string) { gotInst = inst })

	theme := ui.DarkTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		mod2.View.Render(c, theme, false)
	}, 1100, 720)
	tt.Frame()
	if !tt.HasText("OFFLINE") {
		t.Fatalf("expected OFFLINE banner in Canvas view, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Revise"); err != nil {
		t.Fatalf("click Revise on restored draft: %v", err)
	}
	tt.Frame()
	if gotInst != "Add retry backoff diagram to section 2" {
		t.Fatalf("expected restored revision submitted cleanly, got %q", gotInst)
	}
}
