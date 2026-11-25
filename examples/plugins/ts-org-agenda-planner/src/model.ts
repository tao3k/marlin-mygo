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

export function parseHHMM(raw?: string | null): number | undefined {
  if (!raw) return undefined;
  let start = 0;
  let end = raw.length;
  while (start < end && raw.charCodeAt(start) <= 32) start++;
  while (end > start && raw.charCodeAt(end - 1) <= 32) end--;
  if (end - start !== 5 || raw.charCodeAt(start + 2) !== 58 /* ':' */) {
    return undefined;
  }
  const h1 = raw.charCodeAt(start) - 48;
  const h2 = raw.charCodeAt(start + 1) - 48;
  const m1 = raw.charCodeAt(start + 3) - 48;
  const m2 = raw.charCodeAt(start + 4) - 48;
  if (
    h1 < 0 || h1 > 9 || h2 < 0 || h2 > 9 ||
    m1 < 0 || m1 > 9 || m2 < 0 || m2 > 9
  ) {
    return undefined;
  }
  const hh = h1 * 10 + h2;
  const mm = m1 * 10 + m2;
  if (hh > 23 || mm > 59) return undefined;
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
  const keyed: Array<{
    card: OrgizeAgendaViewCardDto;
    doneRank: number;
    urgency: number;
    date: string;
    timeMin: number;
  }> = [];

  for (let i = 0; i < cards.length; i++) {
    const card = cards[i]!;
    const isDone = card.todoState === "done";
    let keep = true;
    switch (filter) {
      case "today":
        keep = !isDone && card.displayDate === todayISO;
        break;
      case "overdue":
        keep = !isDone && card.displayDate !== "" && card.displayDate < todayISO;
        break;
      case "blocked":
        keep = !isDone && card.blockers.length > 0;
        break;
      case "urgent":
        keep = !isDone && card.urgency.total >= 80;
        break;
      case "done":
        keep = isDone;
        break;
      default:
        keep = true;
        break;
    }
    if (!keep) continue;
    keyed.push({
      card,
      doneRank: isDone ? 1 : 0,
      urgency: card.urgency.total,
      date: card.displayDate,
      timeMin: parseHHMM(card.time) ?? 9999,
    });
  }

  keyed.sort((a, b) => {
    if (a.doneRank !== b.doneRank) return a.doneRank - b.doneRank;
    if (b.urgency !== a.urgency) return b.urgency - a.urgency;
    if (a.date !== b.date) return a.date < b.date ? -1 : 1;
    return a.timeMin - b.timeMin;
  });

  const out = new Array<OrgizeAgendaViewCardDto>(keyed.length);
  for (let i = 0; i < keyed.length; i++) {
    out[i] = keyed[i]!.card;
  }
  return out;
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
