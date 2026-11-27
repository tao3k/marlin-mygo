// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { describe, expect, test } from "bun:test";
import {
  canCreateCommit,
  filterGitChanges,
  summarizeGitStatus,
  type GitStatusProjection,
} from "./model";

const SAMPLE: GitStatusProjection = {
  repoName: "tao3k/marlin-mygo",
  branch: "feat/marlin-mygo-frontend-engine",
  upstream: "origin/feat/marlin-mygo-frontend-engine",
  ahead: 2,
  behind: 0,
  recentBranches: ["main", "feat/marlin-mygo-frontend-engine"],
  changes: [
    { path: "control/marketplace/official_sync.go", state: "staged", additions: 140, deletions: 4, hunkSummary: "+ValidateOfficialRegistryURL" },
    { path: "runtime/mygoshell/marketplace_view.go", state: "modified", additions: 88, deletions: 12, hunkSummary: "+3-stream tabs" },
    { path: "examples/plugins/react-git-workbench/src/main.tsx", state: "untracked", additions: 190, deletions: 0, hunkSummary: "new file" },
  ],
};

describe("react-git-workbench model", () => {
  test("summarizes staged, unstaged, conflicts, and line deltas", () => {
    const sum = summarizeGitStatus(SAMPLE);
    expect(sum.totalFiles).toBe(3);
    expect(sum.stagedCount).toBe(1);
    expect(sum.unstagedCount).toBe(2);
    expect(sum.conflictCount).toBe(0);
    expect(sum.totalAdditions).toBe(418);
    expect(sum.totalDeletions).toBe(16);
  });

  test("filters changes by stage state", () => {
    expect(filterGitChanges(SAMPLE.changes, "staged")).toHaveLength(1);
    expect(filterGitChanges(SAMPLE.changes, "modified")).toHaveLength(1);
    expect(filterGitChanges(SAMPLE.changes, "untracked")).toHaveLength(1);
    expect(filterGitChanges(SAMPLE.changes, "all")).toHaveLength(3);
  });

  test("gates commits on staged files, zero conflicts, and non-empty message", () => {
    const sum = summarizeGitStatus(SAMPLE);
    expect(canCreateCommit(sum, "feat: add git workbench")).toBe(true);
    expect(canCreateCommit(sum, "  ")).toBe(false);
    expect(canCreateCommit({ ...sum, stagedCount: 0 }, "feat: empty")).toBe(false);
    expect(canCreateCommit({ ...sum, conflictCount: 1 }, "feat: conflict")).toBe(false);
  });
});
