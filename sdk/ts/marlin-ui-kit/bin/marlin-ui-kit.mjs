#!/usr/bin/env node
// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

/**
 * Zero-dependency CLI for @marlin/ui-kit:
 *   marlin-ui-kit validate <file.json>  — validate sidebar.json / dashboard.json / tokens.json
 *   marlin-ui-kit init <outDir>         — emit starter manifest.toml, tokens.json, sidebar.json
 */
import fs from "node:fs";
import path from "node:path";

const ALLOWED_KINDS = new Set([
  "row", "col", "card", "profile", "clock", "kpi", "hero",
  "gauge", "trend", "control", "composer", "task-list",
  "tile-grid", "action-tile", "badge-card", "link-list",
  "web-island", "scroll", "section", "divider", "text", "spacer",
]);

const ALLOWED_URI_PREFIXES = [
  "buffer:", "feature:", "cmd:", "theme:", "hydra:", "rpc:", "island:",
];

function validateAction(uri, loc) {
  if (!uri) return;
  if (!ALLOWED_URI_PREFIXES.some((p) => uri.startsWith(p))) {
    throw new Error(`${loc}: disallowed action URI ${JSON.stringify(uri)}`);
  }
}

function validateNode(node, loc = "$", depth = 0, counter = { n: 0 }) {
  if (!node || typeof node !== "object") throw new Error(`${loc}: expected object`);
  if (depth > 12) throw new Error(`${loc}: exceeds max depth 12`);
  counter.n += 1;
  if (counter.n > 256) throw new Error(`${loc}: exceeds max node count 256`);
  if (!ALLOWED_KINDS.has(node.kind)) {
    throw new Error(`${loc}: disallowed widget kind ${JSON.stringify(node.kind)}`);
  }
  validateAction(node.onClick, `${loc}.onClick`);
  validateAction(node.playAction, `${loc}.playAction`);
  if (Array.isArray(node.tiles)) {
    node.tiles.forEach((t, i) => validateAction(t.action, `${loc}.tiles[${i}].action`));
  }
  if (Array.isArray(node.links)) {
    node.links.forEach((l, i) => validateAction(l.action, `${loc}.links[${i}].action`));
  }
  if (Array.isArray(node.children)) {
    node.children.forEach((ch, i) => validateNode(ch, `${loc}.children[${i}]`, depth + 1, counter));
  }
}

const [cmd, arg] = process.argv.slice(2);
if (cmd === "validate" && arg) {
  const raw = fs.readFileSync(arg, "utf8");
  const doc = JSON.parse(raw);
  if (doc.schemaVersion === "marlin.ui.sidebar.v1" && doc.body) {
    validateNode(doc.body, "$.body");
    console.log(`OK: valid Marlin sidebar IR (${arg})`);
  } else if (doc.kind) {
    validateNode(doc, "$");
    console.log(`OK: valid Marlin widget IR (${arg})`);
  } else {
    console.log(`OK: valid JSON design token document (${arg})`);
  }
} else if (cmd === "init" && arg) {
  fs.mkdirSync(arg, { recursive: true });
  const manifest = [
    'schema_version = "marlin.ui.theme.v1"',
    'active_theme = "nord-bento"',
    "",
    "[surface]",
    "glass = true",
    "motion = true",
    'vibrancy = "auto"',
    "glass_opacity = 0.34",
    "chrome_opacity = 0.18",
    "viewport_opacity = 0.22",
    "",
  ].join("\n");
  fs.writeFileSync(path.join(arg, "manifest.toml"), manifest, "utf8");
  const tokens = {
    $description: "Marlin W3C DTCG 2025.10 starter design tokens",
    color: {
      accent: { $type: "color", $value: "oklch(0.75 0.12 220)" },
      surface: { $type: "color", $value: "oklch(0.24 0.02 250)" },
    },
    glass: {
      opacity: { $type: "number", $value: 0.34 },
    },
  };
  fs.writeFileSync(path.join(arg, "tokens.json"), JSON.stringify(tokens, null, 2) + "\n", "utf8");
  const sidebar = {
    schemaVersion: "marlin.ui.sidebar.v1",
    body: {
      kind: "scroll",
      children: [
        {
          kind: "section",
          title: "Workspace",
          children: [
            { kind: "action-tile", title: "Dashboard", action: "buffer:*Dashboard*" },
            { kind: "action-tile", title: "Workflow Studio", action: "buffer:*Workflow Canvas*" },
          ],
        },
      ],
    },
  };
  fs.writeFileSync(path.join(arg, "sidebar.json"), JSON.stringify(sidebar, null, 2) + "\n", "utf8");
  console.log(`Initialized Marlin UI kit config in ${arg}`);
} else {
  console.error("Usage: marlin-ui-kit <validate <file.json> | init <outDir>>");
  process.exit(2);
}
