// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { memo, useCallback, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  MarlinPanelProvider,
  PanelHeader,
  PanelShell,
  SegmentedControl,
  StatStrip,
  StatusBadge,
  ToastBanner,
  useMarlinAction,
  useMarlinProjection,
  type SemanticTone,
} from "@tao3k/marlin-panel-sdk/react";
import {
  type StepFilter,
  type WorkflowRunProjection,
  type WorkflowStep,
  filterWorkflowSteps,
  findBlockingGateStep,
  summarizeWorkflowRun,
} from "./model";

const INITIAL_RUN: WorkflowRunProjection = {
  runId: "wf-20261125-01",
  preset: "daily-briefing",
  status: "waiting_gate",
  steps: [
    {
      stepId: "st-1",
      phase: "discover",
      agentRole: "Org Context Scout",
      title: "Project Agenda, Clock, and Memory snapshots",
      status: "done",
      durationMs: 142,
      tokens: 840,
      receiptDigest: "sha256:4a9c1f",
    },
    {
      stepId: "st-2",
      phase: "plan",
      agentRole: "Schedule Planner",
      title: "Resolve afternoon time-block overlaps and draft focus blocks",
      status: "done",
      durationMs: 390,
      tokens: 1620,
      receiptDigest: "sha256:81bd0e",
    },
    {
      stepId: "st-3",
      phase: "patch",
      agentRole: "Notification Dispatcher",
      title: "Push evening reminder to iPhone companion",
      status: "waiting_gate",
      durationMs: 45,
      tokens: 210,
      gateId: "gate-demo-1",
    },
    {
      stepId: "st-4",
      phase: "verify",
      agentRole: "Contract Auditor",
      title: "Verify invariants and archive signed execution receipts",
      status: "queued",
      durationMs: 0,
      tokens: 0,
    },
  ],
};

function parseWorkflowRun(raw: unknown): WorkflowRunProjection | undefined {
  const r = raw as Partial<WorkflowRunProjection> | null;
  if (r && typeof r.runId === "string" && Array.isArray(r.steps)) {
    return {
      runId: r.runId,
      preset: r.preset ?? "custom",
      status: r.status ?? "running",
      steps: r.steps,
    };
  }
  return undefined;
}

const PRESETS = [
  { id: "daily-briefing", label: "Daily Briefing" },
  { id: "pr-security-review", label: "PR Security Review" },
  { id: "org-knowledge-synthesis", label: "Knowledge Synthesis" },
];

const STEP_FILTERS = [
  { key: "all", label: "All" },
  { key: "active", label: "Active" },
  { key: "waiting_gate", label: "Gate" },
  { key: "failed", label: "Failed" },
  { key: "done", label: "Done" },
] as const;

function stepTone(status: string): SemanticTone {
  if (status === "done") return "success";
  if (status === "waiting_gate") return "warning";
  if (status === "failed") return "danger";
  if (status === "running") return "accent";
  return "info";
}

const WorkflowStepRow = memo(function WorkflowStepRow({
  step,
  onRetry,
  onFocusGate,
}: {
  step: WorkflowStep;
  onRetry: (st: WorkflowStep) => void;
  onFocusGate: (st: WorkflowStep) => void;
}) {
  const tone = stepTone(step.status);
  return (
    <div className={`m-item-card tone-${tone} m-row-gap`} style={{ justifyContent: "space-between" }}>
      <div>
        <div className="m-row-gap" style={{ marginBottom: 3 }}>
          <StatusBadge tone={tone}>{step.phase}</StatusBadge>
          <strong>{step.title}</strong>
        </div>
        <div className="m-caption m-mono">
          {step.agentRole} · {step.status} · {step.durationMs}ms · {step.tokens} tok
          {step.receiptDigest ? ` · ${step.receiptDigest}` : ""}
        </div>
      </div>
      <div className="m-row-gap">
        {step.status === "failed" && (
          <button className="m-btn" type="button" onClick={() => onRetry(step)}>
            Retry
          </button>
        )}
        {step.status === "waiting_gate" && (
          <button className="m-btn m-btn-soft-warning" type="button" onClick={() => onFocusGate(step)}>
            Review Gate
          </button>
        )}
      </div>
    </div>
  );
});

