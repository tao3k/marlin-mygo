// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package contextgov implements Marlin's multi-party Shared Context Governance
// layer: a 5-scope visibility lattice (private, dm, channel, workspace,
// external), owner-only scope promotion with explicit declassification,
// monotonic ACL-epoch revocation fencing, and CaMeL-style cross-participant
// taint and confidentiality IFC enforcement.
package contextgov

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tao3k/marlin-mygo/engine/features"
)

var (
	ErrInvalidContextEntry        = errors.New("contextgov: invalid context entry metadata")
	ErrContextNotFound            = errors.New("contextgov: context entry not found")
	ErrCrossTenantContext         = errors.New("contextgov: cross-tenant context access denied")
	ErrPrivateContextDenied       = errors.New("contextgov: private context is strictly owner-only")
	ErrContextScopeDenied         = errors.New("contextgov: principal is not authorized for context scope")
	ErrUnauthorizedScopePromotion = errors.New("contextgov: only the context owner may promote scope")
	ErrAgentCannotPromoteScope    = errors.New("contextgov: AI agents cannot widen or declassify context scope")
	ErrDeclassificationRequired   = errors.New("contextgov: confidential context requires explicit owner declassification before widening scope")
	ErrContextAccessRevoked       = errors.New("contextgov: context handle invalidated by membership removal or ACL epoch change")
	ErrConfidentialFlowDenied     = errors.New("contextgov: confidential private context cannot flow into shared or external sink without declassification")
	ErrTaintedContextToolBlocked  = errors.New("contextgov: tainted multi-party or external shared context cannot drive tool execution without human gate")
)

// ContextScope defines the five-tier visibility and governance lattice for
// Marlin shared context entries.
type ContextScope string

const (
	// ScopePrivate is strictly owner-only (never visible to peers or other users' agents).
	ScopePrivate ContextScope = "private"
	// ScopeDM restricts visibility to an explicit allowlist of bilateral/direct principals.
	ScopeDM ContextScope = "dm"
	// ScopeChannel shares context among current authorized members of one channel inside a tenant.
	ScopeChannel ContextScope = "channel"
	// ScopeWorkspace shares context across authorized members of one tenant/workspace.
	ScopeWorkspace ContextScope = "workspace"
	// ScopeExternal marks untrusted external bridge/webhook/public content (always tainted).
	ScopeExternal ContextScope = "external"
)

// Valid reports whether s is a recognized ContextScope.
func (s ContextScope) Valid() bool {
	switch s {
	case ScopePrivate, ScopeDM, ScopeChannel, ScopeWorkspace, ScopeExternal:
		return true
	default:
		return false
	}
}

// Sensitivity classifies confidentiality independently of transport scope.
type Sensitivity string

const (
	SensitivityPublic       Sensitivity = "public"
	SensitivityInternal     Sensitivity = "internal"
	SensitivityConfidential Sensitivity = "confidential"
)

// Principal identifies a human operator or an AI agent acting on behalf of a human.
type Principal struct {
	ID        string
	TenantID  string
	IsAgent   bool
	ActingFor string // optional human principal ID when IsAgent == true
}

func (p Principal) effectiveHumanID() string {
	if p.IsAgent {
		return strings.TrimSpace(p.ActingFor)
	}
	return strings.TrimSpace(p.ID)
}

// ContextEntry is one governed piece of shared or private context.
type ContextEntry struct {
	ID                string
	TenantID          string
	Scope             ContextScope
	ChannelID         string
	OwnerID           string
	AllowedPrincipals map[string]bool
	Sensitivity       Sensitivity
	Declassified      bool
	Tainted           bool
	ACLEpoch          uint64
	Body              string
	CreatedBy         string
	CreatedAt         time.Time
}

func cloneEntry(e ContextEntry) ContextEntry {
	cp := e
	if e.AllowedPrincipals != nil {
		cp.AllowedPrincipals = make(map[string]bool, len(e.AllowedPrincipals))
		for k, v := range e.AllowedPrincipals {
			cp.AllowedPrincipals[k] = v
		}
	}
	return cp
}

// ContextHandle represents a cached or delegated channel/workspace read handle
// pinned to the exact ACL epoch observed at mint time.
type ContextHandle struct {
	HandleID     string
	HolderID     string
	TenantID     string
	ChannelID    string
	GrantedEpoch uint64
}

