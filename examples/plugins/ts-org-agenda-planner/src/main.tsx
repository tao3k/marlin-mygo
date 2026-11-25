// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { memo, useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
import { createRoot } from "react-dom/client";
import {
  createOrgizeWorkerClient,
  type OrgizeWorkerPort,
} from "@tao3k/marlin-panel-sdk";
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

const VISIBLE_CARD_WINDOW = 40;

interface CaptureSubmitPayload {
  kind: OrgizeAgentCaptureKindDto;
  targetKind: OrgizeAgentCaptureTargetKindDto;
  title: string;
  tagsCsv: string;
  timeWindow: string;
}

const CaptureFormBar = memo(function CaptureFormBar({
  pending,
  onSubmitCapture,
}: {
  pending: boolean;
  onSubmitCapture: (payload: CaptureSubmitPayload) => Promise<boolean>;
}) {
  const [kind, setKind] = useState<OrgizeAgentCaptureKindDto>("task");
  const [targetKind, setTargetKind] = useState<OrgizeAgentCaptureTargetKindDto>("inbox");
  const [titleInput, setTitleInput] = useState("");
  const [tagsInput, setTagsInput] = useState("");
  const [winInput, setWinInput] = useState("");

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!titleInput.trim()) return;
    const ok = await onSubmitCapture({
      kind,
      targetKind,
      title: titleInput,
      tagsCsv: tagsInput,
      timeWindow: winInput,
    });
    if (ok) {
      setTitleInput("");
      setTagsInput("");
      setWinInput("");
    }
  }

  return (
    <form className="capture-bar" onSubmit={(e) => void handleSubmit(e)}>
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
      <button className="primary" type="submit" disabled={pending}>
        Capture
      </button>
    </form>
  );
});

const AgendaCardRow = memo(function AgendaCardRow({
  card,
  onClockIn,
  onToggleDone,
}: {
  card: OrgizeAgendaViewCardDto;
  onClockIn: (card: OrgizeAgendaViewCardDto) => void;
  onToggleDone: (card: OrgizeAgendaViewCardDto) => void;
}) {
  const isDone = card.todoState === "done";
  const timeStr = card.time ? ` · ${card.time}${card.endTime ? `-${card.endTime}` : ""}` : "";
  const tagStr = card.effectiveTags.length > 0 ? ` · :${card.effectiveTags.join(":")}:` : "";
  return (
    <div className={`item ${card.urgency.total >= 80 ? "urgent" : ""} ${isDone ? "done" : ""}`}>
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
        <button type="button" onClick={() => onClockIn(card)}>
          Clock In
        </button>
        <button type="button" onClick={() => onToggleDone(card)}>
          {isDone ? "Reopen" : "Done"}
        </button>
      </div>
    </div>
  );
});

