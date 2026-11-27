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
  filterGitHubItems,
  summarizeGitHubWorkspace,
  type GitHubFilter,
  type GitHubWorkItem,
  type GitHubWorkspaceProjection,
} from "./model";

const INITIAL_WORKSPACE: GitHubWorkspaceProjection = {
  repo: "tao3k/marlin-mygo",
  repoUrl: "https://github.com/tao3k/marlin-mygo",
  defaultBranch: "main",
  items: [
    {
      number: 1,
      kind: "pr",
      title: "High-performance modular Marlin MyGo UI engine, plugin ecosystem & Quint gate",
      author: "tao3k",
      branch: "feat/marlin-mygo-frontend-engine",
      reviewRequested: true,
      ciState: "passing",
      labels: ["ui-engine", "marketplace"],
      updatedAgo: "just now",
    },
    {
      number: 12,
      kind: "pr",
      title: "Official UI Marketplace 3-stream sync (Plugins, Themes, Emacs Layouts)",
      author: "tao3k",
      branch: "feat/official-marketplace-sync",
      reviewRequested: true,
      ciState: "running",
      labels: ["marketplace", "security"],
      updatedAgo: "5m ago",
    },
    {
      number: 24,
      kind: "issue",
      title: "Community submission: add Diff & Blame gutter lens for Git Workbench",
      author: "community-user",
      reviewRequested: false,
      ciState: "none",
      labels: ["community-plugin", "developer-tools"],
      updatedAgo: "42m ago",
    },
  ],
};

function parseGitHubWorkspace(raw: unknown): GitHubWorkspaceProjection | undefined {
  const r = raw as Partial<GitHubWorkspaceProjection> | null;
  if (r && typeof r.repo === "string" && Array.isArray(r.items)) {
    return {
      repo: r.repo,
      repoUrl: typeof r.repoUrl === "string" ? r.repoUrl : undefined,
      defaultBranch: r.defaultBranch ?? "main",
      items: r.items,
    };
  }
  return undefined;
}

const FILTERS = [
  { key: "all", label: "All" },
  { key: "prs", label: "PRs" },
  { key: "review", label: "Review" },
  { key: "failing", label: "CI Failing" },
  { key: "issues", label: "Issues" },
] as const;

function ciTone(state: GitHubWorkItem["ciState"]): SemanticTone {
  if (state === "passing") return "success";
  if (state === "failing") return "danger";
  if (state === "running") return "accent";
  return "info";
}

const GitHubWorkRow = memo(function GitHubWorkRow({
  item,
  onCheckout,
  onRerunChecks,
  onApprove,
  onTriage,
}: {
  item: GitHubWorkItem;
  onCheckout: (it: GitHubWorkItem) => void;
  onRerunChecks: (it: GitHubWorkItem) => void;
  onApprove: (it: GitHubWorkItem) => void;
  onTriage: (it: GitHubWorkItem) => void;
}) {
  const tone = item.kind === "pr" ? ciTone(item.ciState) : "info";
  return (
    <div className={`m-item-card tone-${tone} m-row-gap`} style={{ justifyContent: "space-between" }}>
      <div>
        <div className="m-row-gap" style={{ marginBottom: 3 }}>
          <StatusBadge tone={tone}>
            {item.kind === "pr" ? `PR #${item.number}` : `ISSUE #${item.number}`}
          </StatusBadge>
          <strong>{item.title}</strong>
        </div>
        <div className="m-caption m-mono">
          @{item.author} · {item.updatedAgo}
          {item.branch ? ` · ${item.branch}` : ""}
          {item.kind === "pr" ? ` · CI:${item.ciState}` : ""}
        </div>
      </div>
      <div className="m-row-gap">
        {item.kind === "pr" ? (
          <>
            {item.branch && (
              <button className="m-btn" type="button" onClick={() => onCheckout(item)}>
                Checkout
              </button>
            )}
            {item.ciState === "failing" && (
              <button className="m-btn m-btn-soft-warning" type="button" onClick={() => onRerunChecks(item)}>
                Re-run CI
              </button>
            )}
            {item.reviewRequested && (
              <button className="m-btn m-btn-primary" type="button" onClick={() => onApprove(item)}>
                Review
              </button>
            )}
          </>
        ) : (
          <button className="m-btn" type="button" onClick={() => onTriage(item)}>
            Triage
          </button>
        )}
      </div>
    </div>
  );
});

