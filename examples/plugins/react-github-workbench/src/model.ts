// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

export type GitHubItemKind = "pr" | "issue";
export type GitHubCIState = "passing" | "running" | "failing" | "none";
export type GitHubFilter = "all" | "prs" | "review" | "failing" | "issues";

export interface GitHubWorkItem {
  number: number;
  kind: GitHubItemKind;
  title: string;
  author: string;
  branch?: string;
  reviewRequested: boolean;
  ciState: GitHubCIState;
  labels: string[];
  updatedAgo: string;
}

export interface GitHubWorkspaceProjection {
  repo: string;
  defaultBranch: string;
  items: GitHubWorkItem[];
}

export interface GitHubWorkspaceSummary {
  openPRs: number;
  openIssues: number;
  reviewRequested: number;
  failingChecks: number;
}

export function summarizeGitHubWorkspace(
  proj: GitHubWorkspaceProjection,
): GitHubWorkspaceSummary {
  let openPRs = 0;
  let openIssues = 0;
  let reviewRequested = 0;
  let failingChecks = 0;

  for (const item of proj.items) {
    if (item.kind === "pr") {
      openPRs++;
      if (item.reviewRequested) reviewRequested++;
      if (item.ciState === "failing") failingChecks++;
    } else {
      openIssues++;
    }
  }
  return { openPRs, openIssues, reviewRequested, failingChecks };
}

export function filterGitHubItems(
  items: readonly GitHubWorkItem[],
  filter: GitHubFilter,
): GitHubWorkItem[] {
  switch (filter) {
    case "prs":
      return items.filter((i) => i.kind === "pr");
    case "review":
      return items.filter((i) => i.kind === "pr" && i.reviewRequested);
    case "failing":
      return items.filter((i) => i.ciState === "failing");
    case "issues":
      return items.filter((i) => i.kind === "issue");
    default:
      return [...items];
  }
}
