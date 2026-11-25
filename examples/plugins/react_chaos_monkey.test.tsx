// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { Window } from "happy-dom";
import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { MyGoEventBridge, ScopedEvent } from "@tao3k/marlin-panel-sdk";
import { MarlinPanelProvider } from "@tao3k/marlin-panel-sdk/react";
import { OrgAgendaPlannerApp } from "./ts-org-agenda-planner/src/main";
import { AIMemoryInboxApp } from "./react-ai-memory-inbox/src/main";
import { AIWorkflowCockpitApp } from "./react-ai-workflow-cockpit/src/main";

// Enable React 19 testing act() environment.
(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

let domWin: Window;
let origGlobals: Record<string, unknown> = {};

beforeAll(() => {
  domWin = new Window({ url: "http://localhost/" });
  const keys = [
    "window",
    "document",
    "navigator",
    "HTMLElement",
    "HTMLButtonElement",
    "HTMLInputElement",
    "HTMLSelectElement",
    "HTMLTextAreaElement",
    "Event",
    "MouseEvent",
    "KeyboardEvent",
    "Node",
    "getComputedStyle",
  ] as const;
  for (const k of keys) {
    origGlobals[k] = (globalThis as Record<string, unknown>)[k];
    (globalThis as Record<string, unknown>)[k] = (domWin as unknown as Record<string, unknown>)[k];
  }
});

afterAll(() => {
  for (const [k, v] of Object.entries(origGlobals)) {
    if (v === undefined) {
      delete (globalThis as Record<string, unknown>)[k];
    } else {
      (globalThis as Record<string, unknown>)[k] = v;
    }
  }
  domWin.close();
});

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

interface TrackedBridge extends MyGoEventBridge {
  activeListeners: number;
  emit: (ev: ScopedEvent<unknown>) => void;
}

function createTrackedBridge(): TrackedBridge {
  const listeners = new Set<(payload: unknown) => void>();
  return {
    get activeListeners() {
      return listeners.size;
    },
    on(_eventName: string, cb: (payload: unknown) => void) {
      listeners.add(cb);
      return () => {
        listeners.delete(cb);
      };
    },
    emit(ev: ScopedEvent<unknown>) {
      for (const cb of listeners) {
        cb(ev);
      }
    },
  };
}

const mockFetch: typeof fetch = (async (_input: unknown, _init?: RequestInit) => {
  return new Response(
    JSON.stringify({
      admitted: true,
      receiptId: "rcpt-chaos-ok",
      result: { status: "admitted" },
    }),
    { status: 200, headers: { "Content-Type": "application/json" } },
  );
}) as typeof fetch;

interface ChaosSummary {
  ops: number;
  p50Us: number;
  p95Us: number;
  maxUs: number;
  dropped60Hz: number;
}

function summarize(samplesUs: number[]): ChaosSummary {
  const cp = [...samplesUs].sort((a, b) => a - b);
  const p50Us = cp[Math.floor(cp.length * 0.5)] ?? 0;
  const p95Us = cp[Math.min(cp.length - 1, Math.ceil(cp.length * 0.95) - 1)] ?? 0;
  const maxUs = cp[cp.length - 1] ?? 0;
  const dropped60Hz = cp.filter((us) => us > 16667).length;
  return { ops: cp.length, p50Us, p95Us, maxUs, dropped60Hz };
}

function mountPanel(
  pluginId: string,
  component: () => ReturnType<typeof createElement>,
  bridge: TrackedBridge,
): { container: HTMLElement; root: Root } {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(
      createElement(
        MarlinPanelProvider,
        { pluginId, fetchImpl: mockFetch, bridge },
        createElement(component),
      ),
    );
  });
  return { container, root };
}

function unmountPanel(container: HTMLElement, root: Root) {
  act(() => {
    root.unmount();
  });
  container.remove();
}

