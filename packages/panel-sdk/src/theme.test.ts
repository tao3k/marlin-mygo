// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { describe, expect, test } from "bun:test";
import { Window } from "happy-dom";
import {
  MARLIN_STYLE_ELEMENT_ID,
  MARLIN_THEME_CSS,
  applyMarlinThemeTokens,
  injectMarlinBaseStyles,
} from "./theme";

describe("Marlin unified token-driven theme stylesheet", () => {
  test("MARLIN_THEME_CSS uses semantic tokens with zero dark-only overrides on components", () => {
    expect(MARLIN_THEME_CSS).toContain("--marlin-surface-elevated");
    expect(MARLIN_THEME_CSS).toContain("--marlin-ink-on-accent");
    expect(MARLIN_THEME_CSS).toContain(".m-composer");
    expect(MARLIN_THEME_CSS).toContain(".m-segmented");
  });

  test("injectMarlinBaseStyles and applyMarlinThemeTokens update DOM idempotently", () => {
    const win = new Window();
    const prevDoc = globalThis.document;
    (globalThis as unknown as { document: Document }).document = win.document as unknown as Document;
    try {
      injectMarlinBaseStyles();
      injectMarlinBaseStyles();
      const styles = win.document.querySelectorAll(`#${MARLIN_STYLE_ELEMENT_ID}`);
      expect(styles.length).toBe(1);

      applyMarlinThemeTokens({
        "--marlin-theme-id": "paper",
        "--marlin-appearance": "light",
        "--marlin-canvas": "#f6f7fa",
        "--marlin-ink": "#141822",
      });
      expect(win.document.documentElement.dataset.appearance).toBe("light");
      expect(win.document.documentElement.dataset.themeId).toBe("paper");
      expect(win.document.documentElement.style.getPropertyValue("--marlin-canvas")).toBe("#f6f7fa");
    } finally {
      (globalThis as unknown as { document: Document }).document = prevDoc;
      win.close();
    }
  });
});
