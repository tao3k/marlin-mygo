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

func TestMarketplaceThreeStreamsHotApplyThemesAndLayouts(t *testing.T) {
	reg := transport.NewTargetRegistry()
	_ = reg.Register(&projectionTestTarget{meta: transport.TargetMeta{
		ID: "local", DisplayName: "Local", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})
	slots := shell.NewSlotRegistry()
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle, FallbackRenderer: "main.fb",
	}, NewUIElementRenderer("main.fb", true, slots, func() schema.UIElement {
		return schema.UIElement{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Host Fallback"}}}
	}))
	wb := shell.NewWorkbenchController(slots)
	_ = wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation: 1, ProfileName: "streams-test", Theme: StudioDarkPalette(),
		FrameTree: schema.UIElement{Kind: schema.ElemSlot, SlotID: "workbench.main"},
	})

	bufMain := shell.PaneBuffer{BufferID: "slot:workbench.main", Title: "Workspace Panel", Kind: "webview"}
	bufDock := shell.PaneBuffer{BufferID: "slot:workbench.inspector", Title: "Focus Clock", Kind: "wasm-gpu"}
	bufChat := shell.PaneBuffer{BufferID: "builtin:transcript", Title: "AI Transcript", Kind: "native-gpu"}
	bufProj := shell.PaneBuffer{BufferID: "builtin:projection", Title: "Projection Inspector", Kind: "native-gpu"}
	tree := shell.NewWindowTree(bufMain)
	tree.ApplyBuiltinPerspective("focus", bufMain, bufDock, bufChat, bufProj)

	app := NewWorkbenchApp(reg, slots, wb)
	app.Transcript = NewTranscriptController()
	app.Windows = tree
	app.WindowBuffers = []shell.PaneBuffer{bufMain, bufDock, bufChat, bufProj}

	mkt := NewMarketplaceController()
	themes := append(DefaultOfficialMarketplaceThemes(), MarketplaceThemeItem{
		ThemeID:     "rogue.theme-unsigned",
		Name:        "Rogue Neon",
		Appearance:  "dark",
		PublisherID: "rogue.vendor",
		Summary:     "Unofficial unvetted theme must be blocked in Official-Only mode.",
		TrustTier:   "unofficial",
		Base:        StudioDarkPalette(),
	})
	layouts := append(DefaultOfficialMarketplaceLayouts(), MarketplaceLayoutItem{
		LayoutID:    "rogue.layout-unofficial",
		Name:        "Rogue Split",
		Perspective: "rogue",
		PublisherID: "rogue.vendor",
		Summary:     "Unofficial layout must be blocked in Official-Only mode.",
		TrustTier:   "unofficial",
		Spec: shell.LayoutSpec{
			Perspective: "rogue",
			Root:        &shell.LayoutNodeSpec{BufferID: "slot:workbench.main"},
		},
	})
	mkt.SetThemes(themes)
	mkt.SetLayouts(layouts)

	var appliedThemeID string
	var syncCalled bool
	mkt.OnSyncOfficial = func() error {
		syncCalled = true
		return nil
	}
	mkt.OnApplyTheme = func(item MarketplaceThemeItem) error {
		if item.TrustTier != "official" {
			t.Fatalf("unofficial theme %q must never reach OnApplyTheme", item.ThemeID)
		}
		appliedThemeID = item.ThemeID
		cur, _ := wb.Snapshot()
		next := *cur
		next.Generation++
		next.Theme = item.Base
		return wb.AdmitSnapshot(next)
	}
	mkt.OnApplyLayout = func(item MarketplaceLayoutItem) error {
		if item.TrustTier != "official" {
			t.Fatalf("unofficial layout %q must never reach OnApplyLayout", item.LayoutID)
		}
		return app.ApplyLayoutSpec(item.Spec)
	}

	app.Marketplace = mkt
	app.marketplaceOpen = true

	tt := ui.NewTester(app.View, 1280, 900)

	// 1. Trigger Sync Official button from header.
	if err := tt.Click("Sync Official"); err != nil {
		t.Fatalf("click Sync Official failed: %v", err)
	}
	if !syncCalled {
		t.Fatal("expected Sync Official callback to execute")
	}

	// 2. Switch to Themes stream -> verify unofficial theme blocked & official Paper Light applies live!
	if err := tt.Click("Themes"); err != nil {
		t.Fatalf("click Themes tab failed: %v", err)
	}
	if mkt.ActiveStream() != StreamThemes {
		t.Fatalf("expected active stream %q, got %q", StreamThemes, mkt.ActiveStream())
	}
	if !tt.HasText("BLOCKED · OFFICIAL ONLY") {
		t.Fatalf("expected unofficial theme to render BLOCKED badge, texts=%q", tt.Texts())
	}
	if err := tt.Click("Apply Rogue Neon"); err == nil {
		t.Fatal("unofficial theme must not expose Apply button in Official-Only mode")
	}
	if err := tt.Click("Apply Paper Light"); err != nil {
		t.Fatalf("click Apply Paper Light failed: %v", err)
	}
	if appliedThemeID != "tao3k.theme-paper" {
		t.Fatalf("expected applied theme tao3k.theme-paper, got %q", appliedThemeID)
	}
	snap, _ := wb.Snapshot()
	if snap.Theme != StudioLightPalette() {
		t.Fatalf("expected live WorkbenchController theme to switch to StudioLightPalette, got %+v", snap.Theme)
	}

	// 3. Switch to Layouts stream -> verify unofficial layout blocked & official Cockpit applies live!
	if err := tt.Click("Layouts"); err != nil {
		t.Fatalf("click Layouts tab failed: %v", err)
	}
	if mkt.ActiveStream() != StreamLayouts {
		t.Fatalf("expected active stream %q, got %q", StreamLayouts, mkt.ActiveStream())
	}
	if err := tt.Click("Apply Rogue Split"); err == nil {
		t.Fatal("unofficial layout must not expose Apply button in Official-Only mode")
	}
	if err := tt.Click("Apply AI Engineering Cockpit"); err != nil {
		t.Fatalf("click Apply AI Engineering Cockpit failed: %v", err)
	}
	winSnap := tree.Snapshot()
	if winSnap.Perspective != "cockpit" || winSnap.LeafCount != 3 {
		t.Fatalf("expected live Emacs WindowTree perspective=cockpit with 3 panes, got perspective=%q leaves=%d",
			winSnap.Perspective, winSnap.LeafCount)
	}
}
