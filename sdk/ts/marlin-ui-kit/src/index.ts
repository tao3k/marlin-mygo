// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

/**
 * @marlin/ui-kit — Zero-dependency TypeScript / TSX SDK for authoring:
 *   1. Native 120Hz GPU Bento widgets & dashboards (emitted to dashboard.json / widget.json)
 *   2. Complex floating auto-hide left sidebars (emitted to sidebar.json)
 *   3. W3C Design Tokens with OKLCH perceptual colors (emitted to tokens.json)
 *   4. Hybrid GPU + Sandboxed WebView islands (Flowcharts, BPMN, Mermaid, ECharts)
 */

export type Tone = "accent" | "info" | "success" | "warning" | "danger" | "muted";

export type IconName =
  | "sparkles"
  | "check"
  | "chat"
  | "agents"
  | "search"
  | "command"
  | "sessions"
  | "host"
  | "cpu"
  | "cube"
  | "settings"
  | "zen"
  | "reload"
  | "theme"
  | "glass"
  | "motion"
  | "bolt"
  | "clock"
  | "timer"
  | "drive"
  | "sidebar"
  | "dashboard"
  | "terminal"
  | "plus"
  | "folder"
  | "send";

export type ActionURI =
  | `buffer:${string}`
  | `feature:${string}`
  | `cmd:${string}`
  | `theme:${string}`
  | `hydra:${string}`
  | `rpc:${string}`
  | `island:${string}:${string}`;

export type VibrancyMode = "auto" | "hud" | "under-window" | "sidebar" | "popover" | "none";

export interface SurfaceConfig {
  glass?: boolean;
  motion?: boolean;
  vibrancy?: VibrancyMode;
  glassOpacity?: number;
  chromeOpacity?: number;
  viewportOpacity?: number;
}

export interface ManifestConfig {
  activeTheme?: string;
  surface?: SurfaceConfig;
}

export type WidgetKind =
  | "row"
  | "col"
  | "card"
  | "profile"
  | "clock"
  | "kpi"
  | "hero"
  | "gauge"
  | "trend"
  | "composer"
  | "task-list"
  | "tile-grid"
  | "action-tile"
  | "badge-card"
  | "link-list"
  | "web-island"
  | "scroll"
  | "section"
  | "divider"
  | "text"
  | "spacer";

export interface TileItem {
  id: string;
  icon: IconName;
  label: string;
  tone?: Tone;
  action?: ActionURI;
}

export interface LinkItem {
  id: string;
  icon: IconName;
  label: string;
  tone?: Tone;
  action?: ActionURI;
}

export interface WidgetNode {
  id?: string;
  kind: WidgetKind;
  classes?: string[];
  width?: number;
  height?: number;
  flex?: number;
  gap?: number;
  tone?: Tone;
  solidBg?: boolean;
  icon?: IconName;
  title?: string;
  subtitle?: string;
  body?: string;
  value?: string;
  progress?: string;
  badge?: string;
  onClick?: ActionURI;
  playAction?: ActionURI;
  islandId?: string;
  borderless?: boolean;
  visibleWhen?: string;
  tiles?: TileItem[];
  links?: LinkItem[];
  children?: WidgetNode[];
}

export interface SidebarSpec {
  schemaVersion: "marlin.ui.sidebar.v1";
  width?: number;
  pinned?: boolean;
  body: WidgetNode;
}

export interface DesignTokenLeaf {
  $type?: "color" | "dimension" | "fontFamily" | "fontWeight" | "number";
  $value: string | number | string[];
}

export interface DesignTokenGroup {
  $type?: DesignTokenLeaf["$type"];
  [key: string]: DesignTokenLeaf | DesignTokenGroup | string | undefined;
}

/** Emit a validated JSON payload for ~/.config/marlin/ui/dashboard.json or widget.json. */
export function serializeWidget(node: WidgetNode): string {
  return JSON.stringify(node, null, 2);
}

/** Emit a validated JSON payload for ~/.config/marlin/ui/sidebar.json. */
export function serializeSidebar(spec: Omit<SidebarSpec, "schemaVersion">): string {
  const doc: SidebarSpec = { schemaVersion: "marlin.ui.sidebar.v1", ...spec };
  return JSON.stringify(doc, null, 2);
}

/** Emit a W3C Design Tokens JSON document (supports oklch(...) & {alias} refs). */
export function serializeDesignTokens(tokens: DesignTokenGroup): string {
  return JSON.stringify(tokens, null, 2);
}

/** Emit a governed ~/.config/marlin/ui/manifest.toml with optional [surface] settings. */
export function serializeManifestTOML(cfg: ManifestConfig): string {
  const lines: string[] = ['schema_version = "marlin.ui.theme.v1"'];
  if (cfg.activeTheme) {
    lines.push(`active_theme = ${JSON.stringify(cfg.activeTheme)}`);
  }
  const s = cfg.surface;
  if (s) {
    lines.push("", "[surface]");
    if (s.glass !== undefined) lines.push(`glass = ${s.glass}`);
    if (s.motion !== undefined) lines.push(`motion = ${s.motion}`);
    if (s.vibrancy) lines.push(`vibrancy = ${JSON.stringify(s.vibrancy)}`);
    if (s.glassOpacity !== undefined) lines.push(`glass_opacity = ${s.glassOpacity}`);
    if (s.chromeOpacity !== undefined) lines.push(`chrome_opacity = ${s.chromeOpacity}`);
    if (s.viewportOpacity !== undefined) lines.push(`viewport_opacity = ${s.viewportOpacity}`);
  }
  return lines.join("\n") + "\n";
}

/** Theme detail broadcast live into Hybrid WebView islands via CustomEvent("marlin:theme"). */
export interface MarlinIslandThemeDetail {
  surface: string;
  bar: string;
  ink: string;
  muted: string;
  accent: string;
  info: string;
  success: string;
  warning: string;
  danger: string;
  isLight: boolean;
  glassOpacity: number;
}

// Typed functional / JSX constructors mapped 1:1 to marlin-mygo/runtime/widgets.Node.
export type NodeProps = Omit<WidgetNode, "kind">;
const make = (kind: WidgetKind) => (props: NodeProps = {}): WidgetNode => ({ kind, ...props });

export const Row = make("row");
export const Col = make("col");
export const Card = make("card");
export const Profile = make("profile");
export const Clock = make("clock");
export const KPI = make("kpi");
export const RingGauge = make("gauge");
export const Trend = make("trend");
export const Composer = make("composer");
export const TaskList = make("task-list");
export const TileGrid = make("tile-grid");
export const ActionTile = make("action-tile");
export const LinkList = make("link-list");
export const WebIsland = make("web-island");
export const Scroll = make("scroll");
export const Section = make("section");
export const Divider = make("divider");
export const Text = make("text");
export const Spacer = make("spacer");

/**
 * Declarative Layer-2 -> Layer-3 Vercel AI SDK Generative UI surface helper.
 * Embeds a governed sandboxed WebView island subscribed to `data-*` parts.
 */
export interface GenerativeUISurfaceProps extends NodeProps {
  islandId: string;
  dataType?: `data-${string}`;
}

export function GenerativeUISurface({
  islandId,
  dataType,
  title,
  badge,
  ...rest
}: GenerativeUISurfaceProps): WidgetNode {
  return {
    kind: "web-island",
    islandId,
    title: title ?? dataType ?? "Generative UI",
    badge: badge ?? dataType ?? "ai-sdk",
    ...rest,
  };
}
