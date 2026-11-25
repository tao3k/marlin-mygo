// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { defineMarlinPanel } from "@tao3k/marlin-panel-sdk";
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

let activeFilter: AgendaFilter = "all";
let agendaCards: OrgizeAgendaViewCardDto[] = [
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
    urgency: {
      total: 92,
      ingredients: [{ kind: "priority", score: 40, message: "Priority A" }],
    },
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
    urgency: {
      total: 68,
      ingredients: [{ kind: "scheduled", score: 25, message: "Scheduled today" }],
    },
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
    urgency: {
      total: 105,
      ingredients: [{ kind: "deadline", score: 60, message: "Overdue deadline" }],
    },
    receipts: [],
  },
];

let clockRollup: OrgizeClockRollupResponseDto = {
  schemaVersion: 1,
  records: [
    {
      source: SAMPLE_SOURCE,
      outlinePath: ["Today", "Morning architecture review & PR triage"],
      level: 2,
      title: "Morning architecture review & PR triage",
      localClock: {
        entries: 1,
        closedEntries: 1,
        runningEntries: 0,
        unparsedEntries: 0,
        totalSeconds: 45 * 60,
      },
      subtreeClock: {
        entries: 1,
        closedEntries: 1,
        runningEntries: 0,
        unparsedEntries: 0,
        totalSeconds: 45 * 60,
      },
      effort: {
        subtreeTotalSeconds: 45 * 60,
        deltaSeconds: -15 * 60,
        status: "underEffort",
      },
    },
  ],
};

let alarm: FocusAlarmState = {
  label: "Deep Work Block",
  cardTitle: agendaCards[0]?.title ?? "Focus Session",
  durationSec: 25 * 60,
  remainingSec: 25 * 60,
  status: "idle",
  completedBlocks: 0,
};

const sessionBadgeEl = document.getElementById("session-badge");
const clockBadgeEl = document.getElementById("clock-rollup-badge");
const filterPillsEl = document.getElementById("filter-pills");
const conflictsEl = document.getElementById("conflicts");
const agendaListEl = document.getElementById("agenda-list");
const timelineListEl = document.getElementById("timeline-list");
const alarmLabelEl = document.getElementById("alarm-label");
const alarmClockEl = document.getElementById("alarm-clock");
const alarmStatusEl = document.getElementById("alarm-status");
const alarmToggleBtn = document.getElementById("alarm-toggle");
const receiptEl = document.getElementById("receipt-feed");

function setReceipt(msg: string): void {
  if (receiptEl) receiptEl.textContent = msg;
}

const panel = defineMarlinPanel({
  pluginId: "tao3k.org-agenda-planner",
  events: {
    "org.agenda": (ev) => {
      const dto = ev.payload as Partial<OrgizeAgendaViewResponseDto> | null;
      if (dto && Array.isArray(dto.cards)) {
        agendaCards = dto.cards;
        setReceipt(`Synced ${dto.cards.length} orgize agenda cards (schema v${dto.schemaVersion ?? 1})`);
        render();
      }
    },
    "org.clock": (ev) => {
      const dto = ev.payload as Partial<OrgizeClockRollupResponseDto> | null;
      if (dto && Array.isArray(dto.records)) {
        clockRollup = { schemaVersion: 1, records: dto.records };
        render();
      }
    },
    "session.summary": (ev) => {
      if (sessionBadgeEl && ev.payload && typeof ev.payload === "object") {
        const p = ev.payload as Record<string, unknown>;
        sessionBadgeEl.textContent = `orgize DTOs · session ${String(p.sessionId ?? "live")} · turn #${String(p.turnOrdinal ?? 0)}`;
      }
    },
  },
});

