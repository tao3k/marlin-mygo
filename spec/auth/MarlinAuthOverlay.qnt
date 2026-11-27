// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
// @quint-extends "@deterministic-engineering/quint-models/threat/identity/oauth-token-lifecycle/OAuthTokenLifecycle"

// Marlin downstream-specific pure .qnt extension resolved transparently via
// quint-modules-resolver on top of @deterministic-engineering/quint-models
// OAuthTokenLifecycle.qnt (zero intermediate composed file). Extends State,
// init, step, and monotonically strengthens safety for Vault Lock, Cedar Policy
// Epoch, Anti-Rollback, and OIDC UserInfo sub binding.
module marlin_auth_composed {
  type State = { vaultLocked: bool, cedarPolicyEpoch: int, vaultRevision: int, minVaultRevision: int, oidcSubBound: bool }
  action init = h' = { vaultLocked: false, cedarPolicyEpoch: 1, vaultRevision: 1, minVaultRevision: 1, oidcSubBound: true }
  action lockVault = h' = h.with("vaultLocked", true)
  action unlockVault = if (h.vaultRevision >= h.minVaultRevision)
    h' = h.with("vaultLocked", false)
    else h' = h
  action bumpCedarPolicy = if (h.cedarPolicyEpoch < 3)
    h' = h.with("cedarPolicyEpoch", h.cedarPolicyEpoch + 1)
    else h' = h
  action commitVault = if (not(h.vaultLocked) and h.vaultRevision < 5)
    h' = h.with("vaultRevision", h.vaultRevision + 1).with("minVaultRevision", h.vaultRevision + 1)
    else h' = h
  action step = any { lockVault, unlockVault, bumpCedarPolicy, commitVault }
  val safety = h.cedarPolicyEpoch >= 1 and h.vaultRevision >= h.minVaultRevision and h.oidcSubBound
  run marlinVaultLockAndPolicyTest = init.then(beginOAuth).then(completeCallbackAs(true, true)).then(commitVault).then(bumpCedarPolicy).then(lockVault).then(all { assert(h.vaultLocked), assert(h.cedarPolicyEpoch == 2), assert(h.vaultRevision == 2), assert(safety), h' = h })
}
