// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
// @quint-extends "@deterministic-engineering/quint-models/threat/execution/wasm-sandbox-confinement/WasmSandboxConfinement"

// Marlin downstream-specific WASM-IR + pluginhost sandbox overlay resolved
// transparently via quint-modules-resolver. Extends WasmSandboxConfinement with
// module-identity ScopedProxy binding and E-Stop hostcall cutoff.
module marlin_wasm_sandbox_composed {
  type State = { proxyBound: bool, breakerTripped: bool, revokedCallLeak: bool }
  action init = h' = { proxyBound: true, breakerTripped: false, revokedCallLeak: false }
  action tripSandboxBreaker = h' = h.with("breakerTripped", true).with("proxyBound", false)
  action rearmSandboxProxy = if (h.breakerTripped)
    h' = h.with("breakerTripped", false).with("proxyBound", true)
    else h' = h
  action emitHostAction = if (h.proxyBound and not(h.breakerTripped))
    h' = h.with("revokedCallLeak", h.revokedCallLeak or not(h.proxyBound) or h.breakerTripped)
    else h' = h
  action step = any { tripSandboxBreaker, rearmSandboxProxy, emitHostAction }
  val safety = not(h.revokedCallLeak)
  run marlinWasmSandboxBreakerTest = init.then(guestMemAccessAs(32)).then(guestHostcallAs(1, false)).then(emitHostAction).then(tripSandboxBreaker).then(emitHostAction).then(all { assert(not(h.revokedCallLeak)), assert(h.breakerTripped), assert(safety), h' = h })
}
