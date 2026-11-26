// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package chatroom implements Marlin's opt-in Multi-Role Agent Chat Room
// feature (`marlin_feat_chatroom`), combining shared Marlin context, role-based
// permission governance, collaborative work items, and human-like agent
// messaging and thread replies with zero local disk persistence.
package chatroom

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tao3k/marlin-mygo/engine/contextgov"
	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/promptgate"
	"github.com/tao3k/marlin-mygo/engine/resilience"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

var (
	ErrParticipantNotFound      = errors.New("chatroom: participant not found")
	ErrChannelNotFound          = errors.New("chatroom: channel not found")
	ErrMessageNotFound          = errors.New("chatroom: message not found")
	ErrItemNotFound             = errors.New("chatroom: matter item not found")
	ErrPermissionDenied         = errors.New("chatroom: action denied by room role governance")
	ErrAgentCannotApproveGate   = errors.New("chatroom: governance invariant forbids granting CanApproveGates to an AI agent")
	ErrEmptyContent             = errors.New("chatroom: content cannot be empty")
	ErrGateAlreadyResolved      = errors.New("chatroom: gate receipt already resolved (one-shot replay forbidden)")
	ErrCrossChannelReference    = errors.New("chatroom: thread root or matter does not belong to target channel")
	ErrMatterGateBlocked        = errors.New("chatroom: matter is blocked by a pending or rejected gate and cannot be unblocked by an AI agent")
	ErrUntrustedExternalIngress = errors.New("chatroom: external room ingress failed origin verification")
	ErrSpoofedGateReceipt       = errors.New("chatroom: external ingress cannot claim gate receipt authority or impersonate a human approver")
	ErrCrossTenantRoom          = errors.New("chatroom: cross-tenant room operation denied")
)

// ExternalIngressProof carries verified connector provenance for messages
// entering a Chat Room from an external bridge, webhook, or federated room.
type ExternalIngressProof struct {
	OriginVerified bool
	ConnectorID    string
	TenantID       string
}

// ParticipantKind distinguishes human operators from Marlin AI agent roles.
type ParticipantKind string

const (
	ParticipantHuman ParticipantKind = "human"
	ParticipantAgent ParticipantKind = "agent"
)

// PresenceState reports the live activity status of a participant or agent role.
type PresenceState string

const (
	PresenceOnline      PresenceState = "online"
	PresenceThinking    PresenceState = "thinking"
	PresenceWorking     PresenceState = "working"
	PresenceWaitingGate PresenceState = "waiting-gate"
	PresenceOffline     PresenceState = "offline"
)

// RolePermissions defines explicit per-participant capabilities enforced
// fail-closed inside the room and audited via features.GovernanceLedger.
type RolePermissions struct {
	CanPost         bool // post messages, thread replies, and reactions
	CanManageItems  bool // create, assign, and transition shared matters
	CanInvokeTools  bool // invoke tools inside the room context
	RequireGate     bool // mutating actions require Tier-0 PromptGate sign-off
	CanApproveGates bool // human-only invariant; NEVER permitted for AI agents
}

// Participant represents one human teammate or specialized Marlin Agent role.
type Participant struct {
	ID          string
	Handle      string // e.g. "@architect", "@reviewer", "@security", "@you"
	DisplayName string
	RoleTitle   string
	Kind        ParticipantKind
	HostNode    string // bound Marlin daemon/federation node (e.g. "local-mac")
	Presence    PresenceState
	Permissions RolePermissions
}

// Channel represents one shared discussion space or direct agent room.
type Channel struct {
	ID          string
	Name        string
	Topic       string
	IsDM        bool
	UnreadCount int
}

// ItemStatus tracks the lifecycle of one shared room matter/task.
type ItemStatus string

const (
	ItemOpen       ItemStatus = "OPEN"
	ItemInProgress ItemStatus = "IN_PROGRESS"
	ItemBlocked    ItemStatus = "BLOCKED"
	ItemDone       ItemStatus = "DONE"
)

