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

# Run all 5 standalone Linked-AOT zero-.ss cross-process Rust+Scheme Governance Authority E2E suites.
governance-aot-e2e bin="../../target/debug/marlin-scheme-host-aot":
    #!/usr/bin/env bash
    set -euo pipefail
    BIN={{quote(bin)}}
    if [[ ! -x "$BIN" ]]; then
      echo "ERROR: Linked-AOT binary not found at $BIN (run 'just scheme-host-aot-build' in marlin-agent-core)" >&2
      exit 1
    fi
    TMPDIR_E2E="$(mktemp -d /tmp/marlin-gov-aot-e2e.XXXXXX)"
    trap 'rm -rf "$TMPDIR_E2E"' EXIT

    start_aot() {
      local mode="$1" db="$2" log="$3"
      MARLIN_SCHEME_DIR=/nonexistent-zero-scheme-dir \
      MARLIN_BRIDGE_ADDR=127.0.0.1:0 \
      MARLIN_BRIDGE_TOKEN=e2e-tok \
      MARLIN_BRIDGE_TURSO_PATH="$db" \
      MARLIN_SCHEME_GOVERNANCE_MODE="$mode" \
      "$BIN" >"$log" 2>&1 &
      local pid=$!
      local addr=""
      for _ in $(seq 1 100); do
        if grep -q "^READY " "$log" 2>/dev/null; then
          addr=$(grep "^READY " "$log" | head -n1 | awk '{print $2}')
          break
        fi
        sleep 0.1
      done
      if [[ -z "$addr" ]]; then
        echo "FAILED to start AOT daemon ($mode):" >&2
        cat "$log" >&2
        kill "$pid" 2>/dev/null || true
        return 1
      fi
      echo "$pid $addr"
    }

    stop_aot() {
      kill -TERM "$1" 2>/dev/null || true
      wait "$1" 2>/dev/null || true
    }

    echo "=== [1/5] Incident + Anti-Forgery + RFC 9470 Step-Up Re-Arm ==="
    read -r PID1 ADDR1 < <(start_aot incident "$TMPDIR_E2E/e2e1.db" "$TMPDIR_E2E/e2e1.log")
    MARLIN_SCHEME_HOST_ADDR="$ADDR1" MARLIN_SCHEME_HOST_TOKEN=e2e-tok \
      go test -v -count=1 ./control/grpcwire -run "^TestLiveCrossProcessSchemeGovernanceE2E$"
    stop_aot "$PID1"

    echo "=== [2/5] Dynamic Runtime Events + Live Cedar Probes + CaMeL Taint/Sanitizer ==="
    read -r PID2 ADDR2 < <(start_aot clean "$TMPDIR_E2E/e2e2.db" "$TMPDIR_E2E/e2e2.log")
    MARLIN_SCHEME_HOST_DYNAMIC_ADDR="$ADDR2" MARLIN_SCHEME_HOST_TOKEN=e2e-tok \
      go test -v -count=1 ./control/grpcwire -run "^TestLiveCrossProcessDynamicRuntimeEventGovernanceE2E$"
    stop_aot "$PID2"

    echo "=== [3/5] Cross-OS-Restart MGV1 Anti-Restart-Reset ==="
    DB3="$TMPDIR_E2E/e2e3.db"
    read -r PID3A ADDR3A < <(start_aot incident "$DB3" "$TMPDIR_E2E/e2e3a.log")
    MARLIN_SCHEME_HOST_ADDR="$ADDR3A" MARLIN_SCHEME_HOST_TOKEN=e2e-tok MARLIN_SCHEME_HOST_RESTART_PHASE=1 \
      go test -v -count=1 ./control/grpcwire -run "^TestLiveCrossProcessGovernanceSurvivesOSRestartE2E$"
    stop_aot "$PID3A"
    read -r PID3B ADDR3B < <(start_aot clean "$DB3" "$TMPDIR_E2E/e2e3b.log")
    MARLIN_SCHEME_HOST_ADDR="$ADDR3B" MARLIN_SCHEME_HOST_TOKEN=e2e-tok MARLIN_SCHEME_HOST_RESTART_PHASE=2 \
      go test -v -count=1 ./control/grpcwire -run "^TestLiveCrossProcessGovernanceSurvivesOSRestartE2E$"
    stop_aot "$PID3B"

    echo "=== [4/5] Cross-OS-Restart MGF1 v2 Fact Graph + Dynamic Bi-Temporal DAG Blast Radius ==="
    DB4="$TMPDIR_E2E/e2e4.db"
    read -r PID4A ADDR4A < <(start_aot clean "$DB4" "$TMPDIR_E2E/e2e4a.log")
    MARLIN_SCHEME_HOST_ADDR="$ADDR4A" MARLIN_SCHEME_HOST_TOKEN=e2e-tok MARLIN_SCHEME_HOST_FACT_RESTART_PHASE=1 \
      go test -v -count=1 ./control/grpcwire -run "^TestLiveCrossProcessDurableFactGraphAcrossOSRestartE2E$"
    stop_aot "$PID4A"
    read -r PID4B ADDR4B < <(start_aot clean "$DB4" "$TMPDIR_E2E/e2e4b.log")
    MARLIN_SCHEME_HOST_ADDR="$ADDR4B" MARLIN_SCHEME_HOST_TOKEN=e2e-tok MARLIN_SCHEME_HOST_FACT_RESTART_PHASE=2 \
      go test -v -count=1 ./control/grpcwire -run "^TestLiveCrossProcessDurableFactGraphAcrossOSRestartE2E$"
    stop_aot "$PID4B"

    echo "=== [5/5] Zero-Config Auto-Discover Linked-AOT Scheme Host ==="
    go test -race -v -count=1 ./control/grpcwire -run "^TestAutoDiscoverAndHookRealSchemeHostAOT$"
    echo "OK: ALL 5 LINKED-AOT ZERO-SCHEME-DIR GOVERNANCE E2E SUITES PASSED!"

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
