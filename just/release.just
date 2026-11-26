# SPDX-FileCopyrightText: 2026 tao3k team and Contributors
#
# SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

# Single-source-of-truth desktop binary release packaging (`CGO_ENABLED=0`).
set working-directory := '..'

default:
    @just --list release

# Cross-compile zero-cgo desktop Studio, Mobile Companion preview, and CLI tool for one GOOS/GOARCH target.
# Optionally bundles a matching standalone zero-`.ss` Linked-AOT `marlin-scheme-host-aot` sibling binary.
build-desktop goos goarch out="./dist/release" scheme_aot="":
    #!/usr/bin/env bash
    set -euo pipefail
    EXT=""
    if [[ "{{goos}}" == "windows" ]]; then EXT=".exe"; fi
    OUT_DIR="{{out}}/marlin-mygo-{{goos}}-{{goarch}}"
    mkdir -p "${OUT_DIR}"
    CGO_ENABLED=0 GOOS={{goos}} GOARCH={{goarch}} go build -trimpath -ldflags="-s -w" -o "${OUT_DIR}/marlin-app${EXT}" ./cmd/marlin-app
    CGO_ENABLED=0 GOOS={{goos}} GOARCH={{goarch}} go build -trimpath -ldflags="-s -w" -o "${OUT_DIR}/marlin-mobile${EXT}" ./cmd/marlin-mobile
    CGO_ENABLED=0 GOOS={{goos}} GOARCH={{goarch}} go build -trimpath -ldflags="-s -w" -o "${OUT_DIR}/marlin-mygo-tool${EXT}" ./cmd/marlin-mygo-tool
    SCHEME_AOT={{quote(scheme_aot)}}
    if [[ -n "${SCHEME_AOT}" ]]; then
      if [[ ! -f "${SCHEME_AOT}" || ! -x "${SCHEME_AOT}" ]]; then
        printf 'ERROR: scheme_aot binary not executable: %s\n' "${SCHEME_AOT}" >&2
        exit 1
      fi
      DEST="${OUT_DIR}/marlin-scheme-host-aot${EXT}"
      if [[ "$(uname -s)" == "Darwin" && "{{goos}}" == "darwin" ]]; then
        /usr/bin/ditto --hfsCompression "${SCHEME_AOT}" "${DEST}"
      else
        install -m 0755 -- "${SCHEME_AOT}" "${DEST}"
      fi
    fi
    if [[ "{{goos}}" == "windows" ]]; then
      (cd "{{out}}" && zip -q -r "marlin-mygo-{{goos}}-{{goarch}}.zip" "marlin-mygo-{{goos}}-{{goarch}}")
    else
      tar -C "{{out}}" -czf "{{out}}/marlin-mygo-{{goos}}-{{goarch}}.tar.gz" "marlin-mygo-{{goos}}-{{goarch}}"
    fi

# Verify a staged standalone zero-`.ss` `marlin-scheme-host-aot` binary boots and exits cleanly on SIGTERM.
smoke-scheme-aot binary:
    #!/usr/bin/env bash
    set -euo pipefail
    BIN={{quote(binary)}}
    if [[ ! -f "$BIN" || ! -x "$BIN" ]]; then
      printf 'ERROR: expected executable Linked-AOT binary at %s\n' "$BIN" >&2
      exit 1
    fi
    SMOKE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/marlin-release-aot-smoke.XXXXXX")"
    PID=""
    cleanup() {
      if [[ -n "$PID" ]] && kill -0 "$PID" 2>/dev/null; then
        kill -TERM "$PID" 2>/dev/null || true
        wait "$PID" 2>/dev/null || true
      fi
      rm -rf "$SMOKE_DIR"
    }
    trap cleanup EXIT
    MARLIN_SCHEME_DIR=/nonexistent-zero-scheme-dir \
    MARLIN_BRIDGE_ADDR=127.0.0.1:0 \
    MARLIN_BRIDGE_TOKEN=release-smoke-tok \
    MARLIN_BRIDGE_TURSO_PATH="$SMOKE_DIR/smoke.db" \
    MARLIN_SCHEME_GOVERNANCE_MODE=clean \
    "$BIN" >"$SMOKE_DIR/daemon.log" 2>&1 &
    PID=$!
    READY=""
    for _ in $(seq 1 100); do
      if grep -qE '^READY 127\.0\.0\.1:[0-9]+' "$SMOKE_DIR/daemon.log" 2>/dev/null; then
        READY=$(grep -E '^READY 127\.0\.0\.1:[0-9]+' "$SMOKE_DIR/daemon.log" | head -n1)
        break
      fi
      sleep 0.1
    done
    if [[ -z "$READY" ]]; then
      printf 'FAIL: %s did not emit READY without .ss files:\n' "$BIN" >&2
      cat "$SMOKE_DIR/daemon.log" >&2
      exit 1
    fi
    kill -TERM "$PID"
    wait "$PID"
    PID=""
    printf 'OK: bundled zero-.ss marlin-scheme-host-aot smoke passed (%s -> %s)\n' "$BIN" "$READY"

