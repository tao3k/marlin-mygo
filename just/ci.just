# SPDX-FileCopyrightText: 2026 tao3k team and Contributors
#
# SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

# Single-source-of-truth CI gates: every GitHub Actions job invokes `just ci <job>`.
set working-directory := '..'

default:
    @just --list ci

# Verify Protobuf lint, FileDescriptorSet, Go/TS stubs, and proto/wire-lock.toml zero drift.
proto-contract:
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool descriptor
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool gen
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool gen-ts
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool wire-sync -check
    git diff --exit-code -- gen/ packages/panel-sdk/src/generated/ proto/wire-lock.toml

# Run Pure-Go Quint formal security model-gate (safe + 7 adversarial negative controls).
formal-security samples="500":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool model-gate -backend=typescript -samples={{samples}}

# Run Pure-Go zero-cgo unit/WASM tests, go vet, and desktop + mobile shell builds.
go-unit-and-build:
    CGO_ENABLED=0 go test -v ./pkg/... ./engine/... ./runtime/... ./control/... ./sdk/... ./cmd/...
    CGO_ENABLED=0 go vet ./...
    CGO_ENABLED=0 go build ./cmd/marlin-app
    CGO_ENABLED=0 go build ./cmd/marlin-mobile

# Run Go race detector across concurrent engine, pluginhost, and device planes.
go-race:
    go test -race ./pkg/... ./engine/... ./control/... ./runtime/pluginhost ./runtime/device/...

# Run 10-target Go fuzz smoke suite across protocol, schema, arena, input, and marketplace boundaries.
go-fuzz time="3s":
    @just fuzz-smoke {{time}}

# Run MyGo native GPU per-component perf budgets, violent click chaos monkey, and visual contrast snapshots.
ui-stress-and-visual:
    CGO_ENABLED=0 go test -v -count=1 -run "TestPerComponentUIPerformanceBudgets|TestFluentMonkeyChaosClickStormOnStudioShell|TestFluentRapidHoverAndResizeContinuity|TestFluentEveryControlHitTestableAndAccessible|TestVisualFrameSnapshotsAndContrastInvariants" ./runtime/mygoshell
    go test -race -run "TestSlotRegistryDisposeOutsideLockNoDeadlock|TestQuickPluginSwitcherWarmInstantAndBurstCoalescing|TestFluentMonkeyChaosClickStormOnStudioShell" ./runtime/mygoshell

# Run TypeScript Panel SDK, React 19 perf budgets, and live happy-dom chaos monkey suite.
ts-and-react-chaos:
    bun test packages/panel-sdk examples/plugins

# Enforce Go + TypeScript strict benchmark latency and zero-alloc SLO ceilings.
perf-gate benchtime="250ms" scale="2.5":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool perf-gate -benchtime={{benchtime}} -latency-scale={{scale}}

# Run iOS Swift bridge iOS SDK typecheck (-warnings-as-errors) and native macOS swift test.
ios-swift-bridge:
    @just plugin test-swift

# Run Android Kotlin bridge JVM tests (-Werror) and real Android SDK android.jar compile check.
android-kotlin-bridge:
    @just plugin test-kotlin
    @just plugin test-android-sdk

# Certify, build, Ed25519-sign all 9 example plugins, emit trust-ring.toml, and enforce per-plugin perf budgets.
plugin-admission-gate scale="2.5":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool build-examples -out ./dist/marketplace-ci
    test -s ./dist/marketplace-ci/catalog.json
    test -s ./dist/marketplace-ci/catalog.json.sig
    test -s ./dist/marketplace-ci/publisher.pub
    test -s ./dist/marketplace-ci/trust-ring.toml
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool bench-plugin -dir ./examples/plugins/ts-org-agenda-planner -scale {{scale}}
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool bench-plugin -dir ./examples/plugins/react-ai-memory-inbox -scale {{scale}}
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool bench-plugin -dir ./examples/plugins/react-ai-workflow-cockpit -scale {{scale}}
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool bench-plugin -dir ./examples/plugins/go-focus-clock-reactor -scale {{scale}}
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool bench-plugin -dir ./examples/plugins/declarative-daily-planner-pack -scale {{scale}}

# Build Rust marlin-mygo-host and run real cross-repo TLS 1.3 + Sidecar + Turso replay E2E.
e2e-rust core="../..":
    @just e2e-rust {{core}}
