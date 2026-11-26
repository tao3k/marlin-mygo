// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
// @quint-extends "@deterministic-engineering/quint-models/threat/supply-chain/supply-chain-tuf/SupplyChainTuf"

// Marlin downstream-specific Marketplace TUF Supply-Chain overlay resolved
// transparently via quint-modules-resolver (zero intermediate file). Extends
// SupplyChainTuf with Official-Tier TrustRing verification and Lockfile
// pinned_sha256 integrity enforcement.
module marlin_marketplace_tuf_composed {
  type State = { officialOnly: bool, pinMatched: bool, unpinnedOrDevLeak: bool }
  action init = h' = { officialOnly: true, pinMatched: true, unpinnedOrDevLeak: false }
  action driftLockfilePin = h' = h.with("pinMatched", false)
  action restoreLockfilePin = h' = h.with("pinMatched", true)
  action admitMarketplaceBundleAs(isOfficialPub: bool): bool = if (h.pinMatched and (not(h.officialOnly) or isOfficialPub))
    h' = h.with("unpinnedOrDevLeak", h.unpinnedOrDevLeak or not(h.pinMatched) or (h.officialOnly and not(isOfficialPub)))
    else h' = h
  action admitMarketplaceBundle = {
    nondet isOfficialPub = Set(false, true).oneOf()
    admitMarketplaceBundleAs(isOfficialPub)
  }
  action step = any { driftLockfilePin, restoreLockfilePin, admitMarketplaceBundle }
  val safety = not(h.unpinnedOrDevLeak)
  run marlinMarketplaceTufSafeTest = init.then(publishRelease).then(clientUpdateAs(2, 3, 2, 2, 2)).then(driftLockfilePin).then(admitMarketplaceBundleAs(true)).then(restoreLockfilePin).then(admitMarketplaceBundleAs(false)).then(admitMarketplaceBundleAs(true)).then(all { assert(h.installedVer == 2), assert(not(h.unpinnedOrDevLeak)), assert(safety), h' = h })
}
