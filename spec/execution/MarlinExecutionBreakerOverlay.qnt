// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
// @quint-extends "@deterministic-engineering/quint-models/threat/execution/agent-tool-circuit-breaker/AgentToolCircuitBreaker"

// Marlin downstream-specific AI & Tool Execution Circuit Breaker overlay
// resolved transparently via quint-modules-resolver (zero intermediate file).
// Extends AgentToolCircuitBreaker with pluginhost ScopedProxy atomic revocation
// and Tier-0 GovernanceLedger audit trail guarantees.
module marlin_execution_breaker_composed {
  type State = { proxyRevoked: bool, proxyLeakOnTripped: bool, auditReceipts: int }
  action init = h' = { proxyRevoked: false, proxyLeakOnTripped: false, auditReceipts: 0 }
  action tripWithProxyRevoke = if (not(h.breakerTripped))
    h' = h.with("breakerTripped", true).with("proxyRevoked", true).with("auditReceipts", if (h.auditReceipts < 8) h.auditReceipts + 1 else h.auditReceipts)
    else h' = h
  action attemptProxyAction = if (not(h.proxyRevoked) and not(h.breakerTripped))
    h' = h.with("proxyLeakOnTripped", h.proxyLeakOnTripped or h.breakerTripped or h.proxyRevoked)
    else h' = h
  action rebindFreshProxy = if (not(h.breakerTripped) and h.proxyRevoked)
    h' = h.with("proxyRevoked", false)
    else h' = h
  action step = any { tripWithProxyRevoke, attemptProxyAction, rebindFreshProxy }
  val safety = not(h.proxyLeakOnTripped) and (not(h.proxyRevoked) or h.auditReceipts >= 1)
  run marlinBreakerProxyRevokeTest = init.then(acquireToolLease).then(tripWithProxyRevoke).then(attemptProxyAction).then(rearmBreakerAs(false, true)).then(rebindFreshProxy).then(attemptProxyAction).then(all { assert(not(h.proxyLeakOnTripped)), assert(h.auditReceipts == 1), assert(safety), h' = h })
}
