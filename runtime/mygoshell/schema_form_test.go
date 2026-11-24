// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestBoundedSchemaAutoFormNativeGPURenderingAndValidation(t *testing.T) {
	spec := schema.BoundedSchema{
		ID:    "tao3k.kanban.settings",
		Title: "Kanban Settings",
		Fields: []schema.FieldSpec{
			{Key: "mode", Label: "Layout Mode", Kind: schema.FieldEnum, Required: true, EnumValues: []string{"columns", "swimlanes"}, Default: "columns"},
			{Key: "limit", Label: "Card Limit", Kind: schema.FieldInt, MinInt: 5, MaxInt: 100, Default: 50},
			{Key: "sync", Label: "Auto Sync", Kind: schema.FieldBool, Default: true},
		},
	}
	state := NewSchemaFormState(spec, nil)
	var submitted map[string]any

	tt := ui.NewTester(func(c *ui.Context) {
		ApplyMarlinTheme(c, schema.DefaultUITheme())
		RenderBoundedForm(c, spec, state, func(vals map[string]any) {
			submitted = vals
		})
	}, 640, 480)

	if !tt.HasText("Kanban Settings") || !tt.HasText("swimlanes") {
		t.Fatalf("expected schema form title and enum buttons, got %q", tt.Texts())
	}
	// Click enum option "swimlanes" then click Save Settings.
	if err := tt.Click("swimlanes"); err != nil {
		t.Fatalf("clicking enum button failed: %v", err)
	}
	if err := tt.Click("Save Settings"); err != nil {
		t.Fatalf("clicking Save Settings failed: %v", err)
	}
	if submitted == nil || submitted["mode"] != "swimlanes" || submitted["limit"] != int64(50) || submitted["sync"] != true {
		t.Fatalf("unexpected submitted values: %+v (err=%q)", submitted, state.LastError)
	}

	// Out-of-bounds integer must show inline validation error and block submit.
	submitted = nil
	state.Strings["limit"] = "9999"
	tt.Frame()
	if err := tt.Click("Save Settings"); err != nil {
		t.Fatalf("clicking Save Settings failed: %v", err)
	}
	if submitted != nil || state.LastError == "" {
		t.Fatalf("expected validation failure for limit=9999, got submitted=%+v err=%q", submitted, state.LastError)
	}
}

func TestIRChoiceTemplateDrawListAndHydraKeyTrie(t *testing.T) {
	// 1. ElemChoice + ElemTemplate + RenderDrawList
	facts := map[string]bool{"review": true}
	bindings := map[string]string{"model": "claude-sonnet"}
	tree := schema.UIElement{
		Kind: schema.ElemRows,
		Children: []schema.UIElement{
			{
				Kind: schema.ElemChoice,
				Children: []schema.UIElement{
					{Kind: schema.ElemTemplate, ID: "review", Spans: []schema.UISpan{{Text: "Reviewing with {{model}}", Tone: schema.ToneAccent}}},
					{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Fallback Chat", Tone: schema.ToneNormal}}},
				},
			},
		},
	}
	cmds := []schema.DrawCmd{
		{Op: schema.DrawOpFillRect, Radius: 4, X: 0, Y: 0, W: 40, H: 16, RGBA: 0x51afefFF},
		{Op: schema.DrawOpLine, X: 0, Y: 20, W: 80, H: 2, RGBA: 0xc678ddFF},
	}

	tt := ui.NewTester(func(c *ui.Context) {
		ApplyMarlinTheme(c, schema.DefaultUITheme())
		RenderUIElement(c, tree, nil, shell.RenderContext{
			Surface:  c,
			Theme:    schema.DefaultUITheme(),
			Facts:    facts,
			Bindings: bindings,
		})
		RenderDrawList(c, cmds)
	}, 640, 320)

	if !tt.HasText("Reviewing with claude-sonnet") {
		t.Fatalf("expected ElemChoice+ElemTemplate interpolation, got %q", tt.Texts())
	}
	facts["review"] = false
	tt.Frame()
	if !tt.HasText("Fallback Chat") {
		t.Fatalf("expected ElemChoice fallback branch, got %q", tt.Texts())
	}

	// 2. WorkbenchApp Meow/Hydra KeyTrie + Fuzzy Command Palette
	trie, err := input.CompileKeyTrie([]input.Binding{
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "w", "v"}, ActionKind: "invoke", CommandName: "split-vertical"},
	})
	if err != nil {
		t.Fatal(err)
	}
	targets := transport.NewTargetRegistry()
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	app := NewWorkbenchApp(targets, slots, wb)
	app.KeyTrie = trie
	app.Commands = []input.CommandEntry{
		{Name: "select-buffer"},
		{Name: "split-vertical"},
	}
	var invoked string
	app.OnCommandInvoke = func(name string) { invoked = name }

	kind, hint := app.HandleKeyToken(input.ScopeNormal, "SPC")
	if kind != input.MatchPrefix || hint != "SPC" {
		t.Fatalf("expected prefix SPC, got (%d, %q)", kind, hint)
	}
	ttApp := ui.NewTester(app.View, 900, 500)
	if !ttApp.HasText("HYDRA: SPC …") {
		t.Fatalf("expected Hydra prefix hint in top bar, got %q", ttApp.Texts())
	}
	app.HandleKeyToken(input.ScopeNormal, "w")
	kind, cmd := app.HandleKeyToken(input.ScopeNormal, "v")
	if kind != input.MatchExact || cmd != "split-vertical" || invoked != "split-vertical" {
		t.Fatalf("expected exact split-vertical match, got (%d, %q, invoked=%q)", kind, cmd, invoked)
	}
}
