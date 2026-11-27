// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
// @quint-extends "@deterministic-engineering/quint-models/engineering/concurrency/hybrid-webview-focus-lifecycle/HybridWebViewFocusLifecycle"

// Marlin Hybrid GPU x Sandboxed WebView overlay resolved via
// quint-modules-resolver. Verifies *Workflow Canvas* (Tier-0 WorkflowBridge)
// vs Marketplace Tier-2 Untrusted plugins: zero render-thread mount stalls,
// strict focus revocation on tab hide, and zero untrusted WorkflowBridge calls.
module marlin_hybrid_webview_composed {
  type State = { workflowSynced: int, untrustedBridgeLeak: bool }
  action init = h' = { workflowSynced: 0, untrustedBridgeLeak: false }
  action syncWorkflowGraph = if (h.wvState == 2 and h.tier == 0 and h.workflowSynced < 3)
    h' = h.with("workflowSynced", h.workflowSynced + 1)
    else h' = h
  action auditBridgeBoundary = h' = h.with("untrustedBridgeLeak", h.untrustedBridgeLeak or h.rpcInvokedByTier != 0)
  action step = any { syncWorkflowGraph, auditBridgeBoundary }
  val safety = not(h.untrustedBridgeLeak) and h.workflowSynced >= 0
  run marlinHybridWorkflowBridgeAndRecoveryTest = init
    .then(selectTier0)
    .then(requestMount)
    .then(finishWarmup)
    .then(focusWebView)
    .then(invokeHostRpc)
    .then(syncWorkflowGraph)
    .then(auditBridgeBoundary)
    .then(hideTab)
    .then(crashWebContent)
    .then(recoverWebContent)
    .then(finishWarmup)
    .then(all {
      assert(h.workflowSynced == 1),
      assert(not(h.untrustedBridgeLeak)),
      assert(not(h.wvFocused)),
      assert(safety),
      h' = h
    })
}
