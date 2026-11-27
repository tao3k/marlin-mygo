// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

export type WorkflowPhase = "discover" | "plan" | "patch" | "verify";
export type StepStatus = "queued" | "running" | "waiting_gate" | "done" | "failed";
export type StepFilter = "all" | "active" | "waiting_gate" | "failed" | "done";

export interface WorkflowStep {
  stepId: string;
  phase: WorkflowPhase;
  agentRole: string;
  title: string;
  status: StepStatus;
  durationMs: number;
  tokens: number;
  receiptDigest?: string;
  gateId?: string;
}

export interface WorkflowRunProjection {
  runId: string;
  preset: string;
  status: "running" | "waiting_gate" | "completed" | "failed";
  steps: WorkflowStep[];
}

export interface WorkflowRunSummary {
  totalSteps: number;
  doneSteps: number;
  waitingGates: number;
  failedSteps: number;
  totalDurationMs: number;
  totalTokens: number;
  progressPct: number;
}

export function summarizeWorkflowRun(run: WorkflowRunProjection): WorkflowRunSummary {
  let doneSteps = 0;
  let waitingGates = 0;
  let failedSteps = 0;
  let totalDurationMs = 0;
  let totalTokens = 0;

  for (let i = 0; i < run.steps.length; i++) {
    const s = run.steps[i];
    totalDurationMs += s.durationMs;
    totalTokens += s.tokens;
    if (s.status === "done") doneSteps++;
    else if (s.status === "waiting_gate") waitingGates++;
    else if (s.status === "failed") failedSteps++;
  }

  const totalSteps = run.steps.length;
  const progressPct = totalSteps > 0 ? Math.round((doneSteps * 100) / totalSteps) : 0;
  return { totalSteps, doneSteps, waitingGates, failedSteps, totalDurationMs, totalTokens, progressPct };
}

export function filterWorkflowSteps(
  steps: readonly WorkflowStep[],
  filter: StepFilter,
): WorkflowStep[] {
  if (filter === "all") return steps.slice();
  const out: WorkflowStep[] = [];
  for (let i = 0; i < steps.length; i++) {
    const s = steps[i];
    if (filter === "active" && (s.status === "running" || s.status === "queued")) out.push(s);
    else if (filter === s.status) out.push(s);
  }
  return out;
}

export function findBlockingGateStep(steps: readonly WorkflowStep[]): WorkflowStep | undefined {
  for (let i = 0; i < steps.length; i++) {
    if (steps[i].status === "waiting_gate") return steps[i];
  }
  return undefined;
}
