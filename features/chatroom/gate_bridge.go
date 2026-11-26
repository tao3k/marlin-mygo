// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package chatroom

import (
	"fmt"
	"strings"
	"time"

	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/promptgate"
	"github.com/tao3k/marlin-mygo/engine/resilience"
)

// GateReceiptStatus tracks the human-governed decision lifecycle for an agent
// room action linked to Marlin's Tier-0 PromptGate queue.
type GateReceiptStatus string

const (
	GateStatusPending        GateReceiptStatus = "PENDING"
	GateStatusApproved       GateReceiptStatus = "APPROVED"
	GateStatusRejected       GateReceiptStatus = "REJECTED"
	GateStatusPatchRequested GateReceiptStatus = "PATCH_REQUESTED"
)

// GateReceiptRecord records one auditable human-only approval/rejection
// receipt linked to a room message, thread, and optional shared Matter.
type GateReceiptRecord struct {
	GateID         string
	ChannelID      string
	ThreadRootID   string
	MessageID      string
	MatterID       string
	ActorID        string
	ToolName       string
	Prompt         string
	Risk           promptgate.RiskLevel
	Status         GateReceiptStatus
	IdempotencyKey string
	Rationale      string
	ResolvedBy     string
	CreatedAt      time.Time
	ResolvedAt     time.Time
}

// SetPromptGateQueue attaches Marlin's shared Tier-0 PromptGate queue so
// high-risk agent actions inside the Chat Room raise ESC-unbypassable human
// approval prompts on Desktop, Mobile, and Web shells.
func (c *Controller) SetPromptGateQueue(q *promptgate.Queue) {
	c.mu.Lock()
	c.promptGates = q
	c.mu.Unlock()
}

// RequestAgentGate pauses an agent role's high-risk action, blocks any linked
// Matter, posts a gate-linked message in the channel/thread, and enqueues an
// authoritative Tier-0 PromptAuth request for human-only approval.
func (c *Controller) RequestAgentGate(
	channelID, threadRootID, matterID, actorID, toolName, actionPrompt string,
	risk promptgate.RiskLevel,
) (GateReceiptRecord, promptgate.PromptRequest, error) {
	toolName = strings.TrimSpace(toolName)
	actionPrompt = strings.TrimSpace(actionPrompt)
	if actionPrompt == "" {
		return GateReceiptRecord{}, promptgate.PromptRequest{}, ErrEmptyContent
	}
	if risk == "" {
		risk = promptgate.RiskHigh
	}

	c.mu.Lock()
	actor, ok := c.participants[actorID]
	if !ok {
		c.mu.Unlock()
		return GateReceiptRecord{}, promptgate.PromptRequest{}, ErrParticipantNotFound
	}
	if !actor.Permissions.CanInvokeTools {
		reason := fmt.Sprintf("%s (%s) lacks CanInvokeTools permission", actor.Handle, actor.RoleTitle)
		c.lastError = reason
		ledger := c.ledger
		c.mu.Unlock()
		if ledger != nil {
			ledger.RecordAudit(actor.Handle, toolName, "chatroom.gate.request", features.AuditDenied, reason)
		}
		return GateReceiptRecord{}, promptgate.PromptRequest{}, fmt.Errorf("%w: %s", ErrPermissionDenied, reason)
	}
	chFound := false
	for i := range c.channels {
		if c.channels[i].ID == channelID {
			chFound = true
			break
		}
	}
	if !chFound {
		c.mu.Unlock()
		return GateReceiptRecord{}, promptgate.PromptRequest{}, ErrChannelNotFound
	}
	if threadRootID != "" && !c.threadBelongsToChannelLocked(threadRootID, channelID) {
		c.mu.Unlock()
		return GateReceiptRecord{}, promptgate.PromptRequest{}, ErrCrossChannelReference
	}
	if matterID != "" {
		matterMatch := false
		for i := range c.items {
			if c.items[i].ID == matterID {
				if c.items[i].ChannelID != channelID {
					c.mu.Unlock()
					return GateReceiptRecord{}, promptgate.PromptRequest{}, ErrCrossChannelReference
				}
				matterMatch = true
				break
			}
		}
		if !matterMatch {
			c.mu.Unlock()
			return GateReceiptRecord{}, promptgate.PromptRequest{}, ErrItemNotFound
		}
	}

	c.gateSeq++
	gateID := fmt.Sprintf("room-gate-%d", c.gateSeq)
	c.msgSeq++
	msgID := fmt.Sprintf("msg-%d", c.msgSeq)
	now := time.Now().UTC()

	// Transition agent presence to waiting-gate and block any linked Matter until human sign-off.
	actor.Presence = PresenceWaitingGate
	c.participants[actorID] = actor
	if matterID != "" {
		for i := range c.items {
			if c.items[i].ID == matterID {
				c.items[i].Status = ItemBlocked
				c.items[i].UpdatedBy = actorID
				break
			}
		}
	}

	// Post a transparent room/thread message referencing GateID so teammates see why execution paused.
	msg := RoomMessage{
		ID:           msgID,
		ChannelID:    channelID,
		ThreadRootID: threadRootID,
		AuthorID:     actorID,
		Body:         fmt.Sprintf("[Gate %s · %s] %s", gateID, toolName, actionPrompt),
		GateID:       gateID,
		Delivery:     resilience.DeliveryAcked,
		CreatedAt:    now,
	}
	c.messages = append(c.messages, msg)

	rec := GateReceiptRecord{
		GateID:       gateID,
		ChannelID:    channelID,
		ThreadRootID: threadRootID,
		MessageID:    msgID,
		MatterID:     matterID,
		ActorID:      actorID,
		ToolName:     toolName,
		Prompt:       actionPrompt,
		Risk:         risk,
		Status:       GateStatusPending,
		CreatedAt:    now,
	}
	if c.gateReceipts == nil {
		c.gateReceipts = make(map[string]GateReceiptRecord)
	}
	c.gateReceipts[gateID] = rec

	req := promptgate.PromptRequest{
		ID:             gateID,
		Kind:           promptgate.PromptAuth,
		Title:          fmt.Sprintf("Agent Room Approval (%s)", actor.Handle),
		Prompt:         actionPrompt,
		ToolName:       toolName,
		CallID:         "call-" + gateID,
		AuthFamily:     promptgate.AuthToolReview,
		Risk:           risk,
		DetailsHeading: fmt.Sprintf("Requested by %s (%s) in %s", actor.DisplayName, actor.RoleTitle, channelID),
		DetailsSummary: fmt.Sprintf("Matter=%s · Human-only CanApproveGates required", matterID),
	}

	q := c.promptGates
	ledger := c.ledger
	onMsg := c.OnMessagePosted
	c.lastError = ""
	c.mu.Unlock()

	if q != nil {
		_ = q.Enqueue(req)
	}
	if ledger != nil {
		ledger.RecordAudit(actor.Handle, gateID, "chatroom.gate.requested", features.AuditAdmitted, toolName)
	}
	if onMsg != nil {
		onMsg(msg)
	}
	return rec, req, nil
}

