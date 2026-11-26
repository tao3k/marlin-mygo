// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package promptgate owns Marlin's Tier-0 interactive human-in-the-loop prompt
// and authorization queue (Ask freeform questions, single/multi-select Option
// pickers, and fail-closed marlin.gate.v1 Authorization gates) shared across
// Desktop, Mobile (iOS/Android), and Web WASM shells.
package promptgate

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	ErrPromptInvalid       = errors.New("promptgate: prompt ID and question text are required")
	ErrPromptQueueFull     = errors.New("promptgate: pending prompt queue limit reached")
	ErrPromptNotFound      = errors.New("promptgate: prompt not found or already resolved")
	ErrAnswerRequired      = errors.New("promptgate: an answer is required before submitting")
	ErrSelectionRequired   = errors.New("promptgate: select at least one option before submitting")
	ErrInvalidOptionChoice = errors.New("promptgate: selected option ID is not in prompt choices")
	ErrInvalidAuthAction   = errors.New("promptgate: invalid authorization decision action")
)

const maxQueuedPrompts = 32

// PromptKind classifies the interactive Tier-0 modal / bottom-sheet family.
type PromptKind string

const (
	PromptAsk    PromptKind = "ask"
	PromptOption PromptKind = "option"
	PromptAuth   PromptKind = "auth"
)

// RiskLevel communicates visual urgency for Tier-0 Authorization gates.
type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

// AuthGateFamily mirrors marlin.gate.v1.GateKind without coupling UI state to gRPC.
type AuthGateFamily string

const (
	AuthToolReview       AuthGateFamily = "tool_review"
	AuthGraphHumanGate   AuthGateFamily = "graph_human_gate"
	AuthDeviceCapability AuthGateFamily = "device_capability"
)

// ActionKind identifies the user's explicit terminal decision on a prompt/gate.
type ActionKind string

const (
	ActionSubmit       ActionKind = "submit"
	ActionCancel       ActionKind = "cancel"
	ActionApproveOnce  ActionKind = "approve_once"
	ActionReject       ActionKind = "reject"
	ActionAskQuestion  ActionKind = "ask_question"
	ActionRequestPatch ActionKind = "request_patch"
)

// OptionChoice describes one structured option card rendered in Option or Auth prompts.
type OptionChoice struct {
	ID          string
	Label       string
	Description string
	Recommended bool
}

// PromptRequest is an immutable specification for one Tier-0 Ask, Option, or
// Authorization popup displayed on Desktop modals and Mobile bottom sheets.
type PromptRequest struct {
	ID              string
	Kind            PromptKind
	Title           string
	Prompt          string
	Description     string
	Placeholder     string
	ToolName        string
	CallID          string
	AuthFamily      AuthGateFamily
	Risk            RiskLevel
	DetailsHeading  string
	DetailsSummary  string
	Choices         []OptionChoice
	MultiSelect     bool
	AllowCustom     bool
	RequireNote     bool
	ExpiresAtUnixMS uint64
}

// PromptResponse is the validated, one-shot user decision emitted when a
// prompt or authorization gate is claimed and removed from the queue.
type PromptResponse struct {
	PromptID       string
	Kind           PromptKind
	Action         ActionKind
	SelectedIDs    []string
	TextInput      string
	IdempotencyKey string
}

// Queue maintains an ordered FIFO queue of pending Tier-0 prompts with
// one-shot atomic resolution so concurrent agent questions or gates never
// overwrite each other or replay decisions.
type Queue struct {
	mu      sync.RWMutex
	items   []PromptRequest
	claimed map[string]struct{}
	seq     uint64
}

// NewQueue constructs an empty fail-closed PromptGate Queue.
func NewQueue() *Queue {
	return &Queue{
		claimed: make(map[string]struct{}),
	}
}

