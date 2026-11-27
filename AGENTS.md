<!--
SPDX-FileCopyrightText: 2026 tao3k team and Contributors

SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
-->

# marlin-mygo Agent Rules

## Ownership & Protocol Discipline

- `marlin-agent-core` is the upstream source of truth for execution semantics,
  receipts, capability grants, and domain identifiers. Do not invent shadow
  protocol state machines or unverified compatibility shims in Go or TypeScript.
- Every wire contract lives under `proto/marlin/*/v1/*.proto` without
  language-specific `go_package` edits inside vendored upstream files; inject
  Go import paths via `buf.gen.yaml` / `scripts/gen-stubs.sh`.
- Preserve `MAR-APP-R07` client build isolation: this repository must never
  depend on Gerbil Scheme or internal Marlin Rust execution/storage crates.
- All 64-bit unsigned wire fields (`ordinal`, `sequence`, `byte_size`,
  `generation`) must cross Go ↔ TypeScript JSON boundaries as strings
  (`json:",string"`) to prevent IEEE-754 precision loss above 2^53.
- Opaque cursors (`event_id`, `page_cursor`) must never be parsed or reordered
  by clients; persist applied cursor and view projection atomically.

## MyGo & Plugin Security Boundaries

- Desktop builds MUST stay `CGO_ENABLED=0`. Never introduce `plugin.Open`
  (`.so`) or cgo dependencies in desktop packages.
- Avoid shell/bash scripts for repository automation, codegen, and test
  harnesses; implement typed tooling and tests in Go under `cmd/` and `*_test.go`.
- Untrusted third-party UI plugins MUST run inside either:
  1. `mygo.WebView` with `PageOptions{Untrusted: true}` served via custom
     protocol `marlin-plugin://<plugin-id>/` and scoped by `CallerPage(ctx)` +
     `EmitPage`, or
  2. Pure-Go `wazero` WASI sandboxes emitting bounded `UiElement` IR, or
  3. Data-only declarative packs (zero executable client code).
- Third-party plugins must NEVER receive host gRPC auth tokens, raw `mygo.Bind`
  handles, or arbitrary filesystem/network capabilities.
- Every native mobile/desktop device protocol (`pkg/device`) must register
  explicit `DeviceCapabilityDescriptor` metadata and require Rust-issued gate
  receipts before executing side effects.
