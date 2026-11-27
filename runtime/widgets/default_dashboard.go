// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package widgets

import "fmt"

// DefaultDashboard returns Marlin's fluid edge-to-edge Control Center Bento
// desktop combining iOS/macOS Control Center ergonomics (GPU vector telemetry
// rings, Spotlight-style Ask Marlin composer, Apple squircle Quick Launch,
// Android 16 / iOS Quick-Action capsules, and glowing module switches) with
// real live gRPC and Agent Room interactivity.
func DefaultDashboard(userWidgets []*Node) *Node {
	leftCol := &Node{
		ID:   "bento-left",
		Kind: KindCol,
		Flex: 1.0,
		Gap:  12,
		Children: []*Node{
			{
				ID:       "w-profile",
				Kind:     KindProfile,
				Flex:     1.02,
				Icon:     "sparkles",
				Title:    "Marlin AI",
				Subtitle: "@{{agent.session}}",
				Value:    "{{agent.model}}",
				Body:     "{{agent.host}}",
				Badge:    "{{agent.status}}",
				Tone:     "accent",
				OnClick:  "buffer:*conversation*",
			},
			{
				ID:    "w-gauges",
				Kind:  KindCard,
				Title: "TELEMETRY",
				Badge: "Live",
				Flex:  1.08,
				Gap:   8,
				Children: []*Node{
					{
						ID:   "g-row-1",
						Kind: KindRow,
						Flex: 1,
						Gap:  8,
						Children: []*Node{
							{ID: "g-tasks", Kind: KindGauge, Icon: "check", Title: "Tasks", Value: "{{tasks.completed}}/{{tasks.total}}", Progress: "{{tasks.progress}}", Tone: "danger", OnClick: "feature:chatroom"},
							{ID: "g-agents", Kind: KindGauge, Icon: "agents", Title: "Agents", Value: "{{agents.online}}/{{agents.total}}", Progress: "{{agents.progress}}", Tone: "success", OnClick: "feature:chatroom"},
						},
					},
					{
						ID:   "g-row-2",
						Kind: KindRow,
						Flex: 1,
						Gap:  8,
						Children: []*Node{
							{ID: "g-sess", Kind: KindGauge, Icon: "sessions", Title: "Sessions", Value: "{{sessions.count}}", Progress: "{{sessions.progress}}", Tone: "warning", OnClick: "buffer:*Sessions*"},
							{ID: "g-host", Kind: KindGauge, Icon: "host", Title: "Targets", Value: "{{targets.count}}", Progress: "{{targets.progress}}", Tone: "info", OnClick: "buffer:*Targets*"},
						},
					},
				},
			},
		},
	}

	centerTop := &Node{
		ID:   "bento-center-top",
		Kind: KindRow,
		Flex: 1.08,
		Gap:  12,
		Children: []*Node{
			{
				ID:   "bento-clock-stack",
				Kind: KindCol,
				Flex: 0.92,
				Gap:  12,
				Children: []*Node{
					{
						ID:       "w-clock",
						Kind:     KindClock,
						Flex:     1.12,
						Title:    "{{clock.hour}}:{{clock.min}}",
						Value:    "{{clock.sec}}",
						Badge:    "{{clock.ampm}}",
						Subtitle: "{{clock.weekday}}",
					},
					{
						ID:       "w-uptime",
						Kind:     KindKPI,
						Flex:     0.88,
						Icon:     "timer",
						Value:    "{{uptime.hours}}h {{uptime.mins}}m",
						Title:    "SESSION UPTIME",
						Subtitle: "{{agent.model}} · New Session",
						Tone:     "accent",
						OnClick:  "cmd:new-session",
					},
				},
			},
			{
				ID:       "w-tasks",
				Kind:     KindTaskList,
				Flex:     1.68,
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
		Flex: 1.15,
		Gap:  12,
		Children: []*Node{
			{
				ID:         "w-composer",
				Kind:       KindComposer,
				Flex:       1.58,
				Icon:       "sparkles",
				Title:      "Ask Marlin",
				Badge:      "{{agent.model}}",
				Subtitle:   "Session {{agent.session}}",
				Progress:   "{{tasks.progress}}",
				Tone:       "accent",
				PrevAction: "buffer:*Models*",
				PlayAction: "buffer:*conversation*",
				NextAction: "feature:chatroom",
			},
			{
				ID:    "w-launcher",
				Kind:  KindTileGrid,
				Title: "QUICK LAUNCH",
				Badge: "SPC",
				Flex:  1.02,
				Tiles: []TileItem{
					{ID: "t-chat", Icon: "chat", Label: "Chat", Tone: "info", Action: "buffer:*conversation*"},
					{ID: "t-room", Icon: "agents", Label: "Room", Tone: "accent", Action: "feature:chatroom"},
					{ID: "t-helm", Icon: "search", Label: "Helm", Tone: "warning", Action: "cmd:helm"},
					{ID: "t-mx", Icon: "command", Label: "M-x", Tone: "success", Action: "cmd:mx"},
					{ID: "t-sess", Icon: "sessions", Label: "Sessions", Tone: "info", Action: "buffer:*Sessions*"},
					{ID: "t-targ", Icon: "host", Label: "Targets", Tone: "danger", Action: "buffer:*Targets*"},
					{ID: "t-proc", Icon: "cpu", Label: "Procs", Tone: "warning", Action: "buffer:*Processes*"},
					{ID: "t-mod", Icon: "cube", Label: "Models", Tone: "accent", Action: "buffer:*Models*"},
					{ID: "t-set", Icon: "settings", Label: "Settings", Tone: "success", Action: "buffer:*Settings*"},
				},
			},
		},
	}

	centerBottom := &Node{
		ID:   "bento-center-bot",
		Kind: KindRow,
		Flex: 0.42,
		Gap:  12,
		Children: []*Node{
			{ID: "a-chat", Kind: KindActionTile, Flex: 1, Icon: "chat", Title: "New Chat", Subtitle: "SPC m n", Tone: "info", SolidBg: true, OnClick: "buffer:*conversation*"},
			{ID: "a-helm", Kind: KindActionTile, Flex: 1, Icon: "search", Title: "Helm Search", Subtitle: "SPC s", Tone: "warning", SolidBg: true, OnClick: "cmd:helm"},
			{ID: "a-mx", Kind: KindActionTile, Flex: 1, Icon: "command", Title: "Commands", Subtitle: "SPC SPC", Tone: "accent", SolidBg: true, OnClick: "cmd:mx"},
			{ID: "a-room", Kind: KindActionTile, Flex: 1, Icon: "agents", Title: "Agent Room", Subtitle: "{{tasks.completed}}/{{tasks.total}} done", Tone: "success", SolidBg: true, OnClick: "feature:chatroom"},
		},
	}

	centerCol := &Node{
		ID:       "bento-center",
		Kind:     KindCol,
		Flex:     2.6,
		Gap:      12,
		Children: []*Node{centerTop, centerMid, centerBottom},
	}

	rightCol := &Node{
		ID:   "bento-right",
		Kind: KindCol,
		Flex: 1.0,
		Gap:  12,
		Children: []*Node{
			{
				ID:   "bento-mod-row1",
				Kind: KindRow,
				Flex: 0.44,
				Gap:  12,
				Children: []*Node{
					{ID: "p-glass", Kind: KindActionTile, Flex: 1, Icon: "glass", Title: "Glass", Badge: "{{mod.glass}}", Tone: "info", OnClick: "cmd:toggle-glass"},
					{ID: "p-motion", Kind: KindActionTile, Flex: 1, Icon: "motion", Title: "Motion", Badge: "{{mod.motion}}", Tone: "accent", OnClick: "cmd:toggle-motion"},
				},
			},
			{
				ID:   "bento-mod-row2",
				Kind: KindRow,
				Flex: 0.44,
				Gap:  12,
				Children: []*Node{
					{ID: "p-zen", Kind: KindActionTile, Flex: 1, Icon: "zen", Title: "Zen", Badge: "{{mod.zen}}", Tone: "success", OnClick: "cmd:zen-mode"},
					{ID: "p-reload", Kind: KindActionTile, Flex: 1, Icon: "reload", Title: "Reload", Badge: "UI", Tone: "warning", OnClick: "cmd:reload-theme"},
				},
			},
			{
				ID:    "w-links",
				Kind:  KindLinkList,
				Flex:  1.42,
				Icon:  "drive",
				Title: "WORKSPACE",
				Value: "{{buffers.count}} Buffers",
				Badge: "{{theme.name}}",
				Links: []LinkItem{
					{ID: "l-conv", Icon: "chat", Label: "Conversation · {{turns.count}}", Tone: "info", Action: "buffer:*conversation*"},
					{ID: "l-room", Icon: "agents", Label: "Agent Room · {{tasks.completed}}/{{tasks.total}}", Tone: "success", Action: "feature:chatroom"},
					{ID: "l-sess", Icon: "sessions", Label: "Sessions · {{sessions.count}}", Tone: "warning", Action: "buffer:*Sessions*"},
					{ID: "l-targ", Icon: "host", Label: "Targets · {{targets.count}}", Tone: "danger", Action: "buffer:*Targets*"},
					{ID: "l-thm", Icon: "theme", Label: "Theme · {{theme.name}}", Tone: "accent", Action: "cmd:cycle-theme"},
					{ID: "l-set", Icon: "settings", Label: "Settings & Modules", Tone: "info", Action: "buffer:*Settings*"},
				},
			},
		},
	}

	mainGrid := &Node{
		ID:       "bento-main-grid",
		Kind:     KindRow,
		Flex:     1.0,
		Gap:      12,
		Children: []*Node{leftCol, centerCol, rightCol},
	}

	root := &Node{
		ID:       "bento-dashboard-root",
		Kind:     KindCol,
		Flex:     1.0,
		Gap:      12,
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
					Gap:    12,
				}
				root.Children = append(root.Children, row)
			}
			row.Children = append(row.Children, uw)
		}
	}

	return root
}
