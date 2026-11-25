// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { memo, useCallback, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  MarlinPanelProvider,
  PanelHeader,
  PanelShell,
  SegmentedControl,
  StatStrip,
  StatusBadge,
  ToastBanner,
  useMarlinAction,
  useMarlinLayout,
  useMarlinProjection,
  type SemanticTone,
} from "@tao3k/marlin-panel-sdk/react";
import {
  canCreateCommit,
  filterGitChanges,
  summarizeGitStatus,
  type GitFileChange,
  type GitFilter,
  type GitStatusProjection,
} from "./model";

const INITIAL_STATUS: GitStatusProjection = {
  repoName: "tao3k/marlin-mygo",
  branch: "feat/marlin-mygo-frontend-engine",
  upstream: "origin/feat/marlin-mygo-frontend-engine",
  ahead: 1,
  behind: 0,
  recentBranches: ["feat/marlin-mygo-frontend-engine", "main"],
  changes: [
    {
      path: "control/marketplace/official_sync.go",
      state: "staged",
      additions: 214,
      deletions: 0,
      hunkSummary: "ValidateOfficialRegistryURL + ParseThemePackTOML + ParseLayoutPackJSON",
    },
    {
      path: "runtime/mygoshell/marketplace_view.go",
      state: "modified",
      additions: 132,
      deletions: 18,
      hunkSummary: "3-stream Plugins | Themes | Layouts switcher with live swatches",
    },
    {
      path: "engine/shell/keymap_profile.go",
      state: "untracked",
      additions: 64,
      deletions: 0,
      hunkSummary: "Dual-modal SPC Normal & Emacs Insert Which-Key hints",
    },
  ],
};

function parseGitStatus(raw: unknown): GitStatusProjection | undefined {
  const r = raw as Partial<GitStatusProjection> | null;
  if (r && typeof r.branch === "string" && Array.isArray(r.changes)) {
    return {
      repoName: r.repoName ?? "workspace",
      branch: r.branch,
      upstream: r.upstream ?? "",
      ahead: typeof r.ahead === "number" ? r.ahead : 0,
      behind: typeof r.behind === "number" ? r.behind : 0,
      recentBranches: Array.isArray(r.recentBranches) ? r.recentBranches : [r.branch],
      changes: r.changes,
    };
  }
  return undefined;
}

const FILTERS = [
  { key: "all", label: "All" },
  { key: "staged", label: "Staged" },
  { key: "modified", label: "Modified" },
  { key: "untracked", label: "Untracked" },
  { key: "conflicted", label: "Conflicts" },
] as const;

function fileTone(state: GitFileChange["state"]): SemanticTone {
  if (state === "staged") return "success";
  if (state === "conflicted") return "danger";
  if (state === "modified") return "warning";
  return "info";
}

const GitChangeRow = memo(function GitChangeRow({
  change,
  onStage,
  onUnstage,
}: {
  change: GitFileChange;
  onStage: (path: string) => void;
  onUnstage: (path: string) => void;
}) {
  const tone = fileTone(change.state);
  return (
    <div className={`m-item-card tone-${tone} m-row-gap`} style={{ justifyContent: "space-between" }}>
      <div>
        <div className="m-row-gap" style={{ marginBottom: 3 }}>
          <StatusBadge tone={tone}>{change.state}</StatusBadge>
          <strong className="m-mono">{change.path}</strong>
        </div>
        <div className="m-caption m-mono">
          +{change.additions} -{change.deletions} · {change.hunkSummary}
        </div>
      </div>
      <div className="m-row-gap">
        {change.state === "staged" ? (
          <button className="m-btn" type="button" onClick={() => onUnstage(change.path)}>
            Unstage
          </button>
        ) : (
          <button className="m-btn m-btn-primary" type="button" onClick={() => onStage(change.path)}>
            Stage
          </button>
        )}
      </div>
    </div>
  );
});

