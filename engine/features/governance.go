// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package features

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// AuditOutcome classifies the authoritative result recorded in GovernanceLedger.
type AuditOutcome string

const (
	AuditAdmitted    AuditOutcome = "ADMITTED"
	AuditDenied      AuditOutcome = "DENIED"
	AuditGateClaimed AuditOutcome = "GATE_CLAIMED"
)

// GovernancePolicy defines fail-closed UI governance constraints enforced before
// any frontend feature or untrusted surface activates.
type GovernancePolicy struct {
	BlockedFeatures      map[FeatureID]string
	AllowedCapabilities  map[string]bool // nil/empty = allow standard Tier-0 read/UI scopes
	AllowUntrustedWebUI  bool
	RequireGateForMutate bool
}

// DefaultGovernancePolicy returns Marlin's fail-closed default UI governance posture.
func DefaultGovernancePolicy() GovernancePolicy {
	return GovernancePolicy{
		BlockedFeatures:      make(map[FeatureID]string),
		AllowUntrustedWebUI:  true,
		RequireGateForMutate: true,
	}
}

// GovernanceAuditEntry records one immutable governance admission, policy denial,
// or Tier-0 authorization gate claim receipt for user transparency.
type GovernanceAuditEntry struct {
	ReceiptID string
	Timestamp time.Time
	Actor     string // "policy", "user", "gate", "rust-host"
	Subject   string // feature ID, tool name, or gate ID
	Action    string // "feature.admit", "gate.approve_once", "gate.reject", etc.
	Outcome   AuditOutcome
	Detail    string
}

const maxAuditEntries = 256

// GovernanceLedger owns the active UI GovernancePolicy and bounded immutable
// audit receipt trail rendered in the Tier-0 Governance Console.
type GovernanceLedger struct {
	mu      sync.RWMutex
	policy  GovernancePolicy
	entries []GovernanceAuditEntry
	seq     uint64
}

// NewGovernanceLedger constructs a fail-closed UI GovernanceLedger.
func NewGovernanceLedger(policy GovernancePolicy) *GovernanceLedger {
	if policy.BlockedFeatures == nil {
		policy.BlockedFeatures = make(map[FeatureID]string)
	}
	return &GovernanceLedger{
		policy:  policy,
		entries: make([]GovernanceAuditEntry, 0, 32),
	}
}

// Policy returns a copy of the active GovernancePolicy.
func (g *GovernanceLedger) Policy() GovernancePolicy {
	g.mu.RLock()
	defer g.mu.RUnlock()
	cp := g.policy
	cp.BlockedFeatures = make(map[FeatureID]string, len(g.policy.BlockedFeatures))
	for k, v := range g.policy.BlockedFeatures {
		cp.BlockedFeatures[k] = v
	}
	if g.policy.AllowedCapabilities != nil {
		cp.AllowedCapabilities = make(map[string]bool, len(g.policy.AllowedCapabilities))
		for k, v := range g.policy.AllowedCapabilities {
			cp.AllowedCapabilities[k] = v
		}
	}
	return cp
}

// BlockFeature dynamically adds a fail-closed block rule for feature id.
func (g *GovernanceLedger) BlockFeature(id FeatureID, reason string) {
	if strings.TrimSpace(reason) == "" {
		reason = "blocked by UI governance policy"
	}
	g.mu.Lock()
	if g.policy.BlockedFeatures == nil {
		g.policy.BlockedFeatures = make(map[FeatureID]string)
	}
	g.policy.BlockedFeatures[id] = reason
	g.mu.Unlock()
}

// UnblockFeature removes a block rule for feature id.
func (g *GovernanceLedger) UnblockFeature(id FeatureID) {
	g.mu.Lock()
	delete(g.policy.BlockedFeatures, id)
	g.mu.Unlock()
}

// AdmitFeature evaluates meta against GovernancePolicy fail-closed and appends
// a typed audit receipt (ADMITTED or DENIED).
func (g *GovernanceLedger) AdmitFeature(meta FeatureMeta) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if reason, blocked := g.policy.BlockedFeatures[meta.ID]; blocked {
		g.appendLocked("policy", string(meta.ID), "feature.admit", AuditDenied, reason)
		return fmt.Errorf("%w (%s): %s", ErrFeatureDenied, meta.ID, reason)
	}
	if meta.TrustTier == Tier2UntrustedWebView && !g.policy.AllowUntrustedWebUI {
		reason := "untrusted WebView surfaces disabled by governance policy"
		g.appendLocked("policy", string(meta.ID), "feature.admit", AuditDenied, reason)
		return fmt.Errorf("%w (%s): %s", ErrFeatureDenied, meta.ID, reason)
	}
	if len(g.policy.AllowedCapabilities) > 0 {
		for _, capName := range meta.RequiredCapabilities {
			if !g.policy.AllowedCapabilities[capName] {
				reason := fmt.Sprintf("required capability %q not in allowlist", capName)
				g.appendLocked("policy", string(meta.ID), "feature.admit", AuditDenied, reason)
				return fmt.Errorf("%w (%s): %s", ErrFeatureDenied, meta.ID, reason)
			}
		}
	}
	detail := fmt.Sprintf("tier=%s caps=%d", meta.TrustTier, len(meta.RequiredCapabilities))
	g.appendLocked("policy", string(meta.ID), "feature.admit", AuditAdmitted, detail)
	return nil
}

// RecordAudit appends a custom governance receipt (e.g. PromptGate claim or denial).
func (g *GovernanceLedger) RecordAudit(actor, subject, action string, outcome AuditOutcome, detail string) GovernanceAuditEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.appendLocked(actor, subject, action, outcome, detail)
}

func (g *GovernanceLedger) appendLocked(actor, subject, action string, outcome AuditOutcome, detail string) GovernanceAuditEntry {
	g.seq++
	entry := GovernanceAuditEntry{
		ReceiptID: fmt.Sprintf("gov-%04d", g.seq),
		Timestamp: time.Now().UTC(),
		Actor:     actor,
		Subject:   subject,
		Action:    action,
		Outcome:   outcome,
		Detail:    detail,
	}
	if len(g.entries) >= maxAuditEntries {
		copy(g.entries, g.entries[1:])
		g.entries[len(g.entries)-1] = entry
	} else {
		g.entries = append(g.entries, entry)
	}
	return entry
}

// AuditTrail returns a newest-first copy of recorded governance receipts.
func (g *GovernanceLedger) AuditTrail() []GovernanceAuditEntry {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	n := len(g.entries)
	out := make([]GovernanceAuditEntry, n)
	for i := 0; i < n; i++ {
		out[i] = g.entries[n-1-i]
	}
	return out
}
