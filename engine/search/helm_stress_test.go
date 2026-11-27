// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package search_test

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tao3k/marlin-mygo/engine/search"
)

// TestHelmEngine100kHitsNavigationSourceJumpAndMultiMark verifies 100,000
// streamed hits across multiple prioritized sources, O(1) stale generation
// preemption, Helm C-o source jumping, multi-mark batch target resolution,
// and hard memory cap enforcement.
func TestHelmEngine100kHitsNavigationSourceJumpAndMultiMark(t *testing.T) {
	eng := search.NewHelmEngine(100_000)
	eng.SetViewportRows(16)

	gen1 := eng.BeginQuery("marlin")
	sources := []search.SourceMeta{
		{ID: "buffers", Label: "Open Buffers", Priority: 1},
		{ID: "files", Label: "Workspace Files", Priority: 2},
		{ID: "symbols", Label: "Rust/Go Symbols", Priority: 3},
		{ID: "org", Label: "Org Headings", Priority: 4},
	}

	// Stream 100,000 candidates (25,000 per source) + 500 overflow hits to test hard cap.
	for sIdx, sm := range sources {
		batch := make([]search.Candidate, 25_000)
		for i := range batch {
			batch[i] = search.Candidate{
				ID:            fmt.Sprintf("%s-%d", sm.ID, i),
				SourceID:      sm.ID,
				Title:         fmt.Sprintf("%s match #%d", sm.Label, i),
				Location:      fmt.Sprintf("src/%s_%d.rs:%d", sm.ID, sIdx, i+1),
				Score:         int32(100_000 - i),
				PayloadKind:   sm.ID,
				PayloadTarget: fmt.Sprintf("%s:%d", sm.ID, i),
			}
		}
		admitted, stale := eng.IngestBatch(gen1, sm, batch)
		if stale || admitted != 25_000 {
			t.Fatalf("source %s: expected 25000 admitted, got admitted=%d stale=%v", sm.ID, admitted, stale)
		}
	}
	// Exceeding 100k cap MUST drop cleanly without error or OOM.
	overAdmitted, stale := eng.IngestBatch(gen1, sources[0], []search.Candidate{{ID: "overflow-1", Title: "overflow"}})
	if stale || overAdmitted != 0 {
		t.Fatalf("expected 0 admitted past 100k cap, got admitted=%d stale=%v", overAdmitted, stale)
	}
	totalHits, _, cappedDrops, _ := eng.Stats()
	if totalHits != 100_000 || cappedDrops != 1 {
		t.Fatalf("expected totalHits=100000 cappedDrops=1, got hits=%d capped=%d", totalHits, cappedDrops)
	}

	// VisibleWindow returns ONLY 16 rows out of 100,004 flat rows!
	scratch := make([]search.VisibleRow, 0, 16)
	rows, totalRows, hits, scrollRow, selRow, gen := eng.VisibleWindow(scratch)
	if len(rows) != 16 || totalRows != 100_004 || hits != 100_000 || scrollRow != 0 || selRow != 1 || gen != gen1 {
		t.Fatalf("unexpected visible window: len=%d totalRows=%d hits=%d scroll=%d sel=%d gen=%d", len(rows), totalRows, hits, scrollRow, selRow, gen)
	}
	if !rows[0].IsHeader || rows[0].SourceID != "buffers" || !rows[1].Selected || rows[1].Candidate.ID != "buffers-0" {
		t.Fatalf("unexpected top rows: row0=%+v row1=%+v", rows[0], rows[1])
	}

	// Helm C-o source jumping: buffers -> files -> symbols -> org -> wrap to buffers!
	cand, ok := eng.JumpSource(+1)
	if !ok || cand.SourceID != "files" || cand.ID != "files-0" {
		t.Fatalf("expected JumpSource(+1) -> files-0, got %+v ok=%v", cand, ok)
	}
	cand, ok = eng.JumpSource(+1)
	if !ok || cand.SourceID != "symbols" {
		t.Fatalf("expected JumpSource(+1) -> symbols, got %+v ok=%v", cand, ok)
	}
	cand, ok = eng.JumpSource(-1)
	if !ok || cand.SourceID != "files" {
		t.Fatalf("expected JumpSource(-1) -> files, got %+v ok=%v", cand, ok)
	}

	// Multi-mark 2 candidates in files -> ResolveActionTargets returns both in order!
	marked1, ok1 := eng.ToggleMarkCurrent()
	marked2, ok2 := eng.ToggleMarkCurrent()
	if !marked1 || !ok1 || !marked2 || !ok2 || eng.MarkedCount() != 2 {
		t.Fatalf("expected 2 marked candidates, count=%d", eng.MarkedCount())
	}
	targets := eng.ResolveActionTargets(nil)
	if len(targets) != 2 || targets[0].ID != "files-0" || targets[1].ID != "files-1" {
		t.Fatalf("unexpected marked targets: %+v", targets)
	}

	// Preempt with a new keystroke -> gen1 late batch MUST be dropped immediately (stale=true)!
	gen2 := eng.BeginQuery("marlin-x")
	if gen2 <= gen1 {
		t.Fatalf("expected monotonic gen2 > gen1, got %d <= %d", gen2, gen1)
	}
	admittedLate, staleLate := eng.IngestBatch(gen1, sources[0], []search.Candidate{{ID: "late-1", Title: "stale"}})
	if !staleLate || admittedLate != 0 {
		t.Fatalf("expected stale batch rejected, got admitted=%d stale=%v", admittedLate, staleLate)
	}
	if _, staleDrops, _, _ := eng.Stats(); staleDrops != 1 {
		t.Fatalf("expected staleDrops=1, got %d", staleDrops)
	}
}