export function GitWorkbenchApp() {
  const gitProj = useMarlinProjection("git.status", INITIAL_STATUS, parseGitStatus);
  const layout = useMarlinLayout();
  const stageAct = useMarlinAction("git.file.stage");
  const unstageAct = useMarlinAction("git.file.unstage");
  const commitAct = useMarlinAction("git.commit.create");
  const checkoutAct = useMarlinAction("git.branch.checkout");

  const [filter, setFilter] = useState<GitFilter>("all");
  const [commitMsg, setCommitMsg] = useState("");
  const [toast, setToast] = useState("");

  const summary = useMemo(() => summarizeGitStatus(gitProj.data), [gitProj.data]);
  const visibleChanges = useMemo(
    () => filterGitChanges(gitProj.data.changes, filter),
    [gitProj.data.changes, filter],
  );
  const commitReady = canCreateCommit(summary, commitMsg);

  const stats = useMemo(
    () => [
      { label: "Staged", value: summary.stagedCount },
      { label: "Unstaged", value: summary.unstagedCount },
      { label: "Delta", value: `+${summary.totalAdditions} / -${summary.totalDeletions}` },
      { label: "Sync", value: `↑${gitProj.data.ahead} ↓${gitProj.data.behind}` },
    ],
    [summary, gitProj.data.ahead, gitProj.data.behind],
  );

  const handleStage = useCallback(
    (path: string) => {
      void stageAct.submit({ path }).then((r) => {
        if (r.admitted) setToast(`Staged ${path}`);
      });
    },
    [stageAct],
  );

  const handleUnstage = useCallback(
    (path: string) => {
      void unstageAct.submit({ path }).then((r) => {
        if (r.admitted) setToast(`Unstaged ${path}`);
      });
    },
    [unstageAct],
  );

  async function handleCommit() {
    if (!commitReady) return;
    const r = await commitAct.submit({ message: commitMsg.trim() });
    if (r.admitted) {
      setToast(`Commit queued through host policy: "${commitMsg.trim()}"`);
      setCommitMsg("");
    }
  }

  async function handleCheckout(branch: string) {
    const r = await checkoutAct.submit({ branch });
    if (r.admitted) {
      setToast(`Switched branch to ${branch}`);
    }
  }

  return (
    <PanelShell>
      <PanelHeader
        title="Git Workbench"
        caption={`${gitProj.data.repoName} · ${gitProj.data.branch} · ${layout.perspective} (${layout.leafCount}p)`}
        actions={
          <div className="m-row-gap">
            {gitProj.data.recentBranches.slice(0, 2).map((br) => (
              <button
                key={br}
                type="button"
                className={`m-btn ${br === gitProj.data.branch ? "m-btn-primary" : ""}`}
                onClick={() => void handleCheckout(br)}
              >
                {br}
              </button>
            ))}
          </div>
        }
      />

      <StatStrip items={stats} />

      <div className="m-row-gap" style={{ marginBottom: 12 }}>
        <input
          className="m-input"
          style={{ flex: 1 }}
          placeholder="Commit message (host-gated; requires staged changes)..."
          value={commitMsg}
          onChange={(e) => setCommitMsg(e.target.value)}
        />
        <button
          className="m-btn m-btn-primary"
          type="button"
          disabled={!commitReady || commitAct.pending}
          onClick={() => void handleCommit()}
        >
          Commit ({summary.stagedCount})
        </button>
      </div>

      <div style={{ marginBottom: 12 }}>
        <SegmentedControl options={FILTERS} value={filter} onChange={setFilter} />
      </div>

      {visibleChanges.map((ch) => (
        <GitChangeRow
          key={ch.path}
          change={ch}
          onStage={handleStage}
          onUnstage={handleUnstage}
        />
      ))}

      <ToastBanner message={toast} onDismiss={() => setToast("")} />
    </PanelShell>
  );
}

const el = typeof document !== "undefined" ? document.getElementById("root") : null;
if (el) {
  createRoot(el).render(
    <MarlinPanelProvider pluginId="tao3k.git-workbench">
      <GitWorkbenchApp />
    </MarlinPanelProvider>,
  );
}
