// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
// @quint-extends "@deterministic-engineering/quint-models/threat/identity/delegated-agent-authz/DelegatedAgentAuthz"

// Marlin downstream-specific OBO delegated agent authorization overlay resolved
// transparently via quint-modules-resolver. Extends DelegatedAgentAuthz with
// Tier-0 OSProtectedVault lock/unlock fail-closed propagation.
module marlin_delegated_authz_composed {
  type State = { vaultLocked: bool, lockedExecLeak: bool }
  action init = h' = { vaultLocked: false, lockedExecLeak: false }
  action lockVault = h' = h.with("vaultLocked", true)
  action unlockVault = h' = h.with("vaultLocked", false)
  action invokeVaultBackedTool = if (not(h.vaultLocked) and h.humanSignedIn)
    h' = h.with("lockedExecLeak", h.lockedExecLeak or h.vaultLocked)
    else h' = h
  action step = any { lockVault, unlockVault, invokeVaultBackedTool }
  val safety = not(h.lockedExecLeak)
  run marlinDelegatedVaultLockTest = init.then(delegateToAgentAs(1)).then(lockVault).then(invokeVaultBackedTool).then(unlockVault).then(executeDelegated).then(all { assert(not(h.vaultLocked)), assert(not(h.lockedExecLeak)), assert(safety), h' = h })
}