// RoomItem is a shared matter or action item collaboratively managed by humans
// and permitted agent roles inside a channel.
type RoomItem struct {
	ID              string
	ChannelID       string
	Title           string
	Status          ItemStatus
	OwnerID         string
	LinkedMessageID string
	UpdatedBy       string
}

// RoomMessage is one human or agent message (top-level or thread reply).
type RoomMessage struct {
	ID           string
	ChannelID    string
	ThreadRootID string // empty for top-level messages; root ID for thread replies
	AuthorID     string
	Body         string
	Mentions     []string
	Reactions    map[string][]string // tag -> sorted/ordered participant IDs
	Pinned       bool
	GateID          string                   // optional link to a Tier-0 PromptGate authorization
	TaintedExternal bool                     // true when ingested from an external connector/bridge
	Delivery        resilience.DeliveryState // empty/ACKED for confirmed, QUEUED/FAILED when offline
	SubmissionID string                   // stable client idempotency key for reconnect flush
	CreatedAt    time.Time
}

// RoomSnapshot is an immutable 120Hz-safe view consumed by Desktop and Mobile renderers.
type RoomSnapshot struct {
	Channels        []Channel
	ActiveChannelID string
	ActiveThreadID  string
	Participants    []Participant
	Messages        []RoomMessage
	ThreadReplies   []RoomMessage
	Items           []RoomItem
	CurrentActorID  string
	LastError       string
	Continuity      resilience.ResilienceSnapshot
}

// Controller manages thread-safe room projections, role-based permission checks,
// shared matters, and human-like agent messaging/thread interactions.
type Controller struct {
	mu              sync.RWMutex
	ledger          *features.GovernanceLedger
	promptGates     *promptgate.Queue
	contextGov      *contextgov.GovernanceStore
	breaker         *pluginhost.ExecutionBreaker
	gateReceipts    map[string]GateReceiptRecord
	gateSeq         uint64
	tenantID        string
	channelTainted  map[string]bool
	Resilience      *resilience.SessionResilience
	channels        []Channel
	participants    map[string]Participant
	participantOrd  []string
	messages        []RoomMessage
	items           []RoomItem
	activeChannelID string
	activeThreadID  string
	currentActorID  string
	lastError       string
	msgSeq          uint64
	itemSeq         uint64

	// Thin backend delegation hooks (Rust AppServer owns shared context & durable journal).
	OnMessagePosted     func(msg RoomMessage)
	OnItemUpdated       func(item RoomItem)
	OnPermissionChanged func(target Participant)
	OnOpenGate          func(gateID string)
	OnNeedResync        func(channelID, afterEventID string)
	OnDraftChanged      func(scopeID, text string)
	OnRetryOutbox       func(channelID string)
}

// NewController constructs a governed Multi-Role Agent Chat Room controller.
func NewController(ledger *features.GovernanceLedger) *Controller {
	c := &Controller{
		ledger:         ledger,
		gateReceipts:   make(map[string]GateReceiptRecord),
		channelTainted: make(map[string]bool),
		Resilience:     resilience.NewSessionResilience(),
		participants:   make(map[string]Participant),
	}
	c.SeedDefaultWorkspace()
	return c
}

// SetTenant binds this Chat Room controller to a specific tenant boundary so
// external ingress and cross-tenant operations fail closed on mismatch.
func (c *Controller) SetTenant(tenantID string) {
	c.mu.Lock()
	c.tenantID = strings.TrimSpace(tenantID)
	c.mu.Unlock()
}

// TenantID returns the bound tenant identifier (empty when unbound).
func (c *Controller) TenantID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tenantID
}

// SetGovernanceLedger attaches or updates the Tier-0 GovernanceLedger.
func (c *Controller) SetGovernanceLedger(ledger *features.GovernanceLedger) {
	c.mu.Lock()
	c.ledger = ledger
	c.mu.Unlock()
}

