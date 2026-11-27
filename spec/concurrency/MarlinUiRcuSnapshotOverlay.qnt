// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
// @quint-extends "@deterministic-engineering/quint-models/engineering/concurrency/ui-mainthread-rcu-snapshot/UiMainThreadRcuSnapshot"

// Marlin UI 120Hz Frame.View(c) zero-lock RCU snapshot overlay resolved via
// quint-modules-resolver. Verifies that Tab switches and Theme/Marketplace
// reloads never regress observed frame generation or stall the render thread.
module marlin_ui_rcu_snapshot_composed {
  type State = { lastRenderedGen: int, genRegressed: bool }
  action init = h' = { lastRenderedGen: 0, genRegressed: false }
  action recordMarlinFrame = h' = h
    .with("genRegressed", h.genRegressed or h.renderGen < h.lastRenderedGen)
    .with("lastRenderedGen", if (h.renderGen > h.lastRenderedGen) h.renderGen else h.lastRenderedGen)
  action step = any { recordMarlinFrame }
  val safety = not(h.genRegressed) and h.lastRenderedGen <= h.publishedGen
  run marlinUiZeroLockRcuFrameTest = init
    .then(beginStageIO)
    .then(renderFrame)
    .then(recordMarlinFrame)
    .then(publishSnapshot)
    .then(renderFrame)
    .then(recordMarlinFrame)
    .then(all {
      assert(h.lastRenderedGen == 1),
      assert(not(h.genRegressed)),
      assert(not(h.mainStalled)),
      assert(safety),
      h' = h
    })
}
