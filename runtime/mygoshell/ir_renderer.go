// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

const logicalCellDP float32 = 20 // 1 logical Scheme row/cell = 20dp on GPU surface

// RenderUIElement maps Marlin's 13 host-neutral Scheme UI IR primitives (also
// emitted by wazero Go WASM-IR plugins) onto MyGo's native GPU UI primitives.
// Whenever an ElemSlot node is encountered, it resolves the active renderer(s)
// from slots—automatically falling back to the Scheme/native fallback if any
// Marketplace plugin is absent or unmounted.
func RenderUIElement(c *ui.Context, node schema.UIElement, slots *shell.SlotRegistry, rctx shell.RenderContext) {
	t := c.Theme()
	switch node.Kind {
	case schema.ElemRows:
		col := ui.Column(c).Grow(1).MinWidth(0).Gap(10).AlignItems(ui.Stretch)
		applyLayoutConstraints(col, node)
		col.Children(func() {
			for i := range node.Children {
				RenderUIElement(c, node.Children[i], slots, rctx)
			}
		})

	case schema.ElemColumns:
		row := ui.Row(c).MinWidth(0).Gap(8).AlignItems(ui.Center)
		applyLayoutConstraints(row, node)
		row.Children(func() {
			for i := range node.Children {
				RenderUIElement(c, node.Children[i], slots, rctx)
			}
		})

	case schema.ElemPanel:
		border := t.Border
		if node.Tone != "" && node.Tone != schema.ToneNormal {
			border = ToneColor(rctx.Theme, node.Tone)
		}
		panel := ui.Column(c).MinWidth(0).Padding(12, 14).Gap(6).Radius(10).
			Background(t.Surface).Border(1, border)
		applyLayoutConstraints(panel, node)
		panel.Children(func() {
			if node.Title != "" {
				ui.Text(c, interpolateBindings(node.Title, rctx.Bindings)).Bold().FontSize(12).TextColor(ToneColor(rctx.Theme, node.Tone))
			}
			for i := range node.Children {
				RenderUIElement(c, node.Children[i], slots, rctx)
			}
		})

	case schema.ElemText:
		ui.Row(c).Gap(2).Children(func() {
			for _, sp := range node.Spans {
				ui.Text(c, sp.Text).TextColor(ToneColor(rctx.Theme, sp.Tone))
			}
		})

	case schema.ElemTemplate:
		ui.Row(c).Gap(2).Children(func() {
			for _, sp := range node.Spans {
				ui.Text(c, interpolateBindings(sp.Text, rctx.Bindings)).TextColor(ToneColor(rctx.Theme, sp.Tone))
			}
		})

	case schema.ElemChoice:
		// First child whose ComponentID matches a true fact in rctx.Facts wins;
		// last child acts as the Scheme ui-choice fallback branch.
		if len(node.Children) == 0 {
			return
		}
		chosen := node.Children[len(node.Children)-1]
		for i := 0; i < len(node.Children)-1; i++ {
			ch := node.Children[i]
			if ch.ID != "" && rctx.Facts[string(ch.ID)] {
				chosen = ch
				break
			}
		}
		RenderUIElement(c, chosen, slots, rctx)

	case schema.ElemBar:
		ui.Row(c).Padding(6, 12).Gap(12).AlignItems(ui.Center).
			Background(toColor(rctx.Theme.Bar)).Children(func() {
			for i := range node.Children {
				RenderUIElement(c, node.Children[i], slots, rctx)
			}
		})

	case schema.ElemPrompt, schema.ElemInput:
		ui.Row(c).Padding(6, 10).Gap(8).AlignItems(ui.Center).Radius(6).
			Background(t.Surface).Border(1, ToneColor(rctx.Theme, node.Tone)).Children(func() {
			if node.Title != "" {
				ui.Text(c, interpolateBindings(node.Title, rctx.Bindings)).Bold().TextColor(ToneColor(rctx.Theme, schema.ToneAccent))
			}
			for _, sp := range node.Spans {
				ui.Text(c, interpolateBindings(sp.Text, rctx.Bindings)).TextColor(ToneColor(rctx.Theme, sp.Tone))
			}
		})

	case schema.ElemSlot:
		if slots == nil || node.SlotID == "" {
			return
		}
		for _, r := range slots.Resolve(node.SlotID) {
			r.Render(rctx)
		}

	case schema.ElemRegion, schema.ElemSized, schema.ElemList:
		col := ui.Column(c).MinWidth(0).Gap(6)
		applyLayoutConstraints(col, node)
		col.Children(func() {
			for i := range node.Children {
				RenderUIElement(c, node.Children[i], slots, rctx)
			}
		})

	case schema.ElemButton:
		label := interpolateBindings(node.Title, rctx.Bindings)
		if label == "" {
			label = node.Action
		}
		key := string(node.ID)
		if key == "" {
			key = "ir-btn-" + node.Action + "-" + label
		}
		var clicked bool
		if node.Tone == schema.ToneAccent {
			clicked = ui.PrimaryButton(c.Key(key), label).Clicked()
		} else {
			clicked = ui.Button(c.Key(key), label).Clicked()
		}
		if clicked && rctx.OnAction != nil && node.Action != "" {
			rctx.OnAction(node.Action, node.ActionArgs)
		}

	default:
		if node.Title != "" {
			ui.Text(c, node.Title).TextColor(t.TextMuted)
		}
	}
}

