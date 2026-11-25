// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { describe, expect, test } from "bun:test";
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
  end: { offset: 10, line: 1, column: 11 },
};

function card(overrides: Partial<OrgizeAgendaViewCardDto>): OrgizeAgendaViewCardDto {
  return {
    source: SRC,
    sortedPosition: 0,
    kind: "scheduled",
    displayDate: "2026-11-25",
    targetDate: "2026-11-25",
    title: "Untitled",
    todo: "TODO",
    todoState: "todo",
    effectiveTags: [],
    blockers: [],
    urgency: { total: 50, ingredients: [] },
    receipts: [],
    ...overrides,
  };
}

describe("ts-org-agenda-planner (orgize DTO model)", () => {
  test("buildCaptureRequest emits canonical orgize AgentCaptureRequestDto without Org parsing", () => {
    const req = buildCaptureRequest(
      {
        kind: "task",
        targetKind: "datetree",
        title: "Prepare Q4 planning brief",
        tagsCsv: "#deep_work, review",
        timeWindow: "14:00-15:30",
      },
      "2026-11-25",
    );
    expect(req.kind).toBe("task");
    expect(req.title).toBe("Prepare Q4 planning brief");
    expect(req.target?.kind).toBe("datetree");
    expect(req.target?.date).toEqual({ year: 2026, month: 11, day: 25 });
    expect(req.tags).toEqual(["deep_work", "review"]);
    expect(req.properties).toEqual([{ key: "SCHEDULED_WINDOW", value: "14:00-15:30" }]);
  });

  test("detectTimeBlockOverlaps flags colliding time windows from orgize HH:MM fields", () => {
    const overlaps = detectTimeBlockOverlaps(
      [
        card({ title: "Design Review", time: "10:00", endTime: "11:30" }),
        card({ title: "1:1 Sync", time: "11:00", endTime: "11:45" }),
        card({ title: "Completed Errand", time: "10:15", endTime: "10:45", todoState: "done" }),
      ],
      "2026-11-25",
    );
    expect(overlaps.length).toBe(1);
    expect(overlaps[0].window).toBe("11:00-11:30");
  });

  test("selectAgendaCards sorts by orgize urgency and supports blocked filter", () => {
    const blockedCard = card({
      title: "Wait for kernel receipt",
      urgency: { total: 40, ingredients: [] },
      blockers: [
        {
          kind: "orderedPreviousSibling",
          blocked: { source: SRC, outlinePath: ["Sprint"], level: 2, title: "Wait for kernel receipt" },
          blocker: { source: SRC, outlinePath: ["Sprint"], level: 2, title: "Sign release" },
          message: "Blocked by ordered previous sibling",
        },
      ],
    });
    const urgentCard = card({
      title: "Cut release tag",
      urgency: { total: 110, ingredients: [] },
    });
    const allSorted = selectAgendaCards([blockedCard, urgentCard], "2026-11-25", "all");
    expect(allSorted[0].title).toBe("Cut release tag");

    const blockedOnly = selectAgendaCards([blockedCard, urgentCard], "2026-11-25", "blocked");
    expect(blockedOnly.length).toBe(1);
    expect(blockedOnly[0].title).toBe("Wait for kernel receipt");
  });

  test("summarizeClockRollup aggregates orgize OrgizeClockRollupResponseDto", () => {
    const rollup: OrgizeClockRollupResponseDto = {
      schemaVersion: 1,
      records: [
        {
          source: SRC,
          outlinePath: ["Today"],
          level: 1,
          title: "Deep Work",
          localClock: { entries: 2, closedEntries: 1, runningEntries: 1, unparsedEntries: 0, totalSeconds: 3600 },
          subtreeClock: { entries: 2, closedEntries: 1, runningEntries: 1, unparsedEntries: 0, totalSeconds: 3600 },
          effort: { subtreeTotalSeconds: 3600, deltaSeconds: 600, status: "overEffort" },
        },
      ],
    };
    const sum = summarizeClockRollup(rollup);
    expect(sum.runningCount).toBe(1);
    expect(sum.runningTitle).toBe("Deep Work");
    expect(sum.totalMinutes).toBe(60);
    expect(sum.overEffortCount).toBe(1);
  });

  test("tickFocusAlarm transitions from running to ringing and increments completedBlocks", () => {
    const next = tickFocusAlarm(
      {
        label: "Deep Work",
        cardTitle: "Write RFC",
        durationSec: 1500,
        remainingSec: 2,
        status: "running",
        completedBlocks: 1,
      },
      2,
    );
    expect(next.status).toBe("ringing");
    expect(next.remainingSec).toBe(0);
    expect(next.completedBlocks).toBe(2);
  });
});
