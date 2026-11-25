<!--
SPDX-FileCopyrightText: 2026 tao3k team and Contributors

SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
-->

# Plugin Agent Contract: `tao3k.ai-memory-inbox`

- **Runtime**: `webview-untrusted` (React 19 TSX + `@tao3k/marlin-panel-sdk/react` + `bun build`).
- **DTO Authority**: Consumes canonical `tao3k/orgize` `OrgizeMemoryResponseDto` (`org.memory`) — zero hand-rolled Org parsing.
- **Self-Healing Security & Performance Loop**:
  - `bun test ./examples/plugins/react-ai-memory-inbox/src/model.test.ts`
  - `CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool doctor -dir ./examples/plugins/react-ai-memory-inbox -perf -json`
