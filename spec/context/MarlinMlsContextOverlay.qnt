// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
// @quint-extends "@deterministic-engineering/quint-models/threat/protocol/mls-ratchet-pcs/MlsRatchetPcs"

// Marlin downstream-specific MLS Shared-Context Encryption overlay resolved
// transparently via quint-modules-resolver (zero intermediate file). Extends
// MlsRatchetPcs with synchronized GovernanceStore ACL-epoch revocation on member
// removal so cryptographic key rotation and handle revocation never diverge.
module marlin_mls_context_composed {
  type State = { aclEpoch: int, unrotatedRemoveLeak: bool }
  action init = h' = { aclEpoch: 1, unrotatedRemoveLeak: false }
  action syncAclOnRemove = if (h.bobRemoved and h.aclEpoch < 5)
    h' = h.with("aclEpoch", h.aclEpoch + 1).with("unrotatedRemoveLeak", h.unrotatedRemoveLeak or (h.bobRemoved and h.bobKeyGen == h.keyGen))
    else h' = h
  action step = any { syncAclOnRemove }
  val safety = not(h.unrotatedRemoveLeak) and (not(h.bobRemoved) or h.bobKeyGen != h.keyGen)
  run marlinMlsContextRemoveSyncTest = init.then(sendPastMessage).then(advanceEpochRegular).then(removeBobCommit).then(syncAclOnRemove).then(sendPostRemoveMessage).then(bobDecryptPostRemove).then(all { assert(h.bobRemoved), assert(h.aclEpoch == 2), assert(not(h.removedMemberDecrypted)), assert(not(h.unrotatedRemoveLeak)), assert(safety), h' = h })
}
