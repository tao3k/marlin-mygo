// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestAdaptiveShellFourTargetParityDesktopIOSAndroidWeb(t *testing.T) {
	slots := shell.NewSlotRegistry()
	for _, s := range []schema.SlotID{"workbench.main", "workbench.secondary", "workbench.inspector"} {
		slotID := s
		_ = slots.Declare(schema.SlotDeclaration{
			SlotID: slotID, Cardinality: schema.SlotSingle, FallbackRenderer: "fb-" + string(slotID),
		}, NewUIElementRenderer("fb-"+string(slotID), true, slots, func() schema.UIElement {
			return schema.UIElement{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Slot " + string(slotID)}}}
		}))
	}

	as := NewAdaptiveShell(AdaptiveShellConfig{
		BrandTitle:     "Marlin Unified",
		InitialThemeID: "midnight",
		FormFactor:     FormFactorAuto,
		Slots:          slots,
	})

	// 1. Desktop (>= 720px): dual-modal keymap, 3-stream Official Marketplace, and 4-pane composable layout work out of the box!
	if as.Desktop.KeyTrie == nil || as.Desktop.Marketplace == nil {
		t.Fatal("AdaptiveShell Desktop must initialize KeyTrie and Official Marketplace automatically")
	}
	desk := ui.NewTester(as.View, 1280, 860)
	if !desk.HasText("NORMAL · SPC") || !desk.HasText("Marketplace") {
		t.Fatalf("expected Desktop chrome with NORMAL · SPC and Marketplace, got %q", desk.Texts())
	}

	// Open Official Marketplace -> Themes -> apply Paper Light -> verify BOTH Desktop and Mobile inherit Paper Light!
	if err := desk.Click("Marketplace"); err != nil {
		t.Fatalf("click Marketplace: %v", err)
	}
	if err := desk.Click("Themes"); err != nil {
		t.Fatalf("click Themes: %v", err)
	}
	if err := desk.Click("Apply Paper Light"); err != nil {
		t.Fatalf("click Apply Paper Light: %v", err)
	}
	if as.Mobile.Theme != StudioLightPalette() {
		t.Fatalf("expected Mobile Companion to sync Paper Light theme live, got %+v", as.Mobile.Theme)
	}

	// Apply 4-pane Git + GitHub Composable Studio layout from Marketplace Layouts tab!
	if err := desk.Click("Layouts"); err != nil {
		t.Fatalf("click Layouts: %v", err)
	}
	if err := desk.Click("Apply Git + GitHub Composable Studio"); err != nil {
		t.Fatalf("click Apply 4-pane layout: %v", err)
	}
	if as.Windows.Snapshot().LeafCount != 4 {
		t.Fatalf("expected 4 panes in AdaptiveShell after applying dev-git-github layout, got %d", as.Windows.Snapshot().LeafCount)
	}

	// Stack an extra tab in the focused pane so we can hibernate & wake it from Mobile iOS/Android!
	_, _ = as.Windows.OpenTabInFocused(shell.PaneBuffer{BufferID: "builtin:projection", Title: "Projection", Kind: "native-gpu"})

	// 2. iOS Phone Viewport (393x852 < 720px): auto-adapts to iOS HIG & renders synced Workspace Tabs Memory Saver!
	ios := ui.NewTester(as.View, 393, 852)
	if as.EffectiveFormFactor(393) != FormFactorIOS {
		t.Fatalf("expected FormFactorIOS at 393px, got %v", as.EffectiveFormFactor(393))
	}
	if err := ios.Click("Packs"); err != nil {
		t.Fatalf("click iOS Packs tab: %v", err)
	}
	ios.Frame()
	if !ios.HasText("WORKSPACE TABS & MEMORY SAVER") {
		t.Fatalf("expected synced Workspace Tabs & Memory Saver card on iOS, got %q", ios.Texts())
	}
	if err := ios.Click("Sleep Projection"); err != nil {
		t.Fatalf("click Sleep Projection on iOS: %v", err)
	}
	ios.Frame()
	if as.Windows.Snapshot().HibernatedTabs < 1 {
		t.Fatalf("expected >=1 hibernated tab after tapping Sleep on iOS, got %+v", as.Windows.Snapshot())
	}

	// 3. Android Phone Viewport (412x915): Material 3 chrome sees the exact same hibernated tab and wakes it!
	as.SetFormFactor(FormFactorAndroid)
	android := ui.NewTester(as.View, 412, 915)
	if !android.HasText("Android Kotlin Native Bridge") || !android.HasText("WORKSPACE TABS & MEMORY SAVER") {
		t.Fatalf("expected Android Material 3 Packs & Memory Saver card, got %q", android.Texts())
	}
	if err := android.Click("Wake Projection"); err != nil {
		t.Fatalf("click Wake Projection on Android: %v", err)
	}
	android.Frame()
	if as.Windows.Snapshot().HibernatedTabs != 0 {
		t.Fatalf("expected 0 hibernated tabs after tapping Wake on Android, got %+v", as.Windows.Snapshot())
	}
}
