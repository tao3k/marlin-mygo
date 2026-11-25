# SPDX-FileCopyrightText: 2026 tao3k team and Contributors
#
# SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

# Explicit, Tab-completable plugin subcommands: run `just plugin <TAB>`!
set working-directory := '..'

default:
    @just --list plugin

# === Live macOS / Desktop Showcase (React 19 + Go WASI GPU Dock) ===

# Open Marlin Studio with Org Agenda & AI Daily Planner (React 19) + GPU Focus Clock.
show-agenda:
    CGO_ENABLED=0 go run ./cmd/marlin-app -showcase -showcase-plugin agenda

# Open Marlin Studio with AI Memory & Knowledge Triage Inbox (React 19) + GPU Focus Clock.
show-memory:
    CGO_ENABLED=0 go run ./cmd/marlin-app -showcase -showcase-plugin memory

# Open Marlin Studio with Multi-Agent AI Workflow Cockpit (React 19) + GPU Focus Clock.
show-workflow:
    CGO_ENABLED=0 go run ./cmd/marlin-app -showcase -showcase-plugin workflow

# Preview Marlin Mobile Companion (390x844 App Store 2.5.2 safe shell).
show-mobile:
    CGO_ENABLED=0 go run ./cmd/marlin-mobile

# Build Rust marlin-mygo-host (if needed) and launch full live Rust Sidecar + Studio showcase.
studio-live core="../..":
    cargo build --manifest-path "{{core}}/Cargo.toml" --locked -p marlin-agent-app-server --features turso-host --bin marlin-mygo-host
    CGO_ENABLED=0 go run ./cmd/marlin-app -sidecar "{{core}}/target/debug/marlin-mygo-host" -real-session -showcase -showcase-plugin agenda

# === Live Hot-Reload Dev Watch ===

# Watch & hot-reload Org Agenda (React 19) inside Marlin Studio.
dev-agenda:
    CGO_ENABLED=0 go run ./cmd/marlin-app -dev-plugin ./examples/plugins/ts-org-agenda-planner

# Watch & hot-reload AI Memory Inbox (React 19) inside Marlin Studio.
dev-memory:
    CGO_ENABLED=0 go run ./cmd/marlin-app -dev-plugin ./examples/plugins/react-ai-memory-inbox

# Watch & hot-reload AI Workflow Cockpit (React 19) inside Marlin Studio.
dev-workflow:
    CGO_ENABLED=0 go run ./cmd/marlin-app -dev-plugin ./examples/plugins/react-ai-workflow-cockpit

# Watch & hot-reload Go WASI Focus Clock Reactor inside Marlin Studio.
dev-focus-clock:
    CGO_ENABLED=0 go run ./cmd/marlin-app -dev-plugin ./examples/plugins/go-focus-clock-reactor

# === Explicit Per-Plugin Performance Gates (bench-plugin) ===

# Benchmark Org Agenda (React 19 + 500-card orgize DTO perf macro).
bench-agenda:
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool bench-plugin -dir ./examples/plugins/ts-org-agenda-planner

# Benchmark AI Memory Inbox (React 19 + 500-card memory perf macro).
bench-memory:
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool bench-plugin -dir ./examples/plugins/react-ai-memory-inbox

# Benchmark AI Workflow Cockpit (React 19 + 200-step workflow perf macro).
bench-workflow:
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool bench-plugin -dir ./examples/plugins/react-ai-workflow-cockpit

# Benchmark Go WASI Focus Clock Reactor (wazero UI-IR + DrawList).
bench-focus-clock:
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool bench-plugin -dir ./examples/plugins/go-focus-clock-reactor

# Benchmark Declarative Daily Planner Pack (mobile + desktop OTA).
bench-daily-pack:
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool bench-plugin -dir ./examples/plugins/declarative-daily-planner-pack

# Run performance budgets across all 5 flagship example plugins.
bench-all: bench-agenda bench-memory bench-workflow bench-focus-clock bench-daily-pack

# === Per-Component UI Performance Suite (MyGo GPU Shell + React 19 Components) ===

# Run per-component Go MyGo GPU UI performance budgets + allocation microbenchmarks.
perf-go:
    CGO_ENABLED=0 go test -v -race -run "TestSlotRegistryDisposeOutsideLockNoDeadlock|TestQuickPluginSwitcherWarmInstantAndBurstCoalescing" ./runtime/mygoshell
    CGO_ENABLED=0 go test -v -run "TestPerComponentUIPerformanceBudgets" ./runtime/mygoshell
    CGO_ENABLED=0 go test -run '^$' -bench "BenchmarkComponent" -benchmem -benchtime=400ms ./runtime/mygoshell