// PromptContextBundle is the IFC-labeled context window assembled for an AI
// agent prior to generating a response or invoking a tool.
type PromptContextBundle struct {
	AgentID            string
	ActingFor          string
	TenantID           string
	TargetScope        ContextScope
	TargetChannelID    string
	Entries            []ContextEntry
	HasConfidential    bool
	HasTaintedExternal bool
}

// GovernanceStore enforces scope isolation, owner-only declassification and
// promotion, monotonic ACL-epoch revocation, and multi-party IFC taint tracking.
type GovernanceStore struct {
	mu               sync.RWMutex
	ledger           *features.GovernanceLedger
	entries          map[string]ContextEntry
	channelMembers   map[string]map[string]bool // tenant::channel -> principalID -> true
	channelEpochs    map[string]uint64          // tenant::channel -> monotonic ACL epoch
	channelTainted   map[string]bool            // tenant::channel -> tainted by untrusted/peer ingress
	workspaceMembers map[string]map[string]bool // tenant -> principalID -> true
	workspaceEpochs  map[string]uint64          // tenant -> monotonic ACL epoch
	handleSeq        uint64
}

// NewGovernanceStore constructs a fail-closed Shared Context GovernanceStore.
func NewGovernanceStore(ledger *features.GovernanceLedger) *GovernanceStore {
	return &GovernanceStore{
		ledger:           ledger,
		entries:          make(map[string]ContextEntry),
		channelMembers:   make(map[string]map[string]bool),
		channelEpochs:    make(map[string]uint64),
		channelTainted:   make(map[string]bool),
		workspaceMembers: make(map[string]map[string]bool),
		workspaceEpochs:  make(map[string]uint64),
	}
}

func channelKey(tenantID, channelID string) string {
	return strings.TrimSpace(tenantID) + "::" + strings.TrimSpace(channelID)
}

func (s *GovernanceStore) audit(actor, subject, action string, outcome features.AuditOutcome, detail string) {
	if s.ledger != nil {
		s.ledger.RecordAudit(actor, subject, action, outcome, detail)
	}
}

// RegisterChannelMember admits principalID into (tenantID, channelID), initializing
// the channel ACL epoch to 1 if new.
func (s *GovernanceStore) RegisterChannelMember(tenantID, channelID, principalID string) uint64 {
	key := channelKey(tenantID, channelID)
	pid := strings.TrimSpace(principalID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.channelEpochs[key] == 0 {
		s.channelEpochs[key] = 1
	}
	members := s.channelMembers[key]
	if members == nil {
		members = make(map[string]bool)
		s.channelMembers[key] = members
	}
	members[pid] = true
	return s.channelEpochs[key]
}

// RemoveChannelMember revokes principalID from (tenantID, channelID) and
// atomically bumps the channel ACL epoch so all previously minted handles fail closed.
func (s *GovernanceStore) RemoveChannelMember(tenantID, channelID, principalID string) uint64 {
	key := channelKey(tenantID, channelID)
	pid := strings.TrimSpace(principalID)
	s.mu.Lock()
	if members := s.channelMembers[key]; members != nil {
		delete(members, pid)
	}
	if s.channelEpochs[key] == 0 {
		s.channelEpochs[key] = 1
	}
	s.channelEpochs[key]++
	nextEpoch := s.channelEpochs[key]
	s.mu.Unlock()
	s.audit("policy", key, "context.revoke_member", features.AuditAdmitted, fmt.Sprintf("removed=%s epoch=%d", pid, nextEpoch))
	return nextEpoch
}

// ChannelACLEpoch returns the current monotonic ACL epoch for (tenantID, channelID).
func (s *GovernanceStore) ChannelACLEpoch(tenantID, channelID string) uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.channelEpochs[channelKey(tenantID, channelID)]
}

// IsChannelMember reports whether principalID is currently a member of (tenantID, channelID).
func (s *GovernanceStore) IsChannelMember(tenantID, channelID, principalID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	members := s.channelMembers[channelKey(tenantID, channelID)]
	return members != nil && members[strings.TrimSpace(principalID)]
}

