<!--
SPDX-FileCopyrightText: 2026 tao3k team and Contributors

SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
-->

# Plugin Agent Contract: `tao3k.focus-clock-reactor`

## Role & Architecture

- **Runtime**: `wasm-ui-ir` (Go 1.27 WASI Reactor, 100% zero `unsafe`, 0 MB WebView RAM).
- **Slot**: `workbench.inspector` (`activation = "on-topic"`, `triggers = ["org.clock", "org.agenda"]`, `idle_unload_sec = 600`).
- **Read Scopes**: `org.clock`, `org.agenda` (consumes canonical `tao3k/orgize` DTO JSON projections with zero Org syntax parsing).
- **Declared Actions**: `org.clock.in`, `org.clock.out`, `org.capture.plan`.

## File Layout

- `state.go`: Pure Go state model, `BuildUITree()` (native GPU `UIElement` + 3 `ActionButton`s), and `BuildDrawList()` (zero-JSON binary GPU progress bar + 16-bucket focus timeline).
- `main.go`: `//go:build wasip1` reactor ABI exports delegating exclusively to `sdk/go-ui-ir` (`AllocBuffer`, `DispatchEvent`, `RenderResult`, `DrawListResult`, `EmitAction`).
- `state_test.go`: Native desktop unit tests runnable via `CGO_ENABLED=0 go test ./examples/plugins/go-focus-clock-reactor`.

## Verification Commands

- `CGO_ENABLED=0 go test -v -race ./examples/plugins/go-focus-clock-reactor`
- `CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool doctor -dir ./examples/plugins/go-focus-clock-reactor`
