<!--
SPDX-FileCopyrightText: 2026 tao3k team and Contributors

SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
-->

# Marlin UI Official Marketplace Registry

This directory maintains the canonical **Official UI Marketplace Registry** (`registry.json`) for Marlin Studio and Mobile Companions.

> **Standalone Repository**: `marketplace/registry.json` mirrors the dedicated [`tao3k/marlin-ui-marketplace`](https://github.com/tao3k/marlin-ui-marketplace) repository (`schema_version = 1`, `canonical_repo = "https://github.com/tao3k/marlin-ui-marketplace"`). Community plugins (Git UI, GitHub UI, AI workflows), themes, and Emacs layouts are submitted directly to `tao3k/marlin-ui-marketplace`.

## Security Model (Official-Only Admission)

1. **Production UI never loads arbitrary local folders or unsigned URLs.** End-user Marlin apps verify every `.marlinpkg` and `catalog.json` against Official Ed25519 release keys (`TrustRing.OfficialReleaseTrust()`).
2. **Community & External Plugins Are Welcome via PR** (e.g., **Git UI**, **GitHub UI**, Database Browsers, Kubernetes Dashboards, Custom AI Workflows):
   - Authors submit a Pull Request adding an entry to `marketplace/registry.json` pinned to an immutable 40-character Git `commit_sha` over `https://`.
   - **Official Marketplace CI** checks out the exact commit in a clean sandbox, runs `marlin-mygo-tool doctor -perf` (static security + CSP + 120Hz performance budget gate), builds the bundle reproducibly, signs it with the Official Ed25519 release key, and publishes it to the signed catalog.
3. **Restricted Host Slots Are Forbidden**: No registry entry may target `approval.gate`, `host.auth`, or `host.secrets`.

## Example External Community Submission (`registry.json`)

```json
{
  "id": "community.github-ui",
  "name": "GitHub PR & Actions UI",
  "category": "developer-tools",
  "runtime": "webview-untrusted",
  "slot_id": "workbench.main",
  "trust_tier": "official",
  "publisher_id": "tao3k.official",
  "repository": "https://github.com/example-org/marlin-github-ui",
  "commit_sha": "0123456789abcdef0123456789abcdef01234567",
  "subdir": ".",
  "summary": "Pull request review, diff comments, and GitHub Actions workflow monitor."
}
```

## Validating the Registry Locally

```sh
go run ./cmd/marlin-mygo-tool validate-registry -file ./marketplace/registry.json
```

See [examples/plugins/PUBLISHING.md](../examples/plugins/PUBLISHING.md) for the complete plugin authoring, local hot-reload, certification, and Official Marketplace submission guide.