// RenderDrawList renders high-frequency zero-JSON binary DrawList commands
// emitted from a resident Go WASM plugin directly onto MyGo's GPU scene graph.
func RenderDrawList(c *ui.Context, cmds []schema.DrawCmd) {
	ui.Column(c).Gap(4).Children(func() {
		for _, cmd := range cmds {
			col := unpackRGBA(cmd.RGBA)
			w := cmd.W
			if w <= 0 {
				w = 8
			}
			h := cmd.H
			if h <= 0 {
				h = 8
			}
			switch cmd.Op {
			case schema.DrawOpFillRect, schema.DrawOpBadge:
				ui.Box(c).Width(w).Height(h).Radius(float32(cmd.Radius)).Background(col)
			case schema.DrawOpStrokeRect:
				ui.Box(c).Width(w).Height(h).Radius(float32(cmd.Radius)).Border(1, col)
			case schema.DrawOpLine:
				ui.Box(c).Width(w).Height(2).Background(col)
			}
		}
	})
}

func applyLayoutConstraints(el ui.Element, node schema.UIElement) {
	if node.Flex > 0 {
		el.Grow(float32(node.Flex))
	}
	if node.Cells > 0 {
		el.MinHeight(float32(node.Cells) * logicalCellDP)
	}
}

func unpackRGBA(v uint32) ui.Color {
	return ui.Color{
		R: uint8((v >> 24) & 0xff),
		G: uint8((v >> 16) & 0xff),
		B: uint8((v >> 8) & 0xff),
		A: uint8(v & 0xff),
	}
}

func interpolateBindings(raw string, bindings map[string]string) string {
	if len(bindings) == 0 || !strings.Contains(raw, "{{") {
		return raw
	}
	out := raw
	for k, v := range bindings {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}

// UIElementRenderer adapts a static or dynamically refreshed schema.UIElement
// tree into a shell.SlotRenderer (used for native fallbacks and wazero plugins).
type UIElementRenderer struct {
	id       string
	trusted  bool
	slots    *shell.SlotRegistry
	getTree  func() schema.UIElement
	OnAction func(action, argsJSON string)
}

func NewUIElementRenderer(id string, trusted bool, slots *shell.SlotRegistry, getTree func() schema.UIElement) *UIElementRenderer {
	return &UIElementRenderer{id: id, trusted: trusted, slots: slots, getTree: getTree}
}

func (r *UIElementRenderer) ID() string                        { return r.id }
func (r *UIElementRenderer) Trusted() bool                     { return r.trusted }
func (r *UIElementRenderer) HandleEvent(shell.HostEvent) error { return nil }
func (r *UIElementRenderer) Dispose()                          {}

func (r *UIElementRenderer) Render(ctx shell.RenderContext) {
	c, ok := ctx.Surface.(*ui.Context)
	if !ok || c == nil || r.getTree == nil {
		return
	}
	if r.OnAction != nil {
		ctx.OnAction = r.OnAction
	}
	RenderUIElement(c, r.getTree(), r.slots, ctx)
}
