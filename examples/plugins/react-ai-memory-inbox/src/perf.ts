// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { definePluginPerfSuite, runPluginPerfCLI } from "@tao3k/marlin-panel-sdk";
import type { OrgizeAgentMemoryCardDto } from "./memory_dto";
import { buildMemoryDatetreeCapture, filterMemoryCards, summarizeMemoryCards } from "./model";

const CARDS_500: OrgizeAgentMemoryCardDto[] = Array.from({ length: 500 }, (_, i) => ({
  anchor: `mem-${i}`,
  title: `Memory candidate #${i} about orgize planning workflow`,
  effectiveTags: i % 2 === 0 ? ["architecture", "planning"] : ["ops"],
  decision: {
    code: "PROMOTE",
    kind: "current",
    severity: i % 3 === 0 ? "action" : i % 3 === 1 ? "suppressed" : "info",
    title: "Candidate",
    nextAction: "Review and promote to datetree",
  },
  authority: [{ kind: "agent", label: "Agent", message: "Extracted from session" }],
  evidence: [{ kind: { code: "e", label: "Trace", family: "run" }, value: `receipt-${i}` }],
}));

export const perfSuite = definePluginPerfSuite({
  pluginId: "tao3k.ai-memory-inbox",
  cases: [
    {
      name: "filterMemoryCards_500",
      budgetUs: 1200,
      p95BudgetUs: 3600,
      remediation: "Avoid allocating regexes or cloning card objects inside filterMemoryCards.",
      run: () => {
        const res = filterMemoryCards(CARDS_500, "all", "planning");
        if (res.length === 0) throw new Error("unexpected empty filter");
      },
    },
    {
      name: "summarizeMemoryCards_500",
      budgetUs: 250,
      remediation: "Keep summary aggregation a single linear loop with zero intermediate arrays.",
      run: () => {
        const s = summarizeMemoryCards(CARDS_500);
        if (s.total !== 500) throw new Error("unexpected total");
      },
    },
    {
      name: "buildMemoryDatetreeCapture",
      budgetUs: 100,
      remediation: "Keep OrgizeAgentCaptureRequestDto construction O(evidence_len).",
      run: () => {
        const req = buildMemoryDatetreeCapture(CARDS_500[0], "2026-11-25");
        if (req.target.kind !== "datetree") throw new Error("unexpected target");
      },
    },
  ],
});

if (import.meta.main) {
  runPluginPerfCLI(perfSuite);
}
