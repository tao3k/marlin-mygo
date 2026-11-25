// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { definePluginPerfSuite, runPluginPerfCLI } from "@tao3k/marlin-panel-sdk";
import {
  type WorkflowRunProjection,
  filterWorkflowSteps,
  findBlockingGateStep,
  summarizeWorkflowRun,
} from "./model";

const RUN_200: WorkflowRunProjection = {
  runId: "wf-perf-200",
  preset: "pr-security-review",
  status: "running",
  steps: Array.from({ length: 200 }, (_, i) => ({
    stepId: `st-${i}`,
    phase: (["discover", "plan", "patch", "verify"] as const)[i % 4],
    agentRole: `Worker-${i % 8}`,
    title: `Execute stage node #${i}`,
    status: i === 180 ? "waiting_gate" : i < 150 ? "done" : i % 19 === 0 ? "failed" : "running",
    durationMs: 25 + (i % 90),
    tokens: 300 + (i % 500),
    receiptDigest: i < 150 ? `sha256:${i.toString(16)}` : undefined,
    gateId: i === 180 ? "gate-180" : undefined,
  })),
};

export const perfSuite = definePluginPerfSuite({
  pluginId: "tao3k.ai-workflow-cockpit",
  cases: [
    {
      name: "summarizeWorkflowRun_200",
      budgetUs: 250,
      remediation: "Compute step counts, tokens, and wall-time in a single linear pass.",
      run: () => {
        const s = summarizeWorkflowRun(RUN_200);
        if (s.totalSteps !== 200) throw new Error("unexpected step count");
      },
    },
    {
      name: "filterWorkflowSteps_and_findGate_200",
      budgetUs: 300,
      remediation: "Keep step filtering O(N) without sorting or deep cloning step structs.",
      run: () => {
        const active = filterWorkflowSteps(RUN_200.steps, "active");
        const gate = findBlockingGateStep(RUN_200.steps);
        if (active.length === 0 || !gate) throw new Error("unexpected result");
      },
    },
  ],
});

if (import.meta.main) {
  runPluginPerfCLI(perfSuite);
}
