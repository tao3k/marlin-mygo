// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { definePluginPerfSuite, runPluginPerfCLI } from "@tao3k/marlin-panel-sdk";
import {
  buildCaptureRequest,
  detectTimeBlockOverlaps,
  selectAgendaCards,
  summarizeClockRollup,
  tickFocusAlarm,
} from "./model";
import type { OrgizeAgendaViewCardDto, OrgizeClockRollupResponseDto } from "./orgize_dto";

const SRC = {
  start: { offset: 0, line: 1, column: 1 },
  end: { offset: 12, line: 1, column: 13 },
};

// Realistic 500-card orgize agenda projection fixture so downstream customizations
// catch accidental O(N^2) filtering, sorting, or overlap checks immediately.
const CARDS_500: OrgizeAgendaViewCardDto[] = Array.from({ length: 500 }, (_, i) => {
  const hour = 8 + (i % 12);
  const startHH = String(hour).padStart(2, "0");
  const endHH = String(hour + 1).padStart(2, "0");
  return {
    source: SRC,
    sortedPosition: i,
    kind: i % 3 === 0 ? "deadline" : "scheduled",
    displayDate: i % 5 === 0 ? "2026-11-24" : "2026-11-25",
    targetDate: "2026-11-25",
    time: `${startHH}:00`,
    endTime: `${endHH}:00`,
    title: `Agenda task #${i}`,
    category: "engineering",
    todo: i % 7 === 0 ? "DONE" : "TODO",
    todoState: i % 7 === 0 ? "done" : "todo",
    effectiveTags: i % 2 === 0 ? ["deep_work", "planning"] : ["ops"],
    blockers: [],
    urgency: { total: 40 + (i % 80), ingredients: [] },
    receipts: [],
  };
});

const CLOCK_200: OrgizeClockRollupResponseDto = {
  schemaVersion: 1,
  records: Array.from({ length: 200 }, (_, i) => ({
    source: SRC,
    outlinePath: ["Sprint", `Task ${i}`],
    level: 2,
    title: `Task ${i}`,
    localClock: {
      entries: 2,
      closedEntries: 1,
      runningEntries: i === 0 ? 1 : 0,
      unparsedEntries: 0,
      totalSeconds: 1800,
    },
    subtreeClock: {
      entries: 2,
      closedEntries: 1,
      runningEntries: i === 0 ? 1 : 0,
      unparsedEntries: 0,
      totalSeconds: 1800,
    },
    effort: {
      subtreeTotalSeconds: 1800,
      deltaSeconds: i % 10 === 0 ? 300 : -300,
      status: i % 10 === 0 ? "overEffort" : "underEffort",
    },
  })),
};

export const perfSuite = definePluginPerfSuite({
  pluginId: "tao3k.org-agenda-planner",
  cases: [
    {
      name: "selectAgendaCards_500",
      budgetUs: 1500,
      p95BudgetUs: 4500,
      remediation: "Precompute sort keys (HH:MM -> int minutes) and avoid allocating temporary objects inside Array.prototype.sort comparator.",
      run: () => {
        const res = selectAgendaCards(CARDS_500, "2026-11-25", "all");
        if (res.length === 0) throw new Error("unexpected empty result");
      },
    },
    {
      name: "detectTimeBlockOverlaps_500",
      budgetUs: 1200,
      p95BudgetUs: 3600,
      remediation: "Keep conflict detection O(N log N) sweep-line instead of pairwise O(N^2) comparison.",
      run: () => {
        const cf = detectTimeBlockOverlaps(CARDS_500, "2026-11-25");
        if (cf.length === 0) throw new Error("expected synthetic overlaps");
      },
    },
    {
      name: "summarizeClockRollup_200",
      budgetUs: 300,
      remediation: "Aggregate OrgizeClockRollupResponseDto in a single linear pass with zero intermediate arrays.",
      run: () => {
        const s = summarizeClockRollup(CLOCK_200);
        if (s.totalMinutes <= 0) throw new Error("unexpected zero minutes");
      },
    },
    {
      name: "buildCaptureRequest_and_alarmTick",
      budgetUs: 150,
      remediation: "Keep capture request assembly and 1s focus-alarm ticks O(1) without regex backtracking.",
      run: () => {
        const req = buildCaptureRequest(
          { kind: "task", targetKind: "datetree", title: "Ship perf gate", tagsCsv: "#perf, #plan", timeWindow: "14:00-15:00" },
          "2026-11-25",
        );
        const next = tickFocusAlarm(
          { label: "Deep", cardTitle: req.title, durationSec: 1500, remainingSec: 120, status: "running", completedBlocks: 0 },
          1,
        );
        if (next.remainingSec !== 119) throw new Error("unexpected tick");
      },
    },
  ],
});

if (import.meta.main) {
  runPluginPerfCLI(perfSuite);
}
