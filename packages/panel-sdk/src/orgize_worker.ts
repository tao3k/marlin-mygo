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
/**
 * Spawns a module Web Worker for `tao3k/orgize-wasm` when running in a browser
 * WebView and returns a typed `OrgizeWorkerClient` (plus registers it on
 * `globalThis.__MARLIN_ORGIZE_WORKER__` for plugin Scratchpad drawers).
 */
export function mountOrgizeWorker(
  workerUrl: string | URL,
  sessionId = "marlin-panel",
): OrgizeWorkerClient {
  if (typeof Worker !== "function") {
    throw new Error("Web Worker API is unavailable in this environment");
  }
  const worker = new Worker(workerUrl, { type: "module" }) as unknown as OrgizeWorkerPort;
  (globalThis as Record<string, unknown>).__MARLIN_ORGIZE_WORKER__ = worker;
  return createOrgizeWorkerClient(worker, sessionId);
}

/**
 * Resolves a pre-mounted `tao3k/orgize-wasm` Worker port from global scope if present.
 */
export function resolveGlobalOrgizeWorker(
  sessionId = "marlin-panel",
): OrgizeWorkerClient | null {
  const port = (globalThis as { __MARLIN_ORGIZE_WORKER__?: OrgizeWorkerPort }).__MARLIN_ORGIZE_WORKER__;
  if (!port) return null;
  return createOrgizeWorkerClient(port, sessionId);
}

export interface AutoMountOrgizeOptions {
  sessionId?: string;
  workerUrl?: string | URL;
}

/**
 * Resolves an already-mounted global `tao3k/orgize-wasm` Worker or spawns one
 * from `options.workerUrl` / `globalThis.__MARLIN_ORGIZE_WORKER_URL__` when
 * running in a WebView with Worker support. Never throws in offline/SSR tests.
 */
export function autoMountOrgizeWorker(
  options: AutoMountOrgizeOptions = {},
): OrgizeWorkerClient | null {
  const sessionId = options.sessionId ?? "marlin-panel";
  const existing = resolveGlobalOrgizeWorker(sessionId);
  if (existing) return existing;

  const globalUrl = (globalThis as { __MARLIN_ORGIZE_WORKER_URL__?: string | URL }).__MARLIN_ORGIZE_WORKER_URL__;
  const candidate = options.workerUrl ?? globalUrl;
  if (!candidate || typeof Worker !== "function") {
    return null;
  }
  try {
    return mountOrgizeWorker(candidate, sessionId);
  } catch {
    return null;
  }
}

export interface OrgizeCaptureProposalPayload {
  title: string;
  kind: "task" | "event" | "note";
  tags: string[];
  timeBlock?: string;
  dateISO?: string;
}

/** Builds a normalized, bounded capture action payload for `org.capture.plan`. */
export function buildOrgizeCaptureActionPayload(input: {
  title: string;
  kind?: "task" | "event" | "note";
  tags?: string[] | string;
  timeBlock?: string;
  dateISO?: string;
}): OrgizeCaptureProposalPayload {
  const title = input.title.trim().slice(0, 200);
  if (!title) {
    throw new Error("capture title must not be empty");
  }
  const rawTags = Array.isArray(input.tags)
    ? input.tags
    : typeof input.tags === "string"
      ? input.tags.split(",")
      : [];
  const tags = rawTags
    .map((t) => t.trim().replace(/^:/, "").replace(/:$/, ""))
    .filter((t) => t.length > 0)
    .slice(0, 12);
  const out: OrgizeCaptureProposalPayload = {
    title,
    kind: input.kind ?? "task",
    tags,
  };
  if (input.timeBlock?.trim()) out.timeBlock = input.timeBlock.trim();
  if (input.dateISO?.trim()) out.dateISO = input.dateISO.trim();
  return out;
}

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
