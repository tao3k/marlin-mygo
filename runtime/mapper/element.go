// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mapper

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/style"
)

const cellDP float32 = 22

// Context supplies live facts, bindings, width budget, optional CSS-subset
// StyleSheet, slot resolvers, and typed UI intent callbacks for one MapElement pass.
type Context struct {
	WidthCols   uint32
	Bindings    map[string]string
	Facts       map[string]bool
	Sheet       *style.Sheet
	Slots       *shell.SlotRegistry
	SlotContext shell.RenderContext

	// Host hooks for dynamic Scheme slots and typed intents (dispatched to gRPC).
	OnRenderSlotSource func(c *ui.Context, source schema.UISlotSource, slotID schema.SlotID)
	OnRenderListSource func(c *ui.Context, listID schema.ComponentID, source schema.UIListSourceKind)
	OnCommand          func(commandName string)
	OnAction           func(action, argsJSON string)
	OnSelectRow        func(listID, rowID string)
}

// MapElement recursively maps all 13 host-neutral Scheme UI IR primitives
// (matching crates/marlin-agent-ui/src/element/tree.rs::UiElement) onto mygo/ui.
func MapElement(c *ui.Context, node schema.UIElement, skin Skin, ctx Context) {
	switch node.Kind {
	case schema.ElemRows:
		col := ui.Column(c).MinWidth(0).Gap(6).AlignItems(ui.Stretch)
		applyConstraints(col, node)
		col.Children(func() {
			for i := range node.Children {
				MapElement(c, node.Children[i], skin, ctx)
			}
		})

	case schema.ElemColumns:
		row := ui.Row(c).MinWidth(0).Gap(8).AlignItems(ui.Center)
		applyConstraints(row, node)
		row.Children(func() {
			for i := range node.Children {
				MapElement(c, node.Children[i], skin, ctx)
			}
		})

	case schema.ElemPanel:
		panel := ui.Column(c).MinWidth(0).Padding(10, 14).Gap(6).
			Radius(skin.RadiusControl).Background(skin.SurfaceElevated)
		if node.Tone != "" && node.Tone != schema.ToneNormal {
			panel.Border(1, skin.ToneColor(node.Tone))
		}
		applyNodeStyle(panel, node, ctx.Sheet, style.StateNone)
		applyConstraints(panel, node)
		panel.Children(func() {
			if node.Title != "" {
				ui.Text(c, interpolate(node.Title, ctx.Bindings)).
					Font(skin.Font).FontSize(skin.FontSizeUI).FontWeight(700).
					TextColor(skin.ToneColor(node.Tone))
			}
			for i := range node.Children {
				MapElement(c, node.Children[i], skin, ctx)
			}
		})

	case schema.ElemText:
		ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
			for _, sp := range node.Spans {
				ui.Text(c, sp.Text).Font(skin.Font).FontSize(skin.FontSizeBody).
					TextColor(skin.ToneColor(sp.Tone))
			}
		})

	case schema.ElemTemplate:
		row := ui.Row(c).Gap(4).AlignItems(ui.Center)
		if bg, ok := skin.BackgroundColor(node.Background); ok {
			row.Background(bg)
		}
		row.Children(func() {
			if len(node.TemplateSpans) > 0 {
				for _, sp := range node.TemplateSpans {
					txt := resolveTemplateSpan(sp, ctx.Bindings)
					if txt == "" {
						continue
					}
					el := ui.Text(c, txt).Font(skin.Font).FontSize(skin.FontSizeUI).
						TextColor(skin.ToneColor(sp.Tone)).NoWrap()
					if sp.Bold {
						el.FontWeight(700)
					}
					if sp.Width > 0 {
						el.Width(float32(sp.Width) * 8.5)
					}
				}
				return
			}
			for _, sp := range node.Spans {
				ui.Text(c, interpolate(sp.Text, ctx.Bindings)).
					Font(skin.Font).FontSize(skin.FontSizeUI).TextColor(skin.ToneColor(sp.Tone))
			}
		})

	case schema.ElemPrompt:
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			if node.Title != "" {
				ui.Text(c, interpolate(node.Title, ctx.Bindings)).
					Font(skin.Font).FontSize(skin.FontSizeUI).FontWeight(700).TextColor(skin.Accent).NoWrap()
			}
			for i := range node.Children {
				MapElement(c, node.Children[i], skin, ctx)
			}
		})

	case schema.ElemBar:
		mapBar(c, node, skin, ctx)

	case schema.ElemList:
		mapList(c, node, skin, ctx)

	case schema.ElemInput:
		row := ui.Row(c).Padding(6, 12).Gap(8).AlignItems(ui.Center).Radius(skin.RadiusControl)
		if bg, ok := skin.BackgroundColor(node.Background); ok {
			row.Background(bg)
		} else {
			row.Background(skin.Bar)
		}
		if node.Bordered {
			row.Border(1, skin.Border)
		}
		row.Children(func() {
			for i := range node.Children {
				MapElement(c, node.Children[i], skin, ctx)
			}
		})

	case schema.ElemChoice:
		if len(node.Children) == 0 {
			return
		}
		chosen := node.Children[len(node.Children)-1]
		for i := 0; i < len(node.Children)-1; i++ {
			ch := node.Children[i]
			if ch.ID != "" && ctx.Facts[string(ch.ID)] {
				chosen = ch
				break
			}
		}
		MapElement(c, chosen, skin, ctx)

	case schema.ElemSlot:
		if ctx.OnRenderSlotSource != nil && node.SlotSource != "" {
			ctx.OnRenderSlotSource(c, node.SlotSource, node.SlotID)
			return
		}
		if ctx.Slots != nil && node.SlotID != "" {
			rctx := ctx.SlotContext
			rctx.Surface = c
			for _, r := range ctx.Slots.Resolve(node.SlotID) {
				r.Render(rctx)
			}
		}

	case schema.ElemSized, schema.ElemRegion:
		box := ui.Column(c).MinWidth(0).AlignItems(ui.Stretch)
		applyConstraints(box, node)
		box.Children(func() {
			for i := range node.Children {
				MapElement(c, node.Children[i], skin, ctx)
			}
		})

	case schema.ElemButton:
		label := interpolate(node.Title, ctx.Bindings)
		if label == "" {
			label = node.Action
		}
		key := string(node.ID)
		if key == "" {
			key = "map-btn-" + node.Action + "-" + label
		}
		btn := ui.ButtonBase(c.Key(key)).Label(label).
			Padding(5, 12).Radius(skin.RadiusControl).Background(skin.SurfaceElevated).
			AlignItems(ui.Center).Justify(ui.Center)
		pstate := style.StateNone
		if btn.Hovered() {
			btn.Background(skin.SurfaceHover)
			pstate |= style.StateHover
		}
		cs := applyNodeStyle(btn, node, ctx.Sheet, pstate)
		btn.Children(func() {
			txt := ui.Text(c, label).Font(skin.Font).FontSize(skin.FontSizeMeta).FontWeight(600).
				TextColor(skin.ToneColor(node.Tone)).NoWrap()
			cs.ApplyToText(txt)
		})
		if btn.Clicked() && ctx.OnAction != nil && node.Action != "" {
			ctx.OnAction(node.Action, node.ActionArgs)
		}
	}
}

