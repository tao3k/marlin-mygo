// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { describe, expect, test } from "bun:test";
import {
  autoMountOrgizeWorker,
  buildOrgizeCaptureActionPayload,
  formatArtifactUrl,
  type OrgizeWorkerEnvelope,
  type OrgizeWorkerPort,
  type OrgizeWorkerRequestPayload,
} from "./index";

class FakeOrgizePort implements OrgizeWorkerPort {
  listeners = new Set<(ev: { data: OrgizeWorkerEnvelope }) => void>();
  sent: OrgizeWorkerRequestPayload[] = [];

  postMessage(message: OrgizeWorkerRequestPayload): void {
    this.sent.push(message);
    if (message.command === "parse") {
      const env: OrgizeWorkerEnvelope = {
        type: "result",
        requestId: message.requestId,
        sessionId: message.sessionId ?? "test",
        ok: true,
        result: { projection: message.projection, cards: [{ title: "Ship Wave F" }] },
      };
      for (const l of this.listeners) l({ data: env });
    }
  }

  addEventListener(_type: "message", listener: (ev: { data: OrgizeWorkerEnvelope }) => void): void {
    this.listeners.add(listener);
  }

  removeEventListener(_type: "message", listener: (ev: { data: OrgizeWorkerEnvelope }) => void): void {
    this.listeners.delete(listener);
  }
}

describe("orgize_worker autoMount & artifact URL helpers", () => {
  test("autoMountOrgizeWorker resolves global port cleanly and parses agendaView", async () => {
    const port = new FakeOrgizePort();
    (globalThis as Record<string, unknown>).__MARLIN_ORGIZE_WORKER__ = port;
    try {
      const client = autoMountOrgizeWorker({ sessionId: "agenda-test" });
      expect(client).not.toBeNull();
      const res = await client!.parseAgendaView<{ cards: Array<{ title: string }> }>(
        "* TODO Ship Wave F",
        "2026-11-25",
      );
      expect(res.cards[0]?.title).toBe("Ship Wave F");
      client!.dispose();
    } finally {
      delete (globalThis as Record<string, unknown>).__MARLIN_ORGIZE_WORKER__;
    }
  });

  test("autoMountOrgizeWorker returns null safely when offline without Worker", () => {
    delete (globalThis as Record<string, unknown>).__MARLIN_ORGIZE_WORKER__;
    delete (globalThis as Record<string, unknown>).__MARLIN_ORGIZE_WORKER_URL__;
    expect(autoMountOrgizeWorker()).toBeNull();
  });

  test("buildOrgizeCaptureActionPayload normalizes tags and rejects empty titles", () => {
    const payload = buildOrgizeCaptureActionPayload({
      title: "  Review Artifact CAS  ",
      tags: " :arch:, deep , ",
      timeBlock: "15:00-16:00",
    });
    expect(payload).toEqual({
      title: "Review Artifact CAS",
      kind: "task",
      tags: ["arch", "deep"],
      timeBlock: "15:00-16:00",
    });
    expect(() => buildOrgizeCaptureActionPayload({ title: "   " })).toThrow();
  });

  test("formatArtifactUrl validates 64-char sha256 digest", () => {
    const digest = "a".repeat(64);
    expect(formatArtifactUrl(digest.toUpperCase())).toBe(`marlin-artifact://${digest}/`);
    expect(() => formatArtifactUrl("../etc/passwd")).toThrow();
  });
});