// SeedDefaultWorkspace populates a clean Marlin-native multi-role room with
// least-privilege agent roles (Architect, Reviewer, Security) and human owner.
func (c *Controller) SeedDefaultWorkspace() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.channels) > 0 {
		return
	}
	c.channels = []Channel{
		{ID: "ch-workspace", Name: "#workspace", Topic: "Shared Marlin context, agent coordination, and task handoffs"},
		{ID: "ch-architecture", Name: "#architecture", Topic: "Design reviews, RFC threads, and protocol evolution", UnreadCount: 1},
		{ID: "ch-governance", Name: "#governance", Topic: "Role permissions, approval gates, and audit receipts"},
	}
	c.activeChannelID = "ch-workspace"
	c.currentActorID = "human-owner"

	seedParts := []Participant{
		{
			ID: "human-owner", Handle: "@you", DisplayName: "You", RoleTitle: "Operator",
			Kind: ParticipantHuman, HostNode: "local-mac", Presence: PresenceOnline,
			Permissions: RolePermissions{CanPost: true, CanManageItems: true, CanInvokeTools: true, RequireGate: false, CanApproveGates: true},
		},
		{
			ID: "agent-architect", Handle: "@architect", DisplayName: "Architect", RoleTitle: "System Planner",
			Kind: ParticipantAgent, HostNode: "local-mac", Presence: PresenceOnline,
			Permissions: RolePermissions{CanPost: true, CanManageItems: true, CanInvokeTools: true, RequireGate: true, CanApproveGates: false},
		},
		{
			ID: "agent-reviewer", Handle: "@reviewer", DisplayName: "Reviewer", RoleTitle: "Code Auditor",
			Kind: ParticipantAgent, HostNode: "remote-linux", Presence: PresenceWorking,
			Permissions: RolePermissions{CanPost: true, CanManageItems: false, CanInvokeTools: true, RequireGate: true, CanApproveGates: false},
		},
		{
			ID: "agent-security", Handle: "@security", DisplayName: "Security", RoleTitle: "Governance Guard",
			Kind: ParticipantAgent, HostNode: "local-mac", Presence: PresenceOnline,
			Permissions: RolePermissions{CanPost: true, CanManageItems: true, CanInvokeTools: false, RequireGate: true, CanApproveGates: false},
		},
	}
	for _, p := range seedParts {
		c.participants[p.ID] = p
		c.participantOrd = append(c.participantOrd, p.ID)
	}

	now := time.Now().UTC()
	c.msgSeq = 2
	c.messages = []RoomMessage{
		{
			ID: "msg-1", ChannelID: "ch-workspace", AuthorID: "agent-architect",
			Body:      "Shared context synchronized with Rust AppServer. @reviewer please check the new feature boundary.",
			Mentions:  []string{"@reviewer"},
			Reactions: map[string][]string{"Ack": {"human-owner"}},
			Pinned:    true,
			CreatedAt: now.Add(-2 * time.Minute),
		},
		{
			ID: "msg-2", ChannelID: "ch-workspace", ThreadRootID: "msg-1", AuthorID: "agent-reviewer",
			Body:      "Verified zero cyclic imports and fail-closed RBAC invariants across engine/features.",
			Reactions: map[string][]string{"Verified": {"agent-security"}},
			CreatedAt: now.Add(-1 * time.Minute),
		},
	}
	c.itemSeq = 2
	c.items = []RoomItem{
		{ID: "item-1", ChannelID: "ch-workspace", Title: "Audit role permission boundaries for Agent Room", Status: ItemInProgress, OwnerID: "agent-security", LinkedMessageID: "msg-1", UpdatedBy: "agent-architect"},
		{ID: "item-2", ChannelID: "ch-workspace", Title: "Verify minimal build excludes optional features", Status: ItemOpen, OwnerID: "agent-reviewer", UpdatedBy: "human-owner"},
	}
}