func mapBar(c *ui.Context, node schema.UIElement, skin Skin, ctx Context) {
	bg := skin.Bar
	if custom, ok := skin.BackgroundColor(node.Background); ok {
		bg = custom
	}
	ui.Row(c).Height(skin.BarHeight).Padding(0, 12).Gap(10).AlignItems(ui.Center).Background(bg).Children(func() {
		if len(node.BarSegments) == 0 {
			for i := range node.Children {
				MapElement(c, node.Children[i], skin, ctx)
			}
			return
		}
		var left, right []schema.UIBarSegment
		for _, seg := range node.BarSegments {
			if seg.MinWidth > 0 && ctx.WidthCols > 0 && ctx.WidthCols < seg.MinWidth {
				continue
			}
			if seg.ConditionID != "" && !ctx.Facts[seg.ConditionID] {
				continue
			}
			if seg.Side == schema.BarSideRight {
				right = append(right, seg)
			} else {
				left = append(left, seg)
			}
		}
		for i, seg := range left {
			renderBarSegment(c, fmt.Sprintf("l-%d", i), seg, skin, ctx)
		}
		ui.Spacer(c)
		for i, seg := range right {
			renderBarSegment(c, fmt.Sprintf("r-%d", i), seg, skin, ctx)
		}
	})
}

