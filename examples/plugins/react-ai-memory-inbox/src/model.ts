// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import type {
  OrgizeAgentMemoryCardDto,
  OrgizeAgentMemorySeverityDto,
} from "./memory_dto";

export type MemorySeverityFilter = "all" | OrgizeAgentMemorySeverityDto;

export interface MemoryTriageSummary {
  total: number;
  actionCount: number;
  suppressedCount: number;
  infoCount: number;
  evidenceCount: number;
}

const SEVERITY_RANK: Record<OrgizeAgentMemorySeverityDto, number> = {
  action: 0,
  suppressed: 1,
  info: 2,
};

/**
 * Filters and deterministically ranks `orgize` Agent Memory cards by severity
 * (action -> suppressed -> info) and evidence density.
 */
export function filterMemoryCards(
  cards: readonly OrgizeAgentMemoryCardDto[],
  severity: MemorySeverityFilter,
  query: string,
): OrgizeAgentMemoryCardDto[] {
  const q = query.trim().toLowerCase();
  const matched: OrgizeAgentMemoryCardDto[] = [];

  for (let i = 0; i < cards.length; i++) {
    const c = cards[i];
    if (severity !== "all" && c.decision.severity !== severity) {
      continue;
    }
    if (q !== "") {
      const inTitle = c.title.toLowerCase().includes(q);
      const inNext = c.decision.nextAction.toLowerCase().includes(q);
      let inTag = false;
      for (let t = 0; t < c.effectiveTags.length; t++) {
        if (c.effectiveTags[t].toLowerCase().includes(q)) {
          inTag = true;
          break;
        }
      }
      if (!inTitle && !inNext && !inTag) continue;
    }
    matched.push(c);
  }

  matched.sort((a, b) => {
    const sr = SEVERITY_RANK[a.decision.severity] - SEVERITY_RANK[b.decision.severity];
    if (sr !== 0) return sr;
    return b.evidence.length - a.evidence.length;
  });
  return matched;
}

export function summarizeMemoryCards(cards: readonly OrgizeAgentMemoryCardDto[]): MemoryTriageSummary {
  let actionCount = 0;
  let suppressedCount = 0;
  let infoCount = 0;
  let evidenceCount = 0;
  for (let i = 0; i < cards.length; i++) {
    const c = cards[i];
    evidenceCount += c.evidence.length;
    if (c.decision.severity === "action") actionCount++;
    else if (c.decision.severity === "suppressed") suppressedCount++;
    else infoCount++;
  }
  return { total: cards.length, actionCount, suppressedCount, infoCount, evidenceCount };
}

/**
 * Builds a canonical `org.capture.plan` datetree request when the user promotes
 * a memory card into durable Org knowledge.
 */
export function buildMemoryDatetreeCapture(card: OrgizeAgentMemoryCardDto, dateISO: string) {
  const [y, m, d] = dateISO.split("-").map(Number);
  return {
    kind: "memoryCandidate",
    title: card.title,
    body: card.evidence.map((e) => `- [${e.kind.label}] ${e.value}`).join("\n"),
    target: {
      kind: "datetree",
      date: { year: y || 2026, month: m || 1, day: d || 1 },
      insertPosition: "append",
    },
    tags: card.effectiveTags,
    memoryPolicy: "decision",
    requiresConfirmation: false,
  };
}
