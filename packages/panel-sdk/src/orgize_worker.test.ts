// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { describe, expect, test } from "bun:test";
import {
  createOrgizeWorkerClient,
  type OrgizeWorkerEnvelope,
  type OrgizeWorkerPort,
  type OrgizeWorkerRequestPayload,
} from "./orgize_worker";

describe("@tao3k/marlin-panel-sdk orgize-wasm Worker client", () => {
  test("correlates agendaView, clockRollup, memory, and capturePlan requests and handles errors", async () => {
    const sent: OrgizeWorkerRequestPayload[] = [];
    const listeners = new Set<(ev: { data: OrgizeWorkerEnvelope }) => void>();

    const mockPort: OrgizeWorkerPort = {
      postMessage(msg) {
        sent.push(msg);
        if (msg.command === "dispose") return;
        const reply: OrgizeWorkerEnvelope =
          msg.source === "BROKEN"
            ? {
                type: "error",
                requestId: msg.requestId,
                sessionId: msg.sessionId ?? "test",
                ok: false,
                error: { name: "ParseError", message: "simulated worker failure" },
              }
            : {
                type: "result",
                requestId: msg.requestId,
                sessionId: msg.sessionId ?? "test",
                ok: true,
                result: { schemaVersion: 1, projection: msg.projection, cards: [{ title: "Ship P2" }] },
              };
        queueMicrotask(() => {
          for (const fn of listeners) fn({ data: reply });
        });
      },
      addEventListener(_type, listener) {
        listeners.add(listener);
      },
      removeEventListener(_type, listener) {
        listeners.delete(listener);
      },
    };

    const client = createOrgizeWorkerClient(mockPort, "agenda-scratch");
    const agenda = await client.parseAgendaView<{ schemaVersion: number; cards: Array<{ title: string }> }>(
      "* TODO Ship P2\nSCHEDULED: <2026-11-25 Wed>\n",
      "2026-11-25",
    );
    expect(agenda.schemaVersion).toBe(1);
    expect(agenda.cards[0].title).toBe("Ship P2");
    expect(sent[0].projection).toBe("agendaView");
    expect(sent[0].agendaView?.start).toEqual({ year: 2026, month: 11, day: 25 });

    const mem = await client.parseMemory<{ projection: string }>("* Memory note");
    expect(mem.projection).toBe("memory");

    await expect(client.parseClockRollup("BROKEN")).rejects.toThrow("simulated worker failure");

    client.dispose();
    expect(sent[sent.length - 1].command).toBe("dispose");
    await expect(client.parseMemory("* after dispose")).rejects.toThrow("disposed");
  });
});
