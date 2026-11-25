// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

export type GitFileState = "staged" | "modified" | "untracked" | "conflicted";
export type GitFilter = "all" | GitFileState;

export interface GitFileChange {
  path: string;
  state: GitFileState;
  additions: number;
  deletions: number;
  hunkSummary: string;
}

export interface GitStatusProjection {
  repoName: string;
  branch: string;
  upstream: string;
  ahead: number;
  behind: number;
  recentBranches: string[];
  changes: GitFileChange[];
}

export interface GitStatusSummary {
  totalFiles: number;
  stagedCount: number;
  unstagedCount: number;
  conflictCount: number;
  totalAdditions: number;
  totalDeletions: number;
}

export function summarizeGitStatus(proj: GitStatusProjection): GitStatusSummary {
  let stagedCount = 0;
  let unstagedCount = 0;
  let conflictCount = 0;
  let totalAdditions = 0;
  let totalDeletions = 0;

  for (const ch of proj.changes) {
    totalAdditions += Math.max(0, ch.additions);
    totalDeletions += Math.max(0, ch.deletions);
    if (ch.state === "staged") {
      stagedCount++;
    } else if (ch.state === "conflicted") {
      conflictCount++;
    } else {
      unstagedCount++;
    }
  }

  return {
    totalFiles: proj.changes.length,
    stagedCount,
    unstagedCount,
    conflictCount,
    totalAdditions,
    totalDeletions,
  };
}

export function filterGitChanges(
  changes: readonly GitFileChange[],
  filter: GitFilter,
): GitFileChange[] {
  if (filter === "all") {
    return [...changes];
  }
  return changes.filter((c) => c.state === filter);
}

export function canCreateCommit(
  summary: GitStatusSummary,
  commitMessage: string,
): boolean {
  return summary.stagedCount > 0 && summary.conflictCount === 0 && commitMessage.trim().length >= 3;
}
