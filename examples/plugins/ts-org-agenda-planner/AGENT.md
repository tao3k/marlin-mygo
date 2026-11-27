<!--
SPDX-FileCopyrightText: 2026 tao3k team and Contributors

SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
-->

# Plugin Agent Contract: `tao3k.org-agenda-planner`

## Architecture Principle: Zero Duplicate Org Parsing

- **Authoritative Org Parser**: `tao3k/orgize` (Rust authority kernel) and `tao3k/orgize-wasm` (`orgize` / `orgize/worker` npm package).
- **Never write Org-mode regex parsers inside a UI plugin.** This plugin consumes canonical `OrgizeAgendaViewResponseDto` (`org.agenda`) and `OrgizeClockRollupResponseDto` (`org.clock`) JSON projections directly and emits structured `OrgizeAgentCaptureRequestDto` payloads to `org.capture.plan`.
- If a downstream user wants offline local `.org` scratchpad preview inside the WebView, wire `github:tao3k/orgize-wasm#npm-package` through the `OrgizeWasmDocumentAdapter` interface in `src/orgize_dto.ts`.

## Surface & Security Boundaries

- **Runtime**: `webview-untrusted` (React 19 TSX + `@tao3k/marlin-panel-sdk/react`, compiled via `bun build` into a zero-runtime browser `bundle.js`).
- **Slot**: `workbench.main` (`activation = "on-command"`, `triggers = ["agenda.open", "planner.today"]`, `idle_unload_sec = 600`).
- **Read Scopes**: `org.agenda`, `org.clock`, `session.summary`.
- **Declared Actions**: `org.capture.plan`, `org.todo.toggle`, `org.clock.in`, `org.clock.out`, `agenda.alarm.trigger`, `agent.workflow.plan-day`.
- **CSP**: `default-src 'self'; script-src 'self'` — no remote script URLs, no `eval(`, no `new Function(`.

## Safe Downstream Customizations for Users & LLMs

1. Add custom filter tabs (e.g., weekly milestones, energy tags, category grouping) in `src/model.ts`.
2. Customize time-blocking visualization or Pomodoro alarm presets in `src/main.ts` and `index.html`.
3. Verify edits (security + performance self-healing loop) with:
   - `bun test ./examples/plugins/ts-org-agenda-planner/src/model.test.ts`
   - `CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool doctor -dir ./examples/plugins/ts-org-agenda-planner -perf -json`
   - `CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool bench-plugin -dir ./examples/plugins/ts-org-agenda-planner -json`