async function runLiveDomChaos(
  name: string,
  pluginId: string,
  component: () => ReturnType<typeof createElement>,
  totalOps: number,
  seed: number,
  onTopicPulse?: (bridge: TrackedBridge, step: number) => void,
): Promise<ChaosSummary> {
  const bridge = createTrackedBridge();
  const { container, root } = mountPanel(pluginId, component, bridge);
  const rand = mulberry32(seed);
  const samplesUs: number[] = [];

  try {
    // Warmup 5 clicks to stabilize JIT before measuring.
    for (let w = 0; w < 5; w++) {
      const btns = Array.from(container.querySelectorAll("button")).filter((b) => !b.disabled);
      if (btns.length > 0) {
        await act(async () => {
          btns[w % btns.length]!.click();
        });
      }
    }

    for (let step = 0; step < totalOps; step++) {
      const t0 = performance.now();
      const roll = rand();
      await act(async () => {
        if (onTopicPulse && roll > 0.9) {
          onTopicPulse(bridge, step);
          return;
        }
        const btns = Array.from(container.querySelectorAll("button")).filter((b) => !b.disabled);
        const inputs = Array.from(container.querySelectorAll("input[type='text'], input:not([type])"));
        if (roll < 0.82 && btns.length > 0) {
          const target = btns[Math.floor(rand() * btns.length)]!;
          target.click();
        } else if (inputs.length > 0) {
          const inp = inputs[Math.floor(rand() * inputs.length)] as HTMLInputElement;
          const nativeSetter = Object.getOwnPropertyDescriptor(
            window.HTMLInputElement.prototype,
            "value",
          )?.set;
          nativeSetter?.call(inp, step % 3 === 0 ? "" : `chaos-${step}`);
          inp.dispatchEvent(new window.Event("input", { bubbles: true }));
        } else if (btns.length > 0) {
          btns[0]!.click();
        }
      });
      samplesUs.push((performance.now() - t0) * 1000);
    }

    expect(container.textContent?.length ?? 0).toBeGreaterThan(20);
  } finally {
    unmountPanel(container, root);
  }

  // Zero lingering TopicBus subscriptions after unmount.
  expect(bridge.activeListeners).toBe(0);

  const summary = summarize(samplesUs);
  console.log(
    `  [CHAOS PASS] ${name.padEnd(32)} ops=${summary.ops}  p50=${summary.p50Us.toFixed(1).padStart(7)}us  p95=${summary.p95Us.toFixed(1).padStart(7)}us  max=${summary.maxUs.toFixed(1).padStart(7)}us  dropped60Hz=${summary.dropped60Hz}`,
  );
  return summary;
}

function envChaosOps(fallback: number): number {
  const raw = process.env.MARLIN_CHAOS_OPS;
  if (raw) {
    const n = Number.parseInt(raw, 10);
    if (Number.isFinite(n) && n > 0) return n;
  }
  return fallback;
}

describe("React 19 Live DOM Chaos Monkey & Fluent Interaction Suite", () => {
  test("OrgAgendaPlannerApp survives 2,000 rapid live DOM chaos clicks with 0 leaks & <8ms p95", async () => {
    const ops = envChaosOps(2000);
    const s = await runLiveDomChaos(
      "React19.Chaos.OrgAgenda",
      "tao3k.org-agenda-planner",
      OrgAgendaPlannerApp,
      ops,
      0x11223344,
      (bridge, step) => {
        bridge.emit({
          eventId: `chaos-ev-${step}`,
          pluginId: "tao3k.org-agenda-planner",
          activationGeneration: "1",
          topic: "session.summary",
          payload: { sessionId: "chaos-sess", turnOrdinal: step },
        });
      },
    );
    expect(s.p95Us).toBeLessThan(8000);
    expect(s.dropped60Hz).toBeLessThanOrEqual(Math.max(2, Math.floor(ops * 0.005)));
  }, 30000);

  test("AIMemoryInboxApp survives 1,500 rapid live DOM chaos clicks & search bursts", async () => {
    const ops = envChaosOps(1500);
    const s = await runLiveDomChaos(
      "React19.Chaos.AIMemoryInbox",
      "tao3k.ai-memory-inbox",
      AIMemoryInboxApp,
      ops,
      0x55667788,
    );
    expect(s.p95Us).toBeLessThan(8000);
    expect(s.dropped60Hz).toBeLessThanOrEqual(Math.max(2, Math.floor(ops * 0.005)));
  }, 30000);

  test("AIWorkflowCockpitApp survives 1,500 rapid live DOM chaos preset/filter/gate clicks", async () => {
    const ops = envChaosOps(1500);
    const s = await runLiveDomChaos(
      "React19.Chaos.AIWorkflowCockpit",
      "tao3k.ai-workflow-cockpit",
      AIWorkflowCockpitApp,
      ops,
      0x99aabbcc,
    );
    expect(s.p95Us).toBeLessThan(8000);
    expect(s.dropped60Hz).toBeLessThanOrEqual(Math.max(2, Math.floor(ops * 0.005)));
  }, 30000);

  test("60 repeated mount -> rapid click -> unmount cycles across all 3 React 19 panels leak 0 listeners or DOM nodes", async () => {
    const bridge = createTrackedBridge();
    const panels = [
      { id: "tao3k.org-agenda-planner", comp: OrgAgendaPlannerApp },
      { id: "tao3k.ai-memory-inbox", comp: AIMemoryInboxApp },
      { id: "tao3k.ai-workflow-cockpit", comp: AIWorkflowCockpitApp },
    ];
    const baseBodyChildren = document.body.children.length;
    for (let cycle = 0; cycle < 60; cycle++) {
      const p = panels[cycle % panels.length]!;
      const { container, root } = mountPanel(p.id, p.comp, bridge);
      const btns = Array.from(container.querySelectorAll("button")).filter((b) => !b.disabled);
      await act(async () => {
        for (let k = 0; k < Math.min(8, btns.length); k++) {
          btns[k]!.click();
        }
      });
      unmountPanel(container, root);
      expect(bridge.activeListeners).toBe(0);
      expect(document.body.children.length).toBe(baseBodyChildren);
    }
  });
});
