# SPDX-FileCopyrightText: 2026 tao3k team and Contributors
#
# SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

default:
    @just --list

# Explicit, Tab-completable plugin, CI, release, and web subcommands: `just plugin <TAB>`, `just ci <TAB>`, `just web <TAB>`.
mod plugin 'just/plugin.just'
mod ci 'just/ci.just'
mod release 'just/release.just'
mod web 'just/web.just'

# Quick top-level macOS/desktop studio launches (also under `just plugin show-*`):
show-agenda:
    @just plugin show-agenda

show-memory:
    @just plugin show-memory

show-workflow:
    @just plugin show-workflow

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

# Generate or verify deterministic proto/wire-lock.toml SHA-256 contract lockfile.
proto-sync core="../..":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool wire-sync -core {{core}}

proto-sync-check:
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool wire-sync -check

# Build Rust marlin-mygo-host and run real cross-process TLS 1.3 + Sidecar + WASM Reactor E2E.
e2e-rust core="../..":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool e2e-rust -core {{core}}

# Run Quint formal security model gate (typecheck + tests + 1000-sample run + 7 negative controls).
model-gate:
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool model-gate

# Scaffold a new Marketplace plugin (or use explicit `just plugin new-react|new-go-wasm|new-ts|new-declarative`).
init-plugin id runtime="react" slot="workbench.main" out="":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool init-plugin -id {{id}} -runtime {{runtime}} -slot {{slot}} -out "{{out}}"

# Run Pure-Go zero-cgo test suite across all 5 engine planes + optional features, plus race detector.
test-go:
    CGO_ENABLED=0 go test ./pkg/... ./engine/... ./features/... ./runtime/... ./control/... ./sdk/... ./cmd/...
    go test -race ./pkg/... ./engine/... ./features/... ./control/... ./runtime/pluginhost ./runtime/device/...

# Run iOS SDK Swift typecheck & native macOS swift test (no Simulator runtime needed).
test-swift:
    @just plugin test-swift

# Run Android Kotlin bridge JVM unit & concurrency tests (no Android Emulator needed).
test-kotlin:
    @just plugin test-kotlin

# Run short smoke fuzzing across protocol, schema, manifest, drawlist, arena, artifact, and input boundaries.
fuzz-smoke time="2s":
    CGO_ENABLED=0 go test -run='^$' -fuzz=FuzzValidateDotIdentifier -fuzztime={{time}} ./pkg/schema
    CGO_ENABLED=0 go test -run='^$' -fuzz=FuzzParseSubmissionID -fuzztime={{time}} ./pkg/schema
    CGO_ENABLED=0 go test -run='^$' -fuzz=FuzzDecodeDrawList -fuzztime={{time}} ./pkg/schema
    CGO_ENABLED=0 go test -run='^$' -fuzz=FuzzParsePluginManifestTOML -fuzztime={{time}} ./pkg/schema
    CGO_ENABLED=0 go test -run='^$' -fuzz=FuzzArenaCompileAndDiff -fuzztime={{time}} ./engine/arena
    CGO_ENABLED=0 go test -run='^$' -fuzz=FuzzFuzzyScore -fuzztime={{time}} ./engine/input
    CGO_ENABLED=0 go test -run='^$' -fuzz=FuzzWebViewProtocolHTTP -fuzztime={{time}} ./runtime/pluginhost
    CGO_ENABLED=0 go test -run='^$' -fuzz=FuzzArtifactProtocolHTTP -fuzztime={{time}} ./runtime/pluginhost
    CGO_ENABLED=0 go test -run='^$' -fuzz=FuzzDecodeAndAdmitWasmUITree -fuzztime={{time}} ./runtime/pluginhost
    CGO_ENABLED=0 go test -run='^$' -fuzz=FuzzParsePluginProfileTOML -fuzztime={{time}} ./control/marketplace
    CGO_ENABLED=0 go test -run='^$' -fuzz=FuzzParseTrustRingTOML -fuzztime={{time}} ./control/marketplace
    CGO_ENABLED=0 go test -run='^$' -fuzz=FuzzValidateAndStageAttachment -fuzztime={{time}} ./engine/composer
    CGO_ENABLED=0 go test -run='^$' -fuzz=FuzzSanitizeFilename -fuzztime={{time}} ./engine/composer
    CGO_ENABLED=0 go test -run='^$' -fuzz=FuzzActiveMentionQuery -fuzztime={{time}} ./engine/composer
    CGO_ENABLED=0 go test -run='^$' -fuzz=FuzzDecodeIRJSON -fuzztime={{time}} ./engine/richtext
    CGO_ENABLED=0 go test -run='^$' -fuzz=FuzzDecodeRecoverySnapshot -fuzztime={{time}} ./engine/resilience
    CGO_ENABLED=0 go test -run='^$' -fuzz=FuzzHelmIngestAndNavigate -fuzztime={{time}} ./engine/search

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

