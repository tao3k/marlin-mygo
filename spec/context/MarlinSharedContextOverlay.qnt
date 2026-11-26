// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
// @quint-extends "@deterministic-engineering/quint-models/threat/zero-trust/shared-context-governance/SharedContextGovernance"

// Marlin downstream-specific 5-Scope Shared Context Governance overlay resolved
// transparently via quint-modules-resolver. Extends SharedContextGovernance with
// multi-tenant context isolation and bilateral DM participant isolation.
module marlin_shared_context_composed {
  type State = { entryTenant: int, dmAuthorized: bool, crossTenantContextLeak: bool, dmScopeLeak: bool }
  action init = h' = { entryTenant: 1, dmAuthorized: false, crossTenantContextLeak: false, dmScopeLeak: false }
  action readTenantContextAs(readerTenant: int): bool = if (readerTenant == h.entryTenant)
    h' = h.with("crossTenantContextLeak", h.crossTenantContextLeak or readerTenant != h.entryTenant)
    else h' = h
  action readTenantContext = {
    nondet readerTenant = Set(1, 2).oneOf()
    readTenantContextAs(readerTenant)
  }
  action toggleDmParticipant = h' = h.with("dmAuthorized", not(h.dmAuthorized))
  action readDmContext = if (h.dmAuthorized)
    h' = h.with("dmScopeLeak", h.dmScopeLeak or not(h.dmAuthorized))
    else h' = h
  action step = any { readTenantContext, toggleDmParticipant, readDmContext }
  val safety = not(h.crossTenantContextLeak) and not(h.dmScopeLeak)
  run marlinSharedContextFiveScopeTest = init.then(readTenantContextAs(2)).then(readDmContext).then(toggleDmParticipant).then(readDmContext).then(promoteScopeAs(true, true)).then(mintPeerHandle).then(peerReadContext).then(all { assert(not(h.crossTenantContextLeak)), assert(not(h.dmScopeLeak)), assert(h.peerReads == 1), assert(safety), h' = h })
}
