// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { useEffect, useMemo, useState, type FormEvent } from "react";
import { createRoot } from "react-dom/client";
import {
  MarlinPanelProvider,
  useMarlinAction,
  useMarlinProjection,
} from "@tao3k/marlin-panel-sdk/react";
import {
  type AgendaFilter,
  type FocusAlarmState,
  buildCaptureRequest,
  detectTimeBlockOverlaps,
  formatClock,
  parseHHMM,
  selectAgendaCards,
  summarizeClockRollup,
  tickFocusAlarm,
} from "./model";
import type {
  OrgizeAgentCaptureKindDto,
  OrgizeAgentCaptureTargetKindDto,
  OrgizeAgendaViewCardDto,
  OrgizeAgendaViewResponseDto,
  OrgizeClockRollupResponseDto,
} from "./orgize_dto";

const TODAY_ISO = new Date().toISOString().slice(0, 10);
const EMPTY_POS = { offset: 0, line: 1, column: 1 };
const SAMPLE_SOURCE = { start: EMPTY_POS, end: EMPTY_POS };

const INITIAL_AGENDA: OrgizeAgendaViewResponseDto = {
  schemaVersion: 1,
  totalCandidates: 3,
  cards: [
    {
      source: SAMPLE_SOURCE,
      sortedPosition: 0,
      kind: "scheduled",
      displayDate: TODAY_ISO,
      targetDate: TODAY_ISO,
      time: "09:30",
      endTime: "10:30",
      title: "Morning architecture review & PR triage",
      category: "engineering",
      todo: "NEXT",
      todoState: "todo",
      effectiveTags: ["deep_work", "review"],
      blockers: [],
      urgency: { total: 92, ingredients: [{ kind: "priority", score: 40, message: "Priority A" }] },
      receipts: [{ kind: "scheduled", message: "Scheduled for today" }],
    },
    {
      source: SAMPLE_SOURCE,
      sortedPosition: 1,
      kind: "scheduled",
      displayDate: TODAY_ISO,
      targetDate: TODAY_ISO,
      time: "14:00",
      endTime: "15:00",
      title: "Draft weekly planning & milestone review",
      category: "planning",
      todo: "TODO",
      todoState: "todo",
      effectiveTags: ["planning"],
      blockers: [],
      urgency: { total: 68, ingredients: [{ kind: "scheduled", score: 25, message: "Scheduled today" }] },
      receipts: [],
    },
    {
      source: SAMPLE_SOURCE,
      sortedPosition: 2,
      kind: "deadline",
      displayDate: "2026-11-24",
      targetDate: "2026-11-24",
      title: "Verify mobile declarative plugin sync checklist",
      category: "mobile",
      todo: "TODO",
      todoState: "todo",
      effectiveTags: ["mobile", "release"],
      blockers: [],
      urgency: { total: 105, ingredients: [{ kind: "deadline", score: 60, message: "Overdue deadline" }] },
      receipts: [],
    },
  ],
};

const INITIAL_CLOCK: OrgizeClockRollupResponseDto = {
  schemaVersion: 1,
  records: [
    {
      source: SAMPLE_SOURCE,
      outlinePath: ["Today", "Morning architecture review & PR triage"],
      level: 2,
      title: "Morning architecture review & PR triage",
      localClock: { entries: 1, closedEntries: 1, runningEntries: 0, unparsedEntries: 0, totalSeconds: 45 * 60 },
      subtreeClock: { entries: 1, closedEntries: 1, runningEntries: 0, unparsedEntries: 0, totalSeconds: 45 * 60 },
      effort: { subtreeTotalSeconds: 45 * 60, deltaSeconds: -15 * 60, status: "underEffort" },
    },
  ],
};

function parseAgendaDTO(raw: unknown): OrgizeAgendaViewResponseDto | undefined {
  const dto = raw as Partial<OrgizeAgendaViewResponseDto> | null;
  if (dto && Array.isArray(dto.cards)) {
    return { schemaVersion: 1, totalCandidates: dto.totalCandidates ?? dto.cards.length, cards: dto.cards };
  }
  return undefined;
}

function parseClockDTO(raw: unknown): OrgizeClockRollupResponseDto | undefined {
  const dto = raw as Partial<OrgizeClockRollupResponseDto> | null;
  if (dto && Array.isArray(dto.records)) {
    return { schemaVersion: 1, records: dto.records };
  }
  return undefined;
}

