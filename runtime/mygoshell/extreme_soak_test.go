// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell_test

import (
	"fmt"
	"math/rand/v2"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/resilience"
	"github.com/tao3k/marlin-mygo/engine/search"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

// TestExtreme100kCompositeWindowTreeHelmTranscriptResilienceChaos exercises
// ALL FOUR core frontend engines simultaneously under heavy concurrent load
// (100,000 composite state transitions + live GPU frames under -race):
//  1. WindowTree multi-pane splits, 2D spatial nav, Winner-Mode undo/redo, tab hibernation
//  2. HelmMinibufferController + live multi-source streaming + 4 concurrent producers
//  3. TranscriptController Fenwick-virtualized streaming & folding
//  4. SessionResilience draft persistence, @ mentions, outbox queue/flush, replay dedup
func TestExtreme100kCompositeWindowTreeHelmTranscriptResilienceChaos(t *testing.T) {
	totalOps := 100_000
	if testing.Short() {
		totalOps = 20_000
	}

	proj := mygoshell.NewProjectionController(mygoshell.ProjectionSnapshot{
		DiffFiles: []mygoshell.WorkspaceDiffFileView{
			{Path: "engine/search/helm_stream.go", Status: "added", Additions: 770, HunkLines: []string{"+type HelmEngine struct {}"}},
			{Path: "engine/shell/window_tree.go", Status: "modified", Additions: 24, Deletions: 4, HunkLines: []string{"+Snapshot() 22ns 0 allocs"}},
		},
		GraphNodes: []mygoshell.GraphNodeReceiptView{
			{NodeID: "node-search-admit", Kind: "readiness-gate", Phase: "succeeded", DurationMS: 2, ReceiptID: "rcpt-001", Summary: "Tantivy search stream active"},
		},
		Processes: []mygoshell.ManagedProcessView{
			{ProcessID: "proc-app-server", Command: "marlin-app-server", State: "running", TailLines: []string{"listening on 127.0.0.1:50051"}},
		},
	})
	as := mygoshell.NewAdaptiveShell(mygoshell.AdaptiveShellConfig{
		BrandTitle: "Marlin Extreme Soak",
		Projection: proj,
	})
	app := as.Desktop
	mygoshell.BindDefaultHelmSources(app, nil)
	app.OpenHelmSearch()
	gen := app.HelmSearch.SetQuery("helm")

	var activeGen atomic.Uint64
	activeGen.Store(gen)
	var stopBg atomic.Bool
	var wg sync.WaitGroup

	// 4 concurrent background producers streaming live Rust search batches into HelmEngine.
	for p := 0; p < 4; p++ {
		pid := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(9000+pid), uint64(pid*31+7)))
			batch := make([]search.Candidate, 12)
			src := search.SourceMeta{ID: fmt.Sprintf("rust-src-%d", pid), Label: fmt.Sprintf("RustIdx-%d", pid), Priority: 10 + pid}
			for !stopBg.Load() {
				g := activeGen.Load()
				if g > 1 && rng.IntN(4) == 0 {
					g-- // inject stale generation!
				}
				for i := range batch {
					batch[i] = search.Candidate{
						ID: fmt.Sprintf("bg-%d-%d-%d", pid, g, i), SourceID: src.ID,
						Title: fmt.Sprintf("ripgrep_hit_%d_%d", pid, i), Location: "crates/core/src/search.rs:99",
						Score: int32(500 - i), MatchMask: 0x0f, PayloadKind: "file", PayloadTarget: "crates/core/src/search.rs",
					}
				}
				app.HelmSearch.Engine.IngestBatch(g, src, batch)
				time.Sleep(time.Millisecond)
			}
		}()
	}

	tt := ui.NewTester(app.View, 1280, 820)
	tt.Frame()

	runtime.GC()
	var memBefore, memAfter runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	rng := rand.New(rand.NewPCG(20261126, 100000))
	buffers := []shell.PaneBuffer{
		{BufferID: "builtin:transcript", Title: "Transcript", Kind: "native-gpu"},
		{BufferID: "builtin:projection", Title: "Projection", Kind: "native-gpu"},
		{BufferID: "slot:workbench.main", Title: "Workspace", Kind: "wasm-gpu"},
	}
	dirs := []shell.Direction{shell.DirLeft, shell.DirRight, shell.DirUp, shell.DirDown}
	scratch := make([]search.VisibleRow, 0, 16)
	now := time.Now()

	for step := 0; step < totalOps; step++ {
		switch rng.IntN(16) {
		case 0:
			_, _ = app.Windows.SplitRight(buffers[rng.IntN(len(buffers))])
		case 1:
			_, _ = app.Windows.SplitBelow(buffers[rng.IntN(len(buffers))])
		case 2:
			_ = app.Windows.DeleteFocused()
		case 3:
			app.Windows.FocusDirection(dirs[rng.IntN(len(dirs))])
		case 4:
			_ = app.Windows.Undo()
		case 5:
			_ = app.Windows.Redo()
		case 6:
			_, _ = app.Windows.OpenTabInFocused(buffers[rng.IntN(len(buffers))])
		case 7:
			now = now.Add(2 * time.Minute)
			app.Windows.ReapIdleTabs(now)
		case 8: // Rapid Helm query preemption!
			if rng.IntN(25) == 0 {
				g := app.HelmSearch.SetQuery(fmt.Sprintf("q%d", step%17))
				activeGen.Store(g)
			}
		case 9:
			app.HelmSearch.Engine.MoveSelection(rng.IntN(7) - 3)
		case 10:
			app.HelmSearch.Engine.JumpSource(+1)
		case 11:
			app.HelmSearch.Engine.ScrollBy(rng.IntN(21) - 10)
		case 12:
			rows, totalRows, _, _, selRow, _ := app.HelmSearch.Engine.VisibleWindow(scratch)
			if selRow >= totalRows && totalRows > 0 {
				t.Fatalf("step %d: invalid Helm selRow=%d totalRows=%d", step, selRow, totalRows)
			}
			for _, r := range rows {
				if r.Selected && r.IsHeader {
					t.Fatalf("step %d: Helm selected header row", step)
				}
			}
		case 13: // Transcript streaming + resilience dedup.
			if step%10 == 0 {
				ord := uint64(step/10 + 1)
				evID := fmt.Sprintf("soak-ev-%d", ord)
				if dup, _ := app.Transcript.Resilience.AdmitEvent("chat:soak", evID, ord, ord); dup {
					t.Fatalf("step %d: fresh event %s falsely marked duplicate", step, evID)
				}
				app.Transcript.ApplyCheckpoint(ord, evID, "chunk ")
			}
		case 14: // Resilience draft + @ mention + outbox turnover.
			app.Transcript.Resilience.SetDraft("chat:soak", fmt.Sprintf("draft-%d", step))
			if step%50 == 0 {
				app.Transcript.Composer.AddMention(composer.ContextMention{Kind: "file", Target: "engine/search/helm_stream.go", Label: "@helm_stream.go"})
				app.Transcript.SyncDraftMentionsToResilience()
				_, _ = app.Transcript.Resilience.Outbox.Enqueue("chat:soak", "you", "", fmt.Sprintf("queued-%d", step))
				app.Transcript.Resilience.Outbox.Flush("chat:soak", func(_ resilience.OutboxItem) error { return nil })
			}
		case 15: // Periodic real GPU frame + WindowTree invariant check.
			snap := app.Windows.Snapshot()
			if snap.LeafCount < 1 || snap.LeafCount > shell.MaxOpenWindows {
				t.Fatalf("step %d: WindowTree LeafCount=%d out of bounds", step, snap.LeafCount)
			}
			if step%500 == 0 {
				tt.Frame()
			}
		}
	}

	stopBg.Store(true)
	wg.Wait()
	tt.Frame()

	runtime.GC()
	runtime.ReadMemStats(&memAfter)
	if memAfter.HeapAlloc > memBefore.HeapAlloc+64*1024*1024 {
		t.Fatalf("excessive heap retention after %d composite ops: before=%dMB after=%dMB",
			totalOps, memBefore.HeapAlloc/(1024*1024), memAfter.HeapAlloc/(1024*1024))
	}
}
