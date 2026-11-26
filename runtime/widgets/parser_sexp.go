// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package widgets

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseSexp parses an EWW/Scheme-style S-expression widget tree fail-closed
// into Marlin's unified *Node IR.
func ParseSexp(raw string) (*Node, error) {
	tokens, err := lexSexp(raw)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("widget.sexp: empty layout")
	}
	p := &sexpParser{tokens: tokens}
	root, err := p.parseNode(0)
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.tokens) {
		tok := p.tokens[p.pos]
		return nil, fmt.Errorf("widget.sexp:%d: unexpected trailing token %q", tok.line, tok.val)
	}
	return root, nil
}

type sexpTokKind uint8

const (
	tokLParen sexpTokKind = iota
	tokRParen
	tokString
	tokAtom
)

type sexpTok struct {
	kind sexpTokKind
	val  string
	line int
}

func lexSexp(raw string) ([]sexpTok, error) {
	var out []sexpTok
	line := 1
	for i := 0; i < len(raw); {
		ch := raw[i]
		if ch == '\n' {
			line++
			i++
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\r' {
			i++
			continue
		}
		if ch == ';' {
			for i < len(raw) && raw[i] != '\n' {
				i++
			}
			continue
		}
		if ch == '(' {
			out = append(out, sexpTok{kind: tokLParen, val: "(", line: line})
			i++
			continue
		}
		if ch == ')' {
			out = append(out, sexpTok{kind: tokRParen, val: ")", line: line})
			i++
			continue
		}
		if ch == '"' {
			startLine := line
			i++
			var sb strings.Builder
			closed := false
			for i < len(raw) {
				c := raw[i]
				if c == '\\' && i+1 < len(raw) {
					next := raw[i+1]
					switch next {
					case 'n':
						sb.WriteByte('\n')
					case 't':
						sb.WriteByte('\t')
					default:
						sb.WriteByte(next)
					}
					i += 2
					continue
				}
				if c == '"' {
					i++
					closed = true
					break
				}
				if c == '\n' {
					line++
				}
				sb.WriteByte(c)
				i++
			}
			if !closed {
				return nil, fmt.Errorf("widget.sexp:%d: unterminated string literal", startLine)
			}
			out = append(out, sexpTok{kind: tokString, val: sb.String(), line: startLine})
			continue
		}
		start := i
		for i < len(raw) {
			c := raw[i]
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '(' || c == ')' || c == ';' || c == '"' {
				break
			}
			i++
		}
		out = append(out, sexpTok{kind: tokAtom, val: raw[start:i], line: line})
	}
	return out, nil
}

type sexpParser struct {
	tokens []sexpTok
	pos    int
	nodes  int
}

var allowedKinds = map[Kind]bool{
	KindRow:        true,
	KindCol:        true,
	KindCard:       true,
	KindProfile:    true,
	KindClock:      true,
	KindKPI:        true,
	KindHero:       true,
	KindGauge:      true,
	KindTrend:      true,
	KindControl:    true,
	KindComposer:   true,
	KindTaskList:   true,
	KindTileGrid:   true,
	KindActionTile: true,
	KindBadgeCard:  true,
	KindLinkList:   true,
	KindText:       true,
	KindSpacer:     true,
}

func (p *sexpParser) parseNode(depth int) (*Node, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("widget.sexp: exceeds max nesting depth (%d)", MaxDepth)
	}
	p.nodes++
	if p.nodes > MaxNodes {
		return nil, fmt.Errorf("widget.sexp: exceeds max node count (%d)", MaxNodes)
	}
	if p.pos >= len(p.tokens) || p.tokens[p.pos].kind != tokLParen {
		return nil, fmt.Errorf("widget.sexp: expected '(' to start widget form")
	}
	openTok := p.tokens[p.pos]
	p.pos++
	if p.pos >= len(p.tokens) || p.tokens[p.pos].kind != tokAtom {
		return nil, fmt.Errorf("widget.sexp:%d: expected widget component name after '('", openTok.line)
	}
	kind := Kind(strings.ToLower(p.tokens[p.pos].val))
	if !allowedKinds[kind] {
		return nil, fmt.Errorf("widget.sexp:%d: unknown widget component %q", p.tokens[p.pos].line, kind)
	}
	p.pos++

	node := &Node{Kind: kind}
	for p.pos < len(p.tokens) && p.tokens[p.pos].kind != tokRParen {
		tok := p.tokens[p.pos]
		if tok.kind == tokLParen {
			if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].kind == tokAtom {
				sub := strings.ToLower(p.tokens[p.pos+1].val)
				if sub == "tile" {
					item, err := p.parseTileItem()
					if err != nil {
						return nil, err
					}
					node.Tiles = append(node.Tiles, item)
					continue
				}
				if sub == "link" {
					item, err := p.parseLinkItem()
					if err != nil {
						return nil, err
					}
					node.Links = append(node.Links, item)
					continue
				}
			}
			child, err := p.parseNode(depth + 1)
			if err != nil {
				return nil, err
			}
			node.Children = append(node.Children, child)
			continue
		}
		if tok.kind == tokAtom && strings.HasPrefix(tok.val, ":") {
			key := strings.ToLower(strings.TrimPrefix(tok.val, ":"))
			p.pos++
			if p.pos >= len(p.tokens) || p.tokens[p.pos].kind == tokRParen || p.tokens[p.pos].kind == tokLParen {
				return nil, fmt.Errorf("widget.sexp:%d: keyword :%s requires a value", tok.line, key)
			}
			valTok := p.tokens[p.pos]
			p.pos++
			if err := applyNodeAttr(node, key, valTok.val, tok.line); err != nil {
				return nil, err
			}
			continue
		}
		return nil, fmt.Errorf("widget.sexp:%d: unexpected token %q inside (%s ...)", tok.line, tok.val, kind)
	}
	if p.pos >= len(p.tokens) || p.tokens[p.pos].kind != tokRParen {
		return nil, fmt.Errorf("widget.sexp:%d: unclosed '(%s'", openTok.line, kind)
	}
	p.pos++
	return node, nil
}

