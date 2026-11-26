// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import type { WidgetNode } from "./index.ts";

type Child = WidgetNode | Child[] | null | undefined | false;
type Component = (props: Record<string, unknown>) => WidgetNode;

function flattenChildren(input: Child): WidgetNode[] {
  const out: WidgetNode[] = [];
  const walk = (c: Child): void => {
    if (!c) return;
    if (Array.isArray(c)) {
      for (const item of c) walk(item);
      return;
    }
    out.push(c);
  };
  walk(input);
  return out;
}

export function jsx(type: Component, props: Record<string, unknown>, key?: string): WidgetNode {
  const { children, ...rest } = props ?? {};
  const flat = flattenChildren(children as Child);
  const next: Record<string, unknown> = { ...rest };
  if (key && !next.id) next.id = key;
  if (flat.length > 0) next.children = flat;
  return type(next);
}

export const jsxs = jsx;
export const jsxDEV = jsx;

export namespace JSX {
  export type Element = WidgetNode;
  export interface ElementChildrenAttribute {
    children: {};
  }
}
