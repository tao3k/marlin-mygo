# SPDX-FileCopyrightText: 2026 tao3k team and Contributors
#
# SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

# Static-deployable WebAssembly frontend built from the exact same Go UI library.
set working-directory := '..'

default:
    @just --list web

# Build static-deployable Web/WASM bundle (marlin-web.wasm + wasm_exec.js + index.html).
build out="./dist/web":
    #!/usr/bin/env sh
    set -eu
    mkdir -p "{{out}}"
    GOOS=js GOARCH=wasm CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "{{out}}/marlin-web.wasm" ./cmd/marlin-web
    GOROOT="$(go env GOROOT)"
    if [ -f "${GOROOT}/lib/wasm/wasm_exec.js" ]; then
      cp "${GOROOT}/lib/wasm/wasm_exec.js" "{{out}}/wasm_exec.js"
    else
      cp "${GOROOT}/misc/wasm/wasm_exec.js" "{{out}}/wasm_exec.js"
    fi
    cp ./cmd/marlin-web/index.html "{{out}}/index.html"
    ls -lh "{{out}}/marlin-web.wasm" "{{out}}/wasm_exec.js" "{{out}}/index.html"

# Serve the built static Web/WASM bundle locally over HTTP.
serve dir="./dist/web" addr="127.0.0.1:8088":
    CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool serve-catalog -dir "{{dir}}" -addr "{{addr}}"