// SelectChannel switches the active channel and clears its unread badge.
func (c *Controller) SelectChannel(channelID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.channels {
		if c.channels[i].ID == channelID {
			c.channels[i].UnreadCount = 0
			c.activeChannelID = channelID
			c.activeThreadID = ""
			c.lastError = ""
			return nil
		}
	}
	return ErrChannelNotFound
}

// OpenThread opens the right-hand Thread Working Memory drawer for rootMsgID.
func (c *Controller) OpenThread(rootMsgID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.messages {
		if c.messages[i].ID == rootMsgID && c.messages[i].ThreadRootID == "" {
			c.activeThreadID = rootMsgID
			c.lastError = ""
			return nil
		}
	}
	return ErrMessageNotFound
}

// CloseThread closes the active Thread drawer.
func (c *Controller) CloseThread() {
	c.mu.Lock()
	c.activeThreadID = ""
	c.mu.Unlock()
}

// PostMessage appends a human or agent message (or thread reply), enforcing
// the author's CanPost role permission fail-closed and extracting `@mentions`.
func (c *Controller) PostMessage(channelID, authorID, body, threadRootID string) (RoomMessage, error) {
	text := strings.TrimSpace(body)
	if text == "" {
		return RoomMessage{}, ErrEmptyContent
	}
	c.mu.Lock()
	author, ok := c.participants[authorID]
	if !ok {
		c.mu.Unlock()
		return RoomMessage{}, ErrParticipantNotFound
	}
	if !author.Permissions.CanPost {
		reason := fmt.Sprintf("%s (%s) lacks CanPost permission", author.Handle, author.RoleTitle)
		c.lastError = reason
		ledger := c.ledger
		c.mu.Unlock()
		if ledger != nil {
			ledger.RecordAudit(author.Handle, channelID, "chatroom.post", features.AuditDenied, reason)
		}
		return RoomMessage{}, fmt.Errorf("%w: %s", ErrPermissionDenied, reason)
	}
	if channelID == "" {
		channelID = c.activeChannelID
	}
	chIdx := -1
	for i := range c.channels {
		if c.channels[i].ID == channelID {
			chIdx = i
			break
		}
	}
	if chIdx < 0 {
		c.mu.Unlock()
		return RoomMessage{}, ErrChannelNotFound
	}
	c.msgSeq++
	msg := RoomMessage{
		ID:           fmt.Sprintf("msg-%d", c.msgSeq),
		ChannelID:    channelID,
		ThreadRootID: strings.TrimSpace(threadRootID),
		AuthorID:     authorID,
		Body:         text,
		Mentions:     extractHandles(text),
		Reactions:    make(map[string][]string),
		Delivery:     resilience.DeliveryAcked,
		CreatedAt:    time.Now().UTC(),
	}
	res := c.Resilience
	if res != nil && res.Snapshot("room:"+channelID).State == resilience.ConnOffline {
		if qItem, qErr := res.Outbox.Enqueue("room:"+channelID, authorID, threadRootID, text); qErr == nil {
			msg.Delivery = resilience.DeliveryQueued
			msg.SubmissionID = qItem.SubmissionID
		}
	}
	c.messages = append(c.messages, msg)
	if channelID != c.activeChannelID {
		c.channels[chIdx].UnreadCount++
	}
	c.lastError = ""
	cb := c.OnMessagePosted
	c.mu.Unlock()
	if cb != nil && msg.Delivery != resilience.DeliveryQueued {
		cb(msg)
	}
	return msg, nil
}

