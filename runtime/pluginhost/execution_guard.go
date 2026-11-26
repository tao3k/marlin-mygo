// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package pluginhost

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/tao3k/marlin-mygo/engine/features"
)

var (
	ErrBreakerTripped          = errors.New("pluginhost: execution circuit breaker is tripped (fail-closed E-Stop)")
	ErrStaleExecutionLease     = errors.New("pluginhost: execution lease invalidated by circuit breaker trip or epoch change")
	ErrLeaseAlreadyConsumed    = errors.New("pluginhost: execution lease already committed or cancelled")
	ErrAgentCannotRearmBreaker = errors.New("pluginhost: AI agents or unverified actors cannot re-arm a tripped circuit breaker")
	ErrToolBudgetExceeded      = errors.New("pluginhost: tool execution budget exceeded armed window ceiling")
	ErrToolCapabilityDenied    = errors.New("pluginhost: requested sandbox capability handle is not granted")
	ErrSandboxPathEscape       = errors.New("pluginhost: host root path traversal blocked by preopen sandbox")
	ErrTaintedExecutionBlocked = errors.New("pluginhost: tainted input cannot invoke privileged tool without human gate")
	ErrConfidentialEgressDeny  = errors.New("pluginhost: confidential data egress requires explicit human declassification")
)

// ToolLease is a single-use, trip-epoch-fenced execution lease granted to an AI
// agent or sandboxed tool before committing a side-effect.
type ToolLease struct {
	ID        string
	AgentID   string
	ToolName  string
	TripEpoch uint64
}

// ExecutionBreaker implements Marlin's latched AI & Tool Execution Circuit
// Breaker ("E-Stop / 脱扣控制器"):
//   - Two-phase lease fencing (AcquireToolLease -> CommitToolEffect) blocks
//     in-flight TOCTOU commits if the breaker trips mid-flight or after re-arm.
//   - Hard armed-window tool budget ceiling prevents runaway agent loops.
//   - Human-only re-arm advances tripEpoch monotonically (AI agents can NEVER
//     self-reset their own tripped breaker).
//   - Atomic proxy revocation immediately cuts off bound pluginhost ScopedProxies.
//   - Sandbox capability handles + preopen path confinement + CaMeL IFC checks.
type ExecutionBreaker struct {
	mu             sync.Mutex
	ledger         *features.GovernanceLedger
	maxBudget      int
	budgetUsed     int
	tripped        bool
	tripEpoch      uint64
	tripReason     string
	leaseSeq       uint64
	activeLeases   map[string]ToolLease
	consumedLeases map[string]bool
	boundProxies   []*ScopedProxy
	grantedHandles map[string]bool
}

// NewExecutionBreaker creates a fail-closed ExecutionBreaker with armed-window
// tool ceiling maxBudget (defaults to 1 if <= 0).
func NewExecutionBreaker(maxBudget int, ledger *features.GovernanceLedger) *ExecutionBreaker {
	if maxBudget <= 0 {
		maxBudget = 1
	}
	return &ExecutionBreaker{
		ledger:         ledger,
		maxBudget:      maxBudget,
		tripEpoch:      1,
		activeLeases:   make(map[string]ToolLease),
		consumedLeases: make(map[string]bool),
		grantedHandles: make(map[string]bool),
	}
}

func (b *ExecutionBreaker) audit(actor, subject, action string, outcome features.AuditOutcome, detail string) {
	if b.ledger != nil {
		b.ledger.RecordAudit(actor, subject, action, outcome, detail)
	}
}

// BindProxy attaches a pluginhost ScopedProxy so that tripping the breaker
// atomically revokes the proxy before any stale callback can emit actions.
func (b *ExecutionBreaker) BindProxy(proxy *ScopedProxy) {
	if proxy == nil {
		return
	}
	b.mu.Lock()
	b.boundProxies = append(b.boundProxies, proxy)
	tripped := b.tripped
	b.mu.Unlock()
	if tripped {
		proxy.Revoke()
	}
}

// GrantHandle explicitly authorizes a WASI/sandbox capability handle (e.g. "fs:ro", "net:egress").
func (b *ExecutionBreaker) GrantHandle(handle string) {
	h := strings.TrimSpace(handle)
	if h == "" {
		return
	}
	b.mu.Lock()
	b.grantedHandles[h] = true
	b.mu.Unlock()
}

