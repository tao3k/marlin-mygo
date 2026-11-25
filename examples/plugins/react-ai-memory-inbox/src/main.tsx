// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { useMemo, useState } from "react";
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
import type { OrgizeAgentMemoryCardDto, OrgizeMemoryResponseDto } from "./memory_dto";
import {
  type MemorySeverityFilter,
  buildMemoryDatetreeCapture,
  filterMemoryCards,
  summarizeMemoryCards,
} from "./model";

const TODAY_ISO = new Date().toISOString().slice(0, 10);

const SEVERITY_OPTIONS = [
  { key: "all", label: "All" },
  { key: "action", label: "Action" },
  { key: "suppressed", label: "Suppressed" },
  { key: "info", label: "Info" },
] as const;

const INITIAL_MEMORY: OrgizeMemoryResponseDto = {
  schemaVersion: 1,
  stats: { totalRecords: 3, currentRecords: 3, actionCards: 2, suppressedCards: 1, infoCards: 0, evidence: 4 },
  cards: [
    {
      anchor: "mem-arch-react19",
      title: "Use shared Marlin Design Tokens across GPU shell and WebView panels",
      effectiveTags: ["architecture", "theme", "decision"],
      decision: {
        code: "PROMOTE_DECISION",
        kind: "current",
        severity: "action",
        title: "Durable Architecture Decision",
        nextAction: "Promote into workspace Org datetree and link theme guide",
      },
      authority: [{ kind: "userConfirmed", label: "Confirmed", message: "Aligned across dark, light, and Emacs preset themes" }],
      evidence: [
        { kind: { code: "bench", label: "Perf", family: "metric" }, value: "React 19 minified bundle stays well under 384 KiB ceiling" },
        { kind: { code: "policy", label: "Mobile", family: "security" }, value: "Mobile keeps zero-code declarative OTA + curated native UI" },
      ],
    },
    {
      anchor: "mem-orgize-authority",
      title: "Keep Org syntax parsing authoritative in orgize / orgize-wasm",
      effectiveTags: ["orgize", "protocol"],
      decision: {
        code: "PROMOTE_RULE",
        kind: "current",
        severity: "action",
        title: "Single Parser Authority",
        nextAction: "Attach to workspace contract card for planner panels",
      },
      authority: [{ kind: "invariant", label: "Kernel", message: "orgize owns all AST and projection DTOs" }],
      evidence: [{ kind: { code: "dto", label: "Schema", family: "contract" }, value: "OrgizeAgendaViewResponseDto + OrgizeMemoryResponseDto" }],
    },
    {
      anchor: "mem-stale-preact-shim",
      title: "Legacy preact/compat workaround note from early prototype",
      effectiveTags: ["superseded", "prototype"],
      decision: {
        code: "SUPERSEDE_OLD",
        kind: "background",
        severity: "suppressed",
        title: "Superseded Prototype Note",
        nextAction: "Mark superseded to avoid confusing coding agents",
      },
      authority: [{ kind: "conflict", label: "Superseded", message: "Replaced by native React 19 + shared token SDK" }],
      evidence: [{ kind: { code: "note", label: "Audit", family: "review" }, value: "Eliminates preact/compat edge mismatches" }],
    },
  ],
};

function parseMemoryDTO(raw: unknown): OrgizeMemoryResponseDto | undefined {
  const dto = raw as Partial<OrgizeMemoryResponseDto> | null;
  if (dto && Array.isArray(dto.cards)) {
    return {
      schemaVersion: 1,
      stats: dto.stats ?? INITIAL_MEMORY.stats,
      cards: dto.cards,
    };
  }
  return undefined;
}

function severityTone(sev: string): SemanticTone {
  if (sev === "action") return "accent";
  if (sev === "suppressed") return "warning";
  return "info";
}

