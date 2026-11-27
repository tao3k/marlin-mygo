// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package chatroom

import (
	"errors"
	"testing"

	"github.com/tao3k/marlin-mygo/engine/features"
)

func TestChatRoomMultiRolePermissionsGovernanceAndThreads(t *testing.T) {
	ledger := features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	ctrl := NewController(ledger)

	// 1. Permitted agent role posts a message with @mentions & human/agent reactions work.
	msg, err := ctrl.PostMessage("ch-workspace", "agent-architect", "Ready for final check @reviewer and @security.", "")
	if err != nil {
		t.Fatalf("PostMessage (architect): %v", err)
	}
	if len(msg.Mentions) != 2 || msg.Mentions[0] != "@reviewer" || msg.Mentions[1] != "@security" {
		t.Fatalf("expected 2 extracted mentions, got %+v", msg.Mentions)
	}
	if err := ctrl.ToggleReaction(msg.ID, "agent-security", "Ack"); err != nil {
		t.Fatalf("ToggleReaction (security): %v", err)
	}

	// 2. Agent role lacking CanManageItems (reviewer) is denied fail-closed with audit receipt.
	if _, err := ctrl.CreateItem("ch-workspace", "agent-reviewer", "Unauthorized task", ""); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied for reviewer CreateItem, got %v", err)
	}
	if err := ctrl.TransitionItem("item-2", "agent-reviewer", ItemDone); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied for reviewer TransitionItem, got %v", err)
	}

	// 3. Governance invariant: even a human operator can NEVER grant CanApproveGates to an AI agent!
	if err := ctrl.UpdateRolePermissions("agent-reviewer", "human-owner", RolePermissions{
		CanPost: true, CanManageItems: true, CanInvokeTools: true, RequireGate: true, CanApproveGates: true,
	}); !errors.Is(err, ErrAgentCannotApproveGate) {
		t.Fatalf("expected ErrAgentCannotApproveGate, got %v", err)
	}

	// 4. Non-human agent cannot escalate its own or peer permissions.
	if err := ctrl.UpdateRolePermissions("agent-reviewer", "agent-architect", RolePermissions{
		CanPost: true, CanManageItems: true, CanInvokeTools: true, RequireGate: true, CanApproveGates: false,
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied when agent attempts RBAC mutation, got %v", err)
	}

	// 5. Human operator grants CanManageItems to reviewer -> reviewer can now create & transition matters!
	if err := ctrl.UpdateRolePermissions("agent-reviewer", "human-owner", RolePermissions{
		CanPost: true, CanManageItems: true, CanInvokeTools: true, RequireGate: true, CanApproveGates: false,
	}); err != nil {
		t.Fatalf("UpdateRolePermissions (human->reviewer): %v", err)
	}
	newItem, err := ctrl.CreateItem("ch-workspace", "agent-reviewer", "Verify zero-CGO tags", "agent-reviewer")
	if err != nil || newItem.Status != ItemOpen {
		t.Fatalf("expected reviewer CreateItem after grant, got item=%+v err=%v", newItem, err)
	}
	if err := ctrl.TransitionItem(newItem.ID, "agent-reviewer", ItemDone); err != nil {
		t.Fatalf("expected reviewer TransitionItem after grant: %v", err)
	}

	// 6. Thread working memory: open thread, post reply, verify reply count & snapshot projection.
	if err := ctrl.OpenThread(msg.ID); err != nil {
		t.Fatalf("OpenThread: %v", err)
	}
	if _, err := ctrl.PostMessage("ch-workspace", "agent-reviewer", "Thread reply: all checks green.", msg.ID); err != nil {
		t.Fatalf("PostMessage (thread reply): %v", err)
	}
	if got := ctrl.ThreadReplyCount(msg.ID); got != 1 {
		t.Fatalf("expected 1 thread reply, got %d", got)
	}
	snap := ctrl.Snapshot()
	if len(snap.ThreadReplies) != 1 || snap.ActiveThreadID != msg.ID {
		t.Fatalf("unexpected thread snapshot: active=%q replies=%+v", snap.ActiveThreadID, snap.ThreadReplies)
	}

	// 7. Verify GovernanceLedger recorded all denials and admissions.
	trail := ledger.AuditTrail()
	if len(trail) < 5 {
		t.Fatalf("expected >=5 governance audit receipts, got %d", len(trail))
	}
}
