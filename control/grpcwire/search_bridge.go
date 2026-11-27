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
	"github.com/tao3k/marlin-mygo/runtime/async"
)

// RustSearchBridge dispatches non-blocking, generation-fenced Consult / DSL
// queries strictly to the authoritative Rust marlin-agent-core backend
// (ProjectionQueryService.QueryProjectGraph, UiWorkbenchService.BrowseCommands,
// and UiWorkbenchService.DispatchUiEvent for Embark actions) and streams
// admitted hits directly into a 120Hz search.HelmEngine. Zero backend behavior
// is fabricated in Go; previous in-flight queries are cancelled on every
// keystroke via StructuredScope.
type RustSearchBridge struct {
	Provider  TargetProvider
	Engine    *search.HelmEngine
	ProjectID string
	SessionFn func() string
	OnUpdated func()
	Timeout   time.Duration
	PageLimit uint32

	mu           sync.Mutex
	activeCancel context.CancelFunc
	activeGen    uint64
	scope        *async.StructuredScope
}

func (b *RustSearchBridge) ensureScope() *async.StructuredScope {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.scope == nil {
		b.scope = async.NewStructuredScope(context.Background())
	}
	return b.scope
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

	scope := b.ensureScope()
	scope.Go(func(_ context.Context) {
		defer cancel()
		var wg sync.WaitGroup

		if proj := target.Projection(); proj != nil {
			wg.Add(1)
			if !scope.Go(func(_ context.Context) {
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
					var score int32 = 80
					var mask uint64
					if !cq.IsDSL && (cq.HasStageSep || len(cq.Negative) > 0 || len(cq.Positive) > 0) {
						s, m, ok := search.ScoreOrderlessAndMask(rawQuery, title+" "+rec.GetSummary()+" "+loc)
						if !ok {
							continue
						}
						score = s + 20
						mask = m
						if _, titleMask, _ := search.ScoreOrderlessAndMask(rawQuery, title); titleMask != 0 {
							mask = titleMask
						}
					}
					srcID := "rust-graph"
					srcLabel := "Workspace Search"
					prio := 2
					if cq.IsDSL || strings.HasPrefix(rec.GetKind(), "dsl.") || strings.HasPrefix(rec.GetKind(), "ascent.") {
						srcID = "rust-dsl"
						srcLabel = "Ascent / Dusa DSL"
						prio = 1
					}
					sub := rec.GetKind()
					if digest := rec.GetFacets()["generation_digest"]; digest != "" {
						if len(digest) > 10 {
							digest = digest[:10]
						}
						sub = sub + " · #" + digest
					}
					cands = append(cands, search.Candidate{
						ID:            "rust:" + rec.GetRecordId(),
						SourceID:      srcID,
						Title:         title,
						Subtitle:      sub,
						Location:      loc,
						Snippet:       rec.GetSummary(),
						Score:         score,
						MatchMask:     mask,
						PayloadKind:   "file",
						PayloadTarget: loc,
					})
					_ = srcLabel
					_ = prio
				}
				if len(cands) > 0 {
					srcID := "rust-graph"
					srcLabel := "Workspace Search"
					prio := 2
					if cq.IsDSL {
						srcID = "rust-dsl"
						srcLabel = "Ascent / Dusa DSL"
						prio = 1
					}
					b.Engine.IngestBatch(gen, search.SourceMeta{
						ID:       srcID,
						Label:    srcLabel,
						Priority: prio,
					}, cands)
					if b.OnUpdated != nil {
						b.OnUpdated()
					}
				}
			}) {
				wg.Done()
			}
		}

		if uiClient := target.UI(); uiClient != nil && !cq.IsDSL {
			wg.Add(1)
			if !scope.Go(func(_ context.Context) {
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
						Subtitle:      "Rust / Scheme command",
						Location:      "M-x",
						Snippet:       "Execute admitted Rust/Scheme command: " + name,
						Score:         score + 10,
						MatchMask:     mask,
						PayloadKind:   "command",
						PayloadTarget: name,
					})
				}
				if len(cands) > 0 {
					b.Engine.IngestBatch(gen, search.SourceMeta{
						ID:       "rust-commands",
						Label:    "Commands (M-x)",
						Priority: 6,
					}, cands)
					if b.OnUpdated != nil {
						b.OnUpdated()
					}
				}
			}) {
				wg.Done()
			}
		}

		wg.Wait()
	})
}

// DispatchHelmAction forwards an Embark action (e.g. "open", "export-context",
// "dispatch-agent", or command execution) directly to the Rust host via
// UiWorkbenchService.DispatchUiEvent instead of fabricating backend behavior in Go.
func (b *RustSearchBridge) DispatchHelmAction(ctx context.Context, action string, targets []search.Candidate) error {
	if b == nil || b.Provider == nil {
		return nil
	}
	target := b.Provider.CurrentTarget()
	if target == nil || target.UI() == nil {
		return nil
	}
	sessID := "marlin-main"
	if b.SessionFn != nil {
		if s := strings.TrimSpace(b.SessionFn()); s != "" {
			sessID = s
		}
	}
	refs := make([]string, 0, len(targets))
	for _, c := range targets {
		if c.PayloadTarget != "" {
			refs = append(refs, c.PayloadTarget)
		} else if c.Location != "" {
			refs = append(refs, c.Location)
		} else {
			refs = append(refs, c.Title)
		}
	}
	cmdLine := "helm." + strings.TrimSpace(action)
	if action == "command" && len(refs) > 0 {
		cmdLine = refs[0]
	} else if len(refs) > 0 {
		cmdLine = cmdLine + " " + strings.Join(refs, " ")
	}
	_, err := target.UI().DispatchUiEvent(ctx, &uiv1.DispatchUiEventRequest{
		SessionId: sessID,
		Event: &uiv1.DispatchUiEventRequest_RecordCommand{
			RecordCommand: cmdLine,
		},
	})
	return err
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

// Close cancels and drains all in-flight Rust search goroutines cleanly.
func (b *RustSearchBridge) Close() {
	if b == nil {
		return
	}
	b.Cancel()
	b.mu.Lock()
	sc := b.scope
	b.scope = nil
	b.mu.Unlock()
	if sc != nil {
		sc.CloseAndDrain()
	}
}
