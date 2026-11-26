// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

/**
 * Vercel AI SDK v5/v6 UI Message Stream Adapter for Marlin's 3-Layer Framework.
 *
 * Bridges Layer 1 (Rust gRPC AppServer) -> Layer 2 (Pure-Go marlin-mygo/engine/uistream)
 * -> Layer 3 (Sandboxed Untrusted WebView panels) using 100% structurally compatible
 * Vercel AI SDK `UIMessage` / `UIMessagePart` types and a zero-dependency React 19
 * `useMarlinChat` hook backed by Marlin's `TopicBus` + `ScopedProxy`.
 */

import { useCallback, useEffect, useMemo, useState } from "react";
import { useMarlinPanel } from "./react";

export type UIMessageRole = "system" | "user" | "assistant";
export type ChatStatus = "ready" | "submitted" | "streaming" | "error";

export interface TextUIPart {
  type: "text";
  text: string;
  state?: "streaming" | "done";
}

export interface ReasoningUIPart {
  type: "reasoning";
  text: string;
  state?: "streaming" | "done";
}

export interface ToolUIPart {
  type: "tool-invocation" | `tool-${string}`;
  toolCallId: string;
  toolName?: string;
  state: "input-streaming" | "input-available" | "output-available" | "output-error";
  input?: unknown;
  output?: unknown;
  errorText?: string;
}

export interface SourceUrlUIPart {
  type: "source-url";
  sourceId: string;
  url: string;
  title?: string;
}

export interface FileUIPart {
  type: "file";
  mediaType: string;
  filename?: string;
  url: string;
}

export interface DataUIPart<T = unknown> {
  type: `data-${string}`;
  id?: string;
  data: T;
  transient?: boolean;
}

export type UIMessagePart =
  | TextUIPart
  | ReasoningUIPart
  | ToolUIPart
  | SourceUrlUIPart
  | FileUIPart
  | DataUIPart;

export interface UIMessage<TMetadata = Record<string, unknown>> {
  id: string;
  role: UIMessageRole;
  metadata?: TMetadata;
  parts: UIMessagePart[];
}

export type UIStreamChunk =
  | { type: "start"; messageId?: string }
  | { type: "text-start"; id: string }
  | { type: "text-delta"; id: string; delta: string }
  | { type: "text-end"; id: string }
  | { type: "reasoning-start"; id: string }
  | { type: "reasoning-delta"; id: string; delta: string }
  | { type: "reasoning-end"; id: string }
  | { type: "tool-input-available"; toolCallId: string; toolName: string; input?: unknown }
  | { type: "tool-output-available"; toolCallId: string; output?: unknown }
  | { type: "tool-output-error"; toolCallId: string; errorText: string }
  | { type: "source-url"; sourceId: string; url: string; title?: string }
  | { type: "file"; url: string; mediaType: string; filename?: string }
  | { type: `data-${string}`; id?: string; data: unknown; transient?: boolean }
  | { type: "finish" }
  | { type: "error"; errorText: string };

/** Parse one Vercel AI SDK UI Message Stream SSE line (`data: {...}`). */
export function parseVercelUIStreamLine(line: string): UIStreamChunk | null {
  let trimmed = line.trim();
  if (!trimmed || trimmed.startsWith(":")) return null;
  if (trimmed.startsWith("data:")) {
    trimmed = trimmed.slice(5).trim();
  }
  if (!trimmed || trimmed === "[DONE]") return null;
  const parsed = JSON.parse(trimmed) as Partial<UIStreamChunk>;
  if (!parsed || typeof parsed.type !== "string") {
    throw new Error("Invalid Vercel AI SDK UI stream chunk: missing type");
  }
  return parsed as UIStreamChunk;
}

/**
 * Splits streaming `<think>...</think>` or `<thinking>...</thinking>` blocks
 * from assistant prose so Reasoning drawers stay folded by default.
 */
export function splitThinkTags(raw: string): {
  thinking: string;
  body: string;
  thinkingStreaming: boolean;
} {
  for (const [openTag, closeTag] of [
    ["<think>", "</think>"],
    ["<thinking>", "</thinking>"],
  ] as const) {
    const start = raw.indexOf(openTag);
    if (start < 0) continue;
    const afterOpen = start + openTag.length;
    const end = raw.indexOf(closeTag, afterOpen);
    if (end < 0) {
      return {
        thinking: raw.slice(afterOpen),
        body: raw.slice(0, start),
        thinkingStreaming: true,
      };
    }
    return {
      thinking: raw.slice(afterOpen, end),
      body: (raw.slice(0, start) + raw.slice(end + closeTag.length)).trimStart(),
      thinkingStreaming: false,
    };
  }
  return { thinking: "", body: raw, thinkingStreaming: false };
}

