// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// RenderUIElement maps Marlin's host-neutral Scheme UI IR (also emitted by
// wazero Go WASM-IR plugins) onto MyGo's native GPU UI primitives.
// Whenever an ElemSlot node is encountered, it resolves the active renderer(s)
// from slots—automatically falling back to the Scheme/native fallback if any
// Marketplace plugin is absent or unmounted.
func RenderUIElement(c *ui.Context, node schema.UIElement, slots *shell.SlotRegistry, rctx shell.RenderContext) {
	t := c.Theme()
	switch node.Kind {
	case schema.ElemRows:
		ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
			for i := range node.Children {
				RenderUIElement(c, node.Children[i], slots, rctx)
			}
		})

	case schema.ElemColumns:
		ui.Row(c).Grow(1).MinWidth(0).Gap(10).AlignItems(ui.Stretch).Children(func() {
			for i := range node.Children {
				RenderUIElement(c, node.Children[i], slots, rctx)
			}
		})

	case schema.ElemPanel:
		border := t.Border
		if node.Tone != "" && node.Tone != schema.ToneNormal {
			border = ToneColor(rctx.Theme, node.Tone)
		}
		ui.Column(c).Grow(1).MinWidth(0).Padding(10).Gap(6).Radius(8).
			Background(t.Surface).Border(1, border).Children(func() {
			if node.Title != "" {
				ui.Text(c, node.Title).Bold().FontSize(13).TextColor(ToneColor(rctx.Theme, node.Tone))
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

	case schema.ElemBar:
		ui.Row(c).Padding(6, 12).Gap(12).AlignItems(ui.Center).
			Background(toColor(rctx.Theme.Bar)).Children(func() {
			for i := range node.Children {
				RenderUIElement(c, node.Children[i], slots, rctx)
			}
		})

	case schema.ElemSlot:
		if slots == nil || node.SlotID == "" {
			return
		}
		for _, r := range slots.Resolve(node.SlotID) {
			r.Render(rctx)
		}

	case schema.ElemRegion, schema.ElemSized, schema.ElemList, schema.ElemChoice:
		col := ui.Column(c).MinWidth(0).Gap(6)
		if node.Flex > 0 {
			col.Grow(float32(node.Flex))
		}
		col.Children(func() {
			for i := range node.Children {
				RenderUIElement(c, node.Children[i], slots, rctx)
			}
		})

	default:
		if node.Title != "" {
			ui.Text(c, node.Title).TextColor(t.TextMuted)
		}
	}
}

// UIElementRenderer adapts a static or dynamically refreshed schema.UIElement
// tree into a shell.SlotRenderer (used for native fallbacks and wazero plugins).
type UIElementRenderer struct {
	id      string
	trusted bool
	slots   *shell.SlotRegistry
	getTree func() schema.UIElement
}

func NewUIElementRenderer(id string, trusted bool, slots *shell.SlotRegistry, getTree func() schema.UIElement) *UIElementRenderer {
	return &UIElementRenderer{id: id, trusted: trusted, slots: slots, getTree: getTree}
}

func (r *UIElementRenderer) ID() string              { return r.id }
func (r *UIElementRenderer) Trusted() bool           { return r.trusted }
func (r *UIElementRenderer) HandleEvent(shell.HostEvent) error { return nil }
func (r *UIElementRenderer) Dispose()                {}

func (r *UIElementRenderer) Render(ctx shell.RenderContext) {
	c, ok := ctx.Surface.(*ui.Context)
	if !ok || c == nil || r.getTree == nil {
		return
	}
	RenderUIElement(c, r.getTree(), r.slots, ctx)
}
