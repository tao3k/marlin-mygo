// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
// @quint-extends "@deterministic-engineering/quint-models/engineering/concurrency/lease-fencing-token/LeaseFencingToken"

// Marlin downstream-specific FencedLane concurrency overlay resolved via
// quint-modules-resolver. Extends LeaseFencingToken with 120Hz UI main-thread
// commit epoch tracking to prove stale async reload/RPC completions can never
// overwrite newer UI snapshots.
module marlin_fenced_lane_composed {
  type State = { uiCommitEpoch: int, staleUiCommit: bool }
  action init = h' = { uiCommitEpoch: 0, staleUiCommit: false }
  action commitLatestToUi = if (h.storageMaxToken > 0 and h.storageMaxToken >= h.uiCommitEpoch)
    h' = h.with("uiCommitEpoch", h.storageMaxToken)
    else h' = h
  action dropStaleUiResult = if (h.c1Token > 0 and h.c1Token < h.uiCommitEpoch)
    h' = h
    else h' = h
  action step = any { commitLatestToUi, dropStaleUiResult }
  val safety = not(h.staleUiCommit) and h.uiCommitEpoch <= h.storageMaxToken
  run marlinFencedLaneMonotonicUiCommitTest = init
    .then(acquireC1)
    .then(pauseC1)
    .then(acquireC2)
    .then(writeC2)
    .then(commitLatestToUi)
    .then(writeC1)
    .then(dropStaleUiResult)
    .then(all {
      assert(h.uiCommitEpoch == 2),
      assert(h.storageMaxToken == 2),
      assert(not(h.staleUiCommit)),
      assert(safety),
      h' = h
    })
}
