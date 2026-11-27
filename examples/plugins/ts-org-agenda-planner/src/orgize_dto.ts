// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

/**
 * Canonical DTO contracts matching `tao3k/orgize-wasm` (`orgize/dto`).
 *
 * Plugins MUST NOT re-implement Org-mode syntax parsing in TypeScript.
 * All `.org` parsing, urgency scoring, blocker resolution, clock rollups, and
 * capture planning are owned by `tao3k/orgize` (either via the Rust authority
 * kernel's live TopicBus projections or via `tao3k/orgize-wasm` in a Web Worker).
 */

export interface OrgizeSourcePositionDto {
  offset: number;
  line: number;
  column: number;
}

export interface OrgizeSourceRangeDto {
  start: OrgizeSourcePositionDto;
  end: OrgizeSourcePositionDto;
}

export interface OrgizeTaskBlockerTaskDto {
  source: OrgizeSourceRangeDto;
  outlinePath: string[];
  level: number;
  title: string;
  todo?: string | null;
  todoState?: "todo" | "done" | null;
}

export interface OrgizeTaskBlockerRecordDto {
  kind: "orderedPreviousSibling";
  blocked: OrgizeTaskBlockerTaskDto;
  blocker: OrgizeTaskBlockerTaskDto;
  message: string;
}

export interface OrgizeAgendaUrgencyIngredientDto {
  kind:
    | "priority"
    | "deadline"
    | "scheduled"
    | "todoState"
    | "timeOfDay"
    | "tags"
    | "category"
    | "occurrence";
  score: number;
  message: string;
}

export interface OrgizeAgendaUrgencyScoreDto {
  total: number;
  ingredients: OrgizeAgendaUrgencyIngredientDto[];
}

export interface OrgizeAgendaViewReceiptDto {
  kind: string;
  message: string;
}

export interface OrgizeAgendaViewCardDto {
  source: OrgizeSourceRangeDto;
  sortedPosition: number;
  kind: string;
  displayDate: string; // YYYY-MM-DD from orgize
  targetDate: string; // YYYY-MM-DD from orgize
  targetEndDate?: string | null;
  time?: string | null; // HH:MM from orgize
  endTime?: string | null; // HH:MM from orgize
  title: string;
  category?: string | null;
  todo?: string | null;
  todoState?: "todo" | "done" | null;
  effectiveTags: string[];
  blockers: OrgizeTaskBlockerRecordDto[];
  urgency: OrgizeAgendaUrgencyScoreDto;
  receipts: OrgizeAgendaViewReceiptDto[];
}

export interface OrgizeAgendaViewResponseDto {
  schemaVersion: 1;
  totalCandidates: number;
  limit?: number | null;
  cards: OrgizeAgendaViewCardDto[];
}

export interface OrgizeClockSummaryDto {
  entries: number;
  closedEntries: number;
  runningEntries: number;
  unparsedEntries: number;
  totalSeconds: number;
}

export type OrgizeClockEffortStatusDto =
  | "noEffort"
  | "underEffort"
  | "onEffort"
  | "overEffort";

export interface OrgizeClockEffortSummaryDto {
  subtreeTotalSeconds: number;
  deltaSeconds: number;
  status: OrgizeClockEffortStatusDto;
}

export interface OrgizeClockRollupRecordDto {
  source: OrgizeSourceRangeDto;
  outlinePath: string[];
  level: number;
  title: string;
  localClock: OrgizeClockSummaryDto;
  subtreeClock: OrgizeClockSummaryDto;
  effort: OrgizeClockEffortSummaryDto;
}

export interface OrgizeClockRollupResponseDto {
  schemaVersion: 1;
  records: OrgizeClockRollupRecordDto[];
}

export type OrgizeAgentCaptureKindDto =
  | "idea"
  | "articleNote"
  | "task"
  | "decision"
  | "preference"
  | "correction"
  | "memoryCandidate"
  | "evidence"
  | "note";

export type OrgizeAgentCaptureTargetKindDto =
  | "inbox"
  | "datetree"
  | "outlinePath"
  | "currentSection";

export interface OrgizeAgentCaptureDateDto {
  year: number;
  month: number;
  day: number;
}

export interface OrgizeAgentCaptureTargetRequestDto {
  kind: OrgizeAgentCaptureTargetKindDto;
  sourceFile?: string | null;
  outlinePath?: string[] | null;
  date?: OrgizeAgentCaptureDateDto | null;
  insertPosition?: "append" | "prepend" | "firstChild" | "lastChild" | null;
}

export interface OrgizeAgentCapturePropertyDto {
  key: string;
  value: string;
}

export interface OrgizeAgentCaptureRequestDto {
  kind: OrgizeAgentCaptureKindDto;
  title: string;
  body?: string | null;
  target?: OrgizeAgentCaptureTargetRequestDto | null;
  tags?: string[] | null;
  properties?: OrgizeAgentCapturePropertyDto[] | null;
  requiresConfirmation?: boolean | null;
}

/**
 * Optional local adapter for `tao3k/orgize-wasm` (`new Org(source)` or
 * `orgize/worker`) when a downstream plugin previews an offline `.org` buffer
 * without writing any Org syntax parser in TypeScript.
 */
export interface OrgizeWasmDocumentAdapter {
  agendaViewJson(requestJson: string): string;
  clockRollupJson(): string;
  capturePlanJson(requestJson: string): string;
}