// IngestRemoteMessage idempotently applies a backend-pushed room message using
// eventID deduplication, channel/thread affinity validation, submission
// author/channel pinning, and sequence-gap detection (triggering OnNeedResync).
func (c *Controller) IngestRemoteMessage(eventID string, seq uint64, msg RoomMessage) (duplicate bool, gap bool) {
	c.mu.RLock()
	if msg.ChannelID == "" {
		msg.ChannelID = c.activeChannelID
	}
	if !c.hasChannelLocked(msg.ChannelID) {
		c.mu.RUnlock()
		return false, false
	}
	if msg.ThreadRootID != "" && !c.threadBelongsToChannelLocked(msg.ThreadRootID, msg.ChannelID) {
		c.mu.RUnlock()
		return false, false
	}
	c.mu.RUnlock()

	scope := "room:" + msg.ChannelID
	if c.Resilience != nil {
		dup, detectedGap := c.Resilience.AdmitEvent(scope, eventID, 0, seq)
		if dup {
			return true, false
		}
		gap = detectedGap
	}

	c.mu.Lock()
	if !c.hasChannelLocked(msg.ChannelID) ||
		(msg.ThreadRootID != "" && !c.threadBelongsToChannelLocked(msg.ThreadRootID, msg.ChannelID)) {
		c.mu.Unlock()
		return false, false
	}
	if msg.ID == "" {
		c.msgSeq++
		msg.ID = fmt.Sprintf("msg-%d", c.msgSeq)
	}
	msg.Delivery = resilience.DeliveryAcked
	if msg.Reactions == nil {
		msg.Reactions = make(map[string][]string)
	}
	if len(msg.Mentions) == 0 {
		msg.Mentions = extractHandles(msg.Body)
	}
	// Replace matching optimistic queued message ONLY within the same channel and author.
	replaced := false
	if msg.SubmissionID != "" {
		for i := range c.messages {
			if c.messages[i].SubmissionID == msg.SubmissionID &&
				c.messages[i].ChannelID == msg.ChannelID &&
				c.messages[i].AuthorID == msg.AuthorID {
				c.messages[i] = msg
				replaced = true
				break
			}
		}
	}
	if !replaced {
		c.messages = append(c.messages, msg)
	}
	resyncCB := c.OnNeedResync
	c.mu.Unlock()

	if gap && resyncCB != nil {
		after := ""
		if c.Resilience != nil {
			after = c.Resilience.Cursor(scope).LastEventID
		}
		resyncCB(msg.ChannelID, after)
	}
	return false, gap
}

// IngestExternalMessage enforces fail-closed provenance, tenant isolation,
// anti-impersonation/anti-spoofed-gate checks, channel-thread affinity, and
// CaMeL-style taint marking for messages arriving from external chat connectors.
func (c *Controller) IngestExternalMessage(
	eventID string,
	seq uint64,
	msg RoomMessage,
	proof ExternalIngressProof,
) (duplicate bool, gap bool, err error) {
	if !proof.OriginVerified {
		return false, false, ErrUntrustedExternalIngress
	}
	if strings.TrimSpace(msg.GateID) != "" {
		return false, false, ErrSpoofedGateReceipt
	}
	c.mu.RLock()
	if msg.ChannelID == "" {
		msg.ChannelID = c.activeChannelID
	}
	if c.tenantID != "" && proof.TenantID != c.tenantID {
		c.mu.RUnlock()
		return false, false, ErrCrossTenantRoom
	}
	if !c.hasChannelLocked(msg.ChannelID) {
		c.mu.RUnlock()
		return false, false, ErrChannelNotFound
	}
	if msg.ThreadRootID != "" && !c.threadBelongsToChannelLocked(msg.ThreadRootID, msg.ChannelID) {
		c.mu.RUnlock()
		return false, false, ErrCrossChannelReference
	}
	if existing, ok := c.participants[msg.AuthorID]; ok && (existing.Kind == ParticipantHuman || existing.Permissions.CanApproveGates) {
		c.mu.RUnlock()
		return false, false, ErrSpoofedGateReceipt
	}
	c.mu.RUnlock()

	msg.TaintedExternal = true
	dup, detectedGap := c.IngestRemoteMessage(eventID, seq, msg)
	if !dup {
		c.mu.Lock()
		if c.channelTainted == nil {
			c.channelTainted = make(map[string]bool)
		}
		c.channelTainted[msg.ChannelID] = true
		ctxGov := c.contextGov
		tid := c.tenantID
		c.mu.Unlock()
		if ctxGov != nil {
			if tid == "" {
				tid = proof.TenantID
			}
			ctxGov.MarkChannelTainted(tid, msg.ChannelID, proof.ConnectorID, "external chatroom ingress")
		}
	}
	return dup, detectedGap, nil
}

