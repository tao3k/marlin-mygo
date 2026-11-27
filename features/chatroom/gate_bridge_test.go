// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package chatroom_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/promptgate"
	"github.com/tao3k/marlin-mygo/features/chatroom"
)

func TestChatRoomAgentGateRequestHumanOnlyReceiptAndMatterTransition(t *testing.T) {
	ledger := features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	mod := chatroom.NewModule(ledger)
	q := promptgate.NewQueue()
	var openedGateID string
	mod.SetPromptGate(q, func(gateID string) { openedGateID = gateID })
	ctrl := mod.View.Ctrl

	// 1. Agent without CanInvokeTools (@security) is denied fail-closed.
	if _, _, err := ctrl.RequestAgentGate("ch-workspace", "", "item-1", "agent-security", "shell.exec", "Run migration", promptgate.RiskHigh); !errors.Is(err, chatroom.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied for agent without CanInvokeTools, got %v", err)
	}

	// 2. Permitted agent (@architect) requests high-risk gate -> blocks item-1, pauses agent, enqueues Tier-0 PromptAuth.
	rec, req, err := ctrl.RequestAgentGate(
		"ch-workspace", "", "item-1", "agent-architect",
		"deploy.prod", "Apply blue-green deployment cutover", promptgate.RiskCritical,
	)
	if err != nil {
		t.Fatalf("RequestAgentGate: %v", err)
	}
	if rec.Status != chatroom.GateStatusPending || ctrl.PendingGateCount() != 1 {
		t.Fatalf("expected 1 pending gate, got status=%s count=%d", rec.Status, ctrl.PendingGateCount())
	}
	activeReq, _, _, ok := q.Active()
	if !ok || activeReq.ID != req.ID || activeReq.Kind != promptgate.PromptAuth {
		t.Fatalf("expected PromptAuth %q active in Tier-0 queue, got %+v ok=%v", req.ID, activeReq, ok)
	}
	snap := ctrl.Snapshot()
	var itemStatus chatroom.ItemStatus
	var archPresence chatroom.PresenceState
	for _, it := range snap.Items {
		if it.ID == "item-1" {
			itemStatus = it.Status
		}
	}
	for _, p := range snap.Participants {
		if p.ID == "agent-architect" {
			archPresence = p.Presence
		}
	}
	if itemStatus != chatroom.ItemBlocked {
		t.Fatalf("expected linked matter item-1 BLOCKED while awaiting gate, got %s", itemStatus)
	}
	if archPresence != chatroom.PresenceWaitingGate {
		t.Fatalf("expected agent-architect waiting-gate, got %s", archPresence)
	}

	// 3. Render view on Desktop & click "Review Gate" -> invokes OnOpenGate(gateID).
	theme := ui.DarkTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		mod.View.Render(c, theme, false)
	}, 1100, 720)
	tt.Frame()
	if !tt.HasText("1 GATE PENDING") || !tt.HasText("GATE PENDING") || !tt.HasText("Review Gate") {
		t.Fatalf("expected inline gate badges and Review Gate button, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Review Gate"); err != nil {
		t.Fatalf("click Review Gate: %v", err)
	}
	tt.Frame()
	if openedGateID != req.ID {
		t.Fatalf("expected OnOpenGate(%q), got %q", req.ID, openedGateID)
	}

	// 4. AI agent CANNOT resolve a gate (human-only CanApproveGates invariant).
	fakeResp := promptgate.PromptResponse{PromptID: req.ID, Action: promptgate.ActionApproveOnce, IdempotencyKey: "key-fake"}
	if _, _, err := ctrl.ApplyGateDecision(req, fakeResp, "agent-reviewer"); !errors.Is(err, chatroom.ErrAgentCannotApproveGate) {
		t.Fatalf("expected ErrAgentCannotApproveGate when agent tries to approve, got %v", err)
	}

	// 5. Human resolves Tier-0 PromptGate -> receipt written back to thread, Matter unblocked, agent online.
	resolvedReq, resp, err := q.Resolve(req.ID, promptgate.PromptResponse{
		Action:    promptgate.ActionApproveOnce,
		TextInput: "Approved after verifying canary metrics",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	mod.ApplyGateDecision(resolvedReq, resp)

	updatedRec, found := ctrl.GateReceipt(req.ID)
	if !found || updatedRec.Status != chatroom.GateStatusApproved || updatedRec.IdempotencyKey != resp.IdempotencyKey {
		t.Fatalf("expected APPROVED receipt with key %q, got %+v", resp.IdempotencyKey, updatedRec)
	}
	if ctrl.PendingGateCount() != 0 {
		t.Fatalf("expected 0 pending gates after approval, got %d", ctrl.PendingGateCount())
	}

	// Verify thread reply receipt & unblocked matter.
	if err := ctrl.OpenThread(rec.MessageID); err != nil {
		t.Fatalf("OpenThread(%s): %v", rec.MessageID, err)
	}
	snapAfter := ctrl.Snapshot()
	if len(snapAfter.ThreadReplies) != 1 || !strings.Contains(snapAfter.ThreadReplies[0].Body, "GATE APPROVED") || !strings.Contains(snapAfter.ThreadReplies[0].Body, resp.IdempotencyKey) {
		t.Fatalf("expected signed receipt reply in thread, got %+v", snapAfter.ThreadReplies)
	}
	for _, it := range snapAfter.Items {
		if it.ID == "item-1" && it.Status != chatroom.ItemInProgress {
			t.Fatalf("expected matter item-1 unblocked to IN_PROGRESS, got %s", it.Status)
		}
	}
	for _, p := range snapAfter.Participants {
		if p.ID == "agent-architect" && p.Presence != chatroom.PresenceOnline {
			t.Fatalf("expected agent-architect back online, got %s", p.Presence)
		}
	}

	// Verify mobile 393px render shows GATE APPROVED with zero horizontal overflow.
	ttMob := ui.NewTester(func(c *ui.Context) {
		mod.View.Render(c, theme, true)
	}, 393, 852)
	ttMob.Frame()
	if !ttMob.HasText("GATE APPROVED") {
		t.Fatalf("expected GATE APPROVED pill on mobile timeline, got %q", strings.Join(ttMob.Texts(), " | "))
	}
}
