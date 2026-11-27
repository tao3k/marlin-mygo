// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { describe, expect, test } from "bun:test";
import { buildMemoryDatetreeCapture, filterMemoryCards, summarizeMemoryCards } from "./model";
import { perfSuite } from "./perf";
import type { OrgizeAgentMemoryCardDto } from "./memory_dto";

const SAMPLE_CARDS: OrgizeAgentMemoryCardDto[] = [
  {
    anchor: "a1",
    title: "Use React 19 for rich panels",
    effectiveTags: ["frontend", "decision"],
    decision: { code: "D1", kind: "current", severity: "action", title: "Decision", nextAction: "Promote" },
    authority: [],
    evidence: [{ kind: { code: "b", label: "Bench", family: "m" }, value: "180 KB" }],
  },
  {
    anchor: "a2",
    title: "Old shim note",
    effectiveTags: ["legacy"],
    decision: { code: "D2", kind: "background", severity: "suppressed", title: "Old", nextAction: "Supersede" },
    authority: [],
    evidence: [],
  },
];

describe("react-ai-memory-inbox", () => {
  test("ranks action cards first and filters by query and severity", () => {
    const all = filterMemoryCards([SAMPLE_CARDS[1], SAMPLE_CARDS[0]], "all", "");
    expect(all[0].anchor).toBe("a1");

    const filtered = filterMemoryCards(SAMPLE_CARDS, "action", "react");
    expect(filtered).toHaveLength(1);
    expect(filtered[0].anchor).toBe("a1");
  });

  test("summarizes cards and constructs datetree capture DTO", () => {
    const sum = summarizeMemoryCards(SAMPLE_CARDS);
    expect(sum.actionCount).toBe(1);
    expect(sum.suppressedCount).toBe(1);
    const req = buildMemoryDatetreeCapture(SAMPLE_CARDS[0], "2026-11-25");
    expect(req.target.date).toEqual({ year: 2026, month: 11, day: 25 });
    expect(req.memoryPolicy).toBe("decision");
  });

  test("passes 500-card plugin perf macro suite", () => {
    const rep = perfSuite.run({ scale: 2.0, iterationsOverride: 40 });
    expect(rep.passed).toBe(true);
  });
});