func (c *Controller) hasChannelLocked(channelID string) bool {
	for i := range c.channels {
		if c.channels[i].ID == channelID {
			return true
		}
	}
	return false
}

func (c *Controller) threadBelongsToChannelLocked(threadRootID, channelID string) bool {
	for i := range c.messages {
		if c.messages[i].ID == threadRootID && c.messages[i].ChannelID == channelID {
			return true
		}
	}
	return false
}

func (c *Controller) matterHasBlockingGateLocked(itemID string) bool {
	for _, rec := range c.gateReceipts {
		if rec.MatterID == itemID && (rec.Status == GateStatusPending || rec.Status == GateStatusRejected) {
			return true
		}
	}
	return false
}

// FlushOutbox delivers all queued/failed offline messages for channelID using
// their original idempotent SubmissionIDs and updates optimistic message badges.
func (c *Controller) FlushOutbox(channelID string, deliverFn func(item resilience.OutboxItem) error) (int, int) {
	if c.Resilience == nil || c.Resilience.Outbox == nil {
		return 0, 0
	}
	scope := ""
	if channelID != "" {
		scope = "room:" + channelID
	}
	acked, failed := c.Resilience.Outbox.Flush(scope, func(it resilience.OutboxItem) error {
		if err := deliverFn(it); err != nil {
			c.setMessageDelivery(it.SubmissionID, resilience.DeliveryFailed)
			return err
		}
		c.setMessageDelivery(it.SubmissionID, resilience.DeliveryAcked)
		return nil
	})
	if failed == 0 && acked > 0 {
		c.Resilience.MarkOnline()
	}
	return acked, failed
}

func (c *Controller) setMessageDelivery(submissionID string, st resilience.DeliveryState) {
	if submissionID == "" {
		return
	}
	c.mu.Lock()
	for i := range c.messages {
		if c.messages[i].SubmissionID == submissionID {
			c.messages[i].Delivery = st
			break
		}
	}
	c.mu.Unlock()
}

// ToggleReaction lets any permitted human or agent participant toggle a reaction mark.
func (c *Controller) ToggleReaction(msgID, actorID, tag string) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return ErrEmptyContent
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	actor, ok := c.participants[actorID]
	if !ok {
		return ErrParticipantNotFound
	}
	if !actor.Permissions.CanPost {
		return fmt.Errorf("%w: %s lacks CanPost permission", ErrPermissionDenied, actor.Handle)
	}
	for i := range c.messages {
		if c.messages[i].ID != msgID {
			continue
		}
		if c.messages[i].Reactions == nil {
			c.messages[i].Reactions = make(map[string][]string)
		}
		list := c.messages[i].Reactions[tag]
		removed := false
		for j, id := range list {
			if id == actorID {
				c.messages[i].Reactions[tag] = append(list[:j], list[j+1:]...)
				if len(c.messages[i].Reactions[tag]) == 0 {
					delete(c.messages[i].Reactions, tag)
				}
				removed = true
				break
			}
		}
		if !removed {
			c.messages[i].Reactions[tag] = append(list, actorID)
		}
		return nil
	}
	return ErrMessageNotFound
}