// Enqueue appends req into the FIFO queue, ignoring duplicate pending IDs and
// rejecting already-claimed IDs or malformed requests fail-closed.
func (q *Queue) Enqueue(req PromptRequest) error {
	req.ID = strings.TrimSpace(req.ID)
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.ID == "" || req.Prompt == "" {
		return ErrPromptInvalid
	}
	if req.Kind == "" {
		req.Kind = PromptAuth
	}
	if req.Kind == PromptAuth && req.Risk == "" {
		req.Risk = RiskMedium
	}
	if req.Kind == PromptAuth && req.AuthFamily == "" {
		req.AuthFamily = AuthToolReview
	}
	req.Choices = append([]OptionChoice(nil), req.Choices...)

	q.mu.Lock()
	defer q.mu.Unlock()
	if _, alreadyClaimed := q.claimed[req.ID]; alreadyClaimed {
		return ErrPromptNotFound
	}
	for i := range q.items {
		if q.items[i].ID == req.ID {
			q.items[i] = req
			return nil
		}
	}
	if len(q.items) >= maxQueuedPrompts {
		return ErrPromptQueueFull
	}
	q.items = append(q.items, req)
	return nil
}

// Active returns the head prompt in the queue along with its 1-based position
// and total queued count.
func (q *Queue) Active() (req PromptRequest, pos int, total int, ok bool) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if len(q.items) == 0 {
		return PromptRequest{}, 0, 0, false
	}
	cp := q.items[0]
	cp.Choices = append([]OptionChoice(nil), cp.Choices...)
	return cp, 1, len(q.items), true
}

// Len reports the number of currently pending prompts in the queue.
func (q *Queue) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.items)
}

// HasPending reports whether at least one prompt or gate awaits user action.
func (q *Queue) HasPending() bool {
	return q.Len() > 0
}

// List returns a shallow snapshot of all pending prompts in FIFO order.
func (q *Queue) List() []PromptRequest {
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := make([]PromptRequest, len(q.items))
	copy(out, q.items)
	return out
}

// Resolve validates and atomically claims promptID, removing it from the queue
// and marking its ID as claimed so no replay can re-submit a gate decision.
func (q *Queue) Resolve(promptID string, resp PromptResponse) (PromptRequest, PromptResponse, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	idx := -1
	for i := range q.items {
		if q.items[i].ID == promptID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return PromptRequest{}, PromptResponse{}, ErrPromptNotFound
	}
	req := q.items[idx]
	resp.PromptID = req.ID
	resp.Kind = req.Kind
	resp.TextInput = strings.TrimSpace(resp.TextInput)

	if err := validateResponse(req, resp); err != nil {
		return PromptRequest{}, PromptResponse{}, err
	}

	q.seq++
	if strings.TrimSpace(resp.IdempotencyKey) == "" {
		resp.IdempotencyKey = fmt.Sprintf("pg-%s-%s-%d", req.ID, resp.Action, q.seq)
	}
	resp.SelectedIDs = append([]string(nil), resp.SelectedIDs...)

	q.items = append(q.items[:idx], q.items[idx+1:]...)
	q.claimed[req.ID] = struct{}{}
	return req, resp, nil
}

func validateResponse(req PromptRequest, resp PromptResponse) error {
	switch req.Kind {
	case PromptAsk:
		if resp.Action == ActionCancel {
			return nil
		}
		if resp.Action != ActionSubmit {
			return ErrAnswerRequired
		}
		if resp.TextInput == "" {
			return ErrAnswerRequired
		}
		return nil

	case PromptOption:
		if resp.Action == ActionCancel {
			return nil
		}
		if resp.Action != ActionSubmit {
			return ErrSelectionRequired
		}
		valid := make(map[string]struct{}, len(req.Choices))
		for _, ch := range req.Choices {
			valid[ch.ID] = struct{}{}
		}
		for _, sid := range resp.SelectedIDs {
			if _, ok := valid[sid]; !ok {
				return fmt.Errorf("%w: %q", ErrInvalidOptionChoice, sid)
			}
		}
		if len(resp.SelectedIDs) == 0 && !(req.AllowCustom && resp.TextInput != "") {
			return ErrSelectionRequired
		}
		if !req.MultiSelect && len(resp.SelectedIDs) > 1 {
			return fmt.Errorf("%w: multiple selections not allowed", ErrInvalidOptionChoice)
		}
		return nil

	case PromptAuth:
		switch resp.Action {
		case ActionApproveOnce, ActionReject:
			if req.RequireNote && resp.TextInput == "" {
				return ErrAnswerRequired
			}
			return nil
		case ActionAskQuestion, ActionRequestPatch:
			if req.AuthFamily != AuthGraphHumanGate {
				return ErrInvalidAuthAction
			}
			if resp.TextInput == "" {
				return ErrAnswerRequired
			}
			return nil
		default:
			return ErrInvalidAuthAction
		}
	default:
		return ErrPromptInvalid
	}
}
