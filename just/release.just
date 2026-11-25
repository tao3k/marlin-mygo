# SPDX-FileCopyrightText: 2026 tao3k team and Contributors
#
# SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

# Single-source-of-truth desktop binary release packaging (`CGO_ENABLED=0`).
set working-directory := '..'

default:
    @just --list release

# Cross-compile zero-cgo desktop Studio, Mobile Companion preview, and CLI tool for one GOOS/GOARCH target.
build-desktop goos goarch out="./dist/release":
    #!/usr/bin/env sh
    set -eu
    EXT=""
    if [ "{{goos}}" = "windows" ]; then EXT=".exe"; fi
    OUT_DIR="{{out}}/marlin-mygo-{{goos}}-{{goarch}}"
    mkdir -p "${OUT_DIR}"
    CGO_ENABLED=0 GOOS={{goos}} GOARCH={{goarch}} go build -trimpath -ldflags="-s -w" -o "${OUT_DIR}/marlin-app${EXT}" ./cmd/marlin-app
    CGO_ENABLED=0 GOOS={{goos}} GOARCH={{goarch}} go build -trimpath -ldflags="-s -w" -o "${OUT_DIR}/marlin-mobile${EXT}" ./cmd/marlin-mobile
    CGO_ENABLED=0 GOOS={{goos}} GOARCH={{goarch}} go build -trimpath -ldflags="-s -w" -o "${OUT_DIR}/marlin-mygo-tool${EXT}" ./cmd/marlin-mygo-tool
    if [ "{{goos}}" = "windows" ]; then
      (cd "{{out}}" && zip -q -r "marlin-mygo-{{goos}}-{{goarch}}.zip" "marlin-mygo-{{goos}}-{{goarch}}")
    else
      tar -C "{{out}}" -czf "{{out}}/marlin-mygo-{{goos}}-{{goarch}}.tar.gz" "marlin-mygo-{{goos}}-{{goarch}}"
    fi

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
