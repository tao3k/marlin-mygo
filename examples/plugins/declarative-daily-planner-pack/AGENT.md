<!--
SPDX-FileCopyrightText: 2026 tao3k team and Contributors

SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
-->

# Plugin Agent Contract: `tao3k.daily-planner-pack`

## Role & App Store Compliance

- **Runtime**: `declarative` (zero executable code, zero scripts — safe for Desktop, iOS, and Android OTA distribution).
- **Slot**: `workbench.inspector` (`activation = "on-slot"`, `idle_unload_sec = 300`).
- **Data Model**: Declares personal focus theme tokens, `tao3k/orgize` agenda block section presets, Pomodoro/alarm defaults, structured `OrgizeAgentCaptureRequestDto` capture templates, and Meow/Hydra leader keymaps in `planner-pack.toml`.

## Hard Security Rules

- Never add `.js`, `.ts`, `.wasm`, `.sh`, or native binaries to this directory (`doctor` fails closed via `declarative.appstore_safe`).
- Verify with:
  - `CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool doctor -dir ./examples/plugins/declarative-daily-planner-pack`