export function GitHubWorkbenchApp() {
  const ghProj = useMarlinProjection("github.workspace", INITIAL_WORKSPACE, parseGitHubWorkspace);
  const layout = useMarlinLayout();
  const connectAct = useMarlinAction("github.repo.connect");
  const reviewAct = useMarlinAction("github.pr.review");
  const checkoutAct = useMarlinAction("github.pr.checkout");
  const rerunAct = useMarlinAction("github.checks.rerun");
  const triageAct = useMarlinAction("github.issue.triage");

  const [filter, setFilter] = useState<GitHubFilter>("all");
  const [repoInput, setRepoInput] = useState("");
  const [toast, setToast] = useState("");

  const summary = useMemo(() => summarizeGitHubWorkspace(ghProj.data), [ghProj.data]);
  const visibleItems = useMemo(
    () => filterGitHubItems(ghProj.data.items, filter),
    [ghProj.data.items, filter],
  );
  const stats = useMemo(
    () => [
      { label: "Open PRs", value: summary.openPRs },
      { label: "Needs Review", value: summary.reviewRequested },
      { label: "CI Failing", value: summary.failingChecks },
      { label: "Issues", value: summary.openIssues },
    ],
    [summary],
  );

  const handleCheckout = useCallback(
    (it: GitHubWorkItem) => {
      void checkoutAct.submit({ pr: it.number, branch: it.branch ?? "" }).then((r) => {
        if (r.admitted) setToast(`Checked out PR #${it.number} (${it.branch ?? ""})`);
      });
    },
    [checkoutAct],
  );

  const handleRerunChecks = useCallback(
    (it: GitHubWorkItem) => {
      void rerunAct.submit({ pr: it.number }).then((r) => {
        if (r.admitted) setToast(`Triggered CI re-run for PR #${it.number}`);
      });
    },
    [rerunAct],
  );

  const handleApprove = useCallback(
    (it: GitHubWorkItem) => {
      void reviewAct.submit({ pr: it.number, event: "COMMENT" }).then((r) => {
        if (r.admitted) setToast(`Opened review workflow for PR #${it.number}`);
      });
    },
    [reviewAct],
  );

  const handleTriage = useCallback(
    (it: GitHubWorkItem) => {
      void triageAct.submit({ issue: it.number }).then((r) => {
        if (r.admitted) setToast(`Triaged issue #${it.number}`);
      });
    },
    [triageAct],
  );

  async function handleConnectRepo() {
    const trimmed = repoInput.trim();
    if (!trimmed) return;
    const r = await connectAct.submit({ repoUrl: trimmed });
    if (r.admitted) {
      setToast(`Mounted GitHub URL: ${trimmed}`);
      setRepoInput("");
    }
  }

  return (
    <PanelShell>
      <PanelHeader
        title="GitHub Workbench"
        caption={`${ghProj.data.repoUrl ?? ghProj.data.repo} · default ${ghProj.data.defaultBranch} · ${layout.perspective} (${layout.leafCount}p)`}
      />

      <StatStrip items={stats} />

      <div className="m-row-gap" style={{ marginBottom: 10 }}>
        <input
          className="m-input"
          style={{ flex: 1 }}
          placeholder="Mount GitHub URL (e.g. https://github.com/tao3k/marlin-ui-marketplace)..."
          value={repoInput}
          onChange={(e) => setRepoInput(e.target.value)}
        />
        <button
          className="m-btn"
          type="button"
          disabled={!repoInput.trim() || connectAct.pending}
          onClick={() => void handleConnectRepo()}
        >
          Mount URL
        </button>
      </div>

      <div style={{ marginBottom: 12 }}>
        <SegmentedControl options={FILTERS} value={filter} onChange={setFilter} />
      </div>

      {visibleItems.map((it) => (
        <GitHubWorkRow
          key={`${it.kind}-${it.number}`}
          item={it}
          onCheckout={handleCheckout}
          onRerunChecks={handleRerunChecks}
          onApprove={handleApprove}
          onTriage={handleTriage}
        />
      ))}

      <ToastBanner message={toast} onDismiss={() => setToast("")} />
    </PanelShell>
  );
}

const el = typeof document !== "undefined" ? document.getElementById("root") : null;
if (el) {
  createRoot(el).render(
    <MarlinPanelProvider pluginId="tao3k.github-workbench">
      <GitHubWorkbenchApp />
    </MarlinPanelProvider>,
  );
}