async function submitStructuredCapture(): Promise<void> {
  const kindEl = document.getElementById("capture-kind") as HTMLSelectElement | null;
  const targetEl = document.getElementById("capture-target") as HTMLSelectElement | null;
  const titleEl = document.getElementById("capture-title") as HTMLInputElement | null;
  const tagsEl = document.getElementById("capture-tags") as HTMLInputElement | null;
  const winEl = document.getElementById("capture-window") as HTMLInputElement | null;
  if (!titleEl || !titleEl.value.trim()) return;

  const req = buildCaptureRequest(
    {
      kind: (kindEl?.value as OrgizeAgentCaptureKindDto) ?? "task",
      targetKind: (targetEl?.value as OrgizeAgentCaptureTargetKindDto) ?? "inbox",
      title: titleEl.value,
      tagsCsv: tagsEl?.value ?? "",
      timeWindow: winEl?.value ?? "",
    },
    TODAY_ISO,
  );
  const rcpt = await panel.submit("org.capture.plan", req);
  if (rcpt.admitted) {
    const [startTime, endTime] = (winEl?.value ?? "").split("-").map((s) => s.trim());
    agendaCards = [
      {
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
        urgency: { total: 75, ingredients: [] },
        receipts: [{ kind: "capturePlan", message: `Target: ${req.target?.kind ?? "inbox"}` }],
      },
      ...agendaCards,
    ];
    titleEl.value = "";
    if (tagsEl) tagsEl.value = "";
    if (winEl) winEl.value = "";
    setReceipt(`Submitted orgize capturePlan for "${req.title}" (receipt: ${rcpt.receiptId || "admitted"})`);
    render();
  } else {
    setReceipt(`Capture denied: ${rcpt.denial || "rejected"}`);
  }
}

async function toggleCardDone(card: OrgizeAgendaViewCardDto): Promise<void> {
  const nextDone = card.todoState !== "done";
  const rcpt = await panel.submit("org.todo.toggle", {
    title: card.title,
    source: card.source,
    nextState: nextDone ? "done" : "todo",
  });
  if (rcpt.admitted) {
    agendaCards = agendaCards.map((c) =>
      c === card ? { ...c, todo: nextDone ? "DONE" : "TODO", todoState: nextDone ? "done" : "todo" } : c,
    );
    setReceipt(`Updated "${card.title}" -> ${nextDone ? "DONE" : "TODO"}`);
    render();
  }
}

async function clockInCard(card: OrgizeAgendaViewCardDto): Promise<void> {
  const rcpt = await panel.submit("org.clock.in", {
    title: card.title,
    source: card.source,
    durationSec: 25 * 60,
  });
  if (rcpt.admitted) {
    alarm = {
      ...alarm,
      cardTitle: card.title,
      label: `Clocked: ${card.title}`,
      durationSec: 25 * 60,
      remainingSec: 25 * 60,
      status: "running",
    };
    setReceipt(`Clocked in on "${card.title}" (25m focus alarm armed)`);
    render();
  }
}