# Build native host desktop Studio bundle WITH zero-`.ss` `marlin-scheme-host-aot` sibling + smoke check + SHA256SUMS.
build-desktop-bundle out="./dist/release" scheme_aot="../../target/debug/marlin-scheme-host-aot":
    #!/usr/bin/env bash
    set -euo pipefail
    HOST_GOOS="$(go env GOOS)"
    HOST_GOARCH="$(go env GOARCH)"
    just release build-desktop "$HOST_GOOS" "$HOST_GOARCH" {{quote(out)}} {{quote(scheme_aot)}}
    just release smoke-scheme-aot "{{out}}/marlin-mygo-${HOST_GOOS}-${HOST_GOARCH}/marlin-scheme-host-aot"
    just release checksums {{quote(out)}}

# Build a self-contained macOS Marlin.app bundle shipping `marlin-app` + sibling zero-`.ss` `marlin-scheme-host-aot`.
build-macos-app out="./dist/release" scheme_aot="../../target/debug/marlin-scheme-host-aot" version="0.1.0":
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ "$(uname -s)" != "Darwin" ]]; then
      printf 'ERROR: build-macos-app requires macOS\n' >&2
      exit 1
    fi
    SCHEME_AOT={{quote(scheme_aot)}}
    if [[ ! -f "$SCHEME_AOT" || ! -x "$SCHEME_AOT" ]]; then
      printf 'ERROR: scheme_aot binary not executable: %s\n' "$SCHEME_AOT" >&2
      exit 1
    fi
    APP_DIR="{{out}}/Marlin.app"
    MACOS_DIR="${APP_DIR}/Contents/MacOS"
    rm -rf "${APP_DIR}"
    mkdir -p "${MACOS_DIR}"
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "${MACOS_DIR}/marlin-app" ./cmd/marlin-app
    /usr/bin/ditto --hfsCompression "${SCHEME_AOT}" "${MACOS_DIR}/marlin-scheme-host-aot"
    cat > "${APP_DIR}/Contents/Info.plist" <<EOF
    <?xml version="1.0" encoding="UTF-8"?>
    <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
    <plist version="1.0">
    <dict>
      <key>CFBundleDevelopmentRegion</key><string>en</string>
      <key>CFBundleExecutable</key><string>marlin-app</string>
      <key>CFBundleIdentifier</key><string>dev.tao3k.marlin.studio</string>
      <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
      <key>CFBundleName</key><string>Marlin</string>
      <key>CFBundlePackageType</key><string>APPL</string>
      <key>CFBundleShortVersionString</key><string>{{version}}</string>
      <key>CFBundleVersion</key><string>{{version}}</string>
      <key>NSHighResolutionCapable</key><true/>
    </dict>
    </plist>
    EOF
    just release smoke-scheme-aot "${MACOS_DIR}/marlin-scheme-host-aot"
    printf 'OK: staged self-contained macOS bundle %s\n' "${APP_DIR}"

# Generate deterministic SHA256SUMS.txt over all packaged release archives.
checksums dir="./dist/release":
    #!/usr/bin/env sh
    set -eu
    cd "{{dir}}"
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum marlin-mygo-*.tar.gz marlin-mygo-*.zip 2>/dev/null | sort > SHA256SUMS.txt || sha256sum marlin-mygo-* | sort > SHA256SUMS.txt
    else
      shasum -a 256 marlin-mygo-*.tar.gz marlin-mygo-*.zip 2>/dev/null | sort > SHA256SUMS.txt || shasum -a 256 marlin-mygo-* | sort > SHA256SUMS.txt
    fi
