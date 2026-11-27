// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package widgets

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseWidgetTOML compiles a user-friendly TOML widget file into Marlin's
// unified *Node IR.
func ParseWidgetTOML(id, raw string) (*Node, error) {
	kv, err := parseTOMLMap(raw)
	if err != nil {
		return nil, err
	}
	return buildTOMLWidget(id, kv)
}

// ParseDashboardTOML compiles a declarative dashboard.toml declaring
// `[[widgets]]` cards into Marlin's unified *Node IR.
func ParseDashboardTOML(raw string) (*Node, error) {
	var sections []map[string]string
	var cur map[string]string
	for idx, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == "[[widgets]]" {
			cur = make(map[string]string)
			sections = append(sections, cur)
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			continue
		}
		if cur == nil {
			if _, _, ok := strings.Cut(trimmed, "="); !ok {
				return nil, fmt.Errorf("dashboard.toml:%d: expected key = \"value\"", idx+1)
			}
			continue
		}
		k, v, err := parseTOMLKeyValue(trimmed, idx+1)
		if err != nil {
			return nil, fmt.Errorf("dashboard.toml:%v", err)
		}
		cur[k] = v
	}
	if len(sections) == 0 {
		return nil, fmt.Errorf("dashboard.toml: expected at least one [[widgets]] section")
	}

	root := &Node{ID: "toml-dashboard", Kind: KindCol, Flex: 1, Gap: 14}
	var currentRow *Node
	for i, sec := range sections {
		id := sec["id"]
		if id == "" {
			id = fmt.Sprintf("w-%d", i+1)
		}
		w, err := buildTOMLWidget(id, sec)
		if err != nil {
			return nil, err
		}
		if currentRow == nil || len(currentRow.Children) >= 3 {
			currentRow = &Node{ID: fmt.Sprintf("row-%d", len(root.Children)+1), Kind: KindRow, Flex: 1, Gap: 14}
			root.Children = append(root.Children, currentRow)
		}
		currentRow.Children = append(currentRow.Children, w)
	}
	return root, nil
}

func buildTOMLWidget(id string, kv map[string]string) (*Node, error) {
	kindStr := strings.ToLower(strings.TrimSpace(kv["kind"]))
	if kindStr == "" {
		kindStr = string(KindKPI)
	}
	kind := Kind(kindStr)
	if !allowedKinds[kind] {
		return nil, fmt.Errorf("widget.toml (%s): unknown kind %q", id, kindStr)
	}
	node := &Node{
		ID:       id,
		Kind:     kind,
		Icon:     kv["icon"],
		Title:    kv["title"],
		Subtitle: kv["subtitle"],
		Body:     kv["body"],
		Value:    kv["value"],
		Progress: kv["progress"],
		Badge:    kv["badge"],
		Tone:     kv["tone"],
		OnClick:  kv["on_click"],
		SolidBg:  strings.EqualFold(kv["solid"], "true"),
		Flex:     1,
	}
	if node.Title == "" {
		node.Title = kv["label"]
	}
	if node.OnClick == "" {
		node.OnClick = kv["action"]
	}
	if rawW := kv["width"]; rawW != "" {
		f, err := strconv.ParseFloat(rawW, 32)
		if err != nil {
			return nil, fmt.Errorf("widget.toml (%s): invalid width %q", id, rawW)
		}
		node.Width = float32(f)
		node.Flex = 0
	}
	if rawH := kv["height"]; rawH != "" {
		f, err := strconv.ParseFloat(rawH, 32)
		if err != nil {
			return nil, fmt.Errorf("widget.toml (%s): invalid height %q", id, rawH)
		}
		node.Height = float32(f)
	}
	if cls := kv["class"]; cls != "" {
		node.Classes = strings.Fields(cls)
	}
	if kind == KindKPI || kind == KindGauge || kind == KindText {
		card := &Node{
			ID:       "card-" + id,
			Kind:     KindCard,
			Classes:  node.Classes,
			Width:    node.Width,
			Height:   node.Height,
			Flex:     node.Flex,
			Gap:      10,
			OnClick:  node.OnClick,
			Children: []*Node{node},
		}
		if kind == KindKPI && strings.TrimSpace(node.Progress) != "" {
			card.Children = append(card.Children, &Node{
				ID:       id + "-gauge",
				Kind:     KindGauge,
				Icon:     node.Icon,
				Progress: node.Progress,
				Tone:     node.Tone,
			})
		}
		node.Width, node.Height, node.Flex, node.OnClick = 0, 0, 0, ""
		return card, nil
	}
	return node, nil
}

func parseTOMLMap(raw string) (map[string]string, error) {
	out := make(map[string]string)
	for idx, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || (strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
			continue
		}
		k, v, err := parseTOMLKeyValue(trimmed, idx+1)
		if err != nil {
			return nil, fmt.Errorf("widget.toml:%v", err)
		}
		out[k] = v
	}
	return out, nil
}

func parseTOMLKeyValue(trimmed string, line int) (string, string, error) {
	k, v, ok := strings.Cut(trimmed, "=")
	if !ok {
		return "", "", fmt.Errorf("%d: expected key = \"value\"", line)
	}
	key := strings.ToLower(strings.TrimSpace(k))
	val := strings.TrimSpace(v)
	if strings.HasPrefix(val, `"`) {
		end := strings.Index(val[1:], `"`)
		if end < 0 {
			return "", "", fmt.Errorf("%d: unclosed string literal", line)
		}
		val = val[1 : 1+end]
	} else if c := strings.IndexByte(val, '#'); c >= 0 {
		val = strings.TrimSpace(val[:c])
	}
	return key, val, nil
}
