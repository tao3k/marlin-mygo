// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

/**
 * First-class React 19 Hooks adapter for Marlin Untrusted WebView panels.
 *
 * Keeps `@tao3k/marlin-panel-sdk` core zero-dependency while giving React 19
 * plugin authors ergonomic hooks backed by `ScopedProxy` and `TopicBus`
 * (`useMarlinProjection`, `useMarlinAction`, `useMarlinTheme`).
 */

import {
  createContext,
  createElement,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  useTransition,
  type ReactNode,
} from "react";
import {
  createPanelClient,
  parseThemeTokens,
  type MarlinThemeTokens,
  type MyGoEventBridge,
  type PanelClient,
  type ScopedActionReceipt,
} from "./index";

const MarlinPanelContext = createContext<PanelClient | null>(null);

export interface MarlinPanelProviderProps {
  pluginId: string;
  fetchImpl?: typeof fetch;
  bridge?: MyGoEventBridge;
  children: ReactNode;
}

/**
 * Provides a scoped `PanelClient` to React 19 plugin components using React 19's
 * native Context provider semantics.
 */
export function MarlinPanelProvider({
  pluginId,
  fetchImpl,
  bridge,
  children,
}: MarlinPanelProviderProps) {
  const client = useMemo(
    () => createPanelClient({ pluginId, fetchImpl, bridge }),
    [pluginId, fetchImpl, bridge],
  );
  return createElement(MarlinPanelContext, { value: client }, children);
}

/**
 * Accesses the nearest `PanelClient` inside a `MarlinPanelProvider`.
 */
export function useMarlinPanel(): PanelClient {
  const client = useContext(MarlinPanelContext);
  if (!client) {
    throw new Error("useMarlinPanel must be used inside <MarlinPanelProvider pluginId=...>");
  }
  return client;
}

export interface ProjectionState<T> {
  data: T;
  eventCount: number;
  lastEventId: string;
  setLocal: (updater: T | ((prev: T) => T)) => void;
}

/**
 * Subscribes to a manifest-authorized host `TopicBus` projection (e.g.
 * `org.agenda`, `org.clock`, `org.memory`, `workflow.run`, `session.summary`).
 *
 * Accepts an optional `parse` guard so invalid payloads never corrupt UI state.
 */
export function useMarlinProjection<T>(
  topic: string,
  initialValue: T,
  parse?: (raw: unknown) => T | undefined,
): ProjectionState<T> {
  const client = useMarlinPanel();
  const [data, setData] = useState<T>(initialValue);
  const [meta, setMeta] = useState({ eventCount: 0, lastEventId: "" });

  useEffect(() => {
    return client.onEvent<unknown>(topic, (ev) => {
      const next = parse ? parse(ev.payload) : (ev.payload as T);
      if (next === undefined) return;
      setData(next);
      setMeta((prev) => ({
        eventCount: prev.eventCount + 1,
        lastEventId: ev.eventId,
      }));
    });
  }, [client, topic, parse]);

  return {
    data,
    eventCount: meta.eventCount,
    lastEventId: meta.lastEventId,
    setLocal: setData,
  };
}

export interface MarlinActionHandle<TArgs, TResult> {
  submit: (args: TArgs, idempotencyKey?: string) => Promise<ScopedActionReceipt<TResult>>;
  pending: boolean;
  lastReceipt: ScopedActionReceipt<TResult> | null;
  error: string | null;
}

/**
 * Submits manifest-declared scoped actions through the host's `ScopedProxy`
 * (`POST /__marlin/action`) paired with React 19 `useTransition` pending state
 * and typed receipt tracking.
 */
export function useMarlinAction<TArgs = unknown, TResult = unknown>(
  action: string,
): MarlinActionHandle<TArgs, TResult> {
  const client = useMarlinPanel();
  const [isPending, startTransition] = useTransition();
  const [inflight, setInflight] = useState(false);
  const [lastReceipt, setLastReceipt] = useState<ScopedActionReceipt<TResult> | null>(null);
  const [error, setError] = useState<string | null>(null);

  const submit = useCallback(
    async (args: TArgs, idempotencyKey?: string): Promise<ScopedActionReceipt<TResult>> => {
      setInflight(true);
      setError(null);
      try {
        const receipt = await client.submitAction<TArgs, TResult>(action, args, idempotencyKey);
        startTransition(() => {
          setLastReceipt(receipt);
          if (!receipt.admitted) {
            setError(receipt.denial ?? "Action denied by host policy");
          }
        });
        return receipt;
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        const denied: ScopedActionReceipt<TResult> = { admitted: false, denial: msg };
        startTransition(() => {
          setLastReceipt(denied);
          setError(msg);
        });
        return denied;
      } finally {
        setInflight(false);
      }
    },
    [client, action],
  );

  return {
    submit,
    pending: inflight || isPending,
    lastReceipt,
    error,
  };
}

/**
 * Reads Marlin host-injected CSS custom properties (`--marlin-*`) with safe
 * fallback defaults for both live WKWebView and SSR/test environments.
 */
export function useMarlinTheme(overrides?: Record<string, string>): MarlinThemeTokens {
  return useMemo(() => {
    const vars: Record<string, string> = {};
    if (typeof document !== "undefined" && typeof getComputedStyle === "function") {
      const style = getComputedStyle(document.documentElement);
      const keys = [
        "--marlin-canvas",
        "--marlin-ink",
        "--marlin-bar",
        "--marlin-accent",
        "--marlin-info",
        "--marlin-success",
        "--marlin-warning",
        "--marlin-muted",
      ];
      for (const k of keys) {
        const v = style.getPropertyValue(k).trim();
        if (v) vars[k] = v;
      }
    }
    return parseThemeTokens({ ...vars, ...overrides });
  }, [overrides]);
}