function render(): void {
  // 1. Clock rollup badge (from orgize OrgizeClockRollupResponseDto)
  const clockSummary = summarizeClockRollup(clockRollup);
  if (clockBadgeEl) {
    const activeTxt = clockSummary.runningCount > 0 ? ` · active: ${clockSummary.runningTitle}` : "";
    const warnTxt = clockSummary.overEffortCount > 0 ? ` · ${clockSummary.overEffortCount} over-effort` : "";
    clockBadgeEl.textContent = `Clocked today: ${clockSummary.totalMinutes}m${activeTxt}${warnTxt}`;
  }

  // 2. Filter pills
  if (filterPillsEl) {
    filterPillsEl.innerHTML = "";
    const defs: Array<{ key: AgendaFilter; label: string }> = [
      { key: "all", label: `All (${agendaCards.length})` },
      { key: "today", label: "Today" },
      { key: "overdue", label: "Overdue" },
      { key: "urgent", label: "Urgent (80+)" },
      { key: "blocked", label: "Blocked" },
      { key: "done", label: "Done" },
    ];
    for (const d of defs) {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.textContent = d.label;
      if (activeFilter === d.key) btn.className = "active";
      btn.addEventListener("click", () => {
        activeFilter = d.key;
        render();
      });
      filterPillsEl.appendChild(btn);
    }
  }

  // 3. Schedule overlap banner
  if (conflictsEl) {
    conflictsEl.innerHTML = "";
    for (const cf of detectTimeBlockOverlaps(agendaCards, TODAY_ISO)) {
      const div = document.createElement("div");
      div.className = "conflict";
      div.textContent = cf.summary;
      conflictsEl.appendChild(div);
    }
  }

  // 4. Agenda cards
  if (agendaListEl) {
    agendaListEl.innerHTML = "";
    const visible = selectAgendaCards(agendaCards, TODAY_ISO, activeFilter);
    for (const card of visible) {
      const isDone = card.todoState === "done";
      const row = document.createElement("div");
      row.className = `item ${card.urgency.total >= 80 ? "urgent" : ""} ${isDone ? "done" : ""}`;

      const left = document.createElement("div");
      const title = document.createElement("div");
      title.textContent = `${card.todo ? `[${card.todo}] ` : ""}${card.title}`;

      const urgBadge = document.createElement("span");
      urgBadge.className = "badge";
      urgBadge.textContent = `U:${card.urgency.total}`;
      title.appendChild(urgBadge);

      if (card.blockers.length > 0) {
        const blk = document.createElement("span");
        blk.className = "badge blocked";
        blk.textContent = `Blocked (${card.blockers.length})`;
        title.appendChild(blk);
      }

      const meta = document.createElement("div");
      meta.className = "meta";
      const timeStr = card.time ? ` · ${card.time}${card.endTime ? `-${card.endTime}` : ""}` : "";
      const tagStr = card.effectiveTags.length > 0 ? ` · :${card.effectiveTags.join(":")}:` : "";
      meta.textContent = `${card.displayDate}${timeStr}${tagStr}`;
      left.appendChild(title);
      left.appendChild(meta);

      const actions = document.createElement("div");
      actions.className = "pills";
      const clockBtn = document.createElement("button");
      clockBtn.type = "button";
      clockBtn.textContent = "Clock In";
      clockBtn.addEventListener("click", () => void clockInCard(card));

      const doneBtn = document.createElement("button");
      doneBtn.type = "button";
      doneBtn.textContent = isDone ? "Reopen" : "Done";
      doneBtn.addEventListener("click", () => void toggleCardDone(card));

      actions.appendChild(clockBtn);
      actions.appendChild(doneBtn);
      row.appendChild(left);
      row.appendChild(actions);
      agendaListEl.appendChild(row);
    }
  }

  // 5. Today's chronological time blocks
  if (timelineListEl) {
    timelineListEl.innerHTML = "";
    const blocks = agendaCards
      .filter((c) => c.todoState !== "done" && c.displayDate === TODAY_ISO && parseHHMM(c.time) !== undefined)
      .sort((a, b) => (parseHHMM(a.time) ?? 0) - (parseHHMM(b.time) ?? 0));
    for (const b of blocks) {
      const div = document.createElement("div");
      div.className = "item";
      div.textContent = `${b.time}${b.endTime ? `-${b.endTime}` : ""}  ${b.title}`;
      timelineListEl.appendChild(div);
    }
  }

  // 6. Focus alarm widget
  if (alarmLabelEl) alarmLabelEl.textContent = `${alarm.label} · ${alarm.completedBlocks} completed`;
  if (alarmClockEl) alarmClockEl.textContent = formatClock(alarm.remainingSec);
  if (alarmStatusEl) alarmStatusEl.textContent = `Status: ${alarm.status}`;
  if (alarmToggleBtn) {
    alarmToggleBtn.textContent = alarm.status === "running" ? "Pause" : "Start";
  }
}

document.getElementById("capture-form")?.addEventListener("submit", (e) => {
  e.preventDefault();
  void submitStructuredCapture();
});

alarmToggleBtn?.addEventListener("click", () => {
  alarm = { ...alarm, status: alarm.status === "running" ? "paused" : "running" };
  render();
});

document.getElementById("alarm-break")?.addEventListener("click", () => {
  alarm = { ...alarm, label: "Short Break", durationSec: 5 * 60, remainingSec: 5 * 60, status: "running" };
  render();
});

document.getElementById("alarm-reset")?.addEventListener("click", () => {
  alarm = { ...alarm, label: "Deep Work Block", durationSec: 25 * 60, remainingSec: 25 * 60, status: "idle" };
  render();
});

setInterval(() => {
  if (alarm.status !== "running") return;
  const prev = alarm.status;
  alarm = tickFocusAlarm(alarm, 1);
  if (prev === "running" && alarm.status === "ringing") {
    void panel.submit("org.clock.out", { title: alarm.cardTitle, completedBlocks: alarm.completedBlocks });
    void panel.submit("agenda.alarm.trigger", { label: alarm.label, completedBlocks: alarm.completedBlocks });
    setReceipt(`Focus alarm ringing: clocked out "${alarm.cardTitle}"!`);
  }
  render();
}, 1000);

render();
