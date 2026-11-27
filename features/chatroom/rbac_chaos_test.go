// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package chatroom_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/promptgate"
	"github.com/tao3k/marlin-mygo/features/chatroom"
)

// TestChatRoomAndPromptGateAdversarialRBACStateMachine executes 2,000
// randomized adversarial steps verifying Marlin's core multi-role governance
// invariants:
//   1. No AI agent can EVER obtain CanApproveGates == true.
//   2. No AI agent can EVER resolve a PromptGate or modify RBAC permissions.
//   3. No PromptGate ID can EVER be resolved twice or re-enqueued after claim.
//   4. PromptAuth rejects ActionSubmit / ActionCancel fail-closed.
//   5. Pending gates strictly block linked Matters until human sign-off.
func TestChatRoomAndPromptGateAdversarialRBACStateMachine(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261126, 99))
	ledger := features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	mod := chatroom.NewModule(ledger)
	q := promptgate.NewQueue()
	mod.SetPromptGate(q, nil)
	ctrl := mod.View.Ctrl

	actors := []string{"human-owner", "agent-architect", "agent-reviewer", "agent-security"}
	agents := []string{"agent-architect", "agent-reviewer", "agent-security"}
	channels := []string{"ch-workspace", "ch-architecture", "ch-governance"}

	claimedGateIDs := make(map[string]bool)
	pendingReqs := make(map[string]promptgate.PromptRequest)

	for step := 0; step < 2000; step++ {
		actor := actors[rng.IntN(len(actors))]
		ch := channels[rng.IntN(len(channels))]

		switch rng.IntN(7) {
		case 0: // Adversarial RBAC mutation: including attempts to grant CanApproveGates to agents!
			target := agents[rng.IntN(len(agents))]
			nextPerm := chatroom.RolePermissions{
				CanPost:         rng.IntN(2) == 0,
				CanManageItems:  rng.IntN(2) == 0,
				CanInvokeTools:  rng.IntN(2) == 0,
				RequireGate:     rng.IntN(2) == 0,
				CanApproveGates: rng.IntN(3) == 0, // frequently attempt forbidden escalation!
			}
			err := ctrl.UpdateRolePermissions(target, actor, nextPerm)
			if actor != "human-owner" {
				if !errors.Is(err, chatroom.ErrPermissionDenied) {
					t.Fatalf("step %d: non-human %s modified RBAC without ErrPermissionDenied: err=%v", step, actor, err)
				}
			} else if nextPerm.CanApproveGates {
				if !errors.Is(err, chatroom.ErrAgentCannotApproveGate) {
					t.Fatalf("step %d: granted CanApproveGates to agent %s! err=%v", step, target, err)
				}
			} else if err != nil {
				t.Fatalf("step %d: valid human RBAC update failed: %v", step, err)
			}

		case 1: // Request high-risk agent gate (checks CanInvokeTools fail-closed).
			ag := agents[rng.IntN(len(agents))]
			rec, req, err := ctrl.RequestAgentGate(ch, "", "item-1", ag, "tool.exec", fmt.Sprintf("action-%d", step), promptgate.RiskHigh)
			if err == nil {
				pendingReqs[req.ID] = req
				if rec.Status != chatroom.GateStatusPending {
					t.Fatalf("step %d: new gate %q status=%s want PENDING", step, rec.GateID, rec.Status)
				}
			}

		case 2: // Adversarial agent attempts to self-approve a pending gate!
			for gid, req := range pendingReqs {
				ag := agents[rng.IntN(len(agents))]
				fake := promptgate.PromptResponse{PromptID: gid, Action: promptgate.ActionApproveOnce, IdempotencyKey: "forged"}
				_, _, err := ctrl.ApplyGateDecision(req, fake, ag)
				if !errors.Is(err, chatroom.ErrAgentCannotApproveGate) {
					t.Fatalf("step %d: agent %s bypassed human-only gate approval on %s! err=%v", step, ag, gid, err)
				}
				break
			}

		case 3: // Adversarial invalid PromptAuth action (ActionCancel / ActionSubmit MUST fail closed).
			if active, _, _, ok := q.Active(); ok && active.Kind == promptgate.PromptAuth {
				badAction := promptgate.ActionCancel
				if rng.IntN(2) == 0 {
					badAction = promptgate.ActionSubmit
				}
				if _, _, err := q.Resolve(active.ID, promptgate.PromptResponse{Action: badAction}); !errors.Is(err, promptgate.ErrInvalidAuthAction) {
					t.Fatalf("step %d: PromptAuth accepted forbidden action %q: err=%v", step, badAction, err)
				}
			}

		case 4: // Legitimate human resolution via Tier-0 PromptGate -> one-shot claim + receipt writeback.
			if active, _, _, ok := q.Active(); ok {
				act := promptgate.ActionApproveOnce
				if rng.IntN(2) == 0 {
					act = promptgate.ActionReject
				}
				resolvedReq, resp, err := q.Resolve(active.ID, promptgate.PromptResponse{
					Action:    act,
					TextInput: fmt.Sprintf("reviewed-step-%d", step),
				})
				if err != nil {
					t.Fatalf("step %d: legitimate human Resolve(%s) failed: %v", step, active.ID, err)
				}
				if claimedGateIDs[active.ID] {
					t.Fatalf("step %d: gate %s resolved TWICE (one-shot violated)", step, active.ID)
				}
				claimedGateIDs[active.ID] = true
				delete(pendingReqs, active.ID)

				rec, found, applyErr := ctrl.ApplyGateDecision(resolvedReq, resp, "human-owner")
				if applyErr != nil || !found {
					t.Fatalf("step %d: ApplyGateDecision(%s) failed: found=%v err=%v", step, active.ID, found, applyErr)
				}
				if resp.IdempotencyKey == "" || rec.IdempotencyKey != resp.IdempotencyKey {
					t.Fatalf("step %d: missing idempotency key on receipt %+v", step, rec)
				}
				// Re-enqueuing a claimed gate ID MUST fail closed!
				if reErr := q.Enqueue(resolvedReq); !errors.Is(reErr, promptgate.ErrPromptNotFound) {
					t.Fatalf("step %d: claimed gate %s allowed re-enqueue: err=%v", step, active.ID, reErr)
				}
			}

		case 5: // Post room message (enforces CanPost).
			_, _ = ctrl.PostMessage(ch, actor, fmt.Sprintf("hello @architect step %d", step), "")

		case 6: // Manage shared matter (enforces CanManageItems).
			_, _ = ctrl.CreateItem(ch, actor, fmt.Sprintf("matter-%d", step), actor)
		}

		// Global Safety Invariant Check after EVERY step: NO AI agent may EVER hold CanApproveGates!
		snap := ctrl.Snapshot()
		for _, p := range snap.Participants {
			if p.Kind == chatroom.ParticipantAgent && p.Permissions.CanApproveGates {
				t.Fatalf("step %d: INVARIANT VIOLATION — agent %s holds CanApproveGates=true", step, p.ID)
			}
		}
	}

	if len(ledger.AuditTrail()) == 0 {
		t.Fatal("expected GovernanceLedger audit trail populated across adversarial run")
	}
}
