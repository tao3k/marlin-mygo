// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	appv1 "github.com/tao3k/marlin-mygo/gen/marlin/app/v1"
)

// TurnStreamUpdate represents one ordered durable event streamed from
// marlin.app.v1.AppServer.WatchSessionTurn.
type TurnStreamUpdate struct {
	SessionID   string
	TurnOrdinal uint64
	EventID     string
	Started     bool
	TextDelta   string
	HasOutcome  bool
	Outcome     appv1.SessionTurnOutcome
	ToolCall    *appv1.ToolCallEvent
	HumanGate   *appv1.HumanGateEvent
}

// WatchRetryPolicy configures automatic cursor-resuming reconnects for
// WatchTurnResilient across transient HTTP/2 drops or sidecar restarts.
type WatchRetryPolicy struct {
	InitialBackoff time.Duration // default 50ms
	MaxBackoff     time.Duration // default 2s
	MaxRetries     int           // 0 = default 8; < 0 = unlimited until ctx cancel
	OnReconnect    func(attempt int, afterEventID string, err error)
}

// TurnStreamController drives durable multi-turn session execution and
// resumable journal streaming against a live marlin.app.v1.AppServer Target
// or a hot-swappable TargetProvider (e.g. ResilientSidecarTarget / ManagedChannel).
type TurnStreamController struct {
	Target   *Target
	Provider TargetProvider
}

func NewTurnStreamController(target *Target) *TurnStreamController {
	return &TurnStreamController{Target: target}
}

// NewTurnStreamControllerWithProvider creates a TurnStreamController backed by
// a hot-swappable TargetProvider so active and subsequent turns survive sidecar restarts.
func NewTurnStreamControllerWithProvider(provider TargetProvider) *TurnStreamController {
	return &TurnStreamController{Provider: provider}
}

func (c *TurnStreamController) resolveTarget() (*Target, uint64, error) {
	if c == nil {
		return nil, 0, fmt.Errorf("grpcwire: TurnStreamController is nil")
	}
	t, gen := resolveProviderTarget(c.Provider, c.Target)
	if t == nil {
		return nil, gen, fmt.Errorf("grpcwire: TurnStreamController requires a non-nil Target or Provider")
	}
	return t, gen, nil
}

// OpenSession creates or attaches to an authorized durable session on the Rust host.
func (c *TurnStreamController) OpenSession(ctx context.Context, sessionID string, create bool) (string, error) {
	t, _, err := c.resolveTarget()
	if err != nil {
		return "", err
	}
	mode := appv1.SessionOpenMode_SESSION_OPEN_MODE_EXISTING
	if create {
		mode = appv1.SessionOpenMode_SESSION_OPEN_MODE_CREATE
	}
	resp, err := t.App().OpenSession(ctx, &appv1.OpenSessionRequest{
		SessionId: sessionID,
		Mode:      mode,
	})
	if err != nil {
		return "", err
	}
	return resp.GetSessionId(), nil
}

// StartTurn admits a durable user turn with an idempotent submission key and
// optimistic concurrency cursor (afterTurn).
func (c *TurnStreamController) StartTurn(
	ctx context.Context,
	sessionID string,
	userText string,
	afterTurn uint64,
	submissionID string,
) (*appv1.SessionTurnRef, error) {
	t, _, err := c.resolveTarget()
	if err != nil {
		return nil, err
	}
	resp, err := t.App().StartSessionTurn(ctx, &appv1.StartSessionTurnRequest{
		SessionId:    sessionID,
		UserText:     userText,
		AfterTurn:    afterTurn,
		SubmissionId: submissionID,
	})
	if err != nil {
		return nil, err
	}
	turn := resp.GetTurn()
	if turn == nil {
		return nil, fmt.Errorf("grpcwire: StartSessionTurn returned nil turn reference")
	}
	return turn, nil
}