export function AIWorkflowCockpitApp() {
  const runProj = useMarlinProjection("workflow.run", INITIAL_RUN, parseWorkflowRun);
  const startAct = useMarlinAction("workflow.run.start");
  const retryAct = useMarlinAction("workflow.step.retry");
  const cancelAct = useMarlinAction("workflow.run.cancel");
  const gateFocusAct = useMarlinAction("workflow.gate.focus");

  const [filter, setFilter] = useState<StepFilter>("all");
  const [statusMsg, setStatusMsg] = useState("");

  const summary = useMemo(() => summarizeWorkflowRun(runProj.data), [runProj.data]);
  const visibleSteps = useMemo(
    () => filterWorkflowSteps(runProj.data.steps, filter),
    [runProj.data.steps, filter],
  );
  const gateStep = useMemo(() => findBlockingGateStep(runProj.data.steps), [runProj.data.steps]);
  const stats = useMemo(
    () => [
      { label: "Verified", value: `${summary.doneSteps}/${summary.totalSteps}` },
      { label: "Gates", value: summary.waitingGates },
      { label: "Wall Time", value: `${summary.totalDurationMs}ms` },
      { label: "Tokens", value: summary.totalTokens },
    ],
    [summary],
  );

  async function handleStartPreset(presetId: string) {
    const rcpt = await startAct.submit({ preset: presetId });
    if (rcpt.admitted) {
      setStatusMsg(`Started preset "${presetId}"`);
    }
  }

  const handleRetry = useCallback(
    (step: WorkflowStep) => {
      void retryAct.submit({ runId: runProj.data.runId, stepId: step.stepId }).then((rcpt) => {
        if (rcpt.admitted) setStatusMsg(`Re-queued step ${step.stepId}`);
      });
    },
    [retryAct, runProj.data.runId],
  );

  const handleFocusGate = useCallback(
    (step: WorkflowStep) => {
      void gateFocusAct.submit({ runId: runProj.data.runId, gateId: step.gateId ?? "" }).then((rcpt) => {
        if (rcpt.admitted) setStatusMsg(`Focused Approval Gate ${step.gateId ?? step.stepId}`);
      });
    },
    [gateFocusAct, runProj.data.runId],
  );

  const handleDismiss = useCallback(() => setStatusMsg(""), []);

  async function handleCancel() {
    const rcpt = await cancelAct.submit({ runId: runProj.data.runId });
    if (rcpt.admitted) {
      setStatusMsg(`Cancellation requested for ${runProj.data.runId}`);
    }
  }

  return (
    <PanelShell>
      <PanelHeader
        title="Workflow Cockpit"
        caption={`Run ${runProj.data.runId} · ${summary.progressPct}% complete`}
        actions={
          <button className="m-btn m-btn-soft-warning" type="button" onClick={() => void handleCancel()}>
            Cancel Run
          </button>
        }
      />

      <div className="m-row-gap" style={{ marginBottom: 14 }}>
        {PRESETS.map((p) => (
          <button
            key={p.id}
            type="button"
            className={`m-btn ${runProj.data.preset === p.id ? "m-btn-primary" : ""}`}
            disabled={startAct.pending}
            onClick={() => void handleStartPreset(p.id)}
          >
            {p.label}
          </button>
        ))}
      </div>

      <StatStrip items={stats} />

      {gateStep && (
        <div className="m-callout m-callout-warn m-row-gap" style={{ justifyContent: "space-between", marginBottom: 14 }}>
          <span>
            <strong>Approval Required:</strong> {gateStep.title} ({gateStep.gateId})
          </span>
          <button className="m-btn m-btn-soft-warning" type="button" onClick={() => void handleFocusGate(gateStep)}>
            Open Host Gate
          </button>
        </div>
      )}

      <div style={{ marginBottom: 12 }}>
        <SegmentedControl options={STEP_FILTERS} value={filter} onChange={setFilter} />
      </div>

      {visibleSteps.map((st) => (
        <WorkflowStepRow
          key={st.stepId}
          step={st}
          onRetry={handleRetry}
          onFocusGate={handleFocusGate}
        />
      ))}

      <ToastBanner message={statusMsg} onDismiss={handleDismiss} />
    </PanelShell>
  );
}

const el = typeof document !== "undefined" ? document.getElementById("root") : null;
if (el) {
  createRoot(el).render(
    <MarlinPanelProvider pluginId="tao3k.ai-workflow-cockpit">
      <AIWorkflowCockpitApp />
    </MarlinPanelProvider>,
  );
}
