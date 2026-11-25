// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { describe, expect, test } from "bun:test";
import {
  filterGitHubItems,
  summarizeGitHubWorkspace,
  type GitHubWorkspaceProjection,
} from "./model";

const SAMPLE: GitHubWorkspaceProjection = {
  repo: "tao3k/marlin-mygo",
  defaultBranch: "main",
  items: [
    {
      number: 1,
      kind: "pr",
      title: "High-performance modular Marlin MyGo UI engine & plugin ecosystem",
      author: "tao3k",
      branch: "feat/marlin-mygo-frontend-engine",
      reviewRequested: true,
      ciState: "passing",
      labels: ["ui-engine", "security"],
      updatedAgo: "2m ago",
    },
    {
      number: 2,
      kind: "pr",
      title: "Add Nord Polar theme and Review Triage layout",
      author: "contributor",
      branch: "feat/nord-theme",
      reviewRequested: false,
      ciState: "failing",
      labels: ["marketplace"],
      updatedAgo: "9m ago",
    },
    {
      number: 14,
      kind: "issue",
      title: "Support community GitHub PR inline comment thread projection",
      author: "community-dev",
      reviewRequested: false,
      ciState: "none",
      labels: ["enhancement"],
      updatedAgo: "1h ago",
    },
  ],
};

describe("react-github-workbench model", () => {
  test("summarizes open PRs, issues, review requests, and failing CI checks", () => {
    const sum = summarizeGitHubWorkspace(SAMPLE);
    expect(sum.openPRs).toBe(2);
    expect(sum.openIssues).toBe(1);
    expect(sum.reviewRequested).toBe(1);
    expect(sum.failingChecks).toBe(1);
  });

  test("filters PRs, review queue, failing CI, and issues", () => {
    expect(filterGitHubItems(SAMPLE.items, "prs")).toHaveLength(2);
    expect(filterGitHubItems(SAMPLE.items, "review")).toHaveLength(1);
    expect(filterGitHubItems(SAMPLE.items, "failing")).toHaveLength(1);
    expect(filterGitHubItems(SAMPLE.items, "issues")).toHaveLength(1);
  });
});
