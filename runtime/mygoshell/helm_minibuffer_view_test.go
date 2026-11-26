// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/engine/search"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

func TestHelmMinibuffer100kHitsSPCSSDockModeMarkAndNonDestructiveSplit(t *testing.T) {
	as := mygoshell.NewAdaptiveShell(mygoshell.AdaptiveShellConfig{BrandTitle: "Marlin"})
	app := as.Desktop
	app.HelmSearch = mygoshell.NewHelmMinibufferController(100_000)

	// Load 100,000 live hits across 2 sources (50k files + 50k symbols).
	gen := app.HelmSearch.SetQuery("marlin")
	fileBatch := make([]search.Candidate, 50_000)
	symBatch := make([]search.Candidate, 50_000)
	for i := 0; i < 50_000; i++ {
		fileBatch[i] = search.Candidate{
			ID: fmt.Sprintf("f-%d", i), SourceID: "files",
			Title: fmt.Sprintf("engine_%d.go", i), Location: fmt.Sprintf("engine/file_%d.go:%d", i, i+1),
			Snippet: fmt.Sprintf("func Handle%d() error { return nil }", i),
			PayloadKind: "file", PayloadTarget: fmt.Sprintf("file:engine_%d.go", i),
		}
		symBatch[i] = search.Candidate{
			ID: fmt.Sprintf("s-%d", i), SourceID: "symbols",
			Title: fmt.Sprintf("Symbol_%d", i), Location: fmt.Sprintf("crates/core/src/sym_%d.rs:42", i),
			Snippet: "pub struct AuthorityGateReceipt;",
			PayloadKind: "symbol", PayloadTarget: fmt.Sprintf("sym:Symbol_%d", i),
		}
	}
	app.HelmSearch.Engine.IngestBatch(gen, search.SourceMeta{ID: "files", Label: "Files", Priority: 1}, fileBatch)
	app.HelmSearch.Engine.IngestBatch(gen, search.SourceMeta{ID: "symbols", Label: "Symbols", Priority: 2}, symBatch)

	// 1. Open via SPC s s keyboard chord without disturbing the active WindowTree!
	beforeLeaves := app.Windows.Snapshot().LeafCount
	app.SetKeyMode(input.ScopeNormal)
	for _, tok := range []string{"SPC", "s", "s"} {
		_, _ = app.HandleKeyToken(input.ScopeNormal, tok)
	}
	if !app.IsHelmSearchOpen() {
		t.Fatal("expected SPC s s to open Helm Minibuffer")
	}

	tt := ui.NewTester(app.View, 1280, 820)
	tt.Frame()
	if app.Windows.Snapshot().LeafCount != beforeLeaves {
		t.Fatalf("opening Helm MUST NOT mutate WindowTree leaves: before=%d after=%d", beforeLeaves, app.Windows.Snapshot().LeafCount)
	}
	for _, want := range []string{"HELM SEARCH", "100000 hits", "engine_0.go", "LIVE PREVIEW", "Dock Bottom"} {
		if !tt.HasText(want) {
			t.Fatalf("expected Helm Minibuffer to render %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}

	// 2. Toggle Dock Bottom -> Float Deck & jump straight to Symbols source!
	if err := tt.Click("Dock Bottom"); err != nil {
		t.Fatalf("click Dock Bottom: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Float Deck") {
		t.Fatalf("expected Dock Bottom to toggle to Float Deck, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Next Source"); err != nil {
		t.Fatalf("click Next Source: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Symbol_0") || !tt.HasText("pub struct AuthorityGateReceipt;") {
		t.Fatalf("expected Next Source to jump to Symbol_0 with live preview, got %q", strings.Join(tt.Texts(), " | "))
	}

	// 3. Mark Symbol_0 + Symbol_1 and Attach @ straight into Transcript Composer!
	if err := tt.Click("Mark"); err != nil {
		t.Fatalf("click Mark 1: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Mark"); err != nil {
		t.Fatalf("click Mark 2: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Marked: 2") {
		t.Fatalf("expected Marked: 2 badge, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Attach @"); err != nil {
		t.Fatalf("click Attach @: %v", err)
	}
	tt.Frame()
	if app.IsHelmSearchOpen() {
		t.Fatal("expected Helm closed after executing Attach @ action")
	}
	if ms := app.Transcript.Composer.Snapshot().Mentions; len(ms) != 2 {
		t.Fatalf("expected 2 marked candidates injected into Composer @ mentions, got %+v", ms)
	}

	// 4. Re-open & click Split Right -> cleanly splits WindowTree non-destructively!
	app.OpenHelmSearch()
	tt.Frame()
	if err := tt.Click("Split Right"); err != nil {
		t.Fatalf("click Split Right: %v", err)
	}
	tt.Frame()
	if app.Windows.Snapshot().LeafCount != beforeLeaves+1 {
		t.Fatalf("expected Split Right to add 1 pane, before=%d after=%d", beforeLeaves, app.Windows.Snapshot().LeafCount)
	}
}

func TestHelmLiveDefaultSourcesMatchMaskAndFollowPreview(t *testing.T) {
	proj := mygoshell.NewProjectionController(mygoshell.ProjectionSnapshot{
		DiffFiles: []mygoshell.WorkspaceDiffFileView{
			{Path: "engine/search/helm_stream.go", Status: "added", Additions: 772, HunkLines: []string{"+func (e *HelmEngine) VisibleWindow()"}},
		},
		GraphNodes: []mygoshell.GraphNodeReceiptView{
			{NodeID: "node-helm-admit", Kind: "readiness-gate", Phase: "succeeded", DurationMS: 2, ReceiptID: "rcpt-helm", Summary: "Helm live source ready"},
		},
	})
	as := mygoshell.NewAdaptiveShell(mygoshell.AdaptiveShellConfig{
		BrandTitle: "Marlin",
		Projection: proj,
	})
	app := as.Desktop
	app.Transcript.AppendUser("Inspect helm_stream.go performance budget")
	mygoshell.BindDefaultHelmSources(app, nil)
	app.OpenHelmSearch()
	app.HelmSearch.SetQuery("helm")

	tt := ui.NewTester(app.View, 1280, 820)
	tt.Frame()
	for _, want := range []string{"WORKSPACE FILES", "GRAPH & PROCS", "TRANSCRIPT", "LIVE PREVIEW"} {
		if !tt.HasText(want) {
			t.Fatalf("expected live multi-source Helm view to render %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}
}
