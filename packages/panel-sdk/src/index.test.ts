// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { describe, expect, test } from "bun:test";
import {
  ACTION_ENDPOINT,
  PANEL_EVENT_CHANNEL,
  createPanelClient,
  parseThemeTokens,
  type MyGoEventBridge,
  type ScopedPanelEvent,
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
});