// TestHelmEngineBrutalConcurrentKeystrokeAndStreamStorm hammers HelmEngine
// with 8 concurrent streaming producers + 1 rapid keystroke/navigation/scroll
// UI loop (20,000+ ops under -race) to guarantee zero races, zero stale leaks,
// and strict viewport/selection invariants under heavy backend bursts.
func TestHelmEngineBrutalConcurrentKeystrokeAndStreamStorm(t *testing.T) {
	eng := search.NewHelmEngine(50_000)
	eng.SetViewportRows(16)

	var activeGen atomic.Uint64
	activeGen.Store(eng.BeginQuery("init"))

	sources := []search.SourceMeta{
		{ID: "buffers", Label: "Buffers", Priority: 1},
		{ID: "files", Label: "Files", Priority: 2},
		{ID: "symbols", Label: "Symbols", Priority: 3},
		{ID: "org", Label: "Org", Priority: 4},
		{ID: "transcript", Label: "Transcript", Priority: 5},
		{ID: "commands", Label: "Commands", Priority: 6},
	}

	uiOps := 20_000
	batchesPerProducer := 600
	if testing.Short() {
		uiOps = 4_000
		batchesPerProducer = 150
	}

	var stopProducers atomic.Bool
	var wg sync.WaitGroup
	for p := 0; p < 8; p++ {
		producerID := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(2026+producerID), uint64(producerID*17+3)))
			batch := make([]search.Candidate, 16)
			for i := 0; i < batchesPerProducer && !stopProducers.Load(); i++ {
				g := activeGen.Load()
				// 25% of batches intentionally carry an outdated generation to stress preemption!
				if g > 1 && rng.IntN(4) == 0 {
					g--
				}
				src := sources[rng.IntN(len(sources))]
				for j := range batch {
					batch[j] = search.Candidate{
						ID:       fmt.Sprintf("p%d-g%d-%d-%d", producerID, g, i, j),
						SourceID: src.ID,
						Title:    fmt.Sprintf("hit-%d-%d", i, j),
						Score:    int32(1000 - j),
					}
				}
				eng.IngestBatch(g, src, batch)
			}
		}()
	}

	rng := rand.New(rand.NewPCG(20261126, 777))
	scratch := make([]search.VisibleRow, 0, 16)
	var targetScratch []search.Candidate
	for step := 0; step < uiOps; step++ {
		switch rng.IntN(9) {
		case 0: // Rapid keystroke query reset!
			if rng.IntN(10) == 0 {
				g := eng.BeginQuery(fmt.Sprintf("q-%d", step))
				activeGen.Store(g)
			}
		case 1, 2:
			eng.MoveSelection(rng.IntN(7) - 3)
		case 3:
			if rng.IntN(2) == 0 {
				eng.JumpSource(+1)
			} else {
				eng.JumpSource(-1)
			}
		case 4:
			eng.ScrollBy(rng.IntN(41) - 20)
		case 5:
			eng.ToggleMarkCurrent()
		case 6:
			if rng.IntN(3) == 0 {
				eng.SetSourceFilter("")
			} else {
				eng.SetSourceFilter(sources[rng.IntN(len(sources))].ID)
			}
		case 7:
			targetScratch = eng.ResolveActionTargets(targetScratch)
		case 8:
			rows, totalRows, _, scrollRow, selRow, _ := eng.VisibleWindow(scratch)
			if len(rows) > 16 {
				t.Fatalf("step %d: viewport overflow len=%d > 16", step, len(rows))
			}
			if totalRows > 0 && (scrollRow < 0 || scrollRow >= totalRows) {
				t.Fatalf("step %d: invalid scrollRow=%d for totalRows=%d", step, scrollRow, totalRows)
			}
			if selRow >= totalRows {
				t.Fatalf("step %d: invalid selRow=%d >= totalRows=%d", step, selRow, totalRows)
			}
			for _, r := range rows {
				if r.Selected && r.IsHeader {
					t.Fatalf("step %d: header row %+v must NEVER be selected", step, r)
				}
			}
		}
	}

	stopProducers.Store(true)
	wg.Wait()
}

func FuzzHelmIngestAndNavigate(f *testing.F) {
	f.Add("marlin", "files", "alpha.go", int8(1), int8(1))
	f.Add("", "buffers", "", int8(-3), int8(-1))
	f.Fuzz(func(t *testing.T, q, src, title string, moveDelta, scrollDelta int8) {
		eng := search.NewHelmEngine(256)
		g := eng.BeginQuery(q)
		eng.IngestBatch(g, search.SourceMeta{ID: src, Label: src}, []search.Candidate{
			{ID: "c1", Title: title},
			{ID: "c2", Title: title + "-2"},
		})
		eng.MoveSelection(int(moveDelta))
		eng.ScrollBy(int(scrollDelta))
		eng.JumpSource(int(moveDelta))
		eng.ToggleMarkCurrent()
		rows, totalRows, _, _, selRow, _ := eng.VisibleWindow(nil)
		if selRow >= totalRows && totalRows > 0 {
			t.Fatalf("invalid selRow=%d totalRows=%d", selRow, totalRows)
		}
		for _, r := range rows {
			if r.Selected && r.IsHeader {
				t.Fatalf("header selected in fuzz: %+v", r)
			}
		}
	})
}
