// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

/**
 * Drop-in React 19 Generative Artifact Panel for Layer 3 Sandboxed WebView
 * Islands, built on `useMarlinChat` + `useGenerativeDataParts` and styled
 * strictly via Marlin's unified `--marlin-*` / shadcn / Vercel AI Elements CSS tokens.
 */

import { useState } from "react";
import {
  useGenerativeDataParts,
  useMarlinChat,
  type DataUIPart,
  type UIMessage,
} from "./ai_sdk";

export interface GenerativeArtifactPanelProps {
  /** Optional externally provided messages; falls back to live `useMarlinChat()`. */
  messages?: UIMessage[];
  /** Filter to a specific `data-*` type, or show all `data-*` parts when omitted. */
  dataType?: `data-${string}`;
  title?: string;
}

export function GenerativeArtifactPanel({
  messages: externalMessages,
  dataType,
  title = "Generative Artifact Stage",
}: GenerativeArtifactPanelProps) {
  const chat = useMarlinChat();
  const activeMessages = externalMessages ?? chat.messages;
  const typedParts = useGenerativeDataParts(activeMessages, dataType ?? "data-workflow");
  const allParts: DataUIPart[] = dataType
    ? typedParts
    : activeMessages.flatMap((m) =>
        m.parts.filter((p): p is DataUIPart => p.type.startsWith("data-")),
      );
  const [selectedIdx, setSelectedIdx] = useState(0);
  const active = allParts[Math.min(selectedIdx, Math.max(0, allParts.length - 1))];

  return (
    <div className="m-panel">
      <div className="m-header">
        <div>
          <h2 className="m-title">{title}</h2>
          <div className="m-caption">
            Vercel AI SDK UIMessage Data Parts · Token-synced with Marlin Liquid Glass
          </div>
        </div>
        <div className="m-row-gap">
          <span className="m-badge tone-accent">{allParts.length} artifact(s)</span>
        </div>
      </div>

      {allParts.length === 0 ? (
        <div className="m-empty">
          Waiting for Generative UI parts (`data-mermaid`, `data-workflow`, `data-chart`, `data-table`)...
        </div>
      ) : (
        <div className="m-card">
          <div className="m-card-head">
            <div className="m-segmented">
              {allParts.map((part, idx) => (
                <button
                  key={part.id ?? `${part.type}-${idx}`}
                  type="button"
                  className={`m-btn ${idx === selectedIdx ? "active" : ""}`}
                  onClick={() => setSelectedIdx(idx)}
                >
                  {part.type}
                </button>
              ))}
            </div>
            {active && <span className="m-badge tone-info">{active.type}</span>}
          </div>
          {active && (
            <pre
              className="m-mono"
              style={{
                margin: 0,
                padding: "12px 14px",
                borderRadius: "var(--marlin-radius-sm)",
                background: "var(--marlin-surface-recessed)",
                border: "1px solid var(--marlin-border)",
                overflowX: "auto",
                fontSize: "12px",
                lineHeight: 1.5,
              }}
            >
              {typeof active.data === "string"
                ? active.data
                : JSON.stringify(active.data, null, 2)}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}
