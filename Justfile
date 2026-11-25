# SPDX-FileCopyrightText: 2026 tao3k team and Contributors
#
# SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

default:
    @just --list

# Validate every .proto schema and emit the canonical FileDescriptorSet via Go tool.
proto-descriptor:
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool descriptor

# Lint and generate Go protobuf + gRPC stubs under gen/ via Go tool.
proto-gen:
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool gen
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool gen-ts

# Generate TypeScript wire types from compiled FileDescriptorSet.
proto-gen-ts:
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool gen-ts

# Verify marlin.app.v1 compatibility against a local marlin-agent-core checkout.
proto-compat core="../..":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool compat-check -core {{core}}

# Build Rust marlin-mygo-host and run real cross-process TLS 1.3 + Sidecar + WASM Reactor E2E.
e2e-rust core="../..":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool e2e-rust -core {{core}}

# Run Quint formal security model gate (typecheck + tests + 1000-sample run + 7 negative controls).
model-gate:
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool model-gate

# Scaffold a new Marketplace plugin (runtime: ts | go-wasm | declarative).
init-plugin id runtime="ts" slot="workbench.main" out="":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool init-plugin -id {{id}} -runtime {{runtime}} -slot {{slot}} -out "{{out}}"

# Run Pure-Go zero-cgo test suite across all 5 engine planes, plus race detector.
test-go:
    CGO_ENABLED=0 go test ./pkg/... ./engine/... ./runtime/... ./control/... ./sdk/... ./cmd/...
    go test -race ./pkg/... ./engine/... ./control/... ./runtime/pluginhost ./runtime/device

# Run short smoke fuzzing across protocol, schema, manifest, drawlist, arena, and input boundaries.
fuzz-smoke time="2s":
    CGO_ENABLED=0 go test -fuzz=FuzzValidateDotIdentifier -fuzztime={{time}} ./pkg/schema
    CGO_ENABLED=0 go test -fuzz=FuzzParseSubmissionID -fuzztime={{time}} ./pkg/schema
    CGO_ENABLED=0 go test -fuzz=FuzzDecodeDrawList -fuzztime={{time}} ./pkg/schema
    CGO_ENABLED=0 go test -fuzz=FuzzParsePluginManifestTOML -fuzztime={{time}} ./pkg/schema
    CGO_ENABLED=0 go test -fuzz=FuzzArenaCompileAndDiff -fuzztime={{time}} ./engine/arena
    CGO_ENABLED=0 go test -fuzz=FuzzFuzzyScore -fuzztime={{time}} ./engine/input
    CGO_ENABLED=0 go test -fuzz=FuzzWebViewProtocolHTTP -fuzztime={{time}} ./runtime/pluginhost
    CGO_ENABLED=0 go test -fuzz=FuzzDecodeAndAdmitWasmUITree -fuzztime={{time}} ./runtime/pluginhost
    CGO_ENABLED=0 go test -fuzz=FuzzParsePluginProfileTOML -fuzztime={{time}} ./control/marketplace
    CGO_ENABLED=0 go test -fuzz=FuzzParseTrustRingTOML -fuzztime={{time}} ./control/marketplace

# Run TypeScript panel SDK checks and tests via Bun.
test-ts:
    bun test packages/panel-sdk

# Run full Go + TypeScript benchmark suite and enforce strict latency + zero-alloc perf-gate.
bench:
    CGO_ENABLED=0 go test -bench=. -benchmem -benchtime=500ms ./bench/...
    bun bench/ts/panel_bench.ts

# Enforce tightened latency, byte, and zero-allocation SLO ceilings across Go + TS.
perf-gate scale="1.0":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool perf-gate -benchtime=300ms -latency-scale={{scale}}

# Serve a signed Marketplace catalog directory over local HTTP.
serve-catalog dir="./dist/marketplace" addr="127.0.0.1:8787":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool serve-catalog -dir "{{dir}}" -addr "{{addr}}"

# Watch a plugin directory with transactional hot-reload & automatic rollback.
dev-plugin dir="./examples/plugins/go-graph-inspector-reactor":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool dev-plugin -dir "{{dir}}"

# Run fail-closed security certification audit on a plugin directory.
doctor dir="./examples/plugins/ts-org-kanban":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool doctor -dir "{{dir}}"

# Run automated per-plugin performance budgets & remediation diagnostics (ideal for humans & LLMs).
bench-plugin dir="./examples/plugins/ts-org-agenda-planner" scale="1.0":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool bench-plugin -dir "{{dir}}" -scale={{scale}}

# Certify, sign, verify, and merge a plugin into a Marketplace catalog in one step.
publish dir publisher key pub out="./dist/marketplace":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool publish -dir "{{dir}}" -publisher "{{publisher}}" -key "{{key}}" -pub "{{pub}}" -out "{{out}}"

# Run full repository validation gate.
check: proto-descriptor model-gate test-ts test-go
    CGO_ENABLED=0 go vet ./...
    CGO_ENABLED=0 go build -o /tmp/marlin-app-check ./cmd/marlin-app && rm -f /tmp/marlin-app-check
