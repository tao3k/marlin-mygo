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

// TurnStreamController drives durable multi-turn session execution and
// resumable journal streaming against a live marlin.app.v1.AppServer Target.
type TurnStreamController struct {
	Target *Target
}

func NewTurnStreamController(target *Target) *TurnStreamController {
	return &TurnStreamController{Target: target}
}

// OpenSession creates or attaches to an authorized durable session on the Rust host.
func (c *TurnStreamController) OpenSession(ctx context.Context, sessionID string, create bool) (string, error) {
	if c == nil || c.Target == nil {
		return "", fmt.Errorf("grpcwire: TurnStreamController requires a non-nil Target")
	}
	mode := appv1.SessionOpenMode_SESSION_OPEN_MODE_EXISTING
	if create {
		mode = appv1.SessionOpenMode_SESSION_OPEN_MODE_CREATE
	}
	resp, err := c.Target.App().OpenSession(ctx, &appv1.OpenSessionRequest{
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
	if c == nil || c.Target == nil {
		return nil, fmt.Errorf("grpcwire: TurnStreamController requires a non-nil Target")
	}
	resp, err := c.Target.App().StartSessionTurn(ctx, &appv1.StartSessionTurnRequest{
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
	if c == nil || c.Target == nil {
		return "", appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_UNSPECIFIED, fmt.Errorf("grpcwire: TurnStreamController requires a non-nil Target")
	}
	if turn == nil {
		return "", appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_UNSPECIFIED, fmt.Errorf("grpcwire: WatchTurn requires a non-nil turn reference")
	}
	req := &appv1.WatchSessionTurnRequest{Turn: turn}
	if afterEventID != "" {
		cursor := afterEventID
		req.AfterEventId = &cursor
	}
	stream, err := c.Target.App().WatchSessionTurn(ctx, req)
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
	if c == nil || c.Target == nil {
		return nil, fmt.Errorf("grpcwire: TurnStreamController requires a non-nil Target")
	}
	return c.Target.App().CancelSessionTurn(ctx, &appv1.CancelSessionTurnRequest{Turn: turn})
}

// GetSessionSummary fetches the durable journal head and recovery state for a session.
func (c *TurnStreamController) GetSessionSummary(ctx context.Context, sessionID string) (*appv1.GetSessionSummaryResponse, error) {
	if c == nil || c.Target == nil {
		return nil, fmt.Errorf("grpcwire: TurnStreamController requires a non-nil Target")
	}
	return c.Target.App().GetSessionSummary(ctx, &appv1.GetSessionSummaryRequest{SessionId: sessionID})
}

// GetSessionTurnStatus fetches the committed recovery state and outcome for one turn.
func (c *TurnStreamController) GetSessionTurnStatus(ctx context.Context, turn *appv1.SessionTurnRef) (*appv1.GetSessionTurnStatusResponse, error) {
	if c == nil || c.Target == nil {
		return nil, fmt.Errorf("grpcwire: TurnStreamController requires a non-nil Target")
	}
	return c.Target.App().GetSessionTurnStatus(ctx, &appv1.GetSessionTurnStatusRequest{Turn: turn})
}

// GetSessionTurnCancellation fetches durable cancellation / RuntimeShutdownReceipt
// evidence recorded in Rust's ConversationJournal for one turn.
func (c *TurnStreamController) GetSessionTurnCancellation(ctx context.Context, turn *appv1.SessionTurnRef) (*appv1.GetSessionTurnCancellationResponse, error) {
	if c == nil || c.Target == nil {
		return nil, fmt.Errorf("grpcwire: TurnStreamController requires a non-nil Target")
	}
	return c.Target.App().GetSessionTurnCancellation(ctx, &appv1.GetSessionTurnCancellationRequest{Turn: turn})
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
	if c == nil || c.Target == nil {
		return "", fmt.Errorf("grpcwire: TurnStreamController requires a non-nil Target")
	}
	stream, err := c.Target.App().StreamTurn(ctx, &appv1.StreamTurnRequest{Messages: messages})
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
