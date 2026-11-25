// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

/**
 * Unified Design Token & Base Stylesheet Engine for Marlin Untrusted WebView
 * panels. Every visual surface, border, ink, hover state, and status tint is
 * derived strictly from host-injected `--marlin-*` CSS custom properties so
 * Midnight, Paper Light, Doom One, Tokyo Night, Catppuccin, and any custom
 * Scheme/TOML `deftheme` pack render with zero hardcoded dark/light clashing.
 */

export const MARLIN_STYLE_ELEMENT_ID = "marlin-sdk-theme";

export const MARLIN_THEME_CSS = `
:root {
  --marlin-theme-id: midnight;
  --marlin-appearance: dark;
  --marlin-canvas: #0f1117;
  --marlin-bar: #161a23;
  --marlin-surface: #161a23;
  --marlin-surface-elevated: #1c202b;
  --marlin-surface-recessed: #0c0e14;
  --marlin-surface-hover: #222735;
  --marlin-surface-active: #262544;
  --marlin-border-subtle: rgba(255, 255, 255, 0.06);
  --marlin-border: rgba(255, 255, 255, 0.10);
  --marlin-border-strong: rgba(255, 255, 255, 0.18);
  --marlin-focus-ring: rgba(124, 106, 247, 0.28);
  --marlin-ink: #ebeff7;
  --marlin-ink-secondary: #c5ccd9;
  --marlin-muted: #8b94a6;
  --marlin-ink-on-accent: #ffffff;
  --marlin-accent: #7c6af7;
  --marlin-accent-hover: #8f80f9;
  --marlin-accent-soft: rgba(124, 106, 247, 0.14);
  --marlin-info: #60a5fa;
  --marlin-info-soft: rgba(96, 165, 250, 0.14);
  --marlin-success: #4ade80;
  --marlin-success-soft: rgba(74, 222, 128, 0.14);
  --marlin-warning: #fbbf24;
  --marlin-warning-soft: rgba(251, 191, 36, 0.14);
  --marlin-danger: #f87171;
  --marlin-danger-soft: rgba(248, 113, 113, 0.14);
  --marlin-shadow-sm: 0 1px 3px rgba(0, 0, 0, 0.24);
  --marlin-shadow-md: 0 6px 20px rgba(0, 0, 0, 0.28);
  --marlin-radius-sm: 6px;
  --marlin-radius-md: 9px;
  --marlin-radius-lg: 12px;
  --marlin-font-sans: -apple-system, BlinkMacSystemFont, "SF Pro Text", "Inter", system-ui, sans-serif;
  --marlin-font-mono: ui-monospace, "SF Mono", "JetBrains Mono", Menlo, monospace;
}

*, *::before, *::after { box-sizing: border-box; }

html, body {
  margin: 0;
  padding: 0;
  min-height: 100%;
  background: var(--marlin-canvas);
  color: var(--marlin-ink);
  font: 13px/1.55 var(--marlin-font-sans);
  -webkit-font-smoothing: antialiased;
  text-rendering: optimizeLegibility;
}

.m-panel {
  padding: 18px 22px 24px;
  max-width: 1180px;
  margin: 0 auto;
}

.m-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
  padding-bottom: 14px;
  margin-bottom: 16px;
  border-bottom: 1px solid var(--marlin-border-subtle);
}

.m-title {
  margin: 0;
  font-size: 16px;
  font-weight: 620;
  letter-spacing: -0.02em;
  color: var(--marlin-ink);
}

.m-caption {
  font-size: 12px;
  color: var(--marlin-muted);
  margin-top: 2px;
}

.m-row-gap {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.m-card {
  background: var(--marlin-surface);
  border: 1px solid var(--marlin-border);
  border-radius: var(--marlin-radius-lg);
  padding: 14px 16px;
  margin-bottom: 14px;
  box-shadow: var(--marlin-shadow-sm);
}

.m-card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 10px;
}

.m-section-label {
  margin: 0;
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--marlin-muted);
}

.m-split {
  display: grid;
  grid-template-columns: minmax(0, 1.65fr) minmax(240px, 1fr);
  gap: 16px;
  align-items: start;
}

@media (max-width: 740px) {
  .m-panel { padding: 14px; }
  .m-split { grid-template-columns: 1fr; }
}

.m-stat-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
  margin-bottom: 14px;
}

@media (max-width: 680px) {
  .m-stat-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}

.m-stat {
  background: var(--marlin-surface);
  border: 1px solid var(--marlin-border);
  border-radius: var(--marlin-radius-md);
  padding: 10px 12px;
}

.m-stat-value {
  display: block;
  font-size: 20px;
  font-weight: 620;
  letter-spacing: -0.02em;
  font-variant-numeric: tabular-nums;
  color: var(--marlin-ink);
  line-height: 1.25;
}

.m-stat-label {
  font-size: 11.5px;
  color: var(--marlin-muted);
}

.m-composer {
  background: var(--marlin-surface);
  border: 1px solid var(--marlin-border);
  border-radius: var(--marlin-radius-lg);
  padding: 12px 14px;
  margin-bottom: 16px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.m-composer-top {
  display: flex;
  gap: 8px;
  align-items: center;
}

.m-composer-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.m-input, .m-textarea {
  width: 100%;
  padding: 7px 10px;
  border-radius: var(--marlin-radius-sm);
  border: 1px solid var(--marlin-border);
  background: var(--marlin-surface-recessed);
  color: var(--marlin-ink);
  font: inherit;
  font-size: 12.5px;
  outline: none;
  transition: border-color 120ms ease, box-shadow 120ms ease;
}

.m-input::placeholder, .m-textarea::placeholder {
  color: var(--marlin-muted);
}

.m-input:focus, .m-textarea:focus {
  border-color: var(--marlin-accent);
  box-shadow: 0 0 0 3px var(--marlin-focus-ring);
}

.m-input-compact {
  width: auto;
  min-width: 118px;
  max-width: 160px;
  padding: 5px 9px;
  font-size: 12px;
}

.m-segmented {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 3px;
  border-radius: var(--marlin-radius-md);
  background: var(--marlin-surface-recessed);
  border: 1px solid var(--marlin-border-subtle);
  flex-wrap: wrap;
}

.m-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  border: 1px solid var(--marlin-border);
  border-radius: var(--marlin-radius-sm);
  padding: 5px 11px;
  font: inherit;
  font-size: 12px;
  font-weight: 500;
  line-height: 1.35;
  cursor: pointer;
  background: var(--marlin-surface-elevated);
  color: var(--marlin-ink);
  transition: background 110ms ease, border-color 110ms ease, color 110ms ease;
}

.m-btn:hover:not(:disabled) {
  background: var(--marlin-surface-hover);
  border-color: var(--marlin-border-strong);
}

.m-btn:disabled {
  opacity: 0.48;
  cursor: default;
}

.m-btn-primary {
  background: var(--marlin-accent);
  border-color: transparent;
  color: var(--marlin-ink-on-accent);
  font-weight: 600;
}

.m-btn-primary:hover:not(:disabled) {
  background: var(--marlin-accent-hover);
  border-color: transparent;
}

.m-btn-ghost {
  background: transparent;
  border-color: transparent;
  color: var(--marlin-muted);
}

.m-btn-ghost:hover:not(:disabled) {
  background: var(--marlin-surface-hover);
  color: var(--marlin-ink);
  border-color: transparent;
}

.m-btn-soft-success {
  background: var(--marlin-success-soft);
  border-color: transparent;
  color: var(--marlin-success);
  font-weight: 600;
}

.m-btn-soft-warning {
  background: var(--marlin-warning-soft);
  border-color: transparent;
  color: var(--marlin-warning);
  font-weight: 600;
}

.m-segmented .m-btn {
  border-color: transparent;
  background: transparent;
  color: var(--marlin-muted);
  padding: 4px 9px;
  font-size: 11.5px;
}

.m-segmented .m-btn.active {
  background: var(--marlin-surface-elevated);
  border-color: var(--marlin-border);
  color: var(--marlin-ink);
  box-shadow: var(--marlin-shadow-sm);
}

.m-list {
  display: flex;
  flex-direction: column;
}

.m-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 6px;
  border-bottom: 1px solid var(--marlin-border-subtle);
  transition: background 100ms ease;
}

.m-row:last-child { border-bottom: none; }
.m-row:hover { background: var(--marlin-surface-hover); border-radius: var(--marlin-radius-sm); }

.m-item-card {
  background: var(--marlin-surface);
  border: 1px solid var(--marlin-border);
  border-left: 3px solid var(--marlin-info);
  border-radius: var(--marlin-radius-md);
  padding: 12px 14px;
  margin-bottom: 8px;
}

.m-item-card.tone-accent { border-left-color: var(--marlin-accent); }
.m-item-card.tone-success { border-left-color: var(--marlin-success); }
.m-item-card.tone-warning { border-left-color: var(--marlin-warning); }
.m-item-card.tone-danger { border-left-color: var(--marlin-danger); }
.m-item-card.tone-muted { border-left-color: var(--marlin-muted); opacity: 0.86; }

.m-badge {
  display: inline-flex;
  align-items: center;
  padding: 1px 7px;
  border-radius: 999px;
  font-size: 10.5px;
  font-weight: 600;
  letter-spacing: 0.01em;
  background: var(--marlin-surface-recessed);
  border: 1px solid var(--marlin-border);
  color: var(--marlin-ink-secondary);
}

.m-badge.tone-accent { background: var(--marlin-accent-soft); border-color: transparent; color: var(--marlin-accent); }
.m-badge.tone-info { background: var(--marlin-info-soft); border-color: transparent; color: var(--marlin-info); }
.m-badge.tone-success { background: var(--marlin-success-soft); border-color: transparent; color: var(--marlin-success); }
.m-badge.tone-warning { background: var(--marlin-warning-soft); border-color: transparent; color: var(--marlin-warning); }
.m-badge.tone-danger { background: var(--marlin-danger-soft); border-color: transparent; color: var(--marlin-danger); }

.m-callout {
  font-size: 12px;
  color: var(--marlin-ink-secondary);
  padding: 7px 10px;
  border-radius: var(--marlin-radius-sm);
  background: var(--marlin-surface-recessed);
  border-left: 2px solid var(--marlin-info);
  margin: 6px 0;
}

.m-callout-warn {
  border-left-color: var(--marlin-warning);
  background: var(--marlin-warning-soft);
  color: var(--marlin-ink);
}

.m-clock-face {
  font-family: var(--marlin-font-mono);
  font-size: 32px;
  font-weight: 600;
  letter-spacing: -0.02em;
  font-variant-numeric: tabular-nums;
  color: var(--marlin-ink);
  margin: 4px 0 8px;
}

.m-toast {
  margin-top: 12px;
  padding: 8px 12px;
  border-radius: var(--marlin-radius-sm);
  font-size: 12px;
  color: var(--marlin-ink-secondary);
  background: var(--marlin-surface);
  border: 1px solid var(--marlin-border);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.m-mono { font-family: var(--marlin-font-mono); font-variant-numeric: tabular-nums; }
.m-empty { padding: 20px 12px; text-align: center; color: var(--marlin-muted); font-size: 12.5px; }
`;

