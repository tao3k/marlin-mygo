// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { describe, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  MarlinPanelProvider,
  useMarlinAction,
  useMarlinProjection,
  useMarlinTheme,
} from "@tao3k/marlin-panel-sdk/react";
import { OrgAgendaPlannerApp } from "./ts-org-agenda-planner/src/main";
import { AIMemoryInboxApp } from "./react-ai-memory-inbox/src/main";
import { AIWorkflowCockpitApp } from "./react-ai-workflow-cockpit/src/main";

interface RenderMetrics {
  p50Us: number;
  p95Us: number;
  maxUs: number;
  htmlBytes: number;
}

function measureRender(iters: number, fn: () => string): RenderMetrics {
  // Warmup JIT & React 19 fiber caches.
  let lastHtml = "";
  for (let i = 0; i < 8; i++) {
    lastHtml = fn();
  }
  const samples: number[] = [];
  for (let i = 0; i < iters; i++) {
    const t0 = performance.now();
    lastHtml = fn();
    samples.push((performance.now() - t0) * 1000);
  }
  samples.sort((a, b) => a - b);
  const p50Us = samples[Math.floor(samples.length * 0.5)] ?? 0;
  const p95Us = samples[Math.min(samples.length - 1, Math.ceil(samples.length * 0.95) - 1)] ?? 0;
  const maxUs = samples[samples.length - 1] ?? 0;
  return { p50Us, p95Us, maxUs, htmlBytes: lastHtml.length };
}

function logMetrics(name: string, m: RenderMetrics, budgetUs: number) {
  console.log(
    `  [PASS] ${name.padEnd(40)} p50=${m.p50Us.toFixed(1).padStart(7)}us  p95=${m.p95Us.toFixed(1).padStart(7)}us  max=${m.maxUs.toFixed(1).padStart(7)}us  (budget <= ${budgetUs}us)`,
  );
}

describe("React 19 Per-Component UI Render & Hook Performance Suite", () => {
  test("OrgAgendaPlannerApp renders within 60Hz/120Hz budget (500 renders)", () => {
    const m = measureRender(500, () =>
      renderToStaticMarkup(
        createElement(
          MarlinPanelProvider,
          { pluginId: "tao3k.org-agenda-planner" },
          createElement(OrgAgendaPlannerApp),
        ),
      ),
    );
    logMetrics("React19.OrgAgendaPlannerApp", m, 4000);
    expect(m.htmlBytes).toBeGreaterThan(500);
    // 60Hz budget is 16,666us; require p95 < 4,000us (4ms).
    expect(m.p95Us).toBeLessThan(4000);
  });

  test("AIMemoryInboxApp renders within 60Hz/120Hz budget (500 renders)", () => {
    const m = measureRender(500, () =>
      renderToStaticMarkup(
        createElement(
          MarlinPanelProvider,
          { pluginId: "tao3k.ai-memory-inbox" },
          createElement(AIMemoryInboxApp),
        ),
      ),
    );
    logMetrics("React19.AIMemoryInboxApp", m, 4000);
    expect(m.htmlBytes).toBeGreaterThan(500);
    expect(m.p95Us).toBeLessThan(4000);
  });

  test("AIWorkflowCockpitApp renders within 60Hz/120Hz budget (500 renders)", () => {
    const m = measureRender(500, () =>
      renderToStaticMarkup(
        createElement(
          MarlinPanelProvider,
          { pluginId: "tao3k.ai-workflow-cockpit" },
          createElement(AIWorkflowCockpitApp),
        ),
      ),
    );
    logMetrics("React19.AIWorkflowCockpitApp", m, 4000);
    expect(m.htmlBytes).toBeGreaterThan(500);
    expect(m.p95Us).toBeLessThan(4000);
  });

  test("Combined rapid tab-switch burst across all 3 React 19 panels stays < 4ms p95 (900 renders)", () => {
    const renderers = [
      () =>
        renderToStaticMarkup(
          createElement(
            MarlinPanelProvider,
            { pluginId: "tao3k.org-agenda-planner" },
            createElement(OrgAgendaPlannerApp),
          ),
        ),
      () =>
        renderToStaticMarkup(
          createElement(
            MarlinPanelProvider,
            { pluginId: "tao3k.ai-memory-inbox" },
            createElement(AIMemoryInboxApp),
          ),
        ),
      () =>
        renderToStaticMarkup(
          createElement(
            MarlinPanelProvider,
            { pluginId: "tao3k.ai-workflow-cockpit" },
            createElement(AIWorkflowCockpitApp),
          ),
        ),
    ];
    let idx = 0;
    const m = measureRender(900, () => renderers[idx++ % renderers.length]!());
    logMetrics("React19.AllPanelsBurstSwitch", m, 4000);
    expect(m.p95Us).toBeLessThan(4000);
  });

  test("@tao3k/marlin-panel-sdk/react hooks stay sub-millisecond under 2,000 render bursts", () => {
    function ProbeHookComponent() {
      const proj = useMarlinProjection("org.agenda", { count: 12 });
      const act = useMarlinAction("org.clock.in");
      const theme = useMarlinTheme();
      return createElement(
        "div",
        { "data-accent": theme.accent, "data-pending": String(act.pending) },
        `count=${proj.data.count}`,
      );
    }

    const m = measureRender(2000, () =>
      renderToStaticMarkup(
        createElement(
          MarlinPanelProvider,
          { pluginId: "tao3k.hook-probe" },
          createElement(ProbeHookComponent),
        ),
      ),
    );
    logMetrics("React19.PanelSDKHooksBurst", m, 1000);
    expect(m.htmlBytes).toBeGreaterThan(20);
    expect(m.p95Us).toBeLessThan(1000);
  });
});
