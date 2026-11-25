// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

/**
 * Canonical DTO contracts matching `tao3k/orgize-wasm` (`orgize/dto` operations.d.ts).
 * Zero hand-rolled Org parsing in TypeScript.
 */

export type OrgizeMemoryRecordStateDto = "current" | "background" | "closed" | "archived";
export type OrgizeAgentMemorySeverityDto = "action" | "suppressed" | "info";

export interface OrgizeMemoryStatsDto {
  totalRecords: number;
  currentRecords: number;
  actionCards: number;
  suppressedCards: number;
  infoCards: number;
  evidence: number;
}

export interface OrgizeMemoryEvidenceDto {
  kind: { code: string; label: string; family: string };
  value: string;
}

export interface OrgizeMemoryAuthorityReasonDto {
  kind: string;
  label: string;
  message: string;
}

export interface OrgizeAgentMemoryDecisionDto {
  code: string;
  kind: OrgizeMemoryRecordStateDto;
  severity: OrgizeAgentMemorySeverityDto;
  title: string;
  nextAction: string;
}

export interface OrgizeAgentMemoryCardDto {
  anchor?: string | null;
  title: string;
  todo?: string | null;
  effectiveTags: string[];
  decision: OrgizeAgentMemoryDecisionDto;
  authority: OrgizeMemoryAuthorityReasonDto[];
  evidence: OrgizeMemoryEvidenceDto[];
}

export interface OrgizeMemoryResponseDto {
  schemaVersion: 1;
  stats: OrgizeMemoryStatsDto;
  cards: OrgizeAgentMemoryCardDto[];
}