// HasHandle reports whether handle is currently granted in the sandbox capability set.
func (b *ExecutionBreaker) HasHandle(handle string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.grantedHandles[strings.TrimSpace(handle)]
}

// InvokeSandboxHostcall enforces breaker status, explicit WASI/sandbox handle
// grants, and strict preopen relative-path confinement (rejecting traversal).
func (b *ExecutionBreaker) InvokeSandboxHostcall(handle, relPath string) error {
	b.mu.Lock()
	tripped := b.tripped
	granted := b.grantedHandles[strings.TrimSpace(handle)]
	b.mu.Unlock()

	if tripped {
		b.audit("sandbox", handle, "sandbox.hostcall", features.AuditDenied, "breaker tripped")
		return ErrBreakerTripped
	}
	if !granted {
		b.audit("sandbox", handle, "sandbox.hostcall", features.AuditDenied, "capability handle not granted")
		return fmt.Errorf("%w: %q", ErrToolCapabilityDenied, handle)
	}
	if !isConfinedRelativePath(relPath) {
		b.audit("sandbox", handle, "sandbox.hostcall", features.AuditDenied, "path traversal blocked: "+relPath)
		return fmt.Errorf("%w: %q", ErrSandboxPathEscape, relPath)
	}
	b.audit("sandbox", handle, "sandbox.hostcall", features.AuditAdmitted, relPath)
	return nil
}

func isConfinedRelativePath(raw string) bool {
	if strings.ContainsRune(raw, '\x00') || strings.Contains(raw, "\\") {
		return false
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return true
	}
	if strings.HasPrefix(trimmed, "/") {
		return false
	}
	for _, seg := range strings.Split(trimmed, "/") {
		if seg == ".." {
			return false
		}
	}
	cleaned := path.Clean(trimmed)
	return cleaned != ".." && !strings.HasPrefix(cleaned, "../") && !strings.HasPrefix(cleaned, "/")
}

// AcquireToolLease grants an in-flight execution lease pinned to the current
// tripEpoch when the circuit breaker is armed (not tripped).
func (b *ExecutionBreaker) AcquireToolLease(agentID, toolName string) (ToolLease, error) {
	b.mu.Lock()
	if b.tripped {
		reason := b.tripReason
		b.mu.Unlock()
		b.audit(agentID, toolName, "breaker.acquire_lease", features.AuditDenied, "breaker tripped: "+reason)
		return ToolLease{}, ErrBreakerTripped
	}
	b.leaseSeq++
	lease := ToolLease{
		ID:        fmt.Sprintf("lease-%d-%d", b.tripEpoch, b.leaseSeq),
		AgentID:   strings.TrimSpace(agentID),
		ToolName:  strings.TrimSpace(toolName),
		TripEpoch: b.tripEpoch,
	}
	b.activeLeases[lease.ID] = lease
	b.mu.Unlock()
	return lease, nil
}

// CancelLease removes an aborted or stale in-flight lease without consuming budget.
func (b *ExecutionBreaker) CancelLease(leaseID string) {
	b.mu.Lock()
	delete(b.activeLeases, leaseID)
	b.consumedLeases[leaseID] = true
	b.mu.Unlock()
}

// CommitToolEffect atomically enforces breaker closed state, trip-epoch fencing,
// one-shot lease uniqueness, and armed-window budget ceiling before admitting an effect.
func (b *ExecutionBreaker) CommitToolEffect(lease ToolLease) error {
	return b.CommitToolEffectWithIFC(lease, false, false, false, true)
}

