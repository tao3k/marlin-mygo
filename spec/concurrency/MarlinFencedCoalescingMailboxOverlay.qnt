// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
// @quint-extends "@deterministic-engineering/quint-models/engineering/concurrency/fenced-coalescing-mailbox/FencedCoalescingMailbox"

// Marlin UI coalesced reload/refresh/eval mailbox overlay resolved via
// quint-modules-resolver. Verifies ReloadThemesAsync, RefreshPackagesAsync,
// and WebView Eval lanes keep mailbox depth <= 1 and monotonically apply epochs.
module marlin_fenced_coalescing_mailbox_composed {
  type State = { observedUiEpoch: int, epochRegression: bool }
  action init = h' = { observedUiEpoch: 0, epochRegression: false }
  action observeMarlinLane = h' = h
    .with("epochRegression", h.epochRegression or h.appliedEpoch < h.observedUiEpoch)
    .with("observedUiEpoch", if (h.appliedEpoch > h.observedUiEpoch) h.appliedEpoch else h.observedUiEpoch)
  action step = any { observeMarlinLane }
  val safety = not(h.epochRegression) and h.observedUiEpoch <= h.maxAppliedEpoch
  run marlinCoalescedReloadAndRefreshTest = init
    .then(triggerRequest)
    .then(dispatchWorker)
    .then(triggerRequest)
    .then(applyDirectEpoch)
    .then(observeMarlinLane)
    .then(completeWorker)
    .then(observeMarlinLane)
    .then(all {
      assert(h.observedUiEpoch == 2),
      assert(not(h.epochRegression)),
      assert(h.pendingSlots <= 1),
      assert(safety),
      h' = h
    })
}
