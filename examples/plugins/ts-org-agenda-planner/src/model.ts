// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import type {
  OrgizeAgentCaptureKindDto,
  OrgizeAgentCaptureRequestDto,
  OrgizeAgentCaptureTargetKindDto,
  OrgizeAgendaViewCardDto,
  OrgizeClockRollupResponseDto,
} from "./orgize_dto";

export type AgendaFilter = "all" | "today" | "overdue" | "blocked" | "urgent" | "done";

export interface ScheduleConflict {
  firstTitle: string;
  secondTitle: string;
  window: string;
  summary: string;
}

export interface QuickCaptureFormInput {
  kind: OrgizeAgentCaptureKindDto;
  targetKind: OrgizeAgentCaptureTargetKindDto;
  title: string;
  body?: string;
  tagsCsv?: string;
  timeWindow?: string; // e.g. "14:00-15:00" attached as structured property
}

export interface ClockDashboardSummary {
  runningTitle: string;
  runningCount: number;
  totalMinutes: number;
  overEffortCount: number;
}

export interface FocusAlarmState {
  label: string;
  cardTitle: string;
  durationSec: number;
  remainingSec: number;
  status: "idle" | "running" | "paused" | "ringing";
  completedBlocks: number;
}

const HHMM_RE = /^(\d{2}):(\d{2})$/;

export function parseHHMM(raw?: string | null): number | undefined {
  if (!raw) return undefined;
  const m = HHMM_RE.exec(raw.trim());
  if (!m) return undefined;
  const hh = Number(m[1]);
  const mm = Number(m[2]);
  if (hh < 0 || hh > 23 || mm < 0 || mm > 59) return undefined;
  return hh * 60 + mm;
}

export function formatMinutes(mins: number): string {
  const clamped = Math.max(0, Math.min(24 * 60, Math.floor(mins)));
  const hh = String(Math.floor(clamped / 60)).padStart(2, "0");
  const mm = String(clamped % 60).padStart(2, "0");
  return `${hh}:${mm}`;
}

export function formatClock(sec: number): string {
  const s = Math.max(0, Math.floor(sec));
  const mm = String(Math.floor(s / 60)).padStart(2, "0");
  const rem = String(s % 60).padStart(2, "0");
  return `${mm}:${rem}`;
}

/**
 * Builds a canonical `tao3k/orgize-wasm` `OrgizeAgentCaptureRequestDto` from
 * structured form fields. Zero Org-mode syntax parsing happens in TypeScript;
 * `tao3k/orgize` renders and validates the authoritative Org entry.
 */
