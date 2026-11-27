// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/egoist/mygo"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/notify"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

// wireComposerWorkspaceHooks connects the thin Tier-0 composer & transcript
// actions (model switch, context budget window, @-mentions from Rust
// projections, Retry/Fork turn actions, Cmd+F session search, and multi-session
// replay) to the live gRPC TurnStreamController (or standalone preview).
func wireComposerWorkspaceHooks(
	ctx context.Context,
	app *mygoshell.WorkbenchApp,
	transcript *mygoshell.TranscriptController,
	turnCtrl *grpcwire.TurnStreamController,
	usedTokens *atomic.Int64,
	win *mygo.Window,
	publishProjection func(string, any),
) {
	if app == nil || transcript == nil {
		return
	}
	invalidate := func() {
		if win != nil {
			win.Invalidate()
		}
	}

	// Initialize Context Budget Pill from the active model's advertised context window.
	activeWindow := func() int {
		if transcript.Composer != nil {
			if w := composer.ParseContextWindowTokens(transcript.Composer.Snapshot().ActiveModel.ContextLabel); w > 0 {
				return w
			}
		}
		return 200_000
	}
	transcript.SetContextUsage(int(usedTokens.Load()), activeWindow())

	transcript.OnModelChanged = func(m composer.ModelOption) {
		winToks := composer.ParseContextWindowTokens(m.ContextLabel)
		if winToks <= 0 {
			winToks = 200_000
		}
		transcript.SetContextUsage(int(usedTokens.Load()), winToks)
		effort := composer.EffortBalanced
		if transcript.Composer != nil {
			effort = transcript.Composer.Snapshot().ReasoningEffort
		}
		if publishProjection != nil {
			publishProjection("composer.model", map[string]any{
				"modelId":       m.ID,
				"label":         m.Label,
				"provider":      m.Provider,
				"contextWindow": winToks,
				"effort":        string(effort),
			})
		}
		if app.Notifications != nil {
			_, _ = app.Notifications.Center.Publish(notify.NotificationSpec{
				Severity: notify.SeverityInfo,
				Source:   notify.SourceHost,
				Title:    "Active Model Updated",
				Message:  fmt.Sprintf("%s (%s · %s)", m.Label, m.ContextLabel, effort),
			})
		}
		invalidate()
	}

	// Thin `@` context mention suggestions sourced exclusively from Rust's
	// read-only marlin.projection.v1 snapshot (zero local disk/git scans).
	transcript.OnQueryMentions = func(query string) []composer.ContextMention {
		if app.Projection == nil {
			return nil
		}
		snap := app.Projection.Snapshot()
		q := strings.ToLower(strings.TrimSpace(query))
		out := make([]composer.ContextMention, 0, 8)
		for _, f := range snap.DiffFiles {
			if q == "" || strings.Contains(strings.ToLower(f.Path), q) {
				out = append(out, composer.ContextMention{
					Kind:   "file",
					Target: f.Path,
					Label:  filepath.Base(f.Path),
				})
				if len(out) >= 8 {
					return out
				}
			}
		}
		for _, n := range snap.GraphNodes {
			if q == "" || strings.Contains(strings.ToLower(n.NodeID), q) || strings.Contains(strings.ToLower(n.Kind), q) {
				out = append(out, composer.ContextMention{
					Kind:   "node",
					Target: n.NodeID,
					Label:  n.NodeID,
				})
				if len(out) >= 8 {
					return out
				}
			}
		}
		for _, p := range snap.Processes {
			if q == "" || strings.Contains(strings.ToLower(p.ProcessID), q) || strings.Contains(strings.ToLower(p.Command), q) {
				out = append(out, composer.ContextMention{
					Kind:   "proc",
					Target: p.ProcessID,
					Label:  p.ProcessID,
				})
				if len(out) >= 8 {
					return out
				}
			}
		}
		return out
	}

	// Thin in-memory viewport search over currently loaded transcript rows
	// (no local disk DB or inverted search index).
	transcript.OnSearchSession = func(query string) []mygoshell.SessionSearchHit {
		q := strings.ToLower(strings.TrimSpace(query))
		if q == "" {
			return nil
		}
		n := transcript.Len()
		hits := make([]mygoshell.SessionSearchHit, 0, 16)
		for i := 0; i < n && len(hits) < 64; i++ {
			e, ok := transcript.Entry(i)
			if !ok {
				continue
			}
			hay := strings.ToLower(e.Title + "\n" + e.Text)
			if strings.Contains(hay, q) {
				snip := strings.TrimSpace(e.Text)
				if len(snip) > 96 {
					snip = snip[:96] + "..."
				}
				hits = append(hits, mygoshell.SessionSearchHit{Row: i, Snippet: snip})
			}
		}
		return hits
	}

	transcript.OnRetryTurn = func(turnOrdinal uint64) {
		prompt := findPromptForTurn(transcript, turnOrdinal)
		if prompt != "" && app.OnSendPrompt != nil {
			app.OnSendPrompt(prompt)
			invalidate()
		}
	}

	transcript.OnForkTurn = func(turnOrdinal uint64) {
		baseID := app.ActiveSessionID
		if baseID == "" {
			baseID = "session"
		}
		forkID := fmt.Sprintf("%s-fork-t%d", baseID, turnOrdinal)
		app.Sessions = append(app.Sessions, mygoshell.SessionSummaryView{
			SessionID: forkID,
			Title:     fmt.Sprintf("Fork @ Turn #%d", turnOrdinal),
			TurnCount: turnOrdinal,
		})
		app.ActiveSessionID = forkID
		if app.Notifications != nil {
			_, _ = app.Notifications.Center.Publish(notify.NotificationSpec{
				Severity: notify.SeveritySuccess,
				Source:   notify.SourceHost,
				Title:    "Session Forked",
				Message:  fmt.Sprintf("Created %s from Turn #%d", forkID, turnOrdinal),
			})
		}
		invalidate()
	}

	if turnCtrl != nil {
		bridge := grpcwire.NewComposerBridge(turnCtrl, nil)
		app.OnSelectSession = func(sessionID string) {
			app.ActiveSessionID = sessionID
			go func() {
				if summary, err := bridge.FetchSessionSummary(ctx, sessionID, sessionID); err == nil {
					for i := range app.Sessions {
						if app.Sessions[i].SessionID == sessionID {
							app.Sessions[i].TurnCount = summary.TurnCount
							app.Sessions[i].RecoveryState = summary.RecoveryState
							break
						}
					}
				}
				if _, _, err := turnCtrl.ReplayTurnHistory(ctx, sessionID, func(upd grpcwire.TurnStreamUpdate) {
					if upd.TextDelta != "" {
						transcript.ApplyCheckpoint(upd.TurnOrdinal, upd.EventID, upd.TextDelta)
					}
					if upd.HasOutcome {
						transcript.CompleteTurn(upd.TurnOrdinal, upd.Outcome.String())
					}
				}); err != nil {
					log.Printf("ReplayTurnHistory(%s): %v", sessionID, err)
					if app.Notifications != nil {
						_, _ = app.Notifications.Center.Publish(notify.NotificationSpec{
							Severity: notify.SeverityError,
							Source:   notify.SourceHost,
							Title:    "Session History Replay Failed",
							Message:  err.Error(),
						})
					}
				}
				invalidate()
			}()
		}
	}
}

func findPromptForTurn(tc *mygoshell.TranscriptController, turnOrdinal uint64) string {
	n := tc.Len()
	targetIdx := -1
	for i := n - 1; i >= 0; i-- {
		e, ok := tc.Entry(i)
		if ok && e.Role == mygoshell.RoleAssistant && e.TurnOrdinal == turnOrdinal {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		targetIdx = n
	}
	for i := targetIdx - 1; i >= 0; i-- {
		e, ok := tc.Entry(i)
		if ok && e.Role == mygoshell.RoleUser && strings.TrimSpace(e.Text) != "" {
			return e.Text
		}
	}
	return ""
}