/** Pure immutable reducer applying one Vercel AI SDK UIStreamChunk to UIMessage[]. */
export function reduceUIMessageChunk(
  messages: UIMessage[],
  chunk: UIStreamChunk,
): { messages: UIMessage[]; status: ChatStatus; error: string | null } {
  if (chunk.type === "error") {
    return { messages, status: "error", error: chunk.errorText };
  }
  const next = messages.map((m) => ({ ...m, parts: [...m.parts] }));
  let current = next[next.length - 1];
  if (chunk.type === "start" || !current || current.role !== "assistant") {
    current = {
      id: (chunk.type === "start" && chunk.messageId) || `asst-${next.length + 1}`,
      role: "assistant",
      parts: [],
    };
    next.push(current);
  }

  switch (chunk.type) {
    case "start":
      return { messages: next, status: "streaming", error: null };
    case "text-delta": {
      const last = current.parts[current.parts.length - 1];
      if (last && last.type === "text" && last.state === "streaming") {
        current.parts[current.parts.length - 1] = { ...last, text: last.text + chunk.delta };
      } else {
        current.parts.push({ type: "text", text: chunk.delta, state: "streaming" });
      }
      return { messages: next, status: "streaming", error: null };
    }
    case "text-end": {
      const last = current.parts[current.parts.length - 1];
      if (last && last.type === "text") {
        current.parts[current.parts.length - 1] = { ...last, state: "done" };
      }
      return { messages: next, status: "streaming", error: null };
    }
    case "reasoning-delta": {
      const last = current.parts[current.parts.length - 1];
      if (last && last.type === "reasoning" && last.state === "streaming") {
        current.parts[current.parts.length - 1] = { ...last, text: last.text + chunk.delta };
      } else {
        current.parts.push({ type: "reasoning", text: chunk.delta, state: "streaming" });
      }
      return { messages: next, status: "streaming", error: null };
    }
    case "reasoning-end": {
      const last = current.parts[current.parts.length - 1];
      if (last && last.type === "reasoning") {
        current.parts[current.parts.length - 1] = { ...last, state: "done" };
      }
      return { messages: next, status: "streaming", error: null };
    }
    case "tool-input-available":
      current.parts.push({
        type: "tool-invocation",
        toolCallId: chunk.toolCallId,
        toolName: chunk.toolName,
        state: "input-available",
        input: chunk.input,
      });
      return { messages: next, status: "streaming", error: null };
    case "tool-output-available": {
      const idx = current.parts.findIndex(
        (p) => (p.type === "tool-invocation" || p.type.startsWith("tool-")) && "toolCallId" in p && p.toolCallId === chunk.toolCallId,
      );
      if (idx >= 0) {
        const prev = current.parts[idx] as ToolUIPart;
        current.parts[idx] = { ...prev, state: "output-available", output: chunk.output };
      }
      return { messages: next, status: "streaming", error: null };
    }
    case "tool-output-error": {
      const idx = current.parts.findIndex(
        (p) => (p.type === "tool-invocation" || p.type.startsWith("tool-")) && "toolCallId" in p && p.toolCallId === chunk.toolCallId,
      );
      if (idx >= 0) {
        const prev = current.parts[idx] as ToolUIPart;
        current.parts[idx] = { ...prev, state: "output-error", errorText: chunk.errorText };
      }
      return { messages: next, status: "streaming", error: null };
    }
    case "source-url":
      current.parts.push({
        type: "source-url",
        sourceId: chunk.sourceId,
        url: chunk.url,
        title: chunk.title,
      });
      return { messages: next, status: "streaming", error: null };
    case "file":
      current.parts.push({
        type: "file",
        mediaType: chunk.mediaType,
        filename: chunk.filename,
        url: chunk.url,
      });
      return { messages: next, status: "streaming", error: null };
    case "finish":
      current.parts = current.parts.map((p) =>
        (p.type === "text" || p.type === "reasoning") && p.state === "streaming"
          ? { ...p, state: "done" }
          : p,
      );
      return { messages: next, status: "ready", error: null };
    default:
      if (chunk.type.startsWith("data-")) {
        const dp = chunk as DataUIPart;
        const idx = dp.id ? current.parts.findIndex((p) => p.type === dp.type && "id" in p && p.id === dp.id) : -1;
        if (idx >= 0) {
          current.parts[idx] = dp;
        } else {
          current.parts.push(dp);
        }
      }
      return { messages: next, status: "streaming", error: null };
  }
}