// RegisterWorkspaceMember admits principalID into tenantID's workspace scope.
func (s *GovernanceStore) RegisterWorkspaceMember(tenantID, principalID string) uint64 {
	tid := strings.TrimSpace(tenantID)
	pid := strings.TrimSpace(principalID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.workspaceEpochs[tid] == 0 {
		s.workspaceEpochs[tid] = 1
	}
	members := s.workspaceMembers[tid]
	if members == nil {
		members = make(map[string]bool)
		s.workspaceMembers[tid] = members
	}
	members[pid] = true
	return s.workspaceEpochs[tid]
}

// RemoveWorkspaceMember revokes principalID from tenantID's workspace scope and bumps epoch.
func (s *GovernanceStore) RemoveWorkspaceMember(tenantID, principalID string) uint64 {
	tid := strings.TrimSpace(tenantID)
	pid := strings.TrimSpace(principalID)
	s.mu.Lock()
	if members := s.workspaceMembers[tid]; members != nil {
		delete(members, pid)
	}
	if s.workspaceEpochs[tid] == 0 {
		s.workspaceEpochs[tid] = 1
	}
	s.workspaceEpochs[tid]++
	next := s.workspaceEpochs[tid]
	s.mu.Unlock()
	return next
}

// CreateEntry validates and stores a governed context entry.
func (s *GovernanceStore) CreateEntry(entry ContextEntry) (ContextEntry, error) {
	entry.ID = strings.TrimSpace(entry.ID)
	entry.TenantID = strings.TrimSpace(entry.TenantID)
	entry.OwnerID = strings.TrimSpace(entry.OwnerID)
	entry.ChannelID = strings.TrimSpace(entry.ChannelID)
	if entry.ID == "" || entry.TenantID == "" || entry.OwnerID == "" || !entry.Scope.Valid() {
		return ContextEntry{}, ErrInvalidContextEntry
	}
	if entry.CreatedBy == "" {
		entry.CreatedBy = entry.OwnerID
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}

	switch entry.Scope {
	case ScopePrivate:
		if entry.Sensitivity == "" {
			entry.Sensitivity = SensitivityConfidential
		}
	case ScopeDM:
		if entry.Sensitivity == "" {
			entry.Sensitivity = SensitivityInternal
		}
		if entry.AllowedPrincipals == nil {
			entry.AllowedPrincipals = make(map[string]bool)
		}
		entry.AllowedPrincipals[entry.OwnerID] = true
	case ScopeChannel:
		if entry.ChannelID == "" {
			return ContextEntry{}, fmt.Errorf("%w: channel scope requires ChannelID", ErrInvalidContextEntry)
		}
		if entry.Sensitivity == "" {
			entry.Sensitivity = SensitivityInternal
		}
	case ScopeWorkspace:
		if entry.Sensitivity == "" {
			entry.Sensitivity = SensitivityInternal
		}
	case ScopeExternal:
		entry.Tainted = true
		if entry.Sensitivity == "" {
			entry.Sensitivity = SensitivityPublic
		}
	}

	s.mu.Lock()
	if entry.Scope == ScopeChannel {
		key := channelKey(entry.TenantID, entry.ChannelID)
		if s.channelEpochs[key] == 0 {
			s.channelEpochs[key] = 1
		}
		entry.ACLEpoch = s.channelEpochs[key]
		if entry.Tainted {
			s.channelTainted[key] = true
		}
	} else if entry.Scope == ScopeWorkspace {
		if s.workspaceEpochs[entry.TenantID] == 0 {
			s.workspaceEpochs[entry.TenantID] = 1
		}
		entry.ACLEpoch = s.workspaceEpochs[entry.TenantID]
	}
	stored := cloneEntry(entry)
	s.entries[stored.ID] = stored
	s.mu.Unlock()

	s.audit(stored.CreatedBy, stored.ID, "context.create", features.AuditAdmitted,
		fmt.Sprintf("tenant=%s scope=%s sensitivity=%s tainted=%v", stored.TenantID, stored.Scope, stored.Sensitivity, stored.Tainted))
	return cloneEntry(stored), nil
}

// MarkChannelTainted records that untrusted external or adversarial peer content
// has entered the shared context of (tenantID, channelID).
func (s *GovernanceStore) MarkChannelTainted(tenantID, channelID, actorID, reason string) {
	key := channelKey(tenantID, channelID)
	s.mu.Lock()
	s.channelTainted[key] = true
	s.mu.Unlock()
	s.audit(actorID, key, "context.channel_tainted", features.AuditAdmitted, reason)
}

// IsChannelTainted reports whether (tenantID, channelID) contains tainted shared context.
func (s *GovernanceStore) IsChannelTainted(tenantID, channelID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.channelTainted[channelKey(tenantID, channelID)]
}

// PromoteScope widens an entry's scope (e.g. private -> channel or workspace).
// Strictly enforced invariants:
//   - AI agents may NEVER promote scope or declassify context.
//   - Only the exact human OwnerID inside the same TenantID may promote scope.
//   - Confidential entries require explicitDeclassify == true before widening.
func (s *GovernanceStore) PromoteScope(
	entryID string,
	actor Principal,
	targetScope ContextScope,
	targetChannelID string,
	explicitDeclassify bool,
) (ContextEntry, error) {
	if !targetScope.Valid() {
		return ContextEntry{}, ErrInvalidContextEntry
	}
	if actor.IsAgent {
		s.audit(actor.ID, entryID, "context.promote_scope", features.AuditDenied, "AI agent cannot promote context scope")
		return ContextEntry{}, ErrAgentCannotPromoteScope
	}

	s.mu.Lock()
	entry, ok := s.entries[entryID]
	if !ok {
		s.mu.Unlock()
		return ContextEntry{}, ErrContextNotFound
	}
	if strings.TrimSpace(actor.TenantID) != entry.TenantID {
		s.mu.Unlock()
		s.audit(actor.ID, entryID, "context.promote_scope", features.AuditDenied, "cross-tenant promotion denied")
		return ContextEntry{}, ErrCrossTenantContext
	}
	if strings.TrimSpace(actor.ID) != entry.OwnerID {
		s.mu.Unlock()
		s.audit(actor.ID, entryID, "context.promote_scope", features.AuditDenied, "non-owner promotion denied")
		return ContextEntry{}, ErrUnauthorizedScopePromotion
	}
	if entry.Sensitivity == SensitivityConfidential && !entry.Declassified && targetScope != ScopePrivate && !explicitDeclassify {
		s.mu.Unlock()
		s.audit(actor.ID, entryID, "context.promote_scope", features.AuditDenied, "confidential declassification required")
		return ContextEntry{}, ErrDeclassificationRequired
	}
	if targetScope == ScopeChannel {
		ch := strings.TrimSpace(targetChannelID)
		if ch == "" {
			ch = entry.ChannelID
		}
		if ch == "" {
			s.mu.Unlock()
			return ContextEntry{}, fmt.Errorf("%w: targetChannelID required for channel scope", ErrInvalidContextEntry)
		}
		entry.ChannelID = ch
		key := channelKey(entry.TenantID, ch)
		if s.channelEpochs[key] == 0 {
			s.channelEpochs[key] = 1
		}
		entry.ACLEpoch = s.channelEpochs[key]
	}
	entry.Scope = targetScope
	if explicitDeclassify {
		entry.Declassified = true
	}
	s.entries[entryID] = cloneEntry(entry)
	s.mu.Unlock()

	s.audit(actor.ID, entryID, "context.promote_scope", features.AuditAdmitted,
		fmt.Sprintf("scope=%s declassified=%v", entry.Scope, entry.Declassified))
	return cloneEntry(entry), nil
}

// MintChannelHandle issues a handle pinned to the current ACL epoch of
// (tenantID, channelID) strictly for a current channel member.
func (s *GovernanceStore) MintChannelHandle(tenantID, channelID, holderID string) (ContextHandle, error) {
	key := channelKey(tenantID, channelID)
	hid := strings.TrimSpace(holderID)
	s.mu.Lock()
	defer s.mu.Unlock()
	members := s.channelMembers[key]
	if members == nil || !members[hid] {
		return ContextHandle{}, fmt.Errorf("%w: %s is not a member of %s", ErrContextScopeDenied, hid, channelID)
	}
	if s.channelEpochs[key] == 0 {
		s.channelEpochs[key] = 1
	}
	s.handleSeq++
	return ContextHandle{
		HandleID:     fmt.Sprintf("ctxh-%d", s.handleSeq),
		HolderID:     hid,
		TenantID:     strings.TrimSpace(tenantID),
		ChannelID:    strings.TrimSpace(channelID),
		GrantedEpoch: s.channelEpochs[key],
	}, nil
}

// ReadEntry enforces tenant and scope visibility boundaries for direct reads.
func (s *GovernanceStore) ReadEntry(entryID string, reader Principal) (ContextEntry, error) {
	s.mu.RLock()
	entry, ok := s.entries[entryID]
	if !ok {
		s.mu.RUnlock()
		return ContextEntry{}, ErrContextNotFound
	}
	if strings.TrimSpace(reader.TenantID) != entry.TenantID {
		s.mu.RUnlock()
		s.audit(reader.ID, entryID, "context.read", features.AuditDenied, "cross-tenant read denied")
		return ContextEntry{}, ErrCrossTenantContext
	}
	effID := reader.effectiveHumanID()
	switch entry.Scope {
	case ScopePrivate:
		if effID == "" || effID != entry.OwnerID {
			s.mu.RUnlock()
			s.audit(reader.ID, entryID, "context.read", features.AuditDenied, "private context denied to non-owner")
			return ContextEntry{}, ErrPrivateContextDenied
		}
	case ScopeDM:
		if effID == "" || !entry.AllowedPrincipals[effID] {
			s.mu.RUnlock()
			s.audit(reader.ID, entryID, "context.read", features.AuditDenied, "DM context denied outside participant allowlist")
			return ContextEntry{}, ErrContextScopeDenied
		}
	case ScopeChannel:
		key := channelKey(entry.TenantID, entry.ChannelID)
		members := s.channelMembers[key]
		if effID == "" || members == nil || !members[effID] {
			s.mu.RUnlock()
			s.audit(reader.ID, entryID, "context.read", features.AuditDenied, "channel context denied to non-member")
			return ContextEntry{}, ErrContextScopeDenied
		}
	case ScopeWorkspace:
		members := s.workspaceMembers[entry.TenantID]
		if effID == "" || members == nil || !members[effID] {
			s.mu.RUnlock()
			s.audit(reader.ID, entryID, "context.read", features.AuditDenied, "workspace context denied to non-member")
			return ContextEntry{}, ErrContextScopeDenied
		}
	case ScopeExternal:
		// Readable within same tenant, marked Tainted = true.
	default:
		s.mu.RUnlock()
		return ContextEntry{}, ErrContextScopeDenied
	}
	cp := cloneEntry(entry)
	s.mu.RUnlock()
	return cp, nil
}

// ReadEntryWithHandle enforces private-scope isolation AND monotonic ACL-epoch
// freshness on a cached channel ContextHandle (blocking revoked members).
func (s *GovernanceStore) ReadEntryWithHandle(entryID string, handle ContextHandle) (ContextEntry, error) {
	s.mu.RLock()
	entry, ok := s.entries[entryID]
	if !ok {
		s.mu.RUnlock()
		return ContextEntry{}, ErrContextNotFound
	}
	if strings.TrimSpace(handle.TenantID) != entry.TenantID {
		s.mu.RUnlock()
		s.audit(handle.HolderID, entryID, "context.read_handle", features.AuditDenied, "cross-tenant handle denied")
		return ContextEntry{}, ErrCrossTenantContext
	}
	switch entry.Scope {
	case ScopePrivate:
		if strings.TrimSpace(handle.HolderID) != entry.OwnerID {
			s.mu.RUnlock()
			s.audit(handle.HolderID, entryID, "context.read_handle", features.AuditDenied, "private context denied via channel handle")
			return ContextEntry{}, ErrPrivateContextDenied
		}
	case ScopeDM:
		if !entry.AllowedPrincipals[strings.TrimSpace(handle.HolderID)] {
			s.mu.RUnlock()
			s.audit(handle.HolderID, entryID, "context.read_handle", features.AuditDenied, "DM scope denied via handle")
			return ContextEntry{}, ErrContextScopeDenied
		}
	case ScopeChannel:
		if strings.TrimSpace(handle.ChannelID) != entry.ChannelID {
			s.mu.RUnlock()
			s.audit(handle.HolderID, entryID, "context.read_handle", features.AuditDenied, "cross-channel handle denied")
			return ContextEntry{}, ErrContextScopeDenied
		}
		key := channelKey(entry.TenantID, entry.ChannelID)
		members := s.channelMembers[key]
		curEpoch := s.channelEpochs[key]
		if members == nil || !members[strings.TrimSpace(handle.HolderID)] || handle.GrantedEpoch != curEpoch {
			s.mu.RUnlock()
			s.audit(handle.HolderID, entryID, "context.read_handle", features.AuditDenied,
				fmt.Sprintf("revoked handle epoch=%d current=%d", handle.GrantedEpoch, curEpoch))
			return ContextEntry{}, ErrContextAccessRevoked
		}
	case ScopeWorkspace:
		members := s.workspaceMembers[entry.TenantID]
		if members == nil || !members[strings.TrimSpace(handle.HolderID)] {
			s.mu.RUnlock()
			return ContextEntry{}, ErrContextAccessRevoked
		}
	case ScopeExternal:
		// Tainted external content within tenant.
	default:
		s.mu.RUnlock()
		return ContextEntry{}, ErrContextScopeDenied
	}
	cp := cloneEntry(entry)
	s.mu.RUnlock()
	return cp, nil
}

// AssembleAgentPromptContext collects the requested context entries for an AI
// agent acting on behalf of a human principal, enforcing scope visibility AND
// preventing confidential private data from flowing into shared/external sinks.
func (s *GovernanceStore) AssembleAgentPromptContext(
	agent Principal,
	targetScope ContextScope,
	targetChannelID string,
	entryIDs []string,
) (PromptContextBundle, error) {
	if !targetScope.Valid() {
		return PromptContextBundle{}, ErrInvalidContextEntry
	}
	bundle := PromptContextBundle{
		AgentID:         agent.ID,
		ActingFor:       agent.effectiveHumanID(),
		TenantID:        agent.TenantID,
		TargetScope:     targetScope,
		TargetChannelID: strings.TrimSpace(targetChannelID),
		Entries:         make([]ContextEntry, 0, len(entryIDs)),
	}
	if bundle.TargetChannelID != "" && s.IsChannelTainted(agent.TenantID, bundle.TargetChannelID) {
		bundle.HasTaintedExternal = true
	}
	for _, id := range entryIDs {
		ent, err := s.ReadEntry(id, agent)
		if err != nil {
			return PromptContextBundle{}, err
		}
		if ent.Sensitivity == SensitivityConfidential && !ent.Declassified {
			bundle.HasConfidential = true
		}
		if ent.Tainted {
			bundle.HasTaintedExternal = true
		}
		if ent.Scope == ScopeChannel && s.IsChannelTainted(ent.TenantID, ent.ChannelID) {
			bundle.HasTaintedExternal = true
		}
		bundle.Entries = append(bundle.Entries, ent)
	}
	// Anti-exfiltration IFC: confidential non-declassified entries MUST NOT flow
	// into a wider shared sink (DM/Channel/Workspace/External).
	if bundle.HasConfidential && targetScope != ScopePrivate {
		s.audit(agent.ID, bundle.TargetChannelID, "context.assemble_prompt", features.AuditDenied,
			fmt.Sprintf("confidential flow blocked into target scope %s", targetScope))
		return PromptContextBundle{}, ErrConfidentialFlowDenied
	}
	return bundle, nil
}

// ValidateAgentToolExecution enforces CaMeL multi-party taint containment:
// if the assembled context bundle carries tainted shared/external content,
// privileged tool execution fails closed without explicit human gate approval.
func (s *GovernanceStore) ValidateAgentToolExecution(bundle PromptContextBundle, humanGateApproved bool) error {
	if bundle.HasTaintedExternal && !humanGateApproved {
		s.audit(bundle.AgentID, bundle.TargetChannelID, "context.tool_exec", features.AuditDenied,
			"tainted multi-party/external context blocked without human gate")
		return ErrTaintedContextToolBlocked
	}
	if bundle.HasConfidential && bundle.TargetScope == ScopeExternal && !humanGateApproved {
		s.audit(bundle.AgentID, bundle.TargetChannelID, "context.tool_exec", features.AuditDenied,
			"confidential egress to external sink blocked without human declassification")
		return ErrConfidentialFlowDenied
	}
	s.audit(bundle.AgentID, bundle.TargetChannelID, "context.tool_exec", features.AuditAdmitted,
		fmt.Sprintf("tainted=%v gate=%v", bundle.HasTaintedExternal, humanGateApproved))
	return nil
}
