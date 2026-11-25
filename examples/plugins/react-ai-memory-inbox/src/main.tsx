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
import type { OrgizeAgentMemoryCardDto, OrgizeMemoryResponseDto } from "./memory_dto";
import {
  type MemorySeverityFilter,
  buildMemoryDatetreeCapture,
  filterMemoryCards,
  summarizeMemoryCards,
} from "./model";

const TODAY_ISO = new Date().toISOString().slice(0, 10);

const INITIAL_MEMORY: OrgizeMemoryResponseDto = {
  schemaVersion: 1,
  stats: { totalRecords: 3, currentRecords: 3, actionCards: 2, suppressedCards: 1, infoCards: 0, evidence: 4 },
  cards: [
    {
      anchor: "mem-arch-react19",
      title: "Prefer React 19 + @tao3k/marlin-panel-sdk/react for interactive desktop workflows",
      effectiveTags: ["architecture", "frontend", "decision"],
      decision: {
        code: "PROMOTE_DECISION",
        kind: "current",
        severity: "action",
        title: "Durable Architecture Decision",
        nextAction: "Promote into workspace Org datetree and link plugin guide",
      },
      authority: [{ kind: "userConfirmed", label: "Explicit User Choice", message: "Confirmed in desktop & mobile runtime review" }],
      evidence: [
        { kind: { code: "bench", label: "Perf Gate", family: "metric" }, value: "React 19 minified bundle stays well under 384 KiB ceiling" },
        { kind: { code: "policy", label: "App Store", family: "security" }, value: "Mobile keeps zero-code declarative OTA + curated built-in UI" },
      ],
    },
    {
      anchor: "mem-orgize-authority",
      title: "Never re-implement Org syntax parsing inside UI plugins; consume orgize DTOs",
      effectiveTags: ["orgize", "protocol"],
      decision: {
        code: "PROMOTE_RULE",
        kind: "current",
        severity: "action",
        title: "Single Parser Authority",
        nextAction: "Attach to AGENT.md contract card for all planner plugins",
      },
      authority: [{ kind: "invariant", label: "Kernel Authority", message: "tao3k/orgize & orgize-wasm own all AST & DTO projections" }],
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
      authority: [{ kind: "conflict", label: "Superseded", message: "Replaced by native React 19 + zero-dep core SDK split" }],
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

export function AIMemoryInboxApp() {
  const memProj = useMarlinProjection("org.memory", INITIAL_MEMORY, parseMemoryDTO);
  const acceptAct = useMarlinAction("org.memory.accept");
  const captureAct = useMarlinAction("org.capture.plan");
  const supersedeAct = useMarlinAction("org.memory.supersede");
  const dismissAct = useMarlinAction("org.memory.dismiss");

  const [severity, setSeverity] = useState<MemorySeverityFilter>("all");
  const [query, setQuery] = useState("");
  const [receiptMsg, setReceiptMsg] = useState("Ready to triage orgize Agent Memory cards.");

  const summary = useMemo(() => summarizeMemoryCards(memProj.data.cards), [memProj.data.cards]);
  const visible = useMemo(
    () => filterMemoryCards(memProj.data.cards, severity, query),
    [memProj.data.cards, severity, query],
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
      setReceiptMsg(`Accepted "${card.title}" into Org Datetree (receipt: ${r2.receiptId || "admitted"})`);
    }
  }

  async function handleSupersede(card: OrgizeAgentMemoryCardDto) {
    const rcpt = await supersedeAct.submit({ anchor: card.anchor, title: card.title });
    if (rcpt.admitted) {
      removeCard(card);
      setReceiptMsg(`Marked "${card.title}" superseded (receipt: ${rcpt.receiptId || "admitted"})`);
    }
  }

  async function handleDismiss(card: OrgizeAgentMemoryCardDto) {
    const rcpt = await dismissAct.submit({ anchor: card.anchor, title: card.title });
    if (rcpt.admitted) {
      removeCard(card);
      setReceiptMsg(`Dismissed "${card.title}"`);
    }
  }

  return (
    <div>
      <header>
        <div>
          <strong>AI Knowledge &amp; Memory Triage Inbox</strong>
          <div className="subtitle">React 19 · Consumes tao3k/orgize OrgizeMemoryResponseDto</div>
        </div>
        <div className="subtitle">{summary.evidenceCount} verified evidence items</div>
      </header>

      <div className="stats">
        <div className="stat"><strong>{summary.total}</strong><span className="subtitle">Pending Cards</span></div>
        <div className="stat"><strong>{summary.actionCount}</strong><span className="subtitle">Action Required</span></div>
        <div className="stat"><strong>{summary.suppressedCount}</strong><span className="subtitle">Suppressed</span></div>
        <div className="stat"><strong>{summary.evidenceCount}</strong><span className="subtitle">Evidence Citations</span></div>
      </div>

      <div className="controls">
        {(["all", "action", "suppressed", "info"] as MemorySeverityFilter[]).map((sev) => (
          <button
            key={sev}
            type="button"
            className={severity === sev ? "active" : ""}
            onClick={() => setSeverity(sev)}
          >
            {sev.toUpperCase()}
          </button>
        ))}
        <input
          type="text"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Filter by title, tag, or nextAction..."
        />
      </div>

      {visible.map((card, i) => (
        <article key={card.anchor ?? `${card.title}-${i}`} className={`card ${card.decision.severity}`}>
          <div>
            <span className="badge">{card.decision.severity.toUpperCase()}</span>
            <strong>{card.title}</strong>
          </div>
          <div className="subtitle" style={{ marginTop: 4 }}>
            Next: {card.decision.nextAction} · :{card.effectiveTags.join(":")}:
          </div>
          {card.authority.map((a, idx) => (
            <div className="subtitle" key={idx}>Authority [{a.label}]: {a.message}</div>
          ))}
          {card.evidence.map((ev, idx) => (
            <div className="evidence" key={idx}>[{ev.kind.label}] {ev.value}</div>
          ))}
          <div className="actions">
            <button className="accept" type="button" onClick={() => void handleAccept(card)}>
              Accept -&gt; Datetree
            </button>
            <button className="supersede" type="button" onClick={() => void handleSupersede(card)}>
              Supersede
            </button>
            <button type="button" onClick={() => void handleDismiss(card)}>
              Dismiss
            </button>
          </div>
        </article>
      ))}

      <div className="receipt">{receiptMsg}</div>
    </div>
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