export interface UseMarlinChatOptions {
  /** TopicBus channel receiving Vercel AI SDK UIStreamChunk or UIMessage[] snapshots. Default: "chat.stream". */
  streamTopic?: string;
  /** ScopedProxy action for submitting a prompt. Default: "chat.send". */
  sendAction?: string;
  /** ScopedProxy action for cancelling an in-flight turn. Default: "chat.stop". */
  stopAction?: string;
  initialMessages?: UIMessage[];
}

export interface UseMarlinChatHandle {
  messages: UIMessage[];
  status: ChatStatus;
  error: string | null;
  sendMessage: (text: string) => Promise<void>;
  stop: () => Promise<void>;
  setMessages: (updater: UIMessage[] | ((prev: UIMessage[]) => UIMessage[])) => void;
}

/**
 * Vercel AI SDK `useChat`-ergonomic React 19 hook for Marlin Layer 3 WebView panels.
 * All transport flows strictly through Marlin's sandboxed `PanelClient` (`TopicBus`
 * + `ScopedProxy`), never opening direct browser connections from Untrusted WebViews.
 */
export function useMarlinChat(options: UseMarlinChatOptions = {}): UseMarlinChatHandle {
  const {
    streamTopic = "chat.stream",
    sendAction = "chat.send",
    stopAction = "chat.stop",
    initialMessages = [],
  } = options;
  const client = useMarlinPanel();
  const [messages, setMessages] = useState<UIMessage[]>(initialMessages);
  const [status, setStatus] = useState<ChatStatus>("ready");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    return client.onEvent<unknown>(streamTopic, (ev) => {
      const payload = ev.payload;
      if (Array.isArray(payload)) {
        setMessages(payload as UIMessage[]);
        setStatus("ready");
        return;
      }
      if (typeof payload === "string") {
        const chunk = parseVercelUIStreamLine(payload);
        if (!chunk) return;
        setMessages((prev) => {
          const reduced = reduceUIMessageChunk(prev, chunk);
          setStatus(reduced.status);
          setError(reduced.error);
          return reduced.messages;
        });
        return;
      }
      if (payload && typeof payload === "object" && "type" in payload) {
        const chunk = payload as UIStreamChunk;
        setMessages((prev) => {
          const reduced = reduceUIMessageChunk(prev, chunk);
          setStatus(reduced.status);
          setError(reduced.error);
          return reduced.messages;
        });
      }
    });
  }, [client, streamTopic]);

  const sendMessage = useCallback(
    async (text: string) => {
      const trimmed = text.trim();
      if (!trimmed) return;
      const userMsg: UIMessage = {
        id: `user-${Date.now()}`,
        role: "user",
        parts: [{ type: "text", text: trimmed, state: "done" }],
      };
      setMessages((prev) => [...prev, userMsg]);
      setStatus("submitted");
      setError(null);
      const receipt = await client.submitAction(sendAction, { text: trimmed });
      if (!receipt.admitted) {
        setStatus("error");
        setError(receipt.denial ?? "Prompt rejected by host policy");
      }
    },
    [client, sendAction],
  );

  const stop = useCallback(async () => {
    await client.submitAction(stopAction, {});
    setStatus("ready");
  }, [client, stopAction]);

  return useMemo(
    () => ({ messages, status, error, sendMessage, stop, setMessages }),
    [messages, status, error, sendMessage, stop],
  );
}

/**
 * Extracts all typed Vercel AI SDK `data-*` Generative UI parts matching `dataType`
 * across a conversation's messages.
 */
export function useGenerativeDataParts<T = unknown>(
  messages: UIMessage[],
  dataType: `data-${string}`,
): DataUIPart<T>[] {
  return useMemo(() => {
    const out: DataUIPart<T>[] = [];
    for (const m of messages) {
      for (const p of m.parts) {
        if (p.type === dataType) {
          out.push(p as DataUIPart<T>);
        }
      }
    }
    return out;
  }, [messages, dataType]);
}
