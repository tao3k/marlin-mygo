// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { describe, expect, test } from "bun:test";
import {
  type WorkflowRunProjection,
  filterWorkflowSteps,
  findBlockingGateStep,
  summarizeWorkflowRun,
} from "./model";
import { perfSuite } from "./perf";

const RUN: WorkflowRunProjection = {
  runId: "wf-1",
  preset: "daily-briefing",
  status: "waiting_gate",
  steps: [
    { stepId: "s1", phase: "discover", agentRole: "Scout", title: "Scan", status: "done", durationMs: 100, tokens: 500 },
    { stepId: "s2", phase: "patch", agentRole: "Notify", title: "Send", status: "waiting_gate", durationMs: 20, tokens: 100, gateId: "g-1" },
    { stepId: "s3", phase: "verify", agentRole: "Check", title: "Verify", status: "failed", durationMs: 50, tokens: 200 },
  ],
};

describe("react-ai-workflow-cockpit", () => {
  test("summarizes multi-agent run progress, tokens, and blocking Tier-0 gate", () => {
    const sum = summarizeWorkflowRun(RUN);
    expect(sum.doneSteps).toBe(1);
    expect(sum.waitingGates).toBe(1);
    expect(sum.failedSteps).toBe(1);
    expect(sum.totalTokens).toBe(800);
    expect(findBlockingGateStep(RUN.steps)?.gateId).toBe("g-1");
    expect(filterWorkflowSteps(RUN.steps, "failed")).toHaveLength(1);
  });

  test("passes 200-step plugin perf macro suite", () => {
    const rep = perfSuite.run({ scale: 2.0, iterationsOverride: 40 });
    expect(rep.passed).toBe(true);
  });
});
