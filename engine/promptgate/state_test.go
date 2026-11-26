// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package promptgate

import (
	"errors"
	"testing"
)

func TestPromptQueueAskOptionAndAuthFailClosedLifecycle(t *testing.T) {
	q := NewQueue()

	// 1. Enqueue Ask, Option, and Auth prompts into FIFO queue.
	if err := q.Enqueue(PromptRequest{
		ID:          "ask-1",
		Kind:        PromptAsk,
		Title:       "Clarify Target Branch",
		Prompt:      "Which branch should Marlin rebase onto?",
		Placeholder: "e.g. main or release/v1",
	}); err != nil {
		t.Fatalf("enqueue ask-1: %v", err)
	}
	if err := q.Enqueue(PromptRequest{
		ID:          "opt-1",
		Kind:        PromptOption,
		Title:       "Deployment Strategy",
		Prompt:      "Select deployment targets to enable",
		MultiSelect: true,
		AllowCustom: true,
		Choices: []OptionChoice{
			{ID: "canary", Label: "Canary 10%", Description: "Roll out safely to 10% traffic", Recommended: true},
			{ID: "staging", Label: "Staging", Description: "Deploy to staging cluster first"},
		},
	}); err != nil {
		t.Fatalf("enqueue opt-1: %v", err)
	}
	if err := q.Enqueue(PromptRequest{
		ID:         "gate-1",
		Kind:       PromptAuth,
		AuthFamily: AuthGraphHumanGate,
		Risk:       RiskHigh,
		ToolName:   "shell.exec",
		CallID:     "call-99",
		Prompt:     "Run database migration script?",
	}); err != nil {
		t.Fatalf("enqueue gate-1: %v", err)
	}

	if q.Len() != 3 {
		t.Fatalf("expected 3 queued prompts, got %d", q.Len())
	}

	// 2. Ask prompt rejects empty submit fail-closed, then accepts valid answer.
	if _, _, err := q.Resolve("ask-1", PromptResponse{Action: ActionSubmit, TextInput: "   "}); !errors.Is(err, ErrAnswerRequired) {
		t.Fatalf("expected ErrAnswerRequired on empty ask submit, got %v", err)
	}
	_, resp, err := q.Resolve("ask-1", PromptResponse{Action: ActionSubmit, TextInput: "main"})
	if err != nil || resp.TextInput != "main" || resp.IdempotencyKey == "" {
		t.Fatalf("unexpected ask resolve: resp=%+v err=%v", resp, err)
	}
	// One-shot claim invariant: replaying ask-1 must fail closed.
	if _, _, err := q.Resolve("ask-1", PromptResponse{Action: ActionSubmit, TextInput: "main"}); !errors.Is(err, ErrPromptNotFound) {
		t.Fatalf("expected ErrPromptNotFound on replay, got %v", err)
	}

	// 3. Option prompt advances to head of queue; rejects unknown choice and empty submit; accepts multi-select + custom note.
	active, pos, total, ok := q.Active()
	if !ok || active.ID != "opt-1" || pos != 1 || total != 2 {
		t.Fatalf("expected opt-1 at head (1/2), got id=%q pos=%d total=%d ok=%v", active.ID, pos, total, ok)
	}
	if _, _, err := q.Resolve("opt-1", PromptResponse{Action: ActionSubmit}); !errors.Is(err, ErrSelectionRequired) {
		t.Fatalf("expected ErrSelectionRequired on empty option submit, got %v", err)
	}
	if _, _, err := q.Resolve("opt-1", PromptResponse{Action: ActionSubmit, SelectedIDs: []string{"bogus"}}); !errors.Is(err, ErrInvalidOptionChoice) {
		t.Fatalf("expected ErrInvalidOptionChoice on bogus option, got %v", err)
	}
	_, optResp, err := q.Resolve("opt-1", PromptResponse{
		Action:      ActionSubmit,
		SelectedIDs: []string{"canary", "staging"},
		TextInput:   "notify slack #ops",
	})
	if err != nil || len(optResp.SelectedIDs) != 2 || optResp.TextInput != "notify slack #ops" {
		t.Fatalf("unexpected option resolve: resp=%+v err=%v", optResp, err)
	}

	// 4. Graph Human Gate requires note for ActionRequestPatch, then resolves cleanly and blocks replay.
	if _, _, err := q.Resolve("gate-1", PromptResponse{Action: ActionRequestPatch}); !errors.Is(err, ErrAnswerRequired) {
		t.Fatalf("expected ErrAnswerRequired for RequestPatch without note, got %v", err)
	}
	_, authResp, err := q.Resolve("gate-1", PromptResponse{
		Action:    ActionRequestPatch,
		TextInput: "Use --dry-run first",
	})
	if err != nil || authResp.Action != ActionRequestPatch || authResp.TextInput != "Use --dry-run first" {
		t.Fatalf("unexpected graph gate resolve: resp=%+v err=%v", authResp, err)
	}
	if q.HasPending() {
		t.Fatal("expected empty queue after resolving all 3 prompts")
	}
	if err := q.Enqueue(PromptRequest{ID: "gate-1", Kind: PromptAuth, Prompt: "Replay"}); !errors.Is(err, ErrPromptNotFound) {
		t.Fatalf("expected claimed gate-1 re-enqueue rejected fail-closed, got %v", err)
	}
}
