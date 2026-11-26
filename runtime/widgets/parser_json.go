// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package widgets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// MaxJSONBytes caps declarative JSON widget & sidebar specs emitted by the
// TypeScript/TSX SDK (@marlin/ui-kit) or user config files.
const MaxJSONBytes = 128 * 1024

type jsonNode struct {
	ID         string         `json:"id,omitempty"`
	Kind       string         `json:"kind"`
	Classes    []string       `json:"classes,omitempty"`
	Width      float32        `json:"width,omitempty"`
	Height     float32        `json:"height,omitempty"`
	Flex       float32        `json:"flex,omitempty"`
	Gap        float32        `json:"gap,omitempty"`
	Tone       string         `json:"tone,omitempty"`
	SolidBg    bool           `json:"solidBg,omitempty"`
	Icon       string         `json:"icon,omitempty"`
	Title      string         `json:"title,omitempty"`
	Subtitle   string         `json:"subtitle,omitempty"`
	Body       string         `json:"body,omitempty"`
	Value      string         `json:"value,omitempty"`
	Progress   string         `json:"progress,omitempty"`
	Badge      string         `json:"badge,omitempty"`
	OnClick    string         `json:"onClick,omitempty"`
	PrevAction string         `json:"prevAction,omitempty"`
	PlayAction string         `json:"playAction,omitempty"`
	NextAction string         `json:"nextAction,omitempty"`
	PlayLabel  string         `json:"playLabel,omitempty"`
	IslandID    string         `json:"islandId,omitempty"`
	Borderless  bool           `json:"borderless,omitempty"`
	VisibleWhen string         `json:"visibleWhen,omitempty"`
	Tiles       []jsonTileItem `json:"tiles,omitempty"`
	Links      []jsonLinkItem `json:"links,omitempty"`
	Children   []*jsonNode    `json:"children,omitempty"`
}

type jsonTileItem struct {
	ID     string `json:"id"`
	Icon   string `json:"icon"`
	Label  string `json:"label"`
	Tone   string `json:"tone,omitempty"`
	Action string `json:"action,omitempty"`
}

type jsonLinkItem struct {
	ID     string `json:"id"`
	Icon   string `json:"icon"`
	Label  string `json:"label"`
	Tone   string `json:"tone,omitempty"`
	Action string `json:"action,omitempty"`
}

type jsonSidebarDoc struct {
	SchemaVersion string    `json:"schemaVersion,omitempty"`
	Width         float32   `json:"width,omitempty"`
	Pinned        bool      `json:"pinned,omitempty"`
	Body          *jsonNode `json:"body"`
}

