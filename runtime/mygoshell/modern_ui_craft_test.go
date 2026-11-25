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

func TestModernUICraftTranscriptCodeBlocksDeepThinkAndStatusBar(t *testing.T) {
	reg := transport.NewTargetRegistry()
	_ = reg.Register(&projectionTestTarget{meta: transport.TargetMeta{
		ID: "local", DisplayName: "Local Mac", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})
	slots := shell.NewSlotRegistry()
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle, FallbackRenderer: "main.fb",
	}, NewUIElementRenderer("main.fb", true, slots, func() schema.UIElement {
		return schema.UIElement{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Main Workspace"}}}
	}))
	wb := shell.NewWorkbenchController(slots)
	_ = wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation: 1, ProfileName: "craft-test", Theme: StudioDarkPalette(),
		FrameTree: schema.UIElement{Kind: schema.ElemSlot, SlotID: "workbench.main"},
	})

	bufMain := shell.PaneBuffer{BufferID: "slot:workbench.main", Title: "Workspace", Kind: "webview"}
	bufChat := shell.PaneBuffer{BufferID: "builtin:transcript", Title: "AI Transcript", Kind: "native-gpu"}
	tree := shell.NewWindowTree(bufChat)
	_, _ = tree.OpenTabInFocused(bufMain)
	tree.SelectTab(tree.FocusedID(), 0) // keep AI Transcript active while Workspace pauses

	tc := NewTranscriptController()
	tc.SetModelLabel("DeepSeek-R1")
	var copiedCode string
	tc.OnCopyCode = func(code string) { copiedCode = code }

	tc.AppendUser("Show me a Go hello world snippet.")
	tc.AppendThinking("Reasoning", "Plan a concise Go snippet.", true)
	tc.ApplyCheckpoint(1, "ev-1", "Here is the snippet:\n```go\nfmt.Println(\"hello marlin\")\n```\nDone.")
	tc.CompleteTurn(1, "DONE")

	app := NewWorkbenchApp(reg, slots, wb)
	app.Transcript = tc
	app.Windows = tree
	app.WindowBuffers = []shell.PaneBuffer{bufChat, bufMain}
	if err := app.EnableEmacsWindowKeymap(); err != nil {
		t.Fatalf("EnableEmacsWindowKeymap: %v", err)
	}

	tt := ui.NewTester(app.View, 1280, 860)

	// 1. Verify T3 Chat code-fence card (GO badge + Copy Code button) and DeepSeek thinking summary!
	if !tt.HasText("GO") || !tt.HasText("Copy Code") || !tt.HasText("DeepSeek-R1") {
		t.Fatalf("expected GO code-fence card and DeepSeek-R1 composer badge, texts=%q", tt.Texts())
	}
	if err := tt.Click("Copy Code"); err != nil {
		t.Fatalf("click Copy Code failed: %v", err)
	}
	if copiedCode != `fmt.Println("hello marlin")` {
		t.Fatalf("expected copied code snippet, got %q", copiedCode)
	}

	// 2. Toggle Deep Think pill in composer bar.
	if err := tt.Click("Deep Think"); err != nil {
		t.Fatalf("click Deep Think failed: %v", err)
	}
	if !tc.DeepThinkEnabled() {
		t.Fatal("expected Deep Think toggle to become enabled")
	}

	// 3. Verify Zed/Emacs bottom Status Bar exposes Tabs count & SPC ? cheatsheet trigger.
	if !tt.HasText("Tabs 2 (0 sleep 0MB)") || !tt.HasText("SPC ?") {
		t.Fatalf("expected Zed/Emacs bottom Status Bar with Tabs badge and SPC ?, texts=%q", tt.Texts())
	}
	if err := tt.Click("Tabs 2 (0 sleep 0MB)"); err != nil {
		t.Fatalf("click Status Bar Tabs pill failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Buffer & Tab Minibuffer (SPC b b)") {
		t.Fatalf("expected clicking Status Bar Tabs pill to open Minibuffer Picker, texts=%q", tt.Texts())
	}
}