# Run per-component React 19 UI render and hook performance budgets.
perf-react:
    bun test examples/plugins/react_components_perf.test.tsx

# Run the complete per-component UI performance suite (every MyGo GPU + React 19 component).
perf-ui: perf-go perf-react

# === Violent Click / Chaos Monkey & Fluent 120Hz Stress Framework ===

# Run MyGo native ui.Tester violent click, hover, resize & accessibility chaos monkey.
stress-go:
    CGO_ENABLED=0 go test -v -run "TestFluentMonkeyChaosClickStormOnStudioShell|TestFluentRapidHoverAndResizeContinuity|TestFluentEveryControlHitTestableAndAccessible" ./runtime/mygoshell
    CGO_ENABLED=0 go test -race -run "TestFluentMonkeyChaosClickStormOnStudioShell|TestFluentRapidHoverAndResizeContinuity" ./runtime/mygoshell

# Run React 19 live DOM (happy-dom) violent click chaos monkey & listener-leak suite.
stress-react:
    bun test examples/plugins/react_chaos_monkey.test.tsx

# Run BOTH MyGo native GPU + React 19 live DOM violent click chaos monkey suites (8,000+ ops).
stress-ui: stress-go stress-react

# Heavy endurance soak: 10,000 MyGo GPU chaos ops + 12,000 React 19 live DOM chaos ops + heap/leak guards.
soak-ui ops="10000":
    MARLIN_CHAOS_OPS={{ops}} CGO_ENABLED=0 go test -v -count=1 -run "TestFluentMonkeyChaosClickStormOnStudioShell|TestFluentRapidHoverAndResizeContinuity" ./runtime/mygoshell
    MARLIN_CHAOS_OPS=4000 bun test examples/plugins/react_chaos_monkey.test.tsx

# Render deterministic PNG visual frame snapshots (desktop dark/light/narrow/modal + mobile) & verify contrast invariants.
visual-check out="./dist/visual-snapshots":
    MARLIN_VISUAL_OUT="{{out}}" CGO_ENABLED=0 go test -v -count=1 -run "TestVisualFrameSnapshotsAndContrastInvariants" ./runtime/mygoshell

# === Explicit Security + Performance Certification (doctor -perf) ===

# Run security + performance doctor on Org Agenda (React 19).
doctor-agenda:
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool doctor -dir ./examples/plugins/ts-org-agenda-planner -perf

# Run security + performance doctor on AI Memory Inbox (React 19).
doctor-memory:
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool doctor -dir ./examples/plugins/react-ai-memory-inbox -perf

# Run security + performance doctor on AI Workflow Cockpit (React 19).
doctor-workflow:
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool doctor -dir ./examples/plugins/react-ai-workflow-cockpit -perf

# Run security + performance doctor on Go WASI Focus Clock Reactor.
doctor-focus-clock:
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool doctor -dir ./examples/plugins/go-focus-clock-reactor -perf

# Run security + performance doctor on Declarative Daily Planner Pack.
doctor-daily-pack:
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool doctor -dir ./examples/plugins/declarative-daily-planner-pack -perf

# Run doctor -perf across all 5 flagship example plugins.
doctor-all: doctor-agenda doctor-memory doctor-workflow doctor-focus-clock doctor-daily-pack

# === Build, Sign & Scaffold ===

# Build, certify, and Ed25519-sign all 9 canonical example plugins into ./dist/marketplace.
build-all out="./dist/marketplace":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool build-examples -out "{{out}}"

# Scaffold a new React 19 TSX plugin (Case 1 — Desktop Preferred).
new-react id out="":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool init-plugin -id {{id}} -runtime react -slot workbench.main -out "{{out}}"

# Scaffold a new Go 1.27 WASI GPU Reactor plugin (Case 2 — 0 MB WebView RAM).
new-go-wasm id out="":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool init-plugin -id {{id}} -runtime go-wasm -slot workbench.inspector -out "{{out}}"

# Scaffold a new zero-dependency lightweight TypeScript plugin (Case 3 — ~10 KB).
new-ts id out="":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool init-plugin -id {{id}} -runtime ts -slot workbench.main -out "{{out}}"

# Scaffold a new cross-platform Declarative TOML pack (Case 4 — Desktop + iOS/Android OTA).
new-declarative id out="":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool init-plugin -id {{id}} -runtime declarative -slot workbench.inspector -out "{{out}}"
