// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { describe, expect, test } from "bun:test";
import {
  ACTION_ENDPOINT,
  PANEL_EVENT_CHANNEL,
  assertPerfBudget,
  createPanelClient,
  defineMarlinPanel,
  definePluginPerfSuite,
  parseThemeTokens,
  type MyGoEventBridge,
  type PluginV1PanelRuntimeKind,
  type ScopedPanelEvent,
  type UiV1SlotDeclaration,
} from "./index";

describe("@tao3k/marlin-panel-sdk", () => {
  test("submits scoped action over relative marlin-plugin endpoint", async () => {
    let seenUrl = "";
    let seenBody = "";
    const fakeFetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
      seenUrl = String(input);
      seenBody = String(init?.body ?? "");
      return new Response(
        JSON.stringify({ admitted: true, receiptId: "rcpt-42", result: { moved: true } }),
        { status: 200, headers: { "content-type": "application/json" } },
      );
    }) as typeof fetch;

    const client = createPanelClient({
      pluginId: "tao3k.org-kanban",
      fetchImpl: fakeFetch,
    });

    const receipt = await client.submitAction("org.task.move", { id: "T-7" }, "idem-1");
    expect(seenUrl).toBe(ACTION_ENDPOINT);
    expect(JSON.parse(seenBody)).toEqual({
      action: "org.task.move",
      args: { id: "T-7" },
      idempotencyKey: "idem-1",
    });
    expect(receipt.admitted).toBe(true);
    expect(receipt.receiptId).toBe("rcpt-42");
  });

  test("filters incoming events strictly by pluginId and topic", () => {
    let handler: ((ev: unknown) => void) | undefined;
    const bridge: MyGoEventBridge = {
      on(event, listener) {
        if (event === PANEL_EVENT_CHANNEL) {
          handler = listener as (ev: unknown) => void;
        }
        return () => {
          handler = undefined;
        };
      },
    };

    const client = createPanelClient({ pluginId: "tao3k.org-kanban", bridge });
    const received: ScopedPanelEvent<{ count: number }>[] = [];
    const off = client.onEvent<{ count: number }>("org.tasks", (ev) => received.push(ev));

    // Wrong plugin -> ignored
    handler?.({
      eventId: "e1",
      pluginId: "other.plugin",
      activationGeneration: "1",
      topic: "org.tasks",
      payload: { count: 99 },
    });
    // Wrong topic -> ignored
    handler?.({
      eventId: "e2",
      pluginId: "tao3k.org-kanban",
      activationGeneration: "1",
      topic: "workspace.diff",
      payload: { count: 88 },
    });
    // Exact match -> delivered
    handler?.({
      eventId: "e3",
      pluginId: "tao3k.org-kanban",
      activationGeneration: "1",
      topic: "org.tasks",
      payload: { count: 3 },
    });

    expect(received).toHaveLength(1);
    expect(received[0].payload.count).toBe(3);
    off();
  });

  test("parses injected CSS theme tokens with safe Marlin defaults", () => {
    const tokens = parseThemeTokens({ "--marlin-accent": "#ff00aa" });
    expect(tokens.accent).toBe("#ff00aa");
    expect(tokens.canvas).toBe("#282c34");
  });

  test("exports zero-drift generated Protobuf wire contracts", () => {
    const runtime: PluginV1PanelRuntimeKind = "PANEL_RUNTIME_KIND_WEBVIEW_UNTRUSTED";
    const slot: UiV1SlotDeclaration = {
      slotId: "workbench.main",
      cardinality: "SLOT_CARDINALITY_SINGLE",
      allowUntrustedPlugin: true,
    };
    expect(runtime).toBe("PANEL_RUNTIME_KIND_WEBVIEW_UNTRUSTED");
    expect(slot.slotId).toBe("workbench.main");
  });

  test("defineMarlinPanel binds scoped events and cleans up on dispose", async () => {
    let listener: ((ev: unknown) => void) | undefined;
    const bridge: MyGoEventBridge = {
      on(event, fn) {
        if (event === PANEL_EVENT_CHANNEL) listener = fn as (ev: unknown) => void;
        return () => {
          listener = undefined;
        };
      },
    };
    let count = 0;
    const panel = defineMarlinPanel({
      pluginId: "acme.demo-board",
      bridge,
      events: {
        "board.state": () => {
          count++;
        },
      },
    });
    listener?.({
      eventId: "e-1",
      pluginId: "acme.demo-board",
      activationGeneration: "1",
      topic: "board.state",
      payload: {},
    });
    expect(count).toBe(1);
    panel.dispose();
    expect(listener).toBeUndefined();
  });

  test("definePluginPerfSuite passes fast paths and flags slow regressions with remediation", () => {
    const suite = definePluginPerfSuite({
      pluginId: "acme.perf-demo",
      cases: [
        {
          name: "fast arithmetic",
          budgetUs: 500,
          iterations: 50,
          remediation: "keep loop O(1)",
          run: () => {
            let acc = 0;
            for (let i = 0; i < 16; i++) acc += i;
            if (acc < 0) throw new Error("unreachable");
          },
        },
        {
          name: "intentional slow burn",
          budgetUs: 0.001, // impossible 1ns ceiling -> deterministic negative control
          p95BudgetUs: 0.002,
          iterations: 15,
          remediation: "Replace O(N^2) inner scan with Map index",
          run: () => {
            let s = 0;
            for (let i = 0; i < 5000; i++) s += (i * 17) % 13;
            if (s < 0) throw new Error("unreachable");
          },
        },
      ],
    });
    const rep = suite.run();
    expect(rep.passed).toBe(false);
    expect(rep.cases[0].passed).toBe(true);
    expect(rep.cases[1].passed).toBe(false);
    expect(rep.cases[1].remediation).toContain("Map index");

    const ok = assertPerfBudget("inline fast check", 500, () => {}, "keep fast", { iterations: 20 });
    expect(ok.passed).toBe(true);
  });
});