// ApplyGateDecision writes a human-only Tier-0 PromptGate resolution receipt
// back into the Chat Room thread, restores agent presence, updates any linked
// Matter status, and records an immutable GovernanceLedger entry.
func (c *Controller) ApplyGateDecision(
	req promptgate.PromptRequest,
	resp promptgate.PromptResponse,
	humanActorID string,
) (GateReceiptRecord, bool, error) {
	c.mu.Lock()
	rec, ok := c.gateReceipts[req.ID]
	if !ok {
		c.mu.Unlock()
		return GateReceiptRecord{}, false, nil
	}
	if humanActorID == "" {
		humanActorID = c.currentActorID
	}
	human, exists := c.participants[humanActorID]
	if !exists || human.Kind != ParticipantHuman || !human.Permissions.CanApproveGates {
		reason := fmt.Sprintf("%s lacks human-only CanApproveGates authority", humanActorID)
		c.lastError = reason
		ledger := c.ledger
		c.mu.Unlock()
		if ledger != nil {
			ledger.RecordAudit(humanActorID, req.ID, "chatroom.gate.resolve", features.AuditDenied, reason)
		}
		return GateReceiptRecord{}, true, fmt.Errorf("%w: %s", ErrAgentCannotApproveGate, reason)
	}
	if rec.Status != GateStatusPending {
		reason := fmt.Sprintf("gate %s already resolved with status %s", req.ID, rec.Status)
		c.lastError = reason
		ledger := c.ledger
		c.mu.Unlock()
		if ledger != nil {
			ledger.RecordAudit(human.Handle, req.ID, "chatroom.gate.replay", features.AuditDenied, reason)
		}
		return rec, true, fmt.Errorf("%w: %s", ErrGateAlreadyResolved, reason)
	}

	status := GateStatusRejected
	matterNext := ItemBlocked
	auditOutcome := features.AuditDenied
	switch resp.Action {
	case promptgate.ActionApproveOnce:
		status = GateStatusApproved
		matterNext = ItemInProgress
		auditOutcome = features.AuditGateClaimed
	case promptgate.ActionRequestPatch:
		status = GateStatusPatchRequested
		matterNext = ItemInProgress
	}

	now := time.Now().UTC()
	rec.Status = status
	rec.IdempotencyKey = resp.IdempotencyKey
	rec.Rationale = strings.TrimSpace(resp.TextInput)
	rec.ResolvedBy = human.Handle
	rec.ResolvedAt = now
	c.gateReceipts[req.ID] = rec

	// Restore waiting agent presence.
	if ag, ok := c.participants[rec.ActorID]; ok && ag.Presence == PresenceWaitingGate {
		ag.Presence = PresenceOnline
		c.participants[rec.ActorID] = ag
	}

	// Update linked Matter lifecycle.
	var updatedItem *RoomItem
	if rec.MatterID != "" {
		for i := range c.items {
			if c.items[i].ID == rec.MatterID {
				c.items[i].Status = matterNext
				c.items[i].UpdatedBy = human.ID
				cp := c.items[i]
				updatedItem = &cp
				break
			}
		}
	}

	// Append signed receipt reply in the exact originating thread (or root message thread).
	threadRoot := rec.ThreadRootID
	if threadRoot == "" {
		threadRoot = rec.MessageID
	}
	c.msgSeq++
	body := fmt.Sprintf("[GATE %s · %s · key=%s]", status, rec.GateID, rec.IdempotencyKey)
	if rec.Rationale != "" {
		body += " " + rec.Rationale
	}
	receiptMsg := RoomMessage{
		ID:           fmt.Sprintf("msg-%d", c.msgSeq),
		ChannelID:    rec.ChannelID,
		ThreadRootID: threadRoot,
		AuthorID:     human.ID,
		Body:         body,
		GateID:       rec.GateID,
		Delivery:     resilience.DeliveryAcked,
		CreatedAt:    now,
	}
	c.messages = append(c.messages, receiptMsg)

	ledger := c.ledger
	onItem := c.OnItemUpdated
	onMsg := c.OnMessagePosted
	c.mu.Unlock()

	if ledger != nil {
		ledger.RecordAudit(human.Handle, rec.GateID, "chatroom.gate."+strings.ToLower(string(status)), auditOutcome, rec.IdempotencyKey)
	}
	if updatedItem != nil && onItem != nil {
		onItem(*updatedItem)
	}
	if onMsg != nil {
		onMsg(receiptMsg)
	}
	return rec, true, nil
}

