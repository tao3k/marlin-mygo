// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

/**
 * Thin zero-authority TypeScript SDK for hot-pluggable Marlin panels running
 * inside MyGo's PageOptions{Untrusted: true} WebView under marlin-plugin://<id>/.
 *
 * Security invariant: panels never see host gRPC tokens or mygo.Bind methods.
 * Events arrive exclusively via host EmitPage("marlin:panel-event", ...), and
 * actions cross via same-origin POST /__marlin/action verified by CallerPage(ctx).
 */

export type PluginId = string & { readonly __brand: "PluginId" };
export type SlotId = string & { readonly __brand: "SlotId" };
export type EventCursor = string & { readonly __brand: "EventCursor" };
/** 64-bit unsigned wire integers encoded as decimal strings to avoid 2^53 loss. */
export type Uint64String = string;

export const ACTION_ENDPOINT = "/__marlin/action";
export const PANEL_EVENT_CHANNEL = "marlin:panel-event";

// Re-export generated canonical Protobuf wire types (marlin.*.v1) so plugin
// authors get zero-drift strongly typed contracts directly from proto/.
export * from "./generated/wire_types";

// Re-export declarative plugin performance-budget test macros for human & LLM authors.
export * from "./perf";

export interface ScopedPanelEvent<T = unknown> {
  eventId: EventCursor;
  pluginId: string;
  activationGeneration: Uint64String;
  topic: string;
  payload: T;
}

export interface ScopedActionReceipt<R = unknown> {
  admitted: boolean;
  result?: R;
  receiptId?: string;
  denial?: string;
}

export interface MarlinThemeTokens {
  canvas: string;
  ink: string;
  bar: string;
  accent: string;
  info: string;
  success: string;
  warning: string;
  muted: string;
}

export interface MyGoEventBridge {
  on<T>(event: string, listener: (payload: T) => void): () => void;
}

export interface PanelClientOptions {
  pluginId: string;
  fetchImpl?: typeof fetch;
  bridge?: MyGoEventBridge;
}

export interface PanelClient {
  readonly pluginId: string;
  submitAction<TArgs = unknown, TResult = unknown>(
    action: string,
    args: TArgs,
    idempotencyKey?: string,
  ): Promise<ScopedActionReceipt<TResult>>;
  onEvent<T = unknown>(
    topic: string,
    listener: (ev: ScopedPanelEvent<T>) => void,
  ): () => void;
}

function defaultBridge(): MyGoEventBridge | undefined {
  const g = globalThis as unknown as { mygo?: MyGoEventBridge };
  return g.mygo;
}

function mintIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return `act-${crypto.randomUUID()}`;
  }
  return `act-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

export function createPanelClient(options: PanelClientOptions): PanelClient {
  if (!options.pluginId || !options.pluginId.includes(".")) {
    throw new Error(`Invalid namespaced pluginId: ${options.pluginId}`);
  }
  const doFetch = options.fetchImpl ?? globalThis.fetch;
  const bridge = options.bridge ?? defaultBridge();

  return {
    pluginId: options.pluginId,

    async submitAction<TArgs = unknown, TResult = unknown>(
      action: string,
      args: TArgs,
      idempotencyKey = mintIdempotencyKey(),
    ): Promise<ScopedActionReceipt<TResult>> {
      if (!action.trim()) {
        throw new Error("Action name must be non-empty");
      }
      const res = await doFetch(ACTION_ENDPOINT, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ action, args, idempotencyKey }),
      });
      const receipt = (await res.json()) as ScopedActionReceipt<TResult>;
      if (!res.ok && receipt.admitted) {
        return { admitted: false, denial: `host rejected action with HTTP ${res.status}` };
      }
      return receipt;
    },

    onEvent<T = unknown>(
      topic: string,
      listener: (ev: ScopedPanelEvent<T>) => void,
    ): () => void {
      if (!bridge) {
        return () => {};
      }
      return bridge.on<ScopedPanelEvent<T>>(PANEL_EVENT_CHANNEL, (ev) => {
        if (ev.pluginId !== options.pluginId || ev.topic !== topic) {
          return;
        }
        listener(ev);
      });
    },
  };
}

export function parseThemeTokens(cssVars: Record<string, string>): MarlinThemeTokens {
  return {
    canvas: cssVars["--marlin-canvas"] ?? "#282c34",
    ink: cssVars["--marlin-ink"] ?? "#bbc2cf",
    bar: cssVars["--marlin-bar"] ?? "#1c1f24",
    accent: cssVars["--marlin-accent"] ?? "#c678dd",
    info: cssVars["--marlin-info"] ?? "#51afef",
    success: cssVars["--marlin-success"] ?? "#98be65",
    warning: cssVars["--marlin-warning"] ?? "#ecbe7b",
    muted: cssVars["--marlin-muted"] ?? "#73797e",
  };
}

export interface DefineMarlinPanelSpec {
  pluginId: string;
  fetchImpl?: typeof fetch;
  bridge?: MyGoEventBridge;
  events?: Record<string, (ev: ScopedPanelEvent<any>) => void>;
}

export interface MountedMarlinPanel {
  readonly client: PanelClient;
  submit<TArgs = unknown, TResult = unknown>(
    action: string,
    args: TArgs,
    idempotencyKey?: string,
  ): Promise<ScopedActionReceipt<TResult>>;
  dispose(): void;
}

/**
 * Ergonomic, zero-boilerplate entrypoint designed for human & LLM authors
 * building clean TypeScript Untrusted WebView panels.
 */
export function defineMarlinPanel(spec: DefineMarlinPanelSpec): MountedMarlinPanel {
  const client = createPanelClient({
    pluginId: spec.pluginId,
    fetchImpl: spec.fetchImpl,
    bridge: spec.bridge,
  });
  const unsubs: Array<() => void> = [];
  if (spec.events) {
    for (const [topic, handler] of Object.entries(spec.events)) {
      unsubs.push(client.onEvent(topic, handler));
    }
  }
  return {
    client,
    submit: (action, args, idempotencyKey) => client.submitAction(action, args, idempotencyKey),
    dispose() {
      for (const off of unsubs.splice(0)) off();
    },
  };
}
