// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
// @quint-extends "@deterministic-engineering/quint-models/threat/zero-trust/multi-tenant-isolation/MultiTenantIsolation"

// Marlin downstream-specific multi-tenant overlay resolved transparently via
// quint-modules-resolver (zero intermediate composed file). Extends upstream
// MultiTenantIsolation with Cedar policy epoch progression and Federation
// target tenant pinning.
module marlin_tenant_composed {
  type State = { cedarPolicyEpoch: int, targetTenant: int, targetCrossLeak: bool }
  action init = h' = { cedarPolicyEpoch: 1, targetTenant: 1, targetCrossLeak: false }
  action bumpTenantCedarPolicy = if (h.cedarPolicyEpoch < 3)
    h' = h.with("cedarPolicyEpoch", h.cedarPolicyEpoch + 1)
    else h' = h
  action switchFederationTarget = h' = h.with("targetTenant", if (h.targetTenant == 1) 2 else 1)
  action dispatchToTarget = if (h.activeTenant == h.targetTenant)
    h' = h.with("targetCrossLeak", h.targetCrossLeak or h.activeTenant != h.targetTenant)
    else h' = h
  action step = any { bumpTenantCedarPolicy, switchFederationTarget, dispatchToTarget }
  val safety = h.cedarPolicyEpoch >= 1 and not(h.targetCrossLeak)
  run marlinTenantFederationSafeTest = init.then(issueTicket).then(enforceTicketAs(1)).then(bumpTenantCedarPolicy).then(switchFederationTarget).then(dispatchToTarget).then(all { assert(h.cedarPolicyEpoch == 2), assert(not(h.targetCrossLeak)), assert(safety), h' = h })
}
