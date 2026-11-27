// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
// @quint-extends "@deterministic-engineering/quint-models/engineering/concurrency/structured-cancellation/StructuredCancellation"

// Marlin downstream-specific StructuredScope & Frame.Close() quiescence overlay
// resolved via quint-modules-resolver. Extends StructuredCancellation to verify
// background WebView/Marketplace lanes drain completely before window teardown.
module marlin_structured_scope_composed {
  type State = { frameClosed: bool, leakedTask: bool }
  action init = h' = { frameClosed: false, leakedTask: false }
  action finalizeFrameClose = if (h.parentExited and not(h.frameClosed))
    h' = h.with("frameClosed", true).with("leakedTask", h.leakedTask or not(childrenQuiescent))
    else h' = h
  action step = any { finalizeFrameClose }
  val safety = not(h.leakedTask) and (not(h.frameClosed) or h.parentExited)
  run marlinFrameQuiescenceDrainTest = init
    .then(spawnC1)
    .then(spawnC2)
    .then(requestCancel)
    .then(c1Drain)
    .then(c2Drain)
    .then(c1Complete)
    .then(c2Complete)
    .then(parentExit)
    .then(finalizeFrameClose)
    .then(all {
      assert(h.frameClosed),
      assert(not(h.leakedTask)),
      assert(safety),
      h' = h
    })
}
