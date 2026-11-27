// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

/**
 * Example: Author a complex floating left-edge Sidebar + W3C OKLCH Design Tokens
 * in TypeScript/TSX and emit governed JSON artifacts for ~/.config/marlin/ui/.
 */
import {
  ActionTile,
  Card,
  Col,
  LinkList,
  RingGauge,
  Row,
  WebIsland,
  serializeDesignTokens,
  serializeSidebar,
} from "../src/index.ts";

export const customSidebarJSON = serializeSidebar({
  width: 268,
  pinned: false,
  body: (
    <Col id="ts-sidebar-root" gap={10} flex={1}>
      <Card id="sb-telemetry" title="SYSTEM & AGENTS" badge="Live" gap={8}>
        <Row gap={8}>
          <RingGauge id="sb-tasks" icon="check" title="Tasks" value="{{tasks.completed}}/{{tasks.total}}" progress="{{tasks.progress}}" tone="danger" onClick="feature:chatroom" />
          <RingGauge id="sb-agents" icon="agents" title="Agents" value="{{agents.online}}/{{agents.total}}" progress="{{agents.progress}}" tone="success" onClick="feature:chatroom" />
        </Row>
      </Card>

      <WebIsland id="sb-bpmn-mini" title="FLOW PREVIEW" subtitle="Hybrid GPU + WebView" islandId="workflow-graph" height={156} />

      <LinkList
        id="sb-buffers"
        icon="drive"
        value="{{buffers.count}} Buffers"
        badge="{{theme.name}}"
        flex={1}
        links={[
          { id: "conv", icon: "chat", label: "Conversation · {{turns.count}}", tone: "info", action: "buffer:*conversation*" },
          { id: "room", icon: "agents", label: "Agent Room · {{tasks.completed}}/{{tasks.total}}", tone: "success", action: "feature:chatroom" },
          { id: "web", icon: "cube", label: "Workflow Canvas · BPMN", tone: "accent", action: "feature:webisland" },
        ]}
      />

      <Row gap={8} height={54}>
        <ActionTile id="sb-glass" icon="glass" title="Glass" badge="{{mod.glass}}" tone="info" flex={1} onClick="cmd:toggle-glass" />
        <ActionTile id="sb-zen" icon="zen" title="Zen" badge="{{mod.zen}}" tone="success" flex={1} onClick="cmd:zen-mode" />
      </Row>
    </Col>
  ),
});

export const oklchTokensJSON = serializeDesignTokens({
  color: {
    $type: "color",
    bg: { $value: "oklch(0.22 0.018 258)" },
    surface: { $value: "oklch(0.28 0.020 256)" },
    accent: { $value: "oklch(0.74 0.12 330)" },
    info: { $value: "oklch(0.78 0.08 215)" },
  },
  radius: {
    card: { $type: "dimension", $value: "20px" },
  },
});
