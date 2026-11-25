// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

/**
 * Typed zero-dependency Web Worker client matching `tao3k/orgize-wasm`
 * (`orgize/worker.d.ts`).
 *
 * Allows Marlin TypeScript / React 19 plugins to parse local `.org` scratchpad
 * buffers directly in a browser Worker via `tao3k/orgize-wasm` when running
 * offline without duplicating a single line of Org-mode syntax parsing in JS.
 */

export interface OrgizeDateParts {
  year: number;
  month: number;
  day: number;
}

export interface OrgizeWorkerRequestPayload {
  command: "init" | "parse" | "update" | "projection" | "dispose" | "disposeAll";
  requestId?: string | number;
  sessionId?: string;
  source?: string;
  projection?: string;
  agendaView?: {
    start: OrgizeDateParts;
    end: OrgizeDateParts;
    limit?: number | null;
  };
  capturePlan?: unknown;
  memory?: Record<string, unknown>;
}

export interface OrgizeWorkerResultEnvelope<T = unknown> {
  type: "result";
  requestId?: string | number;
  sessionId: string;
  ok: true;
  result: T;
}

export interface OrgizeWorkerErrorEnvelope {
  type: "error";
  requestId?: string | number;
  sessionId: string;
  ok: false;
  error: {
    name: string;
    message: string;
    stack?: string;
  };
}

export type OrgizeWorkerEnvelope<T = unknown> =
  | OrgizeWorkerResultEnvelope<T>
  | OrgizeWorkerErrorEnvelope;

/** Minimal MessagePort/Worker surface compatible with browser Worker & tests. */
export interface OrgizeWorkerPort {
  postMessage(message: OrgizeWorkerRequestPayload): void;
  addEventListener(type: "message", listener: (ev: { data: OrgizeWorkerEnvelope }) => void): void;
  removeEventListener(type: "message", listener: (ev: { data: OrgizeWorkerEnvelope }) => void): void;
  terminate?: () => void;
}

export interface OrgizeWorkerClient {
  parseAgendaView<T = unknown>(source: string, dateISO: string, limit?: number): Promise<T>;
  parseClockRollup<T = unknown>(source: string): Promise<T>;
  parseMemory<T = unknown>(source: string): Promise<T>;
  planCapture<T = unknown>(source: string, captureRequest: unknown): Promise<T>;
  dispose(): void;
}

function parseDateISO(dateISO: string): OrgizeDateParts {
  const [y, m, d] = dateISO.split("-").map(Number);
  return {
    year: Number.isFinite(y) && y > 0 ? y : 2026,
    month: Number.isFinite(m) && m >= 1 && m <= 12 ? m : 1,
    day: Number.isFinite(d) && d >= 1 && d <= 31 ? d : 1,
  };
}

/**
 * Wraps a `tao3k/orgize-wasm` Worker (`new Worker(new URL("orgize/worker", import.meta.url), { type: "module" })`)
 * into typed Promise helpers with request correlation and clean disposal.
 */
export function createOrgizeWorkerClient(
  port: OrgizeWorkerPort,
  sessionId = "marlin-panel",
): OrgizeWorkerClient {
  let seq = 0;
  let disposed = false;
  const pending = new Map<
    string,
    { resolve: (value: unknown) => void; reject: (err: Error) => void }
  >();

  const onMessage = (ev: { data: OrgizeWorkerEnvelope }) => {
    const msg = ev.data;
    if (!msg || msg.requestId === undefined) return;
    const key = String(msg.requestId);
    const entry = pending.get(key);
    if (!entry) return;
    pending.delete(key);
    if (msg.ok) {
      entry.resolve(msg.result);
    } else {
      entry.reject(new Error(msg.error?.message || "orgize-wasm worker error"));
    }
  };

  port.addEventListener("message", onMessage);

  function call<T>(req: Omit<OrgizeWorkerRequestPayload, "requestId" | "sessionId">): Promise<T> {
    if (disposed) {
      return Promise.reject(new Error("orgize-wasm worker client has been disposed"));
    }
    const requestId = `orgize-${++seq}`;
    return new Promise<T>((resolve, reject) => {
      pending.set(requestId, {
        resolve: resolve as (v: unknown) => void,
        reject,
      });
      port.postMessage({ ...req, requestId, sessionId });
    });
  }

  return {
    parseAgendaView<T = unknown>(source: string, dateISO: string, limit = 100): Promise<T> {
      const d = parseDateISO(dateISO);
      return call<T>({
        command: "parse",
        source,
        projection: "agendaView",
        agendaView: { start: d, end: d, limit },
      });
    },

    parseClockRollup<T = unknown>(source: string): Promise<T> {
      return call<T>({
        command: "parse",
        source,
        projection: "clockRollup",
      });
    },

    parseMemory<T = unknown>(source: string): Promise<T> {
      return call<T>({
        command: "parse",
        source,
        projection: "memory",
        memory: {},
      });
    },

    planCapture<T = unknown>(source: string, captureRequest: unknown): Promise<T> {
      return call<T>({
        command: "parse",
        source,
        projection: "capturePlan",
        capturePlan: captureRequest,
      });
    },

    dispose(): void {
      if (disposed) return;
      disposed = true;
      port.removeEventListener("message", onMessage);
      for (const [, entry] of pending) {
        entry.reject(new Error("orgize-wasm worker client disposed"));
      }
      pending.clear();
      try {
        port.postMessage({ command: "dispose", sessionId });
      } catch {
        // Ignore postMessage failure during teardown.
      }
    },
  };
}