const FILTER_DEFS: Array<{ key: AgendaFilter; label: string }> = [
  { key: "all", label: "All" },
  { key: "today", label: "Today" },
  { key: "overdue", label: "Overdue" },
  { key: "urgent", label: "Urgent (80+)" },
  { key: "blocked", label: "Blocked" },
  { key: "done", label: "Done" },
];

export function OrgAgendaPlannerApp() {
  const agendaProj = useMarlinProjection("org.agenda", INITIAL_AGENDA, parseAgendaDTO);
  const clockProj = useMarlinProjection("org.clock", INITIAL_CLOCK, parseClockDTO);
  const sessionProj = useMarlinProjection<Record<string, unknown>>("session.summary", {});

  const captureAct = useMarlinAction("org.capture.plan");
  const toggleAct = useMarlinAction("org.todo.toggle");
  const clockInAct = useMarlinAction("org.clock.in");
  const clockOutAct = useMarlinAction("org.clock.out");
  const alarmAct = useMarlinAction("agenda.alarm.trigger");
  const aiPlanAct = useMarlinAction("agent.workflow.plan-day");

  const [filter, setFilter] = useState<AgendaFilter>("all");
  const [kind, setKind] = useState<OrgizeAgentCaptureKindDto>("task");
  const [targetKind, setTargetKind] = useState<OrgizeAgentCaptureTargetKindDto>("inbox");
  const [titleInput, setTitleInput] = useState("");
  const [tagsInput, setTagsInput] = useState("");
  const [winInput, setWinInput] = useState("");
  const [receiptMsg, setReceiptMsg] = useState("React 19 + tao3k/orgize DTOs ready.");

  const [alarm, setAlarm] = useState<FocusAlarmState>({
    label: "Deep Work Block",
    cardTitle: INITIAL_AGENDA.cards[0].title,
    durationSec: 25 * 60,
    remainingSec: 25 * 60,
    status: "idle",
    completedBlocks: 0,
  });

  const cards = agendaProj.data.cards;
  const visibleCards = useMemo(() => selectAgendaCards(cards, TODAY_ISO, filter), [cards, filter]);
  const conflicts = useMemo(() => detectTimeBlockOverlaps(cards, TODAY_ISO), [cards]);
  const clockSummary = useMemo(() => summarizeClockRollup(clockProj.data), [clockProj.data]);
  const todayBlocks = useMemo(
    () =>
      cards
        .filter((c) => c.todoState !== "done" && c.displayDate === TODAY_ISO && parseHHMM(c.time) !== undefined)
        .sort((a, b) => (parseHHMM(a.time) ?? 0) - (parseHHMM(b.time) ?? 0)),
    [cards],
  );

  useEffect(() => {
    if (alarm.status !== "running") return;
    const timer = setInterval(() => {
      setAlarm((prev) => {
        const next = tickFocusAlarm(prev, 1);
        if (prev.status === "running" && next.status === "ringing") {
          void clockOutAct.submit({ title: next.cardTitle, completedBlocks: next.completedBlocks });
          void alarmAct.submit({ label: next.label, completedBlocks: next.completedBlocks });
          setReceiptMsg(`Focus alarm ringing: clocked out "${next.cardTitle}"!`);
        }
        return next;
      });
    }, 1000);
    return () => clearInterval(timer);
  }, [alarm.status, alarmAct, clockOutAct]);

  async function handleCapture(e: FormEvent) {
    e.preventDefault();
    if (!titleInput.trim()) return;
    const req = buildCaptureRequest(
      { kind, targetKind, title: titleInput, tagsCsv: tagsInput, timeWindow: winInput },
      TODAY_ISO,
    );
    const rcpt = await captureAct.submit(req);
    if (rcpt.admitted) {
      const [startTime, endTime] = winInput.split("-").map((s) => s.trim());
      const created: OrgizeAgendaViewCardDto = {
        source: SAMPLE_SOURCE,
        sortedPosition: 0,
        kind: req.kind,
        displayDate: TODAY_ISO,
        targetDate: TODAY_ISO,
        time: parseHHMM(startTime) !== undefined ? startTime : null,
        endTime: parseHHMM(endTime) !== undefined ? endTime : null,
        title: req.title,
        todo: req.kind === "task" ? "TODO" : null,
        todoState: req.kind === "task" ? "todo" : null,
        effectiveTags: req.tags ?? [],
        blockers: [],
        urgency: { total: 78, ingredients: [] },
        receipts: [{ kind: "capturePlan", message: `Target: ${req.target?.kind ?? "inbox"}` }],
      };
      agendaProj.setLocal((prev) => ({
        ...prev,
        totalCandidates: prev.cards.length + 1,
        cards: [created, ...prev.cards],
      }));
      setTitleInput("");
      setTagsInput("");
      setWinInput("");
      setReceiptMsg(`Captured "${req.title}" via orgize capturePlan (receipt: ${rcpt.receiptId || "admitted"})`);
    } else {
      setReceiptMsg(`Capture denied: ${rcpt.denial ?? "rejected"}`);
    }
  }

  async function handleToggleDone(card: OrgizeAgendaViewCardDto) {
    const nextDone = card.todoState !== "done";
    const rcpt = await toggleAct.submit({
      title: card.title,
      source: card.source,
      nextState: nextDone ? "done" : "todo",
    });
    if (rcpt.admitted) {
      agendaProj.setLocal((prev) => ({
        ...prev,
        cards: prev.cards.map((c) =>
          c === card ? { ...c, todo: nextDone ? "DONE" : "TODO", todoState: nextDone ? "done" : "todo" } : c,
        ),
      }));
      setReceiptMsg(`Marked "${card.title}" -> ${nextDone ? "DONE" : "TODO"}`);
    }
  }

  async function handleClockIn(card: OrgizeAgendaViewCardDto) {
    const rcpt = await clockInAct.submit({ title: card.title, source: card.source, durationSec: 25 * 60 });
    if (rcpt.admitted) {
      setAlarm((prev) => ({
        ...prev,
        cardTitle: card.title,
        label: `Clocked: ${card.title}`,
        durationSec: 25 * 60,
        remainingSec: 25 * 60,
        status: "running",
      }));
      setReceiptMsg(`Clocked in on "${card.title}" (synced to GPU Focus Clock reactor)`);
    }
  }

  async function handleAIPlanDay() {
    const rcpt = await aiPlanAct.submit({
      dateISO: TODAY_ISO,
      openCount: cards.filter((c) => c.todoState !== "done").length,
      conflictCount: conflicts.length,
    });
    if (rcpt.admitted) {
      setReceiptMsg(`AI Daily Plan workflow admitted (receipt: ${rcpt.receiptId || "admitted"})`);
    }
  }

  const sessionLabel = sessionProj.data.sessionId
    ? `React 19 · orgize DTOs · session ${String(sessionProj.data.sessionId)} · turn #${String(sessionProj.data.turnOrdinal ?? 0)}`
    : "React 19 · Powered by tao3k/orgize DTOs (zero JS Org parsing)";

  return (
    <div>
      <header>
        <div>
          <strong>Org Agenda &amp; AI Daily Planner</strong>
          <div className="subtitle">{sessionLabel}</div>
        </div>
        <div className="pills" style={{ marginBottom: 0 }}>
          <span className="subtitle">
            Clocked today: {clockSummary.totalMinutes}m
            {clockSummary.runningCount > 0 ? ` · active: ${clockSummary.runningTitle}` : ""}
          </span>
          <button
            className="ok"
            type="button"
            disabled={aiPlanAct.pending}
            onClick={() => void handleAIPlanDay()}
          >
            {aiPlanAct.pending ? "Planning..." : "AI Auto-Plan Today"}
          </button>
        </div>
      </header>

      <form className="capture-bar" onSubmit={(e) => void handleCapture(e)}>
        <select value={kind} onChange={(e) => setKind(e.target.value as OrgizeAgentCaptureKindDto)} aria-label="Capture Kind">
          <option value="task">Task</option>
          <option value="note">Note</option>
          <option value="idea">Idea</option>
          <option value="decision">Decision</option>
        </select>
        <select value={targetKind} onChange={(e) => setTargetKind(e.target.value as OrgizeAgentCaptureTargetKindDto)} aria-label="Capture Target">
          <option value="inbox">Inbox</option>
          <option value="datetree">Datetree</option>
        </select>
        <input
          type="text"
          value={titleInput}
          onChange={(e) => setTitleInput(e.target.value)}
          placeholder="Capture title (routed to orgize capturePlan)"
        />
        <input
          type="text"
          value={tagsInput}
          onChange={(e) => setTagsInput(e.target.value)}
          placeholder="tags: deep,arch"
        />
        <input
          type="text"
          value={winInput}
          onChange={(e) => setWinInput(e.target.value)}
          placeholder="14:00-15:00"
        />
        <button className="primary" type="submit" disabled={captureAct.pending}>
          Capture
        </button>
      </form>

      <div className="grid">
        <section>
          <div className="card">
            <h3>Orgize Agenda Queue ({visibleCards.length})</h3>
            <div className="pills">
              {FILTER_DEFS.map((d) => (
                <button
                  key={d.key}
                  type="button"
                  className={filter === d.key ? "active" : ""}
                  onClick={() => setFilter(d.key)}
                >
                  {d.key === "all" ? `All (${cards.length})` : d.label}
                </button>
              ))}
            </div>

            {conflicts.map((cf, idx) => (
              <div className="conflict" key={`${cf.window}-${idx}`}>
                {cf.summary}
              </div>
            ))}

            {visibleCards.map((card, idx) => {
              const isDone = card.todoState === "done";
              const timeStr = card.time ? ` · ${card.time}${card.endTime ? `-${card.endTime}` : ""}` : "";
              const tagStr = card.effectiveTags.length > 0 ? ` · :${card.effectiveTags.join(":")}:` : "";
              return (
                <div
                  key={`${card.title}-${idx}`}
                  className={`item ${card.urgency.total >= 80 ? "urgent" : ""} ${isDone ? "done" : ""}`}
                >
                  <div>
                    <div>
                      {card.todo ? `[${card.todo}] ` : ""}
                      {card.title}
                      <span className="badge">U:{card.urgency.total}</span>
                      {card.blockers.length > 0 && (
                        <span className="badge blocked">Blocked ({card.blockers.length})</span>
                      )}
                    </div>
                    <div className="meta">
                      {card.displayDate}
                      {timeStr}
                      {tagStr}
                    </div>
                  </div>
                  <div className="pills" style={{ marginBottom: 0 }}>
                    <button type="button" onClick={() => void handleClockIn(card)}>
                      Clock In
                    </button>
                    <button type="button" onClick={() => void handleToggleDone(card)}>
                      {isDone ? "Reopen" : "Done"}
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
        </section>

        <aside>
          <div className="card">
            <h3>Focus Alarm &amp; Org Clock</h3>
            <div className="subtitle">
              {alarm.label} · {alarm.completedBlocks} completed
            </div>
            <div className="clock-display">{formatClock(alarm.remainingSec)}</div>
            <div className="pills">
              <button
                className="primary"
                type="button"
                onClick={() =>
                  setAlarm((prev) => ({
                    ...prev,
                    status: prev.status === "running" ? "paused" : "running",
                  }))
                }
              >
                {alarm.status === "running" ? "Pause" : "Start"}
              </button>
              <button
                type="button"
                onClick={() =>
                  setAlarm((prev) => ({
                    ...prev,
                    label: "Short Break",
                    durationSec: 5 * 60,
                    remainingSec: 5 * 60,
                    status: "running",
                  }))
                }
              >
                5m Break
              </button>
              <button
                type="button"
                onClick={() =>
                  setAlarm((prev) => ({
                    ...prev,
                    label: "Deep Work Block",
                    durationSec: 25 * 60,
                    remainingSec: 25 * 60,
                    status: "idle",
                  }))
                }
              >
                Reset
              </button>
            </div>
            <div className="subtitle">Status: {alarm.status}</div>
          </div>

          <div className="card">
            <h3>Today&apos;s Time Blocks</h3>
            {todayBlocks.map((b, i) => (
              <div className="item" key={`${b.title}-${i}`}>
                <span>
                  {b.time}
                  {b.endTime ? `-${b.endTime}` : ""} · {b.title}
                </span>
              </div>
            ))}
          </div>

          <div className="receipt">{receiptMsg}</div>
        </aside>
      </div>
    </div>
  );
}

const rootEl = document.getElementById("root");
if (rootEl) {
  createRoot(rootEl).render(
    <MarlinPanelProvider pluginId="tao3k.org-agenda-planner">
      <OrgAgendaPlannerApp />
    </MarlinPanelProvider>,
  );
}