# Enforce Go modularity hygiene: zero non-generated .go files exceeding max_lines (default 1000).
check-file-size max_lines="1000":
    @OVER=$(find . -name '*.go' -not -path './gen/*' -not -path './.devenv/*' -not -path './.tmp/*' -exec wc -l {} + | awk -v max={{max_lines}} '$1 > max && $2 != "total" {print}'); if [ -n "$OVER" ]; then echo "Go files exceeding {{max_lines}} lines:"; echo "$OVER"; exit 1; else echo "OK: all non-generated Go files are <= {{max_lines}} lines"; fi

# Rust-Cargo-style opt-in feature builds (zero dead code compiled for unselected paradigms).
build-core out="./dist/marlin-app-core":
    CGO_ENABLED=0 go build -tags marlin_minimal -o {{out}} ./cmd/marlin-app

build-features tags="marlin_feat_chatroom" out="./dist/marlin-app-custom":
    CGO_ENABLED=0 go build -tags "{{tags}}" -o {{out}} ./cmd/marlin-app

build-full out="./dist/marlin-app-full":
    CGO_ENABLED=0 go build -tags marlin_full -o {{out}} ./cmd/marlin-app

# Verify Rust-Cargo-style compile-time zero-bloat pruning across Desktop & Web WASM targets.
check-feature-pruning:
    @if go list -tags marlin_minimal -deps ./cmd/marlin-app | grep -q "features/"; then echo "FAIL: marlin_minimal linked optional features into marlin-app"; exit 1; fi
    @if GOOS=js GOARCH=wasm go list -tags marlin_minimal -deps ./cmd/marlin-web | grep -q "features/"; then echo "FAIL: marlin_minimal linked optional features into marlin-web"; exit 1; fi
    @go list -tags marlin_feat_chatroom -deps ./cmd/marlin-app | grep -q "features/chatroom"
    @if go list -tags marlin_feat_chatroom -deps ./cmd/marlin-app | grep -q "features/compare"; then echo "FAIL: marlin_feat_chatroom leaked features/compare"; exit 1; fi
    @go list -tags marlin_full -deps ./cmd/marlin-app | grep -q "features/chatroom"
    @go list -tags marlin_full -deps ./cmd/marlin-app | grep -q "features/compare"
    @go list -tags marlin_full -deps ./cmd/marlin-app | grep -q "features/canvas"
    @CGO_ENABLED=0 go build -tags marlin_minimal -o /dev/null ./cmd/marlin-app
    @CGO_ENABLED=0 go build -tags marlin_full -o /dev/null ./cmd/marlin-app
    @GOOS=js GOARCH=wasm CGO_ENABLED=0 go build -tags marlin_minimal -o /dev/null ./cmd/marlin-web
    @GOOS=js GOARCH=wasm CGO_ENABLED=0 go build -tags marlin_full -o /dev/null ./cmd/marlin-web
    @echo "OK: compile-time feature pruning verified across Desktop & Web WASM"

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
