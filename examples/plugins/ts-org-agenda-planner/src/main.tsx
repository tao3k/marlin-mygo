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
  PanelHeader,
  PanelShell,
  SectionCard,
  SegmentedControl,
  StatusBadge,
  ToastBanner,
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

const KIND_OPTIONS = [
  { key: "task", label: "Task" },
  { key: "note", label: "Note" },
  { key: "idea", label: "Idea" },
  { key: "decision", label: "Decision" },
] as const;

const TARGET_OPTIONS = [
  { key: "inbox", label: "Inbox" },
  { key: "datetree", label: "Datetree" },
] as const;

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
    <form className="m-composer" onSubmit={(e) => void handleSubmit(e)}>
      <div className="m-composer-top">
        <input
          className="m-input"
          type="text"
          value={titleInput}
          onChange={(e) => setTitleInput(e.target.value)}
          placeholder="Add a task, note, or scheduled block..."
        />
        <button className="m-btn m-btn-primary" type="submit" disabled={pending}>
          Capture
        </button>
      </div>
      <div className="m-composer-meta">
        <SegmentedControl
          options={KIND_OPTIONS}
          value={kind}
          onChange={(v) => setKind(v as OrgizeAgentCaptureKindDto)}
        />
        <SegmentedControl
          options={TARGET_OPTIONS}
          value={targetKind}
          onChange={(v) => setTargetKind(v as OrgizeAgentCaptureTargetKindDto)}
        />
        <input
          className="m-input m-input-compact"
          type="text"
          value={tagsInput}
          onChange={(e) => setTagsInput(e.target.value)}
          placeholder="tags: deep, arch"
        />
        <input
          className="m-input m-input-compact m-mono"
          type="text"
          value={winInput}
          onChange={(e) => setWinInput(e.target.value)}
          placeholder="14:00-15:00"
        />
      </div>
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
  const urgent = card.urgency.total >= 80;
  return (
    <div className="m-row" style={isDone ? { opacity: 0.55 } : undefined}>
      <div style={{ minWidth: 0 }}>
        <div className="m-row-gap" style={{ gap: 6 }}>
          {card.todo && (
            <StatusBadge tone={isDone ? "success" : urgent ? "accent" : "info"}>
              {card.todo}
            </StatusBadge>
          )}
          <span style={{ fontWeight: 520, textDecoration: isDone ? "line-through" : "none" }}>
            {card.title}
          </span>
          {card.blockers.length > 0 && (
            <StatusBadge tone="danger">Blocked ({card.blockers.length})</StatusBadge>
          )}
        </div>
        <div className="m-caption m-mono">
          {card.displayDate}
          {timeStr}
          {tagStr} · U:{card.urgency.total}
        </div>
      </div>
      <div className="m-row-gap" style={{ flexShrink: 0 }}>
        <button className="m-btn m-btn-ghost" type="button" onClick={() => onClockIn(card)}>
          Clock In
        </button>
        <button className="m-btn" type="button" onClick={() => onToggleDone(card)}>
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
    <SectionCard
      title="Focus Timer"
      right={<StatusBadge tone={alarm.status === "running" ? "success" : "muted"}>{alarm.status}</StatusBadge>}
    >
      <div className="m-caption">
        {alarm.label} · {alarm.completedBlocks} completed
      </div>
      <div className="m-clock-face">{formatClock(alarm.remainingSec)}</div>
      <div
        style={{
          height: 5,
          borderRadius: 999,
          background: "var(--marlin-surface-recessed)",
          overflow: "hidden",
          marginBottom: 12,
        }}
      >
        <div
          style={{
            width: `${pct}%`,
            height: "100%",
            background: "var(--marlin-accent)",
            transition: "width 250ms linear",
          }}
        />
      </div>
      <div className="m-row-gap">
        <button className="m-btn m-btn-primary" type="button" onClick={onToggleStart}>
          {alarm.status === "running" ? "Pause" : "Start"}
        </button>
        <button className="m-btn" type="button" onClick={onStartBreak}>
          5m Break
        </button>
        <button className="m-btn m-btn-ghost" type="button" onClick={onReset}>
          Reset
        </button>
      </div>
    </SectionCard>
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
    ? `Session ${String(sessionProj.data.sessionId)} · turn #${String(sessionProj.data.turnOrdinal ?? 0)} · ${clockSummary.totalMinutes}m clocked`
    : `${clockSummary.totalMinutes}m clocked today${clockSummary.runningCount > 0 ? ` · active: ${clockSummary.runningTitle}` : ""}`;

  const handleFilterChange = useCallback((next: AgendaFilter) => {
    setFilter(next);
    setVisibleLimit(VISIBLE_CARD_WINDOW);
  }, []);

  const handleDismissReceipt = useCallback(() => setReceiptMsg(""), []);

  const filterOptions = useMemo(
    () =>
      FILTER_DEFS.map((d) => ({
        key: d.key,
        label: d.key === "all" ? `All (${cards.length})` : d.label,
      })),
    [cards.length],
  );

  return (
    <PanelShell>
      <PanelHeader
        title="Agenda"
        caption={sessionLabel}
        actions={
          <>
            <button className="m-btn m-btn-ghost" type="button" onClick={() => setScratchOpen((o) => !o)}>
              {scratchOpen ? "Close Scratchpad" : "Scratchpad"}
            </button>
            <button
              className="m-btn m-btn-soft-success"
              type="button"
              disabled={aiPlanAct.pending}
              onClick={() => void handleAIPlanDay()}
            >
              {aiPlanAct.pending ? "Planning..." : "AI Auto-Plan"}
            </button>
          </>
        }
      />

      {scratchOpen && (
        <SectionCard title="Org Scratchpad">
          <textarea
            className="m-textarea m-mono"
            rows={4}
            style={{ marginBottom: 8 }}
            value={scratchInput}
            onChange={(e) => setScratchInput(e.target.value)}
            placeholder="Paste .org notes or agenda projection JSON..."
          />
          <button className="m-btn m-btn-primary" type="button" onClick={() => void handleApplyScratchpad()}>
            Project
          </button>
        </SectionCard>
      )}

      <CaptureFormBar pending={captureAct.pending} onSubmitCapture={handleCaptureSubmit} />

      <div className="m-split">
        <section>
          <SectionCard
            title={`Queue (${visibleCards.length})`}
            right={
              <SegmentedControl
                options={filterOptions}
                value={filter}
                onChange={handleFilterChange}
              />
            }
          >
            {conflicts.map((cf, idx) => (
              <div className="m-callout m-callout-warn" key={`${cf.window}-${idx}`}>
                {cf.summary}
              </div>
            ))}

            <div className="m-list">
              {renderedCards.map((card, idx) => (
                <AgendaCardRow
                  key={`${card.title}-${idx}`}
                  card={card}
                  onClockIn={handleClockIn}
                  onToggleDone={handleToggleDone}
                />
              ))}
            </div>

            {visibleCards.length > renderedCards.length && (
              <div style={{ marginTop: 10 }}>
                <button
                  className="m-btn"
                  type="button"
                  onClick={() => setVisibleLimit((n) => n + VISIBLE_CARD_WINDOW)}
                >
                  Show more ({visibleCards.length - renderedCards.length} remaining)
                </button>
              </div>
            )}
          </SectionCard>
        </section>

        <aside>
          <FocusClockCard
            alarm={alarm}
            onToggleStart={handleToggleAlarmStart}
            onStartBreak={handleStartBreak}
            onReset={handleResetAlarm}
          />

          <SectionCard title="Today's Schedule">
            <div className="m-list">
              {todayBlocks.map((b, i) => (
                <div className="m-row" key={`${b.title}-${i}`}>
                  <span className="m-mono" style={{ fontSize: 12 }}>
                    {b.time}
                    {b.endTime ? `-${b.endTime}` : ""}
                  </span>
                  <span style={{ flex: 1 }}>{b.title}</span>
                </div>
              ))}
            </div>
          </SectionCard>
        </aside>
      </div>

      <ToastBanner message={receiptMsg} onDismiss={handleDismissReceipt} />
    </PanelShell>
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