// WatchTurn streams committed turn events from Rust's ConversationJournal
// starting strictly after afterEventID (or from turn start when empty).
func (c *TurnStreamController) WatchTurn(
	ctx context.Context,
	turn *appv1.SessionTurnRef,
	afterEventID string,
	onUpdate func(TurnStreamUpdate),
) (lastEventID string, outcome appv1.SessionTurnOutcome, err error) {
	t, _, err := c.resolveTarget()
	if err != nil {
		return "", appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_UNSPECIFIED, err
	}
	if turn == nil {
		return "", appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_UNSPECIFIED, fmt.Errorf("grpcwire: WatchTurn requires a non-nil turn reference")
	}
	req := &appv1.WatchSessionTurnRequest{Turn: turn}
	if afterEventID != "" {
		cursor := afterEventID
		req.AfterEventId = &cursor
	}
	stream, err := t.App().WatchSessionTurn(ctx, req)
	if err != nil {
		return afterEventID, appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_UNSPECIFIED, err
	}
	lastEventID = afterEventID
	for {
		ev, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				return lastEventID, outcome, nil
			}
			return lastEventID, outcome, recvErr
		}
		if ev.GetEventId() != "" {
			lastEventID = ev.GetEventId()
		}
		upd := TurnStreamUpdate{
			SessionID:   turn.GetSessionId(),
			TurnOrdinal: turn.GetOrdinal(),
			EventID:     ev.GetEventId(),
		}
		switch payload := ev.GetEvent().(type) {
		case *appv1.SessionTurnEvent_Started:
			upd.Started = true
		case *appv1.SessionTurnEvent_TextCheckpoint:
			upd.TextDelta = payload.TextCheckpoint.GetText()
		case *appv1.SessionTurnEvent_Outcome:
			upd.HasOutcome = true
			upd.Outcome = payload.Outcome
			outcome = payload.Outcome
		case *appv1.SessionTurnEvent_ToolCall:
			upd.ToolCall = payload.ToolCall
		case *appv1.SessionTurnEvent_HumanGate:
			upd.HumanGate = payload.HumanGate
		}
		if onUpdate != nil {
			onUpdate(upd)
		}
	}
}