func (p *sexpParser) parseTileItem() (TileItem, error) {
	openTok := p.tokens[p.pos]
	p.pos += 2
	var item TileItem
	for p.pos < len(p.tokens) && p.tokens[p.pos].kind != tokRParen {
		tok := p.tokens[p.pos]
		if tok.kind != tokAtom || !strings.HasPrefix(tok.val, ":") {
			return item, fmt.Errorf("widget.sexp:%d: expected keyword inside (tile ...)", tok.line)
		}
		k := strings.ToLower(strings.TrimPrefix(tok.val, ":"))
		p.pos++
		if p.pos >= len(p.tokens) {
			return item, fmt.Errorf("widget.sexp:%d: missing value for :%s", tok.line, k)
		}
		v := p.tokens[p.pos].val
		p.pos++
		switch k {
		case "id":
			item.ID = v
		case "icon":
			item.Icon = v
		case "label":
			item.Label = v
		case "tone":
			item.Tone = v
		case "on-click", "action":
			item.Action = v
		default:
			return item, fmt.Errorf("widget.sexp:%d: unknown (tile) attribute :%s", tok.line, k)
		}
	}
	if p.pos >= len(p.tokens) {
		return item, fmt.Errorf("widget.sexp:%d: unclosed (tile ...)", openTok.line)
	}
	p.pos++
	return item, nil
}

func (p *sexpParser) parseLinkItem() (LinkItem, error) {
	openTok := p.tokens[p.pos]
	p.pos += 2
	var item LinkItem
	for p.pos < len(p.tokens) && p.tokens[p.pos].kind != tokRParen {
		tok := p.tokens[p.pos]
		if tok.kind != tokAtom || !strings.HasPrefix(tok.val, ":") {
			return item, fmt.Errorf("widget.sexp:%d: expected keyword inside (link ...)", tok.line)
		}
		k := strings.ToLower(strings.TrimPrefix(tok.val, ":"))
		p.pos++
		if p.pos >= len(p.tokens) {
			return item, fmt.Errorf("widget.sexp:%d: missing value for :%s", tok.line, k)
		}
		v := p.tokens[p.pos].val
		p.pos++
		switch k {
		case "id":
			item.ID = v
		case "icon":
			item.Icon = v
		case "label":
			item.Label = v
		case "tone":
			item.Tone = v
		case "on-click", "action":
			item.Action = v
		default:
			return item, fmt.Errorf("widget.sexp:%d: unknown (link) attribute :%s", tok.line, k)
		}
	}
	if p.pos >= len(p.tokens) {
		return item, fmt.Errorf("widget.sexp:%d: unclosed (link ...)", openTok.line)
	}
	p.pos++
	return item, nil
}

func applyNodeAttr(node *Node, key, val string, line int) error {
	switch key {
	case "id":
		node.ID = val
	case "class":
		node.Classes = append(node.Classes, strings.Fields(val)...)
	case "width":
		f, err := strconv.ParseFloat(val, 32)
		if err != nil {
			return fmt.Errorf("widget.sexp:%d: invalid :width %q", line, val)
		}
		node.Width = float32(f)
	case "height":
		f, err := strconv.ParseFloat(val, 32)
		if err != nil {
			return fmt.Errorf("widget.sexp:%d: invalid :height %q", line, val)
		}
		node.Height = float32(f)
	case "flex":
		f, err := strconv.ParseFloat(val, 32)
		if err != nil {
			return fmt.Errorf("widget.sexp:%d: invalid :flex %q", line, val)
		}
		node.Flex = float32(f)
	case "gap":
		f, err := strconv.ParseFloat(val, 32)
		if err != nil {
			return fmt.Errorf("widget.sexp:%d: invalid :gap %q", line, val)
		}
		node.Gap = float32(f)
	case "tone":
		node.Tone = val
	case "solid":
		node.SolidBg = strings.EqualFold(val, "true") || val == "1"
	case "icon":
		node.Icon = val
	case "title", "label":
		node.Title = val
	case "subtitle":
		node.Subtitle = val
	case "body":
		node.Body = val
	case "value":
		node.Value = val
	case "progress":
		node.Progress = val
	case "badge":
		node.Badge = val
	case "on-click", "action":
		node.OnClick = val
	case "prev-action":
		node.PrevAction = val
	case "play-action":
		node.PlayAction = val
	case "next-action":
		node.NextAction = val
	case "play-label":
		node.PlayLabel = val
	default:
		return fmt.Errorf("widget.sexp:%d: unknown widget attribute :%s", line, key)
	}
	return nil
}
