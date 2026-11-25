// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import {
  MarlinPanelProvider,
  useMarlinAction,
  useMarlinProjection,
  useMarlinTheme,
} from "./react";

function SampleReactPanel() {
  const { data } = useMarlinProjection<{ title: string }>("org.agenda", { title: "Daily Plan" });
  const action = useMarlinAction<{ id: string }>("org.todo.toggle");
  const theme = useMarlinTheme({ "--marlin-accent": "#c678dd" });
  return (
    <section data-accent={theme.accent} data-pending={String(action.pending)}>
      <h2>{data.title}</h2>
    </section>
  );
}

describe("@tao3k/marlin-panel-sdk/react (React 19 Hooks)", () => {
  test("renders React 19 component with MarlinPanelProvider, projection, action, and theme hooks", () => {
    const html = renderToStaticMarkup(
      <MarlinPanelProvider pluginId="tao3k.react-demo">
        <SampleReactPanel />
      </MarlinPanelProvider>,
    );
    expect(html).toContain("Daily Plan");
    expect(html).toContain('data-accent="#c678dd"');
    expect(html).toContain('data-pending="false"');
  });
});