// WatchTurnResilient wraps WatchTurn with bounded exponential backoff and
// strict ConversationJournal cursor resumption (afterEventID). If a transient
// HTTP/2 error, premature EOF before terminal outcome, or sidecar restart
// occurs mid-turn, WatchTurnResilient reconnects strictly after the last
// committed event ID with zero duplicated deltas and zero lost checkpoints.
func (c *TurnStreamController) WatchTurnResilient(
	ctx context.Context,
	turn *appv1.SessionTurnRef,
	afterEventID string,
	policy WatchRetryPolicy,
	onUpdate func(TurnStreamUpdate),
) (lastEventID string, outcome appv1.SessionTurnOutcome, err error) {
	if turn == nil {
		return "", appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_UNSPECIFIED, fmt.Errorf("grpcwire: WatchTurnResilient requires a non-nil turn reference")
	}
	initialBackoff := policy.InitialBackoff
	if initialBackoff <= 0 {
		initialBackoff = 50 * time.Millisecond
	}
	maxBackoff := policy.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 2 * time.Second
	}
	maxRetries := policy.MaxRetries
	if maxRetries == 0 {
		maxRetries = 8
	}

	cursor := afterEventID
	backoff := initialBackoff
	attempt := 0
	for {
		_, seenGen, _ := c.resolveTarget()
		nextCursor, out, watchErr := c.WatchTurn(ctx, turn, cursor, func(upd TurnStreamUpdate) {
			// Reset backoff whenever forward progress is made on the stream.
			backoff = initialBackoff
			if onUpdate != nil {
				onUpdate(upd)
			}
		})
		if nextCursor != "" {
			cursor = nextCursor
		}
		if out != appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_UNSPECIFIED {
			return cursor, out, nil
		}
		if watchErr == nil {
			// Stream closed cleanly (EOF) without terminal outcome; if caller
			// cancelled, return cleanly; otherwise treat as transient server close.
			if ctx.Err() != nil {
				return cursor, out, ctx.Err()
			}
			watchErr = io.ErrUnexpectedEOF
		}
		if !isRetryableStreamError(ctx, watchErr) {
			return cursor, out, watchErr
		}
		attempt++
		if maxRetries > 0 && attempt > maxRetries {
			return cursor, out, watchErr
		}
		if policy.OnReconnect != nil {
			policy.OnReconnect(attempt, cursor, watchErr)
		}
		if !waitReconnectOrSwap(ctx, c.Provider, seenGen, backoff) {
			return cursor, out, ctx.Err()
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// StartAndWatchTurn submits a new durable turn and streams all committed
// checkpoints until the terminal outcome arrives or ctx is cancelled.
func (c *TurnStreamController) StartAndWatchTurn(
	ctx context.Context,
	sessionID string,
	userText string,
	afterTurn uint64,
	submissionID string,
	onUpdate func(TurnStreamUpdate),
) (*appv1.SessionTurnRef, string, appv1.SessionTurnOutcome, error) {
	turn, err := c.StartTurn(ctx, sessionID, userText, afterTurn, submissionID)
	if err != nil {
		return nil, "", appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_UNSPECIFIED, err
	}
	lastEventID, outcome, err := c.WatchTurn(ctx, turn, "", onUpdate)
	return turn, lastEventID, outcome, err
}

// CancelTurn requests authoritative cancellation of an active turn's runtime task scope.
func (c *TurnStreamController) CancelTurn(ctx context.Context, turn *appv1.SessionTurnRef) (*appv1.CancelSessionTurnResponse, error) {
	t, _, err := c.resolveTarget()
	if err != nil {
		return nil, err
	}
	return t.App().CancelSessionTurn(ctx, &appv1.CancelSessionTurnRequest{Turn: turn})
}

// GetSessionSummary fetches the durable journal head and recovery state for a session.
func (c *TurnStreamController) GetSessionSummary(ctx context.Context, sessionID string) (*appv1.GetSessionSummaryResponse, error) {
	t, _, err := c.resolveTarget()
	if err != nil {
		return nil, err
	}
	return t.App().GetSessionSummary(ctx, &appv1.GetSessionSummaryRequest{SessionId: sessionID})
}

// GetSessionTurnStatus fetches the committed recovery state and outcome for one turn.
func (c *TurnStreamController) GetSessionTurnStatus(ctx context.Context, turn *appv1.SessionTurnRef) (*appv1.GetSessionTurnStatusResponse, error) {
	t, _, err := c.resolveTarget()
	if err != nil {
		return nil, err
	}
	return t.App().GetSessionTurnStatus(ctx, &appv1.GetSessionTurnStatusRequest{Turn: turn})
}

// GetSessionTurnCancellation fetches durable cancellation / RuntimeShutdownReceipt
// evidence recorded in Rust's ConversationJournal for one turn.
func (c *TurnStreamController) GetSessionTurnCancellation(ctx context.Context, turn *appv1.SessionTurnRef) (*appv1.GetSessionTurnCancellationResponse, error) {
	t, _, err := c.resolveTarget()
	if err != nil {
		return nil, err
	}
	return t.App().GetSessionTurnCancellation(ctx, &appv1.GetSessionTurnCancellationRequest{Turn: turn})
}

// ReplayTurnHistory queries GetSessionSummary and replays committed journal
// events for turns 1..=latestOrdinal in order (e.g. after a Rust sidecar restart).
func (c *TurnStreamController) ReplayTurnHistory(
	ctx context.Context,
	sessionID string,
	onUpdate func(TurnStreamUpdate),
) (uint64, *appv1.GetSessionSummaryResponse, error) {
	summary, err := c.GetSessionSummary(ctx, sessionID)
	if err != nil {
		return 0, nil, err
	}
	latest := summary.GetLatestTurn().GetOrdinal()
	for ord := uint64(1); ord <= latest; ord++ {
		ref := &appv1.SessionTurnRef{SessionId: sessionID, Ordinal: ord}
		if _, _, err := c.WatchTurn(ctx, ref, "", onUpdate); err != nil {
			return ord - 1, summary, err
		}
	}
	return latest, summary, nil
}

// StreamStatelessTurn streams a stateless text turn via AppServer.StreamTurn.
func (c *TurnStreamController) StreamStatelessTurn(
	ctx context.Context,
	messages []*appv1.ConversationMessage,
	onDelta func(sequence uint64, delta string),
) (string, error) {
	t, _, err := c.resolveTarget()
	if err != nil {
		return "", err
	}
	stream, err := t.App().StreamTurn(ctx, &appv1.StreamTurnRequest{Messages: messages})
	if err != nil {
		return "", err
	}
	var full strings.Builder
	for {
		ev, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				return full.String(), nil
			}
			return full.String(), recvErr
		}
		if td, ok := ev.GetEvent().(*appv1.TurnEvent_TextDelta); ok && td.TextDelta != nil {
			delta := td.TextDelta.GetText()
			full.WriteString(delta)
			if onDelta != nil {
				onDelta(td.TextDelta.GetSequence(), delta)
			}
		}
	}
}