export function buildCaptureRequest(
  input: QuickCaptureFormInput,
  todayISO: string,
): OrgizeAgentCaptureRequestDto {
  const [yStr, mStr, dStr] = todayISO.split("-");
  const dateObj = {
    year: Number(yStr) || 2026,
    month: Number(mStr) || 1,
    day: Number(dStr) || 1,
  };
  const tags = (input.tagsCsv ?? "")
    .split(/[,\s]+/)
    .map((t) => t.replace(/^#/, "").trim().toLowerCase())
    .filter(Boolean);

  const properties: Array<{ key: string; value: string }> = [];
  if (input.timeWindow && input.timeWindow.trim() !== "") {
    properties.push({ key: "SCHEDULED_WINDOW", value: input.timeWindow.trim() });
  }

  return {
    kind: input.kind,
    title: input.title.trim(),
    body: input.body?.trim() ? input.body.trim() : null,
    target: {
      kind: input.targetKind,
      date: input.targetKind === "datetree" ? dateObj : null,
      insertPosition: "append",
    },
    tags: tags.length > 0 ? tags : null,
    properties: properties.length > 0 ? properties : null,
    requiresConfirmation: false,
  };
}

/**
 * Detects overlapping time-blocked Org agenda cards on targetDateISO in O(N log N)
 * directly from `orgize`'s parsed `time` and `endTime` fields.
 */
export function detectTimeBlockOverlaps(
  cards: readonly OrgizeAgendaViewCardDto[],
  targetDateISO: string,
): ScheduleConflict[] {
  const blocks = cards
    .map((card) => ({
      card,
      startMin: parseHHMM(card.time),
      endMin: parseHHMM(card.endTime),
    }))
    .filter(
      (b): b is { card: OrgizeAgendaViewCardDto; startMin: number; endMin: number } =>
        b.card.todoState !== "done" &&
        b.card.displayDate === targetDateISO &&
        typeof b.startMin === "number" &&
        typeof b.endMin === "number" &&
        b.endMin > b.startMin,
    )
    .sort((a, b) => a.startMin - b.startMin);

  const conflicts: ScheduleConflict[] = [];
  for (let i = 1; i < blocks.length; i++) {
    const prev = blocks[i - 1];
    const cur = blocks[i];
    if (cur.startMin < prev.endMin) {
      const win = `${formatMinutes(cur.startMin)}-${formatMinutes(Math.min(prev.endMin, cur.endMin))}`;
      conflicts.push({
        firstTitle: prev.card.title,
        secondTitle: cur.card.title,
        window: win,
        summary: `Overlap (${win}): "${prev.card.title}" vs "${cur.card.title}"`,
      });
    }
  }
  return conflicts;
}

/**
 * Filters and sorts canonical `OrgizeAgendaViewCardDto` items using `orgize`'s
 * pre-computed `urgency.total`, `blockers`, `displayDate`, and `time`.
 */
export function selectAgendaCards(
  cards: readonly OrgizeAgendaViewCardDto[],
  todayISO: string,
  filter: AgendaFilter,
): OrgizeAgendaViewCardDto[] {
  const filtered = cards.filter((card) => {
    const isDone = card.todoState === "done";
    switch (filter) {
      case "today":
        return !isDone && card.displayDate === todayISO;
      case "overdue":
        return !isDone && card.displayDate !== "" && card.displayDate < todayISO;
      case "blocked":
        return !isDone && card.blockers.length > 0;
      case "urgent":
        return !isDone && card.urgency.total >= 80;
      case "done":
        return isDone;
      default:
        return true;
    }
  });

  return filtered.slice().sort((a, b) => {
    const aDone = a.todoState === "done";
    const bDone = b.todoState === "done";
    if (aDone !== bDone) return aDone ? 1 : -1;
    if (b.urgency.total !== a.urgency.total) return b.urgency.total - a.urgency.total;
    if (a.displayDate !== b.displayDate) return a.displayDate.localeCompare(b.displayDate);
    return (parseHHMM(a.time) ?? 9999) - (parseHHMM(b.time) ?? 9999);
  });
}

/**
 * Summarizes an `OrgizeClockRollupResponseDto` into a compact header badge.
 */
export function summarizeClockRollup(rollup: OrgizeClockRollupResponseDto): ClockDashboardSummary {
  let runningTitle = "";
  let runningCount = 0;
  let totalSeconds = 0;
  let overEffortCount = 0;

  for (const rec of rollup.records) {
    totalSeconds += rec.localClock.totalSeconds;
    if (rec.localClock.runningEntries > 0) {
      runningCount += rec.localClock.runningEntries;
      if (!runningTitle) runningTitle = rec.title;
    }
    if (rec.effort.status === "overEffort") {
      overEffortCount++;
    }
  }

  return {
    runningTitle,
    runningCount,
    totalMinutes: Math.floor(totalSeconds / 60),
    overEffortCount,
  };
}

/**
 * Pure state-machine step for the personal Focus Alarm / Pomodoro countdown.
 */
export function tickFocusAlarm(state: FocusAlarmState, deltaSec: number): FocusAlarmState {
  if (state.status !== "running" || deltaSec <= 0) {
    return state;
  }
  const nextRem = Math.max(0, state.remainingSec - deltaSec);
  if (nextRem === 0) {
    return {
      ...state,
      remainingSec: 0,
      status: "ringing",
      completedBlocks: state.completedBlocks + 1,
    };
  }
  return { ...state, remainingSec: nextRem };
}
