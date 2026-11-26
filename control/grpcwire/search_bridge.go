// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tao3k/marlin-mygo/engine/search"
	commonv1 "github.com/tao3k/marlin-mygo/gen/marlin/common/v1"
	projectionv1 "github.com/tao3k/marlin-mygo/gen/marlin/projection/v1"
	uiv1 "github.com/tao3k/marlin-mygo/gen/marlin/ui/v1"
)

// RustSearchBridge dispatches non-blocking, generation-fenced Consult queries
// to the Rust marlin-agent-core backend (ProjectionQueryService.QueryProjectGraph
// and UiWorkbenchService.BrowseCommands) and streams admitted hits directly into
// a 120Hz search.HelmEngine. Previous in-flight queries are cancelled immediately
// on every new keystroke.
type RustSearchBridge struct {
	Provider   TargetProvider
	Engine     *search.HelmEngine
	ProjectID  string
	SessionFn  func() string
	OnUpdated  func()
	Timeout    time.Duration
	PageLimit  uint32

	mu           sync.Mutex
	activeCancel context.CancelFunc
	activeGen    uint64
}

// QueryAsync cancels any previous in-flight Rust search RPC and launches a fresh
// background query bound to generation gen. Stale responses are discarded both
// via context cancellation and HelmEngine monotonic generation fencing.
func (b *RustSearchBridge) QueryAsync(parent context.Context, gen uint64, rawQuery string) {
	if b == nil || b.Provider == nil || b.Engine == nil {
		return
	}
	target := b.Provider.CurrentTarget()
	if target == nil {
		return
	}

	cq := search.ParseConsultQuery(rawQuery)
	timeout := b.Timeout
	if timeout <= 0 {
		timeout = 1800 * time.Millisecond
	}
	limit := b.PageLimit
	if limit == 0 {
		limit = 64
	}
	sessID := "marlin-main"
	if b.SessionFn != nil {
		if s := strings.TrimSpace(b.SessionFn()); s != "" {
			sessID = s
		}
	}
	projID := b.ProjectID
	if projID == "" {
		projID = "default"
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	b.mu.Lock()
	if b.activeCancel != nil {
		b.activeCancel()
	}
	b.activeCancel = cancel
	b.activeGen = gen
	b.mu.Unlock()

	go func() {
		defer cancel()
		var wg sync.WaitGroup

		if proj := target.Projection(); proj != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := proj.QueryProjectGraph(ctx, &projectionv1.QueryProjectGraphRequest{
					ProjectId: projID,
					SessionId: &sessID,
					Surfaces: []projectionv1.GraphVisibleSurface{
						projectionv1.GraphVisibleSurface_GRAPH_VISIBLE_SURFACE_WORKSPACE,
						projectionv1.GraphVisibleSurface_GRAPH_VISIBLE_SURFACE_MEMORY,
						projectionv1.GraphVisibleSurface_GRAPH_VISIBLE_SURFACE_TOOLS,
						projectionv1.GraphVisibleSurface_GRAPH_VISIBLE_SURFACE_SESSIONS,
						projectionv1.GraphVisibleSurface_GRAPH_VISIBLE_SURFACE_CONTENT,
						projectionv1.GraphVisibleSurface_GRAPH_VISIBLE_SURFACE_TOPOLOGY,
					},
					SecretVisibility: projectionv1.SecretVisibility_SECRET_VISIBILITY_DENIED,
					Query:            cq.BackendQuery,
					Page:             &commonv1.CursorPageRequest{MaxItems: limit},
				})
				if err != nil || ctx.Err() != nil || resp == nil {
					return
				}
				cands := make([]search.Candidate, 0, len(resp.GetRecords()))
				for _, rec := range resp.GetRecords() {
					title := strings.TrimSpace(rec.GetTitle())
					if title == "" {
						title = rec.GetRecordId()
					}
					loc := rec.GetSourcePath()
					if loc != "" && rec.GetStartLine() > 0 {
						loc = fmt.Sprintf("%s:L%d", loc, rec.GetStartLine())
						if rec.GetEndLine() > rec.GetStartLine() {
							loc = fmt.Sprintf("%s-%d", loc, rec.GetEndLine())
						}
					}
					if loc == "" {
						loc = rec.GetKind()
					}
					score, mask, ok := search.ScoreOrderlessAndMask(rawQuery, title+" "+rec.GetSummary()+" "+loc)
					if !ok {
						continue
					}
					// Recompute highlight mask strictly over Title for clean display highlighting.
					_, titleMask, _ := search.ScoreOrderlessAndMask(rawQuery, title)
					if titleMask != 0 {
						mask = titleMask
					}
					cands = append(cands, search.Candidate{
						ID:            "rust:" + rec.GetRecordId(),
						SourceID:      "rust-graph",
						Title:         title,
						Subtitle:      rec.GetKind(),
						Location:      loc,
						Snippet:       rec.GetSummary(),
						Score:         score + 20,
						MatchMask:     mask,
						PayloadKind:   "file",
						PayloadTarget: loc,
					})
				}
				if len(cands) > 0 {
					b.Engine.IngestBatch(gen, search.SourceMeta{
						ID:       "rust-graph",
						Label:    "Rust Search",
						Priority: 2,
					}, cands)
					if b.OnUpdated != nil {
						b.OnUpdated()
					}
				}
			}()
		}

		if uiClient := target.UI(); uiClient != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := uiClient.BrowseCommands(ctx, &uiv1.BrowseCommandsRequest{
					SessionId: sessID,
					Query:     cq.BackendQuery,
				})
				if err != nil || ctx.Err() != nil || resp == nil || resp.GetCandidates() == nil {
					return
				}
				names := resp.GetCandidates().GetNames()
				cands := make([]search.Candidate, 0, len(names))
				for _, name := range names {
					score, mask, ok := search.ScoreOrderlessAndMask(rawQuery, name)
					if !ok {
						continue
					}
					cands = append(cands, search.Candidate{
						ID:            "rust-cmd:" + name,
						SourceID:      "rust-commands",
						Title:         name,
						Subtitle:      "Scheme / Rust command",
						Location:      "M-x",
						Snippet:       "Execute admitted Scheme command: " + name,
						Score:         score + 10,
						MatchMask:     mask,
						PayloadKind:   "command",
						PayloadTarget: name,
					})
				}
				if len(cands) > 0 {
					b.Engine.IngestBatch(gen, search.SourceMeta{
						ID:       "rust-commands",
						Label:    "Scheme Commands",
						Priority: 6,
					}, cands)
					if b.OnUpdated != nil {
						b.OnUpdated()
					}
				}
			}()
		}

		wg.Wait()
	}()
}

// Cancel aborts any active in-flight Rust search RPC.
func (b *RustSearchBridge) Cancel() {
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.activeCancel != nil {
		b.activeCancel()
		b.activeCancel = nil
	}
	b.mu.Unlock()
}