/**
 * Applies host-broadcast `--marlin-*` CSS variables and `data-appearance` /
 * `data-theme-id` attributes atomically to `document.documentElement`.
 */
export function applyMarlinThemeTokens(tokens: Record<string, string>): void {
  if (typeof document === "undefined" || !document.documentElement) {
    return;
  }
  const root = document.documentElement;
  for (const [key, value] of Object.entries(tokens)) {
    if (!key.startsWith("--marlin-") || typeof value !== "string") continue;
    root.style.setProperty(key, value);
  }
  if (tokens["--marlin-appearance"]) {
    root.dataset.appearance = tokens["--marlin-appearance"];
  }
  if (tokens["--marlin-theme-id"]) {
    root.dataset.themeId = tokens["--marlin-theme-id"];
  }
}

/**
 * Idempotently injects Marlin's shared token-driven base stylesheet into
 * `<head>` so plugin HTML files require zero hardcoded theme CSS.
 */
export function injectMarlinBaseStyles(): void {
  if (typeof document === "undefined" || !document.head) {
    return;
  }
  if (document.getElementById(MARLIN_STYLE_ELEMENT_ID)) {
    return;
  }
  const style = document.createElement("style");
  style.id = MARLIN_STYLE_ELEMENT_ID;
  style.textContent = MARLIN_THEME_CSS;
  document.head.appendChild(style);
}
