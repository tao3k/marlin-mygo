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

# Run TypeScript panel SDK checks and tests via Bun.
test-ts:
    bun test packages/panel-sdk

# Run full Go + TypeScript Frontend Engine benchmark suite.
bench:
    CGO_ENABLED=0 go test -bench=. -benchmem -benchtime=500ms ./bench/...
    bun bench/ts/panel_bench.ts

# Run full repository validation gate.
check: proto-descriptor model-gate test-ts test-go
    CGO_ENABLED=0 go vet ./...
    CGO_ENABLED=0 go build -o /tmp/marlin-app-check ./cmd/marlin-app && rm -f /tmp/marlin-app-check
