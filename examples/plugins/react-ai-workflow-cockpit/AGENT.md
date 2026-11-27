<!--
SPDX-FileCopyrightText: 2026 tao3k team and Contributors

SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
-->

# Plugin Agent Contract: `tao3k.ai-workflow-cockpit`

- **Runtime**: `webview-untrusted` (React 19 TSX + `@tao3k/marlin-panel-sdk/react` + `bun build`).
- **Security**: Never targets `approval.gate`; routes human approval handoff strictly via `workflow.gate.focus` so Tier-0 `host.gate` owns every approval decision.
- **Self-Healing Security & Performance Loop**:
  - `bun test ./examples/plugins/react-ai-workflow-cockpit/src/model.test.ts`
  - `CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool doctor -dir ./examples/plugins/react-ai-workflow-cockpit -perf -json`
