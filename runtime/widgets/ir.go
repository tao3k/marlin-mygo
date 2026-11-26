// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package widgets

// Governance bounds preventing unbounded user widget trees.
const (
	MaxNodes = 256
	MaxDepth = 12
)

// Kind identifies one standard Marlin Bento widget component.
type Kind string

const (
	KindRow        Kind = "row"
	KindCol        Kind = "col"
	KindCard       Kind = "card"
	KindProfile    Kind = "profile"
	KindClock      Kind = "clock"
	KindKPI        Kind = "kpi"
	KindHero       Kind = "hero"
	KindGauge      Kind = "gauge"
	KindTrend      Kind = "trend"
	KindControl    Kind = "control"
	KindComposer   Kind = "composer"
	KindTaskList   Kind = "task-list"
	KindTileGrid   Kind = "tile-grid"
	KindActionTile Kind = "action-tile"
	KindBadgeCard  Kind = "badge-card"
	KindLinkList   Kind = "link-list"
	KindText       Kind = "text"
	KindSpacer     Kind = "spacer"
)

// TileItem describes one launcher tile inside KindTileGrid.
type TileItem struct {
	ID     string
	Icon   string // semantic icon ID from runtime/kit (SF Symbol + SVG fallback)
	Label  string
	Tone   string
	Action string
}

// LinkItem describes one clickable workspace/folder/buffer row inside KindLinkList.
type LinkItem struct {
	ID     string
	Icon   string
	Label  string
	Tone   string
	Action string
}

// Node is Marlin's unified declarative Bento Widget IR produced by built-in
// layouts, TOML widget specs, S-expression (EWW/Scheme) layouts, or plugins.
type Node struct {
	ID       string
	Kind     Kind
	Classes  []string
	Width    float32
	Height   float32
	Flex     float32
	Gap      float32
	Tone     string
	SolidBg  bool
	Icon     string
	Title    string
	Subtitle string
	Body     string
	Value    string
	Progress string
	Badge    string
	OnClick  string

	PrevAction string
	PlayAction string
	NextAction string
	PlayLabel  string

	Tiles    []TileItem
	Links    []LinkItem
	Children []*Node
}

// ActionHandler executes a declarative widget action string.
type ActionHandler func(action string)
