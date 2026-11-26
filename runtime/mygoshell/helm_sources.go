// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tao3k/marlin-mygo/engine/search"
)

// BindDefaultHelmSources connects app.HelmSearch to live in-memory host
// projections (open Emacs WindowTree buffers/tabs, Rust Projection DiffFiles /
// GraphNodes / Processes, loaded Transcript rows, and Command Palette entries)
// with zero local disk or git scans. An optional backendStreamFn may also push
// async Rust Tantivy/ripgrep batches into app.HelmSearch.Engine.IngestBatch.
func BindDefaultHelmSources(app *WorkbenchApp, backendStreamFn func(gen uint64, query string)) {
	if app == nil {
		return
	}
	if app.HelmSearch == nil {
		app.HelmSearch = NewHelmMinibufferController(search.DefaultMaxCandidates)
	}
	hm := app.HelmSearch
	if hm.OnExecuteAction == nil {
		hm.OnExecuteAction = app.handleHelmAction
	}
	hm.OnQueryChanged = func(gen uint64, query string) {
		q := strings.TrimSpace(query)

		// 1. Open Buffers & Tabs (Priority 1)
		if app.Windows != nil || len(app.WindowBuffers) > 0 {
			seen := make(map[string]struct{}, 16)
			var cands []search.Candidate
			if app.Windows != nil {
				for _, tab := range app.Windows.AllTabs() {
					b := tab.Tab.Buffer
					if b.BufferID == "" {
						continue
					}
					if _, dup := seen[b.BufferID]; dup {
						continue
					}
					seen[b.BufferID] = struct{}{}
					title := b.Title
					if title == "" {
						title = b.BufferID
					}
					score, mask, ok := search.ScoreOrderlessAndMask(q, title+" "+b.BufferID)
					if !ok {
						continue
					}
					if _, tm, tok := search.ScoreOrderlessAndMask(q, title); tok && tm != 0 {
						mask = tm
					}
					cands = append(cands, search.Candidate{
						ID:            "buf:" + b.BufferID,
						SourceID:      "buffers",
						Title:         title,
						Location:      fmt.Sprintf("%s · %s", tab.WindowID, strings.ToUpper(string(tab.Tab.State))),
						Snippet:       fmt.Sprintf("BufferID: %s\nKind: %s\nPane: %s", b.BufferID, b.Kind, tab.WindowID),
						Score:         score,
						MatchMask:     mask,
						PayloadKind:   "buffer",
						PayloadTarget: b.BufferID,
					})
				}
			}
			for _, b := range app.WindowBuffers {
				if b.BufferID == "" {
					continue
				}
				if _, dup := seen[b.BufferID]; dup {
					continue
				}
				seen[b.BufferID] = struct{}{}
				title := b.Title
				if title == "" {
					title = b.BufferID
				}
				score, mask, ok := search.ScoreAndMask(q, title+" "+b.BufferID)
				if !ok {
					continue
				}
				cands = append(cands, search.Candidate{
					ID:            "buf:" + b.BufferID,
					SourceID:      "buffers",
					Title:         title,
					Location:      b.Kind,
					Snippet:       fmt.Sprintf("Registered Buffer: %s (%s)", b.BufferID, b.Kind),
					Score:         score,
					MatchMask:     mask,
					PayloadKind:   "buffer",
					PayloadTarget: b.BufferID,
				})
			}
			sortCandidatesByScore(cands)
			hm.Engine.IngestBatch(gen, search.SourceMeta{ID: "buffers", Label: "Buffers", Priority: 1}, cands)
		}

		// 2. Rust Read-Only Projection Surface: Diff Files, Graph Nodes & Managed Processes (Priority 2-3)
		if app.Projection != nil {
			psnap := app.Projection.Snapshot()
			var fileCands []search.Candidate
			for _, f := range psnap.DiffFiles {
				score, mask, ok := search.ScoreOrderlessAndMask(q, f.Path)
				if !ok {
					continue
				}
				snip := strings.Join(f.HunkLines, "\n")
				if snip == "" {
					snip = fmt.Sprintf("%s (+%d -%d)", f.Status, f.Additions, f.Deletions)
				}
				fileCands = append(fileCands, search.Candidate{
					ID:            "file:" + f.Path,
					SourceID:      "files",
					Title:         f.Path,
					Location:      fmt.Sprintf("+%d -%d · %s", f.Additions, f.Deletions, f.Status),
					Snippet:       snip,
					Score:         score,
					MatchMask:     mask,
					PayloadKind:   "file",
					PayloadTarget: f.Path,
				})
			}
			sortCandidatesByScore(fileCands)
			hm.Engine.IngestBatch(gen, search.SourceMeta{ID: "files", Label: "Workspace Files", Priority: 2}, fileCands)

			var symCands []search.Candidate
			for _, n := range psnap.GraphNodes {
				score, mask, ok := search.ScoreAndMask(q, n.NodeID+" "+n.Kind+" "+n.Summary)
				if !ok {
					continue
				}
				symCands = append(symCands, search.Candidate{
					ID:            "node:" + n.NodeID,
					SourceID:      "nodes",
					Title:         n.NodeID,
					Location:      fmt.Sprintf("%s · %s · %dms", n.Kind, n.Phase, n.DurationMS),
					Snippet:       strings.TrimSpace(n.Summary + "\nReceipt: " + n.ReceiptID + "\n" + n.GatewayNote),
					Score:         score,
					MatchMask:     mask,
					PayloadKind:   "node",
					PayloadTarget: n.NodeID,
				})
			}
			for _, p := range psnap.Processes {
				score, mask, ok := search.ScoreAndMask(q, p.ProcessID+" "+p.Command)
				if !ok {
					continue
				}
				symCands = append(symCands, search.Candidate{
					ID:            "proc:" + p.ProcessID,
					SourceID:      "nodes",
					Title:         p.ProcessID + " · " + p.Command,
					Location:      fmt.Sprintf("%s (exit %d)", p.State, p.ExitCode),
					Snippet:       strings.Join(p.TailLines, "\n"),
					Score:         score,
					MatchMask:     mask,
					PayloadKind:   "proc",
					PayloadTarget: p.ProcessID,
				})
			}
			sortCandidatesByScore(symCands)
			hm.Engine.IngestBatch(gen, search.SourceMeta{ID: "nodes", Label: "Graph & Procs", Priority: 3}, symCands)
		}

		// 3. Loaded Transcript Turns (Priority 4, bounded viewport scan)
		if app.Transcript != nil {
			n := app.Transcript.Len()
			var turnCands []search.Candidate
			limitScan := n
			if limitScan > 500 {
				limitScan = 500
			}
			for i := n - 1; i >= n-limitScan && len(turnCands) < 64; i-- {
				e, ok := app.Transcript.Entry(i)
				if !ok || strings.TrimSpace(e.Text) == "" {
					continue
				}
				score, mask, matched := search.ScoreAndMask(q, e.Title+" "+e.Text)
				if !matched {
					continue
				}
				title := strings.TrimSpace(e.Text)
				if len(title) > 64 {
					title = title[:64] + "..."
				}
				turnCands = append(turnCands, search.Candidate{
					ID:            fmt.Sprintf("turn:%d", i),
					SourceID:      "transcript",
					Title:         title,
					Location:      fmt.Sprintf("Row #%d · %s", i+1, e.Role),
					Snippet:       e.Text,
					Score:         score,
					MatchMask:     mask,
					PayloadKind:   "turn",
					PayloadTarget: "builtin:transcript",
				})
			}
			sortCandidatesByScore(turnCands)
			hm.Engine.IngestBatch(gen, search.SourceMeta{ID: "transcript", Label: "Transcript", Priority: 4}, turnCands)
		}

		// 4. Studio & Feature Commands (Priority 5)
		if len(app.Commands) > 0 {
			var cmdCands []search.Candidate
			for _, cmd := range app.Commands {
				score, mask, ok := search.ScoreAndMask(q, cmd.Name+" "+cmd.Description)
				if !ok {
					continue
				}
				cmdCands = append(cmdCands, search.Candidate{
					ID:            "cmd:" + cmd.Name,
					SourceID:      "commands",
					Title:         cmd.Name,
					Location:      "command",
					Snippet:       cmd.Description,
					Score:         score,
					MatchMask:     mask,
					PayloadKind:   "command",
					PayloadTarget: cmd.Name,
				})
			}
			sortCandidatesByScore(cmdCands)
			hm.Engine.IngestBatch(gen, search.SourceMeta{ID: "commands", Label: "Commands", Priority: 5}, cmdCands)
		}

		if backendStreamFn != nil {
			backendStreamFn(gen, q)
		}
	}

	hm.OnRequestPreview = func(gen uint64, cand search.Candidate) {
		body := strings.TrimSpace(cand.Snippet)
		if body == "" {
			body = cand.Location
		}
		hm.Engine.UpdatePreview(gen, cand.ID, cand.Title, body)
	}
}

func sortCandidatesByScore(cands []search.Candidate) {
	sort.SliceStable(cands, func(i, j int) bool {
		return cands[i].Score > cands[j].Score
	})
}
