// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package widgets

import "fmt"

// DefaultDashboard returns Marlin's flagship fluid edge-to-edge tiling Bento
// widget desktop (inspired by EWW + iOS 26 Liquid Glass + Android 16 M3
// Expressive), with real native SF Symbols, GPU shader ThinkingOrb, live
// interactive Tasks, Quick-Ask Agent Composer, activity sparkline, and
// hot-pluggable UI module switches.
func DefaultDashboard(userWidgets []*Node) *Node {
	leftCol := &Node{
		ID:   "bento-left",
		Kind: KindCol,
		Flex: 1.0,
		Gap:  14,
		Children: []*Node{
			{
				ID:       "w-profile",
				Kind:     KindProfile,
				Flex:     1.10,
				Icon:     "sparkles",
				Title:    "Marlin AI",
				Subtitle: "@{{agent.session}} · {{agent.host}}",
				Badge:    "{{agent.status}}",
				Tone:     "accent",
				OnClick:  "buffer:*conversation*",
			},
			{
				ID:   "w-gauges",
				Kind: KindCard,
				Flex: 1.05,
				Gap:  8,
				Children: []*Node{
					{ID: "g-tasks", Kind: KindGauge, Icon: "check", Title: "Tasks", Value: "{{tasks.completed}}/{{tasks.total}}", Progress: "{{tasks.progress}}", Tone: "danger", OnClick: "feature:chatroom"},
					{ID: "g-agents", Kind: KindGauge, Icon: "agents", Title: "Agents", Value: "{{agents.online}}/{{agents.total}}", Progress: "{{agents.progress}}", Tone: "success", OnClick: "feature:chatroom"},
					{ID: "g-sess", Kind: KindGauge, Icon: "sessions", Title: "Turns", Value: "{{turns.count}}", Progress: "{{sessions.progress}}", Tone: "warning", OnClick: "buffer:*Sessions*"},
					{ID: "g-host", Kind: KindGauge, Icon: "host", Title: "Targets", Value: "{{targets.count}}", Progress: "{{targets.progress}}", Tone: "info", OnClick: "buffer:*Targets*"},
					{ID: "g-trend", Kind: KindTrend, Tone: "info", Height: 34},
				},
			},
		},
	}

	centerTop := &Node{
		ID:   "bento-center-top",
		Kind: KindRow,
		Flex: 1.15,
		Gap:  14,
		Children: []*Node{
			{
				ID:   "bento-clock-stack",
				Kind: KindCol,
				Flex: 0.92,
				Gap:  14,
				Children: []*Node{
					{
						ID:       "w-clock",
						Kind:     KindClock,
						Flex:     1.0,
						Title:    "{{clock.hour}}:{{clock.min}}",
						Value:    "{{clock.sec}}",
						Badge:    "{{clock.ampm}}",
						Subtitle: "{{clock.weekday}}",
					},
					{
						ID:       "w-uptime",
						Kind:     KindKPI,
						Flex:     1.0,
						Icon:     "clock",
						Value:    "{{uptime.hours}}h {{uptime.mins}}m",
						Title:    "New Session",
						Subtitle: "{{sessions.count}} active · {{agent.model}}",
						Tone:     "accent",
						OnClick:  "cmd:new-session",
					},
				},
			},
			{
				ID:       "w-tasks",
				Kind:     KindTaskList,
				Flex:     1.65,
				Icon:     "check",
				Title:    "Agent Tasks",
				Value:    "{{tasks.completed}}/{{tasks.total}} Done",
				Subtitle: "{{agents.online}} agents ready",
				Tone:     "info",
				OnClick:  "feature:chatroom",
			},
		},
	}

	centerMid := &Node{
		ID:   "bento-center-mid",
		Kind: KindRow,
		Flex: 1.12,
		Gap:  14,
		Children: []*Node{
			{
				ID:         "w-composer",
				Kind:       KindComposer,
				Flex:       1.65,
				Icon:       "bolt",
				Title:      "Quick Ask · {{agent.model}}",
				Subtitle:   "Session {{agent.session}}",
				Progress:   "{{tasks.progress}}",
				Tone:       "accent",
				PrevAction: "buffer:*Models*",
				PlayAction: "buffer:*conversation*",
				NextAction: "feature:chatroom",
			},
			{
				ID:   "w-launcher",
				Kind: KindTileGrid,
				Flex: 0.92,
				Tiles: []TileItem{
					{ID: "t-chat", Icon: "chat", Label: "Chat", Tone: "info", Action: "buffer:*conversation*"},
					{ID: "t-room", Icon: "agents", Label: "Room", Tone: "accent", Action: "feature:chatroom"},
					{ID: "t-helm", Icon: "search", Label: "Helm", Tone: "warning", Action: "cmd:helm"},
					{ID: "t-mx", Icon: "command", Label: "M-x", Tone: "success", Action: "cmd:mx"},
					{ID: "t-sess", Icon: "sessions", Label: "Sess", Tone: "info", Action: "buffer:*Sessions*"},
					{ID: "t-targ", Icon: "host", Label: "Host", Tone: "danger", Action: "buffer:*Targets*"},
					{ID: "t-proc", Icon: "cpu", Label: "Proc", Tone: "warning", Action: "buffer:*Processes*"},
					{ID: "t-mod", Icon: "cube", Label: "Mod", Tone: "accent", Action: "buffer:*Models*"},
					{ID: "t-set", Icon: "settings", Label: "Cfg", Tone: "success", Action: "buffer:*Settings*"},
				},
			},
		},
	}

	centerBottom := &Node{
		ID:   "bento-center-bot",
		Kind: KindRow,
		Flex: 0.54,
		Gap:  14,
		Children: []*Node{
			{ID: "a-chat", Kind: KindActionTile, Flex: 1, Icon: "chat", Title: "Chat", Tone: "info", SolidBg: false, OnClick: "buffer:*conversation*"},
			{ID: "a-helm", Kind: KindActionTile, Flex: 1, Icon: "search", Title: "Helm", Tone: "warning", SolidBg: true, OnClick: "cmd:helm"},
			{ID: "a-mx", Kind: KindActionTile, Flex: 1, Icon: "command", Title: "M-x", Tone: "info", SolidBg: true, OnClick: "cmd:mx"},
			{ID: "a-room", Kind: KindActionTile, Flex: 1, Icon: "agents", Title: "Room", Tone: "danger", SolidBg: true, OnClick: "feature:chatroom"},
			{
				ID:      "a-counter",
				Kind:    KindBadgeCard,
				Flex:    2.0,
				Icon:    "check",
				Value:   "{{tasks.completed}}",
				Title:   "Tasks Done",
				Tone:    "success",
				OnClick: "feature:chatroom",
			},
		},
	}

	centerCol := &Node{
		ID:       "bento-center",
		Kind:     KindCol,
		Flex:     2.6,
		Gap:      14,
		Children: []*Node{centerTop, centerMid, centerBottom},
	}

	rightCol := &Node{
		ID:   "bento-right",
		Kind: KindCol,
		Flex: 1.0,
		Gap:  14,
		Children: []*Node{
			{
				ID:   "bento-mod-row1",
				Kind: KindRow,
				Flex: 0.48,
				Gap:  14,
				Children: []*Node{
					{ID: "p-glass", Kind: KindActionTile, Flex: 1, Icon: "glass", Title: "Glass", Badge: "{{mod.glass}}", Tone: "info", OnClick: "cmd:toggle-glass"},
					{ID: "p-motion", Kind: KindActionTile, Flex: 1, Icon: "motion", Title: "Motion", Badge: "{{mod.motion}}", Tone: "accent", OnClick: "cmd:toggle-motion"},
				},
			},
			{
				ID:   "bento-mod-row2",
				Kind: KindRow,
				Flex: 0.48,
				Gap:  14,
				Children: []*Node{
					{ID: "p-zen", Kind: KindActionTile, Flex: 1, Icon: "zen", Title: "Zen", Badge: "{{mod.zen}}", Tone: "success", OnClick: "cmd:zen-mode"},
					{ID: "p-reload", Kind: KindActionTile, Flex: 1, Icon: "reload", Title: "Reload", Badge: "UI", Tone: "warning", OnClick: "cmd:reload-theme"},
				},
			},
			{
				ID:    "w-links",
				Kind:  KindLinkList,
				Flex:  1.45,
				Icon:  "folder",
				Value: "{{buffers.count}} Buffers",
				Badge: "{{theme.name}}",
				Links: []LinkItem{
					{ID: "l-conv", Icon: "chat", Label: "*conversation* ({{turns.count}})", Tone: "danger", Action: "buffer:*conversation*"},
					{ID: "l-room", Icon: "agents", Label: "*Agent Room* ({{tasks.completed}}/{{tasks.total}})", Tone: "success", Action: "feature:chatroom"},
					{ID: "l-sess", Icon: "sessions", Label: "*Sessions* ({{sessions.count}})", Tone: "warning", Action: "buffer:*Sessions*"},
					{ID: "l-targ", Icon: "host", Label: "*Targets* ({{targets.count}})", Tone: "info", Action: "buffer:*Targets*"},
					{ID: "l-thm", Icon: "theme", Label: "Theme · {{theme.name}}", Tone: "accent", Action: "cmd:cycle-theme"},
					{ID: "l-set", Icon: "settings", Label: "*Settings* & Modules", Tone: "info", Action: "buffer:*Settings*"},
				},
			},
		},
	}

	mainGrid := &Node{
		ID:       "bento-main-grid",
		Kind:     KindRow,
		Flex:     1.0,
		Gap:      14,
		Children: []*Node{leftCol, centerCol, rightCol},
	}

	root := &Node{
		ID:       "bento-dashboard-root",
		Kind:     KindCol,
		Flex:     1.0,
		Gap:      14,
		Children: []*Node{mainGrid},
	}

	if len(userWidgets) > 0 {
		var row *Node
		for _, uw := range userWidgets {
			if row == nil || len(row.Children) >= 3 {
				row = &Node{
					ID:     fmt.Sprintf("bento-user-row-%d", len(root.Children)),
					Kind:   KindRow,
					Height: 112,
					Gap:    14,
				}
				root.Children = append(root.Children, row)
			}
			row.Children = append(row.Children, uw)
		}
	}

	return root
}
