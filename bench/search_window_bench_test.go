// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package bench_test

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/search"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

func buildBench100kHelmEngine() (*search.HelmEngine, uint64) {
	eng := search.NewHelmEngine(100_000)
	eng.SetViewportRows(16)
	gen := eng.BeginQuery("marlin")
	sources := []search.SourceMeta{
		{ID: "buffers", Label: "Buffers", Priority: 1},
		{ID: "files", Label: "Files", Priority: 2},
		{ID: "symbols", Label: "Symbols", Priority: 3},
		{ID: "org", Label: "Org", Priority: 4},
	}
	for _, sm := range sources {
		batch := make([]search.Candidate, 25_000)
		for i := range batch {
			batch[i] = search.Candidate{
				ID:            fmt.Sprintf("%s-%d", sm.ID, i),
				SourceID:      sm.ID,
				Title:         fmt.Sprintf("item_%d", i),
				Location:      "src/lib.rs:42",
				Score:         int32(100_000 - i),
				PayloadKind:   sm.ID,
				PayloadTarget: "target",
			}
		}
		eng.IngestBatch(gen, sm, batch)
	}
	eng.Materialize()
	return eng, gen
}

func BenchmarkHelm100kHits_VisibleWindowZeroAlloc(b *testing.B) {
	eng, _ := buildBench100kHelmEngine()
	scratch := make([]search.VisibleRow, 0, 16)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scratch, _, _, _, _, _ = eng.VisibleWindow(scratch)
	}
	if len(scratch) != 16 {
		b.Fatalf("expected 16 visible rows, got %d", len(scratch))
	}
}

func BenchmarkHelm100kHits_ScrollAndMoveSelection(b *testing.B) {
	eng, _ := buildBench100kHelmEngine()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i&1 == 0 {
			eng.MoveSelection(+1)
		} else {
			eng.ScrollBy(+3)
		}
	}
}

func BenchmarkHelmStaleBatchDropZeroAlloc(b *testing.B) {
	eng := search.NewHelmEngine(100_000)
	oldGen := eng.BeginQuery("m")
	_ = eng.BeginQuery("marlin")
	src := search.SourceMeta{ID: "files", Label: "Files", Priority: 1}
	batch := []search.Candidate{{ID: "stale-1", Title: "stale.go"}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = eng.IngestBatch(oldGen, src, batch)
	}
}

func BenchmarkWindowTree_Snapshot120HzZeroAlloc(b *testing.B) {
	wt := shell.NewWindowTree(shell.PaneBuffer{BufferID: "b0", Title: "B0", Kind: "native-gpu"})
	for i := 1; i < shell.MaxOpenWindows; i++ {
		buf := shell.PaneBuffer{BufferID: fmt.Sprintf("b%d", i), Title: fmt.Sprintf("B%d", i), Kind: "native-gpu"}
		if i%2 == 0 {
			_, _ = wt.SplitBelow(buf)
		} else {
			_, _ = wt.SplitRight(buf)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	var snap shell.WindowTreeSnapshot
	for i := 0; i < b.N; i++ {
		snap = wt.Snapshot()
	}
	if snap.LeafCount != shell.MaxOpenWindows {
		b.Fatalf("expected %d leaves, got %d", shell.MaxOpenWindows, snap.LeafCount)
	}
}

func BenchmarkHelmMinibuffer_100kHitsGPUFrame(b *testing.B) {
	as := mygoshell.NewAdaptiveShell(mygoshell.AdaptiveShellConfig{BrandTitle: "Marlin"})
	as.Desktop.HelmSearch = mygoshell.NewHelmMinibufferController(100_000)
	eng, _ := buildBench100kHelmEngine()
	as.Desktop.HelmSearch.Engine = eng
	as.Desktop.OpenHelmSearch()
	tt := ui.NewTester(as.Desktop.View, 1280, 800)
	tt.Frame()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tt.Frame()
	}
}
