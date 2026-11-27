// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package search_test

import (
	"fmt"
	"math/rand/v2"
	"runtime"
	"testing"

	"github.com/tao3k/marlin-mygo/engine/search"
)

// TestHelm250kHitsFullCapacityNavigationAndZeroHeapLeakSoak stresses
// HelmEngine at its full 250,000-candidate HardMaxCandidates ceiling, executes
// 100,000 navigation/scroll/viewport/source-jump operations, and runs 30 full
// query reset/re-ingest cycles verifying bounded heap memory with zero leaks.
func TestHelm250kHitsFullCapacityNavigationAndZeroHeapLeakSoak(t *testing.T) {
	eng := search.NewHelmEngine(search.HardMaxCandidates)
	eng.SetViewportRows(18)

	sources := []search.SourceMeta{
		{ID: "buffers", Label: "Buffers", Priority: 1},
		{ID: "files", Label: "Files", Priority: 2},
		{ID: "symbols", Label: "Symbols", Priority: 3},
		{ID: "org", Label: "Org Headings", Priority: 4},
		{ID: "transcript", Label: "Transcript", Priority: 5},
	}

	gen := eng.BeginQuery("marlin")
	const perSource = 50_000 // 5 * 50,000 = 250,000 candidates
	for _, sm := range sources {
		batch := make([]search.Candidate, perSource)
		for i := range batch {
			batch[i] = search.Candidate{
				ID:            fmt.Sprintf("%s-%d", sm.ID, i),
				SourceID:      sm.ID,
				Title:         fmt.Sprintf("marlin_%s_%d", sm.ID, i),
				Location:      "src/engine.rs:1",
				Score:         int32(perSource - i),
				MatchMask:     0x3f,
				PayloadKind:   sm.ID,
				PayloadTarget: "t",
			}
		}
		admitted, stale := eng.IngestBatch(gen, sm, batch)
		if stale || admitted != perSource {
			t.Fatalf("source %s: expected %d admitted, got %d stale=%v", sm.ID, perSource, admitted, stale)
		}
	}
	eng.Materialize()

	totalHits, _, _, _ := eng.Stats()
	if totalHits != search.HardMaxCandidates {
		t.Fatalf("expected %d hits, got %d", search.HardMaxCandidates, totalHits)
	}

	navOps := 100_000
	if testing.Short() {
		navOps = 15_000
	}
	rng := rand.New(rand.NewPCG(20261126, 250000))
	scratch := make([]search.VisibleRow, 0, 18)
	for i := 0; i < navOps; i++ {
		switch rng.IntN(5) {
		case 0:
			eng.MoveSelection(rng.IntN(11) - 5)
		case 1:
			eng.ScrollBy(rng.IntN(201) - 100)
		case 2:
			if rng.IntN(2) == 0 {
				eng.JumpSource(+1)
			} else {
				eng.JumpSource(-1)
			}
		case 3:
			rows, totalRows, _, scrollRow, selRow, _ := eng.VisibleWindow(scratch)
			if len(rows) != 18 || totalRows != search.HardMaxCandidates+len(sources) {
				t.Fatalf("step %d: unexpected window len=%d totalRows=%d", i, len(rows), totalRows)
			}
			if scrollRow < 0 || selRow < 0 || selRow >= totalRows {
				t.Fatalf("step %d: invalid indices scroll=%d sel=%d", i, scrollRow, selRow)
			}
			for _, r := range rows {
				if r.Selected && r.IsHeader {
					t.Fatalf("step %d: header selected at %d", i, r.FlatIndex)
				}
			}
		case 4:
			// Stale batch rejection MUST stay O(1) and never leak into 250k table.
			if _, stale := eng.IngestBatch(gen-1, sources[0], []search.Candidate{{ID: "stale"}}); !stale {
				t.Fatalf("step %d: stale batch not rejected", i)
			}
		}
	}

	// Soak 30 rapid full query resets + re-ingests and verify heap stays bounded (zero leak).
	batch1k := make([]search.Candidate, 1_000)
	for i := range batch1k {
		batch1k[i] = search.Candidate{ID: fmt.Sprintf("soak-%d", i), Title: "soak_target"}
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for cycle := 0; cycle < 30; cycle++ {
		g := eng.BeginQuery(fmt.Sprintf("cycle-%d", cycle))
		for _, sm := range sources {
			eng.IngestBatch(g, sm, batch1k)
		}
		eng.Materialize()
		_, _, _, _, _, _ = eng.VisibleWindow(scratch)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	if after.HeapAlloc > before.HeapAlloc+16*1024*1024 {
		t.Fatalf("unexpected heap growth across 30 query soak cycles: before=%dMB after=%dMB",
			before.HeapAlloc/(1024*1024), after.HeapAlloc/(1024*1024))
	}
}

func TestScoreAndMaskHighlightSpans(t *testing.T) {
	score, mask, ok := search.ScoreAndMask("mrl", "marlin_engine.go")
	if !ok || score <= 0 || mask == 0 {
		t.Fatalf("expected positive fuzzy match + mask, got score=%d mask=%b ok=%v", score, mask, ok)
	}
	spans := search.SplitHighlightSpans("marlin_engine.go", mask, nil)
	var rebuilt string
	matchedCount := 0
	for _, sp := range spans {
		rebuilt += sp.Text
		if sp.Matched {
			matchedCount += len(sp.Text)
		}
	}
	if rebuilt != "marlin_engine.go" || matchedCount != 3 {
		t.Fatalf("unexpected highlight spans %+v (rebuilt=%q matched=%d)", spans, rebuilt, matchedCount)
	}
}
