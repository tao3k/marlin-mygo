// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell_test

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/richtext"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

func TestRichDocumentOrgRZ1AndMarkdownGPURenderingAndLightbox(t *testing.T) {
	tc := mygoshell.NewTranscriptController()
	var copied string
	tc.OnCopyCode = func(code string) { copied = code }

	// 1. Admit pre-parsed Org-mode IR JSON (as emitted by org-rz1 WASM or Rust backend) with ZERO Go-side parsing!
	orgIRJSON := []byte(`{
		"format": "org",
		"blocks": [
			{"kind":"heading","level":1,"is_org":true,"todo_word":"TODO","priority":"A","tags":["marlin","org"],"raw_text":"Ship Org-RZ1 GPU Renderer"},
			{"kind":"org-planning","is_org":true,"planning":[{"keyword":"SCHEDULED","timestamp":"<2026-11-26 Thu 10:00>"},{"keyword":"DEADLINE","timestamp":"<2026-11-27 Fri>"}]},
			{"kind":"drawer","is_org":true,"drawer_name":"PROPERTIES","drawer_rows":[{"key":"ID","value":"org-rz1-01"}]},
			{"kind":"table","is_org":true,"headers":["Feature","Engine"],"rows":[["Org Agenda","Native GPU"]]},
			{"kind":"list","items":[{"check":"X","raw_text":"Org headings & tags"},{"check":"-","raw_text":"Drawer cards"},{"check":" ","raw_text":"Export agenda"}]},
			{"kind":"code","is_org":true,"lang":"scheme","code":"(display \"marlin-org-rz1-live\")"}
		]
	}`)
	orgBlocks, err := richtext.DecodeIRJSON("turn-1", orgIRJSON)
	if err != nil {
		t.Fatalf("DecodeIRJSON failed: %v", err)
	}
	if _, err := tc.AppendAssistantRichBlocks(1, "Org-RZ1 Turn", orgBlocks, "DONE"); err != nil {
		t.Fatalf("AppendAssistantRichBlocks failed: %v", err)
	}

	// 2. Admit pre-parsed Markdown IR blocks (from Rust backend) with GitHub Alert & GFM table.
	mdBlocks := []richtext.Block{
		{Kind: richtext.BlockHeading, Level: 2, RawText: "Audit Report"},
		{Kind: richtext.BlockQuote, AlertKind: "WARNING", RawText: "Approval gates stay fail-closed."},
		{Kind: richtext.BlockTable, Headers: []string{"Gate", "Result"}, Rows: [][]string{{"Fuzz", "Pass"}}},
	}
	if _, err := tc.AppendAssistantRichBlocks(2, "Markdown Turn", mdBlocks, "DONE"); err != nil {
		t.Fatalf("AppendAssistantRichBlocks (MD) failed: %v", err)
	}

	// 3. Wire pluggable external parser callback (org-rz1 WASM / Rust) for `.org` Lightbox preview!
	var parsedFormats []richtext.Format
	tc.OnParseRichDoc = func(format richtext.Format, _ string, _ string) ([]richtext.Block, error) {
		parsedFormats = append(parsedFormats, format)
		return []richtext.Block{
			{Kind: richtext.BlockHeading, Level: 1, IsOrg: true, TodoWord: "NEXT", Priority: "B", Tags: []string{"desktop"}, RawText: "Sync Org Agenda"},
		}, nil
	}
	if _, err := tc.AttachFile(composer.AttachmentInput{
		Filename: "roadmap.org",
		Data:     []byte("* NEXT [#B] Sync Org Agenda :desktop:\n"),
	}); err != nil {
		t.Fatalf("attach roadmap.org failed: %v", err)
	}

	theme := ui.DarkTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		tc.Render(c, theme, nil, nil)
	}, 1200, 900)
	tt.Frame()

	for _, want := range []string{
		"TODO", "[#A]", "Ship Org-RZ1 GPU Renderer", ":marlin:", ":org:",
		"SCHEDULED <2026-11-26 Thu 10:00>", "DEADLINE <2026-11-27 Fri>",
		"ORG DRAWER", ":PROPERTIES:", ":ID:", "org-rz1-01",
		"Feature", "Org Agenda", "Native GPU",
		"[X]", "[-]", "[ ]",
		"SCHEME", "ORG SRC", `(display "marlin-org-rz1-live")`,
		"Audit Report", "WARNING", "Approval gates stay fail-closed.", "Fuzz",
	} {
		if !tt.HasText(want) {
			t.Fatalf("expected rich Org-RZ1+Markdown GPU render to show %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}

	// Verify 1-click Copy Code works on Org #+BEGIN_SRC block!
	if err := tt.Click("Copy Code"); err != nil {
		t.Fatalf("click Copy Code failed: %v", err)
	}
	if !strings.Contains(copied, "marlin-org-rz1-live") {
		t.Fatalf("expected Copy Code to copy Scheme snippet, got %q", copied)
	}

	// Open roadmap.org in Lightbox -> invokes external org-rz1/Rust parser hook & renders Org preview!
	if err := tt.Click("Inspect roadmap.org"); err != nil {
		t.Fatalf("click Inspect roadmap.org failed: %v", err)
	}
	tt.Frame()
	if len(parsedFormats) != 1 || parsedFormats[0] != richtext.FormatOrg {
		t.Fatalf("expected OnParseRichDoc invoked with FormatOrg, got %+v", parsedFormats)
	}
	for _, want := range []string{"ORG-MODE PREVIEW", "NEXT", "[#B]", "Sync Org Agenda", ":desktop:"} {
		if !tt.HasText(want) {
			t.Fatalf("expected Lightbox Org preview to render %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}
}

func TestRichDocumentInteractiveOutlineFoldingAndChecklistToggle(t *testing.T) {
	tc := mygoshell.NewTranscriptController()
	var toggledEntry, toggledNext string
	var toggledBlock, toggledItem int
	tc.OnToggleRichCheck = func(entryID string, blockIdx, itemIdx int, nextCheck string) {
		toggledEntry = entryID
		toggledBlock = blockIdx
		toggledItem = itemIdx
		toggledNext = nextCheck
	}

	blocks := []richtext.Block{
		{Kind: richtext.BlockHeading, Level: 1, IsOrg: true, TodoWord: "TODO", RawText: "Release Checklist"},
		{Kind: richtext.BlockList, Items: []richtext.ListItem{
			{Check: " ", RawText: "Verify zero-CGO build"},
			{Check: "X", RawText: "Run race tests"},
		}},
		{Kind: richtext.BlockTable, Headers: []string{"Col1", "Col2", "Col3", "Col4"}, Rows: [][]string{{"a", "b", "c", "d"}}},
		{Kind: richtext.BlockHeading, Level: 1, IsOrg: true, RawText: "Next Milestone"},
	}
	if _, err := tc.AppendAssistantRichBlocks(1, "Org Outline", blocks, "DONE"); err != nil {
		t.Fatalf("AppendAssistantRichBlocks: %v", err)
	}

	theme := ui.DarkTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		tc.Render(c, theme, nil, nil)
	}, 1200, 820)
	tt.Frame()

	// Verify wide 4-col table badge renders.
	if !tt.HasText("TABLE · 4 cols × 1 rows") || !tt.HasText("Verify zero-CGO build") {
		t.Fatalf("expected wide table header and checklist rendered, got %q", strings.Join(tt.Texts(), " | "))
	}

	// Click checklist [ ] -> cycles to [X] and fires OnToggleRichCheck!
	if err := tt.Click("Toggle [ ] Verify zero-CGO build"); err != nil {
		t.Fatalf("click checklist item failed: %v", err)
	}
	tt.Frame()
	if toggledEntry != "turn-1" || toggledBlock != 1 || toggledItem != 0 || toggledNext != "X" {
		t.Fatalf("unexpected OnToggleRichCheck callback: entry=%q b=%d i=%d next=%q", toggledEntry, toggledBlock, toggledItem, toggledNext)
	}

	// Click Fold heading -> hides subordinate list & table (2 blocks) while keeping sibling heading visible!
	if err := tt.Click("Fold Release Checklist"); err != nil {
		t.Fatalf("click Fold Release Checklist failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("... 2 hidden block(s)") || tt.HasText("Verify zero-CGO build") || !tt.HasText("Next Milestone") {
		t.Fatalf("expected folded outline to hide 2 subordinate blocks and keep sibling heading, got %q", strings.Join(tt.Texts(), " | "))
	}

	// Click Expand heading -> restores subordinate list & table cleanly!
	if err := tt.Click("Expand Release Checklist"); err != nil {
		t.Fatalf("click Expand Release Checklist failed: %v", err)
	}
	tt.Frame()
	if tt.HasText("... 2 hidden block(s)") || !tt.HasText("Verify zero-CGO build") {
		t.Fatalf("expected expanded outline to restore checklist item, got %q", strings.Join(tt.Texts(), " | "))
	}
}
