// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

/**
 * TypeScript Panel SDK microbenchmark (run via `bun bench/ts/panel_bench.ts`).
 * Measures scoped event dispatch filtering throughput and theme token parsing
 * inside the Untrusted WebView plugin boundary.
 */

import {
  PANEL_EVENT_CHANNEL,
  createPanelClient,
  parseThemeTokens,
  type MyGoEventBridge,
} from "../../packages/panel-sdk/src/index";

function runBench(name: string, iterations: number, fn: () => void) {
  // Warmup
  for (let i = 0; i < Math.min(iterations / 10, 5000); i++) fn();

  const start = Bun.nanoseconds();
  for (let i = 0; i < iterations; i++) fn();
  const elapsedNs = Bun.nanoseconds() - start;
  const nsPerOp = elapsedNs / iterations;
  const opsPerSec = Math.round(1e9 / nsPerOp);
  console.log(
    `${name.padEnd(46)} ${iterations.toString().padStart(8)} iters  ${nsPerOp.toFixed(1).padStart(7)} ns/op  ${opsPerSec.toLocaleString().padStart(12)} ops/s`,
  );
}

let dispatch: ((ev: unknown) => void) | undefined;
const bridge: MyGoEventBridge = {
  on(event, listener) {
    if (event === PANEL_EVENT_CHANNEL) {
      dispatch = listener as (ev: unknown) => void;
    }
    return () => {
      dispatch = undefined;
    };
  },
};

const client = createPanelClient({ pluginId: "tao3k.org-kanban", bridge });
let delivered = 0;
client.onEvent("org.tasks", () => {
  delivered++;
});

const matchingEvent = {
  eventId: "ev-1",
  pluginId: "tao3k.org-kanban",
  activationGeneration: "1",
  topic: "org.tasks",
  payload: { count: 12 },
};

const foreignEvent = {
  eventId: "ev-2",
  pluginId: "other.plugin",
  activationGeneration: "1",
  topic: "org.tasks",
  payload: { count: 99 },
};

const cssVars = {
  "--marlin-canvas": "#282c34",
  "--marlin-ink": "#bbc2cf",
  "--marlin-bar": "#1c1f24",
  "--marlin-accent": "#c678dd",
  "--marlin-info": "#51afef",
  "--marlin-success": "#98be65",
  "--marlin-warning": "#ecbe7b",
  "--marlin-muted": "#73797e",
};

runBench("TS PanelSDK ScopedEvent (matching)", 500_000, () => {
  dispatch?.(matchingEvent);
});

runBench("TS PanelSDK ScopedEvent (foreign-dropped)", 500_000, () => {
  dispatch?.(foreignEvent);
});

runBench("TS PanelSDK parseThemeTokens (8 tokens)", 200_000, () => {
  parseThemeTokens(cssVars);
});

if (delivered === 0) {
  throw new Error("benchmark sanity check failed: no events delivered");
}