const FocusClockCard = memo(function FocusClockCard({
  alarm,
  onToggleStart,
  onStartBreak,
  onReset,
}: {
  alarm: FocusAlarmState;
  onToggleStart: () => void;
  onStartBreak: () => void;
  onReset: () => void;
}) {
  const pct =
    alarm.durationSec > 0
      ? Math.min(100, Math.round(((alarm.durationSec - alarm.remainingSec) * 100) / alarm.durationSec))
      : 0;
  return (
    <div className="card">
      <h3>Focus Alarm &amp; Org Clock</h3>
      <div className="subtitle">
        {alarm.label} · {alarm.completedBlocks} completed
      </div>
      <div className="clock-display">{formatClock(alarm.remainingSec)}</div>
      <div className="progress-track">
        <div className="progress-fill" style={{ width: `${pct}%` }} />
      </div>
      <div className="pills">
        <button className="primary" type="button" onClick={onToggleStart}>
          {alarm.status === "running" ? "Pause" : "Start"}
        </button>
        <button type="button" onClick={onStartBreak}>
          5m Break
        </button>
        <button type="button" onClick={onReset}>
          Reset
        </button>
      </div>
      <div className="subtitle">Status: {alarm.status}</div>
    </div>
  );
});

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
  const [visibleLimit, setVisibleLimit] = useState(VISIBLE_CARD_WINDOW);
  const [scratchOpen, setScratchOpen] = useState(false);
  const [scratchInput, setScratchInput] = useState("");
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
  const setAgendaLocal = agendaProj.setLocal;
  const visibleCards = useMemo(() => selectAgendaCards(cards, TODAY_ISO, filter), [cards, filter]);
  const renderedCards = useMemo(
    () => (visibleCards.length > visibleLimit ? visibleCards.slice(0, visibleLimit) : visibleCards),
    [visibleCards, visibleLimit],
  );
  const conflicts = useMemo(() => detectTimeBlockOverlaps(cards, TODAY_ISO), [cards]);
  const clockSummary = useMemo(() => summarizeClockRollup(clockProj.data), [clockProj.data]);
  const todayBlocks = useMemo(
    () =>
      cards
        .filter((c) => c.todoState !== "done" && c.displayDate === TODAY_ISO && parseHHMM(c.time) !== undefined)
        .sort((a, b) => (parseHHMM(a.time) ?? 0) - (parseHHMM(b.time) ?? 0)),
    [cards],
  );

  const submitCapture = captureAct.submit;
  const submitToggle = toggleAct.submit;
  const submitClockIn = clockInAct.submit;
  const submitClockOut = clockOutAct.submit;
  const submitAlarm = alarmAct.submit;

  useEffect(() => {
    if (alarm.status !== "running") return;
    const timer = setInterval(() => {
      setAlarm((prev) => {
        const next = tickFocusAlarm(prev, 1);
        if (prev.status === "running" && next.status === "ringing") {
          void submitClockOut({ title: next.cardTitle, completedBlocks: next.completedBlocks });
          void submitAlarm({ label: next.label, completedBlocks: next.completedBlocks });
          setReceiptMsg(`Focus alarm ringing: clocked out "${next.cardTitle}"!`);
        }
        return next;
      });
    }, 1000);
    return () => clearInterval(timer);
  }, [alarm.status, submitAlarm, submitClockOut]);

  const handleCaptureSubmit = useCallback(
    async (payload: CaptureSubmitPayload): Promise<boolean> => {
      const req = buildCaptureRequest(payload, TODAY_ISO);
      const rcpt = await submitCapture(req);
      if (rcpt.admitted) {
        const [startTime, endTime] = payload.timeWindow.split("-").map((s) => s.trim());
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
        setAgendaLocal((prev) => ({
          ...prev,
          totalCandidates: prev.cards.length + 1,
          cards: [created, ...prev.cards],
        }));
        setReceiptMsg(`Captured "${req.title}" via orgize capturePlan (receipt: ${rcpt.receiptId || "admitted"})`);
        return true;
      }
      setReceiptMsg(`Capture denied: ${rcpt.denial ?? "rejected"}`);
      return false;
    },
    [setAgendaLocal, submitCapture],
  );

  const handleToggleDone = useCallback(
    (card: OrgizeAgendaViewCardDto) => {
      const nextDone = card.todoState !== "done";
      void submitToggle({
        title: card.title,
        source: card.source,
        nextState: nextDone ? "done" : "todo",
      }).then((rcpt) => {
        if (rcpt.admitted) {
          setAgendaLocal((prev) => ({
            ...prev,
            cards: prev.cards.map((c) =>
              c === card ? { ...c, todo: nextDone ? "DONE" : "TODO", todoState: nextDone ? "done" : "todo" } : c,
            ),
          }));
          setReceiptMsg(`Marked "${card.title}" -> ${nextDone ? "DONE" : "TODO"}`);
        }
      });
    },
    [setAgendaLocal, submitToggle],
  );

  const handleClockIn = useCallback(
    (card: OrgizeAgendaViewCardDto) => {
      void submitClockIn({ title: card.title, source: card.source, durationSec: 25 * 60 }).then((rcpt) => {
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
      });
    },
    [submitClockIn],
  );

  const handleToggleAlarmStart = useCallback(() => {
    setAlarm((prev) => ({
      ...prev,
      status: prev.status === "running" ? "paused" : "running",
    }));
  }, []);

  const handleStartBreak = useCallback(() => {
    setAlarm((prev) => ({
      ...prev,
      label: "Short Break",
      durationSec: 5 * 60,
      remainingSec: 5 * 60,
      status: "running",
    }));
  }, []);

  const handleResetAlarm = useCallback(() => {
    setAlarm((prev) => ({
      ...prev,
      label: "Deep Work Block",
      durationSec: 25 * 60,
      remainingSec: 25 * 60,
      status: "idle",
    }));
  }, []);

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

  async function handleApplyScratchpad() {
    const raw = scratchInput.trim();
    if (!raw) return;
    const g = globalThis as unknown as { __MARLIN_ORGIZE_WORKER__?: OrgizeWorkerPort };
    if (g.__MARLIN_ORGIZE_WORKER__) {
      const workerClient = createOrgizeWorkerClient(g.__MARLIN_ORGIZE_WORKER__, "agenda-scratch");
      try {
        const parsed = await workerClient.parseAgendaView<OrgizeAgendaViewResponseDto>(raw, TODAY_ISO);
        if (parsed && Array.isArray(parsed.cards)) {
          agendaProj.setLocal(parsed);
          setReceiptMsg(`Parsed ${parsed.cards.length} cards offline via tao3k/orgize-wasm Worker`);
          setScratchOpen(false);
        }
      } catch (err) {
        setReceiptMsg(`orgize-wasm parse error: ${err instanceof Error ? err.message : String(err)}`);
      } finally {
        workerClient.dispose();
      }
      return;
    }
    try {
      const dto = parseAgendaDTO(JSON.parse(raw));
      if (dto) {
        agendaProj.setLocal(dto);
        setReceiptMsg(`Loaded ${dto.cards.length} cards from pasted orgize DTO snapshot`);
        setScratchOpen(false);
        return;
      }
    } catch {
      // Fall through with guidance when no local orgize-wasm Worker is mounted.
    }
    setReceiptMsg("Attach tao3k/orgize-wasm Worker (__MARLIN_ORGIZE_WORKER__) for raw .org parsing, or paste an OrgizeAgendaViewResponseDto JSON.");
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
          <button type="button" onClick={() => setScratchOpen((o) => !o)}>
            {scratchOpen ? "Close Scratchpad" : "Orgize Scratchpad"}
          </button>
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

      {scratchOpen && (
        <div className="card">
          <h3>Offline tao3k/orgize-wasm Scratchpad (Zero JS Org Parsing)</h3>
          <textarea
            rows={4}
            style={{ width: "100%", background: "var(--bg)", color: "var(--fg)", border: "1px solid var(--muted)", borderRadius: 6, padding: 8, marginBottom: 8 }}
            value={scratchInput}
            onChange={(e) => setScratchInput(e.target.value)}
            placeholder="Paste .org text (when tao3k/orgize-wasm Worker is mounted) or an OrgizeAgendaViewResponseDto JSON snapshot..."
          />
          <button className="primary" type="button" onClick={() => void handleApplyScratchpad()}>
            Project via orgize
          </button>
        </div>
      )}

      <CaptureFormBar pending={captureAct.pending} onSubmitCapture={handleCaptureSubmit} />

      <div className="grid">
        <section>
          <div className="card">
            <h3>Orgize Agenda Queue ({visibleCards.length})</h3>
            <div className="segmented">
              {FILTER_DEFS.map((d) => (
                <button
                  key={d.key}
                  type="button"
                  className={filter === d.key ? "active" : ""}
                  onClick={() => {
                    setFilter(d.key);
                    setVisibleLimit(VISIBLE_CARD_WINDOW);
                  }}
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

            {renderedCards.map((card, idx) => (
              <AgendaCardRow
                key={`${card.title}-${idx}`}
                card={card}
                onClockIn={handleClockIn}
                onToggleDone={handleToggleDone}
              />
            ))}
            {visibleCards.length > renderedCards.length && (
              <div className="pills" style={{ marginTop: 10, marginBottom: 0 }}>
                <button
                  type="button"
                  onClick={() => setVisibleLimit((n) => n + VISIBLE_CARD_WINDOW)}
                >
                  Show more ({visibleCards.length - renderedCards.length} remaining)
                </button>
              </div>
            )}
          </div>
        </section>

        <aside>
          <FocusClockCard
            alarm={alarm}
            onToggleStart={handleToggleAlarmStart}
            onStartBreak={handleStartBreak}
            onReset={handleResetAlarm}
          />

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

const rootEl = typeof document !== "undefined" ? document.getElementById("root") : null;
if (rootEl) {
  createRoot(rootEl).render(
    <MarlinPanelProvider pluginId="tao3k.org-agenda-planner">
      <OrgAgendaPlannerApp />
    </MarlinPanelProvider>,
  );
}
