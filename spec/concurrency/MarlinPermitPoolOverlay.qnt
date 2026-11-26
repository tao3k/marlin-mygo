// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
// @quint-extends "@deterministic-engineering/quint-models/engineering/concurrency/backpressure-memory/BackpressureOwners"

// Marlin downstream-specific PermitPool backpressure overlay resolved via
// quint-modules-resolver. Extends BackpressureOwners with WebView/RPC worker
// admission tracking to verify bounded concurrency and cancel-retains-permit
// cleanup discipline.
module marlin_permit_pool_composed {
  type State = { peakAdmitted: int, overCapacityObserved: bool }
  action init = h' = { peakAdmitted: 0, overCapacityObserved: false }
  action observePermitPressure = h' = h
    .with("peakAdmitted", if (size(h.admitted) > h.peakAdmitted) size(h.admitted) else h.peakAdmitted)
    .with("overCapacityObserved", h.overCapacityObserved or size(h.admitted) > h.limit)
  action step = any { observePermitPressure }
  val safety = not(h.overCapacityObserved) and h.peakAdmitted <= h.limit
  run marlinPermitPoolBoundedAndCancelDrainTest = init
    .then(admitJob(0))
    .then(startJob(0))
    .then(admitJob(1))
    .then(observePermitPressure)
    .then(cancelJob(0))
    .then(cleanJob(0))
    .then(startJob(1))
    .then(finishJob(1))
    .then(drainJob(1))
    .then(all {
      assert(h.peakAdmitted == 2),
      assert(not(h.overCapacityObserved)),
      assert(size(h.admitted) == 0),
      assert(safety),
      h' = h
    })
}
