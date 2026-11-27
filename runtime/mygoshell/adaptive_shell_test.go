// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestAdaptiveShellUnifiesDesktopIOSAndroidAndThemeSync(t *testing.T) {
	reg := transport.NewTargetRegistry()
	_ = reg.Register(&projectionTestTarget{meta: transport.TargetMeta{
		ID: "studio", DisplayName: "Marlin Core", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})
	slots := shell.NewSlotRegistry()
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle, FallbackRenderer: "fb.main",
	}, NewUIElementRenderer("fb.main", true, slots, func() schema.UIElement {
		return schema.UIElement{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Adaptive Main Pane"}}}
	}))

	var lastTheme DerivedTheme
	as := NewAdaptiveShell(AdaptiveShellConfig{
		BrandTitle:     "Marlin Studio",
		InitialThemeID: "midnight",
		Targets:        reg,
		Slots:          slots,
		OnThemeChange:  func(dt DerivedTheme) { lastTheme = dt },
	})

	// Wide viewport (>= 720px) automatically renders Desktop Emacs multi-pane shell.
	if got := as.EffectiveFormFactor(1280); got != FormFactorDesktop {
		t.Fatalf("expected FormFactorDesktop at 1280px, got %s", got)
	}
	deskTT := ui.NewTester(as.View, 1280, 820)
	if !deskTT.HasText("Adaptive Main Pane") || !deskTT.HasText("Cockpit") {
		t.Fatalf("expected desktop Emacs shell on 1280px viewport, got %q", deskTT.Texts())
	}

	// Compact viewport (< 720px) automatically renders Phone Companion shell.
	if got := as.EffectiveFormFactor(393); got != FormFactorIOS {
		t.Fatalf("expected FormFactorIOS at 393px, got %s", got)
	}
	mobTT := ui.NewTester(as.View, 393, 852)
	if !mobTT.HasText("Marlin Mobile") || !mobTT.HasText("Today's Agenda") {
		t.Fatalf("expected iOS phone shell on 393px viewport, got %q", mobTT.Texts())
	}

	// Theme switch updates Desktop, Mobile, and OnThemeChange callback simultaneously.
	dt := as.ApplyThemePreset("tokyo-night")
	if dt.ID != "tokyo-night" || lastTheme.ID != "tokyo-night" {
		t.Fatalf("expected synchronized tokyo-night theme, got dt=%q cb=%q", dt.ID, lastTheme.ID)
	}

	// Explicit Android override renders Material 3 channels even on wide preview.
	as.SetFormFactor(FormFactorAndroid)
	andTT := ui.NewTester(as.View, 412, 915)
	if !andTT.HasText("Android") {
		t.Fatalf("expected Android phone chrome when FormFactorAndroid is set, got %q", andTT.Texts())
	}
}