// CreateItem creates a shared matter inside channelID after verifying CanManageItems.
func (c *Controller) CreateItem(channelID, actorID, title, ownerID string) (RoomItem, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return RoomItem{}, ErrEmptyContent
	}
	c.mu.Lock()
	actor, ok := c.participants[actorID]
	if !ok {
		c.mu.Unlock()
		return RoomItem{}, ErrParticipantNotFound
	}
	if !actor.Permissions.CanManageItems {
		reason := fmt.Sprintf("%s (%s) lacks CanManageItems permission", actor.Handle, actor.RoleTitle)
		c.lastError = reason
		ledger := c.ledger
		c.mu.Unlock()
		if ledger != nil {
			ledger.RecordAudit(actor.Handle, channelID, "chatroom.item.create", features.AuditDenied, reason)
		}
		return RoomItem{}, fmt.Errorf("%w: %s", ErrPermissionDenied, reason)
	}
	if channelID == "" {
		channelID = c.activeChannelID
	}
	if ownerID == "" {
		ownerID = actorID
	}
	c.itemSeq++
	item := RoomItem{
		ID:        fmt.Sprintf("item-%d", c.itemSeq),
		ChannelID: channelID,
		Title:     title,
		Status:    ItemOpen,
		OwnerID:   ownerID,
		UpdatedBy: actorID,
	}
	c.items = append(c.items, item)
	c.lastError = ""
	ledger := c.ledger
	cb := c.OnItemUpdated
	c.mu.Unlock()
	if ledger != nil {
		ledger.RecordAudit(actor.Handle, item.ID, "chatroom.item.create", features.AuditAdmitted, title)
	}
	if cb != nil {
		cb(item)
	}
	return item, nil
}

// TransitionItem updates a shared matter's status after verifying CanManageItems.
func (c *Controller) TransitionItem(itemID, actorID string, next ItemStatus) error {
	c.mu.Lock()
	actor, ok := c.participants[actorID]
	if !ok {
		c.mu.Unlock()
		return ErrParticipantNotFound
	}
	if !actor.Permissions.CanManageItems {
		reason := fmt.Sprintf("%s (%s) lacks CanManageItems permission", actor.Handle, actor.RoleTitle)
		c.lastError = reason
		ledger := c.ledger
		c.mu.Unlock()
		if ledger != nil {
			ledger.RecordAudit(actor.Handle, itemID, "chatroom.item.transition", features.AuditDenied, reason)
		}
		return fmt.Errorf("%w: %s", ErrPermissionDenied, reason)
	}
	for i := range c.items {
		if c.items[i].ID == itemID {
			if actor.Kind == ParticipantAgent && c.items[i].Status == ItemBlocked && next != ItemBlocked && c.matterHasBlockingGateLocked(itemID) {
				reason := fmt.Sprintf("%s cannot unblock gate-blocked matter %s without human gate approval", actor.Handle, itemID)
				c.lastError = reason
				ledger := c.ledger
				c.mu.Unlock()
				if ledger != nil {
					ledger.RecordAudit(actor.Handle, itemID, "chatroom.item.unblock", features.AuditDenied, reason)
				}
				return fmt.Errorf("%w: %s", ErrMatterGateBlocked, reason)
			}
			c.items[i].Status = next
			c.items[i].UpdatedBy = actorID
			updated := c.items[i]
			c.lastError = ""
			ledger := c.ledger
			cb := c.OnItemUpdated
			c.mu.Unlock()
			if ledger != nil {
				ledger.RecordAudit(actor.Handle, itemID, "chatroom.item.transition", features.AuditAdmitted, string(next))
			}
			if cb != nil {
				cb(updated)
			}
			return nil
		}
	}
	c.mu.Unlock()
	return ErrItemNotFound
}