func renderBarSegment(c *ui.Context, key string, seg schema.UIBarSegment, skin Skin, ctx Context) {
	if seg.Child == nil {
		return
	}
	if seg.CommandName != "" && ctx.OnCommand != nil {
		btn := ui.ButtonBase(c.Key("bar-cmd-"+key+"-"+seg.CommandName)).Label(seg.CommandName).
			Padding(2, 6).Radius(skin.RadiusSmall).AlignItems(ui.Center)
		if btn.Hovered() {
			btn.Background(skin.SurfaceHover)
		}
		btn.Children(func() {
			MapElement(c, *seg.Child, skin, ctx)
		})
		if btn.Clicked() {
			ctx.OnCommand(seg.CommandName)
		}
		return
	}
	MapElement(c, *seg.Child, skin, ctx)
}

func mapList(c *ui.Context, node schema.UIElement, skin Skin, ctx Context) {
	if ctx.OnRenderListSource != nil && node.ListSource != "" && node.ListSource != schema.ListSourceInlineRows {
		ctx.OnRenderListSource(c, node.ID, node.ListSource)
		return
	}
	ui.Column(c.Key("map-list-"+string(node.ID))).Gap(2).AlignItems(ui.Stretch).Children(func() {
		for idx, r := range node.InlineRows {
			row := r
			rowID := row.ID
			if rowID == "" {
				rowID = fmt.Sprintf("row-%d", idx)
			}
			// Render row fields in deterministic sorted key order (or using Child template if provided).
			if len(node.Children) > 0 {
				rowCtx := ctx
				merged := make(map[string]string, len(ctx.Bindings)+len(row.Fields))
				for k, v := range ctx.Bindings {
					merged[k] = v
				}
				for k, v := range row.Fields {
					merged[k] = v
				}
				rowCtx.Bindings = merged
				btn := ui.ButtonBase(c.Key("lrow-"+string(node.ID)+"-"+rowID)).Label(rowID).
					Height(skin.RowHeight).Padding(0, 12).Radius(skin.RadiusSmall).AlignItems(ui.Center).Justify(ui.Start)
				if btn.Hovered() {
					btn.Background(skin.SurfaceHover)
				}
				btn.Children(func() {
					MapElement(c, node.Children[0], skin, rowCtx)
				})
				if btn.Clicked() && ctx.OnSelectRow != nil {
					ctx.OnSelectRow(string(node.ID), rowID)
				}
				continue
			}

			keys := make([]string, 0, len(row.Fields))
			for k := range row.Fields {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts := make([]string, 0, len(keys))
			for _, k := range keys {
				parts = append(parts, row.Fields[k])
			}
			label := strings.Join(parts, "  ")
			btn := ui.ButtonBase(c.Key("lrow-"+string(node.ID)+"-"+rowID)).Label(label).
				Height(skin.RowHeight).Padding(0, 12).Radius(skin.RadiusSmall).AlignItems(ui.Center).Justify(ui.Start)
			if btn.Hovered() {
				btn.Background(skin.SurfaceHover)
			}
			btn.Children(func() {
				ui.Text(c, label).Font(skin.Font).FontSize(skin.FontSizeUI).TextColor(skin.Ink).NoWrap()
			})
			if btn.Clicked() && ctx.OnSelectRow != nil {
				ctx.OnSelectRow(string(node.ID), rowID)
			}
		}
	})
}

func applyNodeStyle(el ui.Element, node schema.UIElement, sheet *style.Sheet, state style.PseudoState) style.ComputedStyle {
	if sheet == nil || len(node.Classes) == 0 {
		return style.ComputedStyle{}
	}
	cs := sheet.Resolve(node.Classes, state)
	cs.ApplyToElement(el)
	return cs
}

func applyConstraints(el ui.Element, node schema.UIElement) {
	if node.Flex > 0 {
		el.Grow(float32(node.Flex))
	}
	if node.Cells > 0 {
		el.MinHeight(float32(node.Cells) * cellDP)
	}
}

func resolveTemplateSpan(sp schema.UITemplateSpan, bindings map[string]string) string {
	if sp.Literal != "" {
		return sp.Literal
	}
	if sp.BindingID != "" {
		return bindings[sp.BindingID]
	}
	if sp.ColumnID != "" {
		return bindings[sp.ColumnID]
	}
	if sp.TailID != "" {
		return bindings[sp.TailID]
	}
	return ""
}

func interpolate(raw string, bindings map[string]string) string {
	if len(bindings) == 0 || !strings.Contains(raw, "{{") {
		return raw
	}
	out := raw
	for k, v := range bindings {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}
