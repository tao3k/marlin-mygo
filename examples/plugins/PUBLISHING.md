<!--
SPDX-FileCopyrightText: 2026 tao3k team and Contributors

SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
-->

# Publishing a Plugin to the Marlin Official Marketplace

Marlin supports 4 plugin runtimes — all hot-pluggable, zero-authority, and verified by the Official Marketplace before reaching end users:

| Runtime | Use Case | Target Platforms |
| --- | --- | --- |
| `webview-untrusted` (React 19 + `@tao3k/marlin-panel-sdk`) | Rich interactive panels (**Git UI**, **GitHub UI**, Org Agenda, AI Workflow Cockpits) | Desktop (macOS / Linux / Windows) |
| `wasm-ui-ir` (Go 1.27 WASI Reactor) | 120Hz zero-WebView GPU docks, telemetry, clocks, graph viewers | Desktop |
| `webview-untrusted` (Zero-dep TypeScript) | Ultra-lightweight (~10 KB) web panels | Desktop |
| `declarative` (TOML pack) | App Store 2.5.2-compliant themes, forms, and mobile capture packs | Desktop + iOS + Android + Web |

---

## Step 1: Scaffold Your Plugin

```sh
# React 19 + TypeScript (recommended for Git UI, GitHub UI, AI tools)
just plugin new-react acme.git-ui ./my-git-ui

# Or Go 1.27 WASI GPU Reactor (0 MB WebView RAM)
just plugin new-go-wasm acme.perf-dock ./my-perf-dock

# Or Cross-Platform Declarative OTA Pack (iOS + Android + Desktop)
just plugin new-declarative acme.daily-pack ./my-daily-pack
```

## Step 2: Develop Locally with Live Hot-Reload (CLI Dev Mode Only)

Local hot-reload is strictly an explicit CLI developer workflow — production UI never exposes arbitrary local directory registration:

```sh
CGO_ENABLED=0 go run ./cmd/marlin-app -dev-plugin ./my-git-ui
```

Every file save automatically rebuilds, signs with an ephemeral dev key, hot-swaps to `gen+1`, revokes the previous generation's `ScopedProxy`, and automatically rolls back to the last-known-good build if compilation or verification fails.

## Step 3: Run Security & Performance Certification (`doctor -perf`)

Before submitting to the Official Marketplace, your plugin must pass static security admission (strict CSP, zero raw network exfiltration, valid manifest scopes/actions, non-restricted slot) and the 120Hz performance gate:

```sh
CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool doctor -dir ./my-git-ui -perf
```

## Step 4: Submit a PR to the Official Marketplace Registry

1. Push your plugin repository to GitHub (HTTPS) and note the exact 40-character `commit_sha` of your release commit.
2. Open a Pull Request to `marketplace/registry.json` (or the dedicated `tao3k/marlin-marketplace` repository once mirrored) adding your entry:

```json
{
  "id": "acme.git-ui",
  "name": "Git Staging & Diff UI",
  "category": "developer-tools",
  "runtime": "webview-untrusted",
  "slot_id": "workbench.main",
  "trust_tier": "official",
  "publisher_id": "tao3k.official",
  "repository": "https://github.com/acme/marlin-git-ui",
  "commit_sha": "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
  "subdir": ".",
  "summary": "Interactive Git staging, branch graph, and hunk diff panel."
}
```

3. Verify the registry schema locally before opening the PR:

```sh
CGO_ENABLED=0 go run ./cmd/marlin-mygo-tool validate-registry -file ./marketplace/registry.json
```

## Step 5: Official CI Clean Build, Signing & Distribution

When your PR is reviewed and merged:
1. Official Marketplace CI checks out your exact pinned `commit_sha` in an isolated runner.
2. CI runs `marlin-mygo-tool doctor -perf` and builds a reproducible `.marlinpkg` archive.
3. Official release automation signs the `.marlinpkg` and `catalog.json` with the Official `tao3k.official` Ed25519 key (authors never upload self-signed production binaries).
4. Your plugin immediately appears in every Marlin user's **Official Marketplace** drawer with one-click Hot-Plug install, Upgrade (with permission-diff confirmation), Rollback, and Hot-Unplug!