// CommitToolEffectWithIFC combines ExecutionBreaker safety gates with CaMeL IFC
// taint and confidential-egress declassification enforcement.
func (b *ExecutionBreaker) CommitToolEffectWithIFC(
	lease ToolLease,
	taintedControl bool,
	confidentialData bool,
	declassified bool,
	humanGateApproved bool,
) error {
	b.mu.Lock()
	if b.tripped {
		b.mu.Unlock()
		b.audit(lease.AgentID, lease.ToolName, "breaker.commit_effect", features.AuditDenied, "breaker tripped at commit")
		return ErrBreakerTripped
	}
	if lease.TripEpoch != b.tripEpoch {
		b.mu.Unlock()
		b.audit(lease.AgentID, lease.ToolName, "breaker.commit_effect", features.AuditDenied,
			fmt.Sprintf("stale lease epoch=%d current=%d", lease.TripEpoch, b.tripEpoch))
		return ErrStaleExecutionLease
	}
	if b.consumedLeases[lease.ID] {
		b.mu.Unlock()
		return ErrLeaseAlreadyConsumed
	}
	if _, ok := b.activeLeases[lease.ID]; !ok {
		b.mu.Unlock()
		return ErrStaleExecutionLease
	}
	if taintedControl && !humanGateApproved {
		b.mu.Unlock()
		b.audit(lease.AgentID, lease.ToolName, "breaker.commit_effect", features.AuditDenied, "tainted control without human gate")
		return ErrTaintedExecutionBlocked
	}
	if confidentialData && !declassified {
		b.mu.Unlock()
		b.audit(lease.AgentID, lease.ToolName, "breaker.commit_effect", features.AuditDenied, "confidential egress without declassification")
		return ErrConfidentialEgressDeny
	}
	if b.budgetUsed >= b.maxBudget {
		b.mu.Unlock()
		b.audit(lease.AgentID, lease.ToolName, "breaker.commit_effect", features.AuditDenied,
			fmt.Sprintf("budget cap %d reached", b.maxBudget))
		return ErrToolBudgetExceeded
	}
	delete(b.activeLeases, lease.ID)
	b.consumedLeases[lease.ID] = true
	b.budgetUsed++
	used := b.budgetUsed
	b.mu.Unlock()

	b.audit(lease.AgentID, lease.ToolName, "breaker.commit_effect", features.AuditAdmitted,
		fmt.Sprintf("lease=%s epoch=%d budget=%d/%d", lease.ID, lease.TripEpoch, used, b.maxBudget))
	return nil
}

// Trip latches the circuit breaker open (fail-closed E-Stop) and atomically
// revokes every bound ScopedProxy so in-flight sandbox callbacks cannot fire.
func (b *ExecutionBreaker) Trip(actorID, reason string) {
	if strings.TrimSpace(reason) == "" {
		reason = "emergency execution circuit breaker tripped"
	}
	b.mu.Lock()
	b.tripped = true
	b.tripReason = reason
	proxies := append([]*ScopedProxy(nil), b.boundProxies...)
	epoch := b.tripEpoch
	b.mu.Unlock()

	for _, p := range proxies {
		p.Revoke()
	}
	b.audit(actorID, "execution-breaker", "breaker.trip", features.AuditDenied,
		fmt.Sprintf("epoch=%d reason=%s revoked_proxies=%d", epoch, reason, len(proxies)))
}

// Rearm resets a tripped circuit breaker strictly for an authenticated human
// operator, advancing tripEpoch so no pre-trip in-flight lease can ever commit.
// AI agents (isAgent == true) or unverified actors are rejected fail-closed.
func (b *ExecutionBreaker) Rearm(actorID string, isAgent bool, verifiedHuman bool) (uint64, error) {
	if isAgent || !verifiedHuman {
		b.audit(actorID, "execution-breaker", "breaker.rearm", features.AuditDenied,
			fmt.Sprintf("forbidden rearm attempt isAgent=%v verifiedHuman=%v", isAgent, verifiedHuman))
		return 0, ErrAgentCannotRearmBreaker
	}
	b.mu.Lock()
	if !b.tripped {
		epoch := b.tripEpoch
		b.mu.Unlock()
		return epoch, nil
	}
	b.tripped = false
	b.tripReason = ""
	b.tripEpoch++
	b.budgetUsed = 0
	// Permanently invalidate all pre-trip leases.
	for id := range b.activeLeases {
		b.consumedLeases[id] = true
	}
	b.activeLeases = make(map[string]ToolLease)
	nextEpoch := b.tripEpoch
	b.mu.Unlock()

	b.audit(actorID, "execution-breaker", "breaker.rearm", features.AuditAdmitted,
		fmt.Sprintf("rearmed by human epoch=%d", nextEpoch))
	return nextEpoch, nil
}

// State returns an atomic snapshot of breaker status for monitoring and MBT assertions.
func (b *ExecutionBreaker) State() (tripped bool, tripEpoch uint64, budgetUsed int, activeLeaseCount int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tripped, b.tripEpoch, b.budgetUsed, len(b.activeLeases)
}
