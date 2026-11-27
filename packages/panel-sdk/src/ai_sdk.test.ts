// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { describe, expect, test } from "bun:test";
import {
  parseVercelUIStreamLine,
  reduceUIMessageChunk,
  splitThinkTags,
  type UIMessage,
} from "./ai_sdk";
import { mapMarlinToShadcnTokens } from "./theme";

describe("Vercel AI SDK 3-layer UI adapter", () => {
  test("parses and reduces full reasoning + tool + text + data stream", () => {
    const lines = [
      'data: {"type":"start","messageId":"m-1"}',
      'data: {"type":"reasoning-delta","id":"r1","delta":"Step 1. "}',
      'data: {"type":"reasoning-delta","id":"r1","delta":"Step 2."}',
      'data: {"type":"reasoning-end","id":"r1"}',
      'data: {"type":"tool-input-available","toolCallId":"tc-1","toolName":"helm_search","input":{"q":"@lang:rust"}}',
      'data: {"type":"tool-output-available","toolCallId":"tc-1","output":{"count":5}}',
      'data: {"type":"text-delta","id":"t1","delta":"Found 5 matches."}',
      'data: {"type":"data-workflow","id":"wf-1","data":{"nodes":2}}',
      'data: {"type":"finish"}',
      "data: [DONE]",
    ];

    let msgs: UIMessage[] = [];
    let status = "ready";
    for (const line of lines) {
      const chunk = parseVercelUIStreamLine(line);
      if (!chunk) continue;
      const reduced = reduceUIMessageChunk(msgs, chunk);
      msgs = reduced.messages;
      status = reduced.status;
    }

    expect(status).toBe("ready");
    expect(msgs.length).toBe(1);
    expect(msgs[0].id).toBe("m-1");
    expect(msgs[0].parts.length).toBe(4);
    expect(msgs[0].parts[0]).toEqual({ type: "reasoning", text: "Step 1. Step 2.", state: "done" });
    expect(msgs[0].parts[1]).toMatchObject({
      type: "tool-invocation",
      toolCallId: "tc-1",
      toolName: "helm_search",
      state: "output-available",
      output: { count: 5 },
    });
    expect(msgs[0].parts[2]).toEqual({ type: "text", text: "Found 5 matches.", state: "done" });
    expect(msgs[0].parts[3]).toEqual({ type: "data-workflow", id: "wf-1", data: { nodes: 2 } });
  });

  test("splits streaming and closed <think> tags cleanly", () => {
    const streaming = splitThinkTags("<think>analyzing architecture...");
    expect(streaming.thinkingStreaming).toBe(true);
    expect(streaming.thinking).toBe("analyzing architecture...");
    expect(streaming.body).toBe("");

    const closed = splitThinkTags("<think>done</think>\nFinal answer");
    expect(closed.thinkingStreaming).toBe(false);
    expect(closed.thinking).toBe("done");
    expect(closed.body).toBe("Final answer");
  });

  test("maps Marlin OKLCH/Liquid Glass tokens to shadcn & Vercel AI Elements variables", () => {
    const mapped = mapMarlinToShadcnTokens({
      "--marlin-canvas": "#1e222a",
      "--marlin-ink": "#e5e9f0",
      "--marlin-accent": "#d48ab8",
      "--marlin-border": "rgba(255,255,255,0.12)",
    });
    expect(mapped["--background"]).toBe("#1e222a");
    expect(mapped["--foreground"]).toBe("#e5e9f0");
    expect(mapped["--primary"]).toBe("#d48ab8");
    expect(mapped["--border"]).toBe("rgba(255,255,255,0.12)");
  });
});