// ParseWidgetJSON parses a strict, fail-closed JSON widget tree (as emitted by
// @marlin/ui-kit TypeScript/TSX widgets or ~/.config/marlin/ui/dashboard.json)
// into a validated *Node IR. Unknown JSON fields, disallowed Kinds, unsafe
// action URIs, and oversized trees fail closed.
func ParseWidgetJSON(raw string) (*Node, error) {
	if len(raw) > MaxJSONBytes {
		return nil, fmt.Errorf("widget json exceeds max bytes (%d > %d)", len(raw), MaxJSONBytes)
	}
	var root jsonNode
	dec := json.NewDecoder(bytes.NewBufferString(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("widget json decode: %w", err)
	}
	count := 0
	return convertJSONNode(&root, 0, &count)
}

// ParseSidebarJSON parses a strict, fail-closed JSON floating sidebar spec
// (emitted by @marlin/ui-kit or ~/.config/marlin/ui/sidebar.json) into a
// validated *SidebarSpec ready for kit.EdgeRail.CustomContent.
func ParseSidebarJSON(raw string) (*SidebarSpec, error) {
	if len(raw) > MaxJSONBytes {
		return nil, fmt.Errorf("sidebar json exceeds max bytes (%d > %d)", len(raw), MaxJSONBytes)
	}
	var doc jsonSidebarDoc
	dec := json.NewDecoder(bytes.NewBufferString(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("sidebar json decode: %w", err)
	}
	if doc.SchemaVersion != "" && doc.SchemaVersion != "marlin.ui.sidebar.v1" {
		return nil, fmt.Errorf("unsupported sidebar schemaVersion %q", doc.SchemaVersion)
	}
	if doc.Body == nil {
		return nil, fmt.Errorf("sidebar json requires non-null body")
	}
	count := 0
	body, err := convertJSONNode(doc.Body, 0, &count)
	if err != nil {
		return nil, err
	}
	w := doc.Width
	if w <= 0 {
		w = 240
	}
	if w < 58 {
		w = 58
	}
	if w > 420 {
		w = 420
	}
	return &SidebarSpec{
		Width:  w,
		Pinned: doc.Pinned,
		Body:   body,
	}, nil
}

func convertJSONNode(in *jsonNode, depth int, count *int) (*Node, error) {
	if in == nil {
		return nil, fmt.Errorf("nil widget node")
	}
	if depth > MaxDepth {
		return nil, fmt.Errorf("widget tree exceeds max depth %d", MaxDepth)
	}
	*count++
	if *count > MaxNodes {
		return nil, fmt.Errorf("widget tree exceeds max node count %d", MaxNodes)
	}
	kind := Kind(strings.ToLower(strings.TrimSpace(in.Kind)))
	if !isAllowedKind(kind) {
		return nil, fmt.Errorf("disallowed widget kind %q", in.Kind)
	}
	for _, act := range []string{in.OnClick, in.PrevAction, in.PlayAction, in.NextAction} {
		if err := validateActionURI(act); err != nil {
			return nil, err
		}
	}
	out := &Node{
		ID:         strings.TrimSpace(in.ID),
		Kind:       kind,
		Classes:    append([]string(nil), in.Classes...),
		Width:      in.Width,
		Height:     in.Height,
		Flex:       in.Flex,
		Gap:        in.Gap,
		Tone:       strings.TrimSpace(in.Tone),
		SolidBg:    in.SolidBg,
		Icon:       strings.TrimSpace(in.Icon),
		Title:      in.Title,
		Subtitle:   in.Subtitle,
		Body:       in.Body,
		Value:      in.Value,
		Progress:   in.Progress,
		Badge:      in.Badge,
		OnClick:    strings.TrimSpace(in.OnClick),
		PrevAction: strings.TrimSpace(in.PrevAction),
		PlayAction: strings.TrimSpace(in.PlayAction),
		NextAction: strings.TrimSpace(in.NextAction),
		PlayLabel:   in.PlayLabel,
		IslandID:    strings.TrimSpace(in.IslandID),
		Borderless:  in.Borderless,
		VisibleWhen: strings.TrimSpace(in.VisibleWhen),
	}
	for _, t := range in.Tiles {
		if err := validateActionURI(t.Action); err != nil {
			return nil, err
		}
		out.Tiles = append(out.Tiles, TileItem{
			ID:     t.ID,
			Icon:   t.Icon,
			Label:  t.Label,
			Tone:   t.Tone,
			Action: strings.TrimSpace(t.Action),
		})
	}
	for _, l := range in.Links {
		if err := validateActionURI(l.Action); err != nil {
			return nil, err
		}
		out.Links = append(out.Links, LinkItem{
			ID:     l.ID,
			Icon:   l.Icon,
			Label:  l.Label,
			Tone:   l.Tone,
			Action: strings.TrimSpace(l.Action),
		})
	}
	for _, ch := range in.Children {
		cn, err := convertJSONNode(ch, depth+1, count)
		if err != nil {
			return nil, err
		}
		out.Children = append(out.Children, cn)
	}
	return out, nil
}

func isAllowedKind(k Kind) bool {
	switch k {
	case KindRow, KindCol, KindCard, KindProfile, KindClock, KindKPI, KindHero,
		KindGauge, KindTrend, KindControl, KindComposer, KindTaskList,
		KindTileGrid, KindActionTile, KindBadgeCard, KindLinkList,
		KindWebIsland, KindScroll, KindSection, KindDivider, KindText, KindSpacer:
		return true
	default:
		return false
	}
}

func validateActionURI(act string) error {
	a := strings.TrimSpace(act)
	if a == "" {
		return nil
	}
	for _, pfx := range []string{"buffer:", "feature:", "cmd:", "theme:", "hydra:", "rpc:", "island:"} {
		if strings.HasPrefix(a, pfx) {
			return nil
		}
	}
	return fmt.Errorf("disallowed widget action URI %q (expected buffer:, feature:, cmd:, theme:, hydra:, rpc:, or island:)", act)
}
