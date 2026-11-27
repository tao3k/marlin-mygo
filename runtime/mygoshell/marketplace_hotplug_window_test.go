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

func TestOfficialOnlyMarketplaceBlocksUnofficialAndSyncsWindowTreeOnHotPlug(t *testing.T) {
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
		Generation: 1, ProfileName: "official-test", Theme: StudioDarkPalette(),
		FrameTree: schema.UIElement{Kind: schema.ElemSlot, SlotID: "workbench.main"},
	})

	bufFallback := shell.PaneBuffer{BufferID: "builtin:transcript", Title: "AI Transcript", Kind: "native-gpu"}
	tree := shell.NewWindowTree(bufFallback)

	app := NewWorkbenchApp(reg, slots, wb)
	app.Transcript = NewTranscriptController()
	app.Windows = tree
	app.WindowBuffers = []shell.PaneBuffer{bufFallback}

	mkt := NewMarketplaceController()
	if !mkt.OfficialOnly {
		t.Fatal("expected NewMarketplaceController() to enforce OfficialOnly=true by default")
	}

	var installedOfficial bool
	var disabledOfficial bool
	mkt.SetItems([]MarketplaceItem{
		{
			PluginID:          "tao3k.official-kanban",
			Version:           "1.2.0",
			PublisherID:       "tao3k.official",
			Summary:           "Official signed Kanban workspace",
			SlotID:            "workbench.main",
			Runtime:           schema.RuntimeWebViewUntrusted,
			SignatureVerified: true,
			TrustTier:         "official",
		},
		{
			PluginID:          "rogue.unsigned-panel",
			Version:           "0.1.0",
			PublisherID:       "unknown.vendor",
			Summary:           "Unverified third-party panel",
			SlotID:            "workbench.main",
			Runtime:           schema.RuntimeWebViewUntrusted,
			SignatureVerified: false,
			TrustTier:         "unofficial",
		},
		{
			PluginID:          "community.thirdparty-panel",
			Version:           "0.9.0",
			PublisherID:       "community.vendor",
			Summary:           "Non-official tier publisher",
			SlotID:            "workbench.main",
			Runtime:           schema.RuntimeWebViewUntrusted,
			SignatureVerified: true,
			TrustTier:         "verified",
		},
	})

	pluginBuf := shell.PaneBuffer{BufferID: "plugin:tao3k.official-kanban", Title: "Official Kanban", Kind: "webview"}
	mkt.OnInstall = func(id string) error {
		if id != "tao3k.official-kanban" {
			t.Fatalf("unofficial plugin %q must never reach OnInstall", id)
		}
		installedOfficial = true
		mkt.MarkInstalled(id, "1.2.0", 1, "HEALTHY")
		app.RegisterWindowBuffer(pluginBuf)
		_, _ = tree.SplitRight(pluginBuf)
		return nil
	}
	mkt.OnDisable = func(id string) error {
		disabledOfficial = true
		mkt.MarkDisabled(id)
		app.UnregisterWindowBuffer(pluginBuf.BufferID, bufFallback)
		return nil
	}
	app.Marketplace = mkt
	app.marketplaceOpen = true

	tt := ui.NewTester(app.View, 1280, 860)

	// 1. Official-Only banner visible; unofficial items visibly BLOCKED with no Install button!
	if !tt.HasText("OFFICIAL ONLY") || !tt.HasText("BLOCKED · OFFICIAL ONLY") {
		t.Fatalf("expected OFFICIAL ONLY policy & blocked badges, got %q", tt.Texts())
	}
	if err := tt.Click("Install rogue.unsigned-panel"); err == nil {
		t.Fatal("unsigned plugin must not expose an Install button in Official-Only mode")
	}
	if err := tt.Click("Install community.thirdparty-panel"); err == nil {
		t.Fatal("non-official tier plugin must not expose an Install button in Official-Only mode")
	}

	// 2. Official verified plugin hot-plugs cleanly into WindowBuffers & Emacs split tree!
	if err := tt.Click("Install tao3k.official-kanban"); err != nil {
		t.Fatalf("click Install official plugin: %v", err)
	}
	if !installedOfficial || tree.Snapshot().LeafCount != 2 {
		t.Fatalf("expected official plugin hot-plugged into 2-pane tree, installed=%v leaves=%d",
			installedOfficial, tree.Snapshot().LeafCount)
	}

	// 3. Hot-unplugging via Disable migrates the Emacs pane cleanly back to fallback buffer!
	if err := tt.Click("Disable tao3k.official-kanban"); err != nil {
		t.Fatalf("click Disable official plugin: %v", err)
	}
	if !disabledOfficial {
		t.Fatal("expected OnDisable called")
	}
	if _, focusedBuf := tree.FocusedBuffer(); focusedBuf.BufferID != bufFallback.BufferID {
		t.Fatalf("expected unplugged pane migrated to fallback %q, got %q", bufFallback.BufferID, focusedBuf.BufferID)
	}
}