export function AIMemoryInboxApp() {
  const memProj = useMarlinProjection("org.memory", INITIAL_MEMORY, parseMemoryDTO);
  const acceptAct = useMarlinAction("org.memory.accept");
  const captureAct = useMarlinAction("org.capture.plan");
  const supersedeAct = useMarlinAction("org.memory.supersede");
  const dismissAct = useMarlinAction("org.memory.dismiss");

  const [severity, setSeverity] = useState<MemorySeverityFilter>("all");
  const [query, setQuery] = useState("");
  const [statusMsg, setStatusMsg] = useState("");

  const summary = useMemo(() => summarizeMemoryCards(memProj.data.cards), [memProj.data.cards]);
  const visible = useMemo(
    () => filterMemoryCards(memProj.data.cards, severity, query),
    [memProj.data.cards, severity, query],
  );
  const stats = useMemo(
    () => [
      { label: "Pending", value: summary.total },
      { label: "Action", value: summary.actionCount },
      { label: "Suppressed", value: summary.suppressedCount },
      { label: "Citations", value: summary.evidenceCount },
    ],
    [summary],
  );

  function removeCard(card: OrgizeAgentMemoryCardDto) {
    memProj.setLocal((prev) => ({
      ...prev,
      cards: prev.cards.filter((c) => c !== card),
    }));
  }

  async function handleAccept(card: OrgizeAgentMemoryCardDto) {
    const r1 = await acceptAct.submit({ anchor: card.anchor, title: card.title });
    const r2 = await captureAct.submit(buildMemoryDatetreeCapture(card, TODAY_ISO));
    if (r1.admitted && r2.admitted) {
      removeCard(card);
      setStatusMsg(`Saved "${card.title}" to Datetree`);
    }
  }

  async function handleSupersede(card: OrgizeAgentMemoryCardDto) {
    const rcpt = await supersedeAct.submit({ anchor: card.anchor, title: card.title });
    if (rcpt.admitted) {
      removeCard(card);
      setStatusMsg(`Marked "${card.title}" superseded`);
    }
  }

  async function handleDismiss(card: OrgizeAgentMemoryCardDto) {
    const rcpt = await dismissAct.submit({ anchor: card.anchor, title: card.title });
    if (rcpt.admitted) {
      removeCard(card);
      setStatusMsg(`Dismissed "${card.title}"`);
    }
  }

  return (
    <PanelShell>
      <PanelHeader
        title="Memory Inbox"
        caption={`${summary.total} pending · ${summary.evidenceCount} verified citations`}
      />

      <StatStrip items={stats} />

      <div className="m-row-gap" style={{ marginBottom: 14 }}>
        <SegmentedControl
          options={SEVERITY_OPTIONS}
          value={severity}
          onChange={setSeverity}
        />
        <input
          className="m-input"
          style={{ flex: 1, minWidth: 180 }}
          type="text"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search memory cards, tags, or next action..."
        />
      </div>

      {visible.length === 0 ? (
        <div className="m-empty">No matching memory cards.</div>
      ) : (
        visible.map((card, i) => {
          const tone = severityTone(card.decision.severity);
          return (
            <article key={card.anchor ?? `${card.title}-${i}`} className={`m-item-card tone-${tone}`}>
              <div className="m-row-gap" style={{ marginBottom: 4 }}>
                <StatusBadge tone={tone}>{card.decision.severity}</StatusBadge>
                <strong>{card.title}</strong>
              </div>
              <div className="m-caption">
                {card.decision.nextAction} · :{card.effectiveTags.join(":")}:
              </div>
              {card.evidence.map((ev, idx) => (
                <div className="m-callout" key={idx}>
                  <strong>{ev.kind.label}</strong> — {ev.value}
                </div>
              ))}
              <div className="m-row-gap" style={{ marginTop: 10 }}>
                <button className="m-btn m-btn-soft-success" type="button" onClick={() => void handleAccept(card)}>
                  Accept -&gt; Datetree
                </button>
                <button className="m-btn m-btn-soft-warning" type="button" onClick={() => void handleSupersede(card)}>
                  Supersede
                </button>
                <button className="m-btn m-btn-ghost" type="button" onClick={() => void handleDismiss(card)}>
                  Dismiss
                </button>
              </div>
            </article>
          );
        })
      )}

      <ToastBanner message={statusMsg} onDismiss={() => setStatusMsg("")} />
    </PanelShell>
  );
}

const el = typeof document !== "undefined" ? document.getElementById("root") : null;
if (el) {
  createRoot(el).render(
    <MarlinPanelProvider pluginId="tao3k.ai-memory-inbox">
      <AIMemoryInboxApp />
    </MarlinPanelProvider>,
  );
}