// UpdateRolePermissions modifies targetID's room permissions under Marlin's
// fail-closed RBAC invariants: only a human with CanApproveGates may modify
// permissions, and CanApproveGates can NEVER be granted to an AI agent.
func (c *Controller) UpdateRolePermissions(targetID, actorID string, next RolePermissions) error {
	c.mu.Lock()
	actor, ok := c.participants[actorID]
	if !ok {
		c.mu.Unlock()
		return ErrParticipantNotFound
	}
	if actor.Kind != ParticipantHuman || !actor.Permissions.CanApproveGates {
		reason := fmt.Sprintf("%s cannot modify role permissions", actor.Handle)
		c.lastError = reason
		ledger := c.ledger
		c.mu.Unlock()
		if ledger != nil {
			ledger.RecordAudit(actor.Handle, targetID, "chatroom.rbac.update", features.AuditDenied, reason)
		}
		return fmt.Errorf("%w: %s", ErrPermissionDenied, reason)
	}
	target, ok := c.participants[targetID]
	if !ok {
		c.mu.Unlock()
		return ErrParticipantNotFound
	}
	if target.Kind == ParticipantAgent && next.CanApproveGates {
		c.lastError = ErrAgentCannotApproveGate.Error()
		ledger := c.ledger
		c.mu.Unlock()
		if ledger != nil {
			ledger.RecordAudit(actor.Handle, target.Handle, "chatroom.rbac.grant_gate_approve", features.AuditDenied, ErrAgentCannotApproveGate.Error())
		}
		return ErrAgentCannotApproveGate
	}
	target.Permissions = next
	c.participants[targetID] = target
	c.lastError = ""
	ledger := c.ledger
	cb := c.OnPermissionChanged
	c.mu.Unlock()
	if ledger != nil {
		detail := fmt.Sprintf("post=%v items=%v tools=%v gate=%v", next.CanPost, next.CanManageItems, next.CanInvokeTools, next.RequireGate)
		ledger.RecordAudit(actor.Handle, target.Handle, "chatroom.rbac.update", features.AuditAdmitted, detail)
	}
	if cb != nil {
		cb(target)
	}
	return nil
}

// Snapshot returns a thread-safe immutable view for 120Hz GPU rendering.
func (c *Controller) Snapshot() RoomSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	snap := RoomSnapshot{
		Channels:        append([]Channel(nil), c.channels...),
		ActiveChannelID: c.activeChannelID,
		ActiveThreadID:  c.activeThreadID,
		CurrentActorID:  c.currentActorID,
		LastError:       c.lastError,
	}
	for _, pid := range c.participantOrd {
		if p, ok := c.participants[pid]; ok {
			snap.Participants = append(snap.Participants, p)
		}
	}
	for _, m := range c.messages {
		if m.ChannelID != c.activeChannelID {
			continue
		}
		cp := m
		if len(m.Reactions) > 0 {
			cp.Reactions = make(map[string][]string, len(m.Reactions))
			for k, v := range m.Reactions {
				cp.Reactions[k] = append([]string(nil), v...)
			}
		}
		if m.ThreadRootID == "" {
			snap.Messages = append(snap.Messages, cp)
		} else if m.ThreadRootID == c.activeThreadID {
			snap.ThreadReplies = append(snap.ThreadReplies, cp)
		}
	}
	for _, it := range c.items {
		if it.ChannelID == c.activeChannelID {
			snap.Items = append(snap.Items, it)
		}
	}
	if c.Resilience != nil {
		snap.Continuity = c.Resilience.Snapshot("room:" + c.activeChannelID)
	} else {
		snap.Continuity = resilience.ResilienceSnapshot{State: resilience.ConnOnline}
	}
	return snap
}

// ThreadReplyCount reports how many replies belong to rootMsgID.
func (c *Controller) ThreadReplyCount(rootMsgID string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	n := 0
	for i := range c.messages {
		if c.messages[i].ThreadRootID == rootMsgID {
			n++
		}
	}
	return n
}

func extractHandles(text string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, tok := range strings.Fields(text) {
		if !strings.HasPrefix(tok, "@") || len(tok) < 2 {
			continue
		}
		clean := strings.TrimRight(tok, ".,!?:;)")
		if len(clean) < 2 {
			continue
		}
		key := strings.ToLower(clean)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, clean)
	}
	return out
}