// GateReceipt returns the current GateReceiptRecord for gateID if tracked.
func (c *Controller) GateReceipt(gateID string) (GateReceiptRecord, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rec, ok := c.gateReceipts[gateID]
	return rec, ok
}

// PendingGateCount returns how many agent room gates currently await human sign-off.
func (c *Controller) PendingGateCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	n := 0
	for _, r := range c.gateReceipts {
		if r.Status == GateStatusPending {
			n++
		}
	}
	return n
}

// InvokeAgentTool enforces CaMeL-style external taint containment inside a Chat
// Room: if channelID contains untrusted external ingress, privileged agent tool
// invocation fails closed unless backed by a human-APPROVED gate receipt for
// that exact channel.
func (c *Controller) InvokeAgentTool(channelID, actorID, toolName, approvedGateID string) error {
	c.mu.Lock()
	actor, ok := c.participants[actorID]
	if !ok {
		c.mu.Unlock()
		return ErrParticipantNotFound
	}
	if !actor.Permissions.CanInvokeTools {
		reason := fmt.Sprintf("%s lacks CanInvokeTools permission", actor.Handle)
		c.lastError = reason
		ledger := c.ledger
		c.mu.Unlock()
		if ledger != nil {
			ledger.RecordAudit(actor.Handle, toolName, "chatroom.tool.invoke", features.AuditDenied, reason)
		}
		return fmt.Errorf("%w: %s", ErrPermissionDenied, reason)
	}
	if !c.hasChannelLocked(channelID) {
		c.mu.Unlock()
		return ErrChannelNotFound
	}
	if c.channelTainted[channelID] {
		rec, exists := c.gateReceipts[approvedGateID]
		if !exists || rec.ChannelID != channelID || rec.Status != GateStatusApproved {
			reason := fmt.Sprintf("channel %s has tainted external ingress; human-approved gate required before invoking %s", channelID, toolName)
			c.lastError = reason
			ledger := c.ledger
			c.mu.Unlock()
			if ledger != nil {
				ledger.RecordAudit(actor.Handle, toolName, "chatroom.tool.taint_blocked", features.AuditDenied, reason)
			}
			return fmt.Errorf("%w: %s", ErrPermissionDenied, reason)
		}
	}
	ledger := c.ledger
	c.mu.Unlock()
	if ledger != nil {
		ledger.RecordAudit(actor.Handle, toolName, "chatroom.tool.invoke", features.AuditAdmitted, approvedGateID)
	}
	return nil
}
