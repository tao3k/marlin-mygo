// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  MarlinPanelProvider,
  useMarlinAction,
  useMarlinProjection,
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
      title: "Project orgize Agenda, Clock & Memory snapshots",
      status: "done",
      durationMs: 142,
      tokens: 840,
      receiptDigest: "sha256:4a9c1f",
    },
    {
      stepId: "st-2",
      phase: "plan",
      agentRole: "Daily Schedule Planner",
      title: "Resolve 2 afternoon time-block overlaps & draft focus blocks",
      status: "done",
      durationMs: 390,
      tokens: 1620,
      receiptDigest: "sha256:81bd0e",
    },
    {
      stepId: "st-3",
      phase: "patch",
      agentRole: "Notification Dispatcher",
      title: "Push evening reminder to iPhone Personal companion",
      status: "waiting_gate",
      durationMs: 45,
      tokens: 210,
      gateId: "gate-demo-1",
    },
    {
      stepId: "st-4",
      phase: "verify",
      agentRole: "Deterministic Auditor",
      title: "Verify Org contract invariants & archive signed receipts",
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
  { id: "daily-briefing", label: "Run Daily Briefing" },
  { id: "pr-security-review", label: "Run PR Security Review" },
  { id: "org-knowledge-synthesis", label: "Run Knowledge Synthesis" },
];

export function AIWorkflowCockpitApp() {
  const runProj = useMarlinProjection("workflow.run", INITIAL_RUN, parseWorkflowRun);
  const startAct = useMarlinAction("workflow.run.start");
  const retryAct = useMarlinAction("workflow.step.retry");
  const cancelAct = useMarlinAction("workflow.run.cancel");
  const gateFocusAct = useMarlinAction("workflow.gate.focus");

  const [filter, setFilter] = useState<StepFilter>("all");
  const [receiptMsg, setReceiptMsg] = useState("Multi-Agent AI Workflow Cockpit ready.");

  const summary = useMemo(() => summarizeWorkflowRun(runProj.data), [runProj.data]);
  const visibleSteps = useMemo(
    () => filterWorkflowSteps(runProj.data.steps, filter),
    [runProj.data.steps, filter],
  );
  const gateStep = useMemo(() => findBlockingGateStep(runProj.data.steps), [runProj.data.steps]);

  async function handleStartPreset(presetId: string) {
    const rcpt = await startAct.submit({ preset: presetId });
    if (rcpt.admitted) {
      setReceiptMsg(`Started workflow preset "${presetId}" (receipt: ${rcpt.receiptId || "admitted"})`);
    }
  }

  async function handleRetry(step: WorkflowStep) {
    const rcpt = await retryAct.submit({ runId: runProj.data.runId, stepId: step.stepId });
    if (rcpt.admitted) {
      setReceiptMsg(`Re-queued step ${step.stepId} (receipt: ${rcpt.receiptId || "admitted"})`);
    }
  }

  async function handleFocusGate(step: WorkflowStep) {
    const rcpt = await gateFocusAct.submit({ runId: runProj.data.runId, gateId: step.gateId ?? "" });
    if (rcpt.admitted) {
      setReceiptMsg(`Requested Tier-0 host Approval Gate focus for ${step.gateId ?? step.stepId}`);
    }
  }

  async function handleCancel() {
    const rcpt = await cancelAct.submit({ runId: runProj.data.runId });
    if (rcpt.admitted) {
      setReceiptMsg(`Cancellation requested for ${runProj.data.runId} (receipt: ${rcpt.receiptId || "admitted"})`);
    }
  }

  return (
    <div>
      <header>
        <div>
          <strong>Multi-Agent AI Workflow Cockpit</strong>
          <div className="subtitle">
            React 19 · Run {runProj.data.runId} ({runProj.data.preset}) · {summary.progressPct}% complete
          </div>
        </div>
        <button className="warn" type="button" onClick={() => void handleCancel()}>
          Cancel Run
        </button>
      </header>

      <div className="presets">
        {PRESETS.map((p) => (
          <button
            key={p.id}
            type="button"
            className={runProj.data.preset === p.id ? "primary" : ""}
            disabled={startAct.pending}
            onClick={() => void handleStartPreset(p.id)}
          >
            {p.label}
          </button>
        ))}
      </div>

      <div className="stats">
        <div className="stat"><strong>{summary.doneSteps}/{summary.totalSteps}</strong><span className="subtitle">Steps Verified</span></div>
        <div className="stat"><strong>{summary.waitingGates}</strong><span className="subtitle">Pending Gates</span></div>
        <div className="stat"><strong>{summary.totalDurationMs}ms</strong><span className="subtitle">Wall Time</span></div>
        <div className="stat"><strong>{summary.totalTokens}</strong><span className="subtitle">Total Tokens</span></div>
      </div>

      {gateStep && (
        <div className="step waiting_gate">
          <div>
            <strong>Tier-0 Approval Required:</strong> {gateStep.title} ({gateStep.gateId})
          </div>
          <button className="warn" type="button" onClick={() => void handleFocusGate(gateStep)}>
            Open Host Gate
          </button>
        </div>
      )}

      <div className="presets">
        {(["all", "active", "waiting_gate", "failed", "done"] as StepFilter[]).map((f) => (
          <button
            key={f}
            type="button"
            className={filter === f ? "active" : ""}
            onClick={() => setFilter(f)}
          >
            {f.toUpperCase()}
          </button>
        ))}
      </div>

      {visibleSteps.map((st) => (
        <div key={st.stepId} className={`step ${st.status}`}>
          <div>
            <div>
              <span className="badge">{st.phase.toUpperCase()}</span>
              <strong>{st.title}</strong>
            </div>
            <div className="subtitle">
              {st.agentRole} · {st.status} · {st.durationMs}ms · {st.tokens} tok
              {st.receiptDigest ? ` · ${st.receiptDigest}` : ""}
            </div>
          </div>
          <div>
            {st.status === "failed" && (
              <button type="button" onClick={() => void handleRetry(st)}>Retry</button>
            )}
            {st.status === "waiting_gate" && (
              <button className="warn" type="button" onClick={() => void handleFocusGate(st)}>Review Gate</button>
            )}
          </div>
        </div>
      ))}

      <div className="receipt">{receiptMsg}</div>
    </div>
  );
}

const el = document.getElementById("root");
if (el) {
  createRoot(el).render(
    <MarlinPanelProvider pluginId="tao3k.ai-workflow-cockpit">
      <AIWorkflowCockpitApp />
    </MarlinPanelProvider>,
  );
}
