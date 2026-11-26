// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package widgets

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/runtime/kit"
	"github.com/tao3k/marlin-mygo/runtime/style"
)

// Render paints a declarative Bento *Node tree onto mygo/ui using live Signals,
// framework-level runtime/kit overflow-proof GPU primitives (iOS 26 Liquid
// Glass, native SF Symbols, GPU shader ThinkingOrb, Apple Intelligence edge
// glow, Oklab SparkGauges, and ui/chart MiniTrendBars), and interactive
// Runtime callbacks.
func Render(c *ui.Context, node *Node, sig *Signals, pal kit.Palette, sheet *style.Sheet, rt *Runtime) {
	if node == nil {
		return
	}
	if rt == nil {
		rt = &Runtime{Modules: DefaultModules()}
	}
	renderNode(c, node, sig, pal, sheet, rt, 0)
}

func surfOpts(rt *Runtime) kit.SurfaceOptions {
	if rt == nil {
		return kit.SurfaceOptions{Glass: true, Motion: true}
	}
	return kit.SurfaceOptions{Glass: rt.Modules.Glass, Motion: rt.Modules.Motion}
}

func renderNode(c *ui.Context, n *Node, sig *Signals, pal kit.Palette, sheet *style.Sheet, rt *Runtime, idx int) {
	if n == nil {
		return
	}
	key := n.ID
	if key == "" {
		key = fmt.Sprintf("w-%s-%d", n.Kind, idx)
	}
	opts := surfOpts(rt)
	cardRad := kit.ResolveRadius(sheet, "--radius-card", kit.RadiusCard)

	switch n.Kind {
	case KindRow:
		gap := n.Gap
		if gap <= 0 {
			gap = 14
		}
		el := kit.SafeRow(c, key, gap).AlignItems(ui.Stretch)
		applySizing(el, n)
		el.Children(func() {
			for i, ch := range n.Children {
				renderNode(c, ch, sig, pal, sheet, rt, i)
			}
		})

	case KindCol:
		gap := n.Gap
		if gap <= 0 {
			gap = 14
		}
		el := kit.SafeCol(c, key, gap).AlignItems(ui.Stretch)
		applySizing(el, n)
		el.Children(func() {
			for i, ch := range n.Children {
				renderNode(c, ch, sig, pal, sheet, rt, i)
			}
		})

	case KindSpacer:
		ui.Spacer(c)

	case KindCard:
		renderCardContainer(c, key, n, sig, pal, sheet, rt, func() {
			for i, ch := range n.Children {
				renderNode(c, ch, sig, pal, sheet, rt, i)
			}
		})

	case KindProfile:
		renderCardContainer(c, key, n, sig, pal, sheet, rt, func() {
			kit.SafeCol(c, key+"-body", 12).Fill().Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				kit.ThinkingOrb(c, key+"-orb", 82, rt.AgentWorking, opts.Motion, pal)
				kit.SafeCol(c, key+"-titles", 4).AlignItems(ui.Center).Children(func() {
					kit.Headline(c, sig.Interpolate(n.Title), 19, pal.Ink)
					if sub := sig.Interpolate(n.Subtitle); sub != "" {
						kit.Caption(c, sub, pal.Muted)
					}
				})
				if badge := sig.Interpolate(n.Badge); badge != "" {
					ui.Row(c).Padding(4, 12).Radius(kit.RadiusPill).
						Background(kit.WithAlpha(pal.Bar, 0.72)).AlignItems(ui.Center).Gap(7).Children(func() {
						dot := ui.Box(c.Key(key+"-dot")).Width(8).Height(8).Radius(kit.RadiusPill).Background(pal.Success)
						if opts.Motion {
							pulse := float32(0.55)
							if time.Now().Second()%2 == 0 {
								pulse = 1.0
							}
							dot.Opacity(dot.AnimateTo("pulse", pulse, ui.Smooth))
						}
						kit.Line(c, badge, kit.TextSpec{Font: kit.FontDisplay, Size: 11.5, Weight: 600, Color: pal.Ink})
					})
				}
			})
		})

	case KindClock:
		renderCardContainer(c, key, n, sig, pal, sheet, rt, func() {
			tStr := sig.Interpolate(n.Title)
			secStr := sig.Interpolate(n.Value)
			parts := strings.SplitN(tStr, ":", 2)
			hr, mn := tStr, ""
			if len(parts) == 2 {
				hr, mn = parts[0], parts[1]
			}
			kit.SafeRow(c, key+"-row", 6).Fill().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				kit.SafeRow(c, key+"-hms", 2).AlignItems(ui.End).Children(func() {
					kit.MonoStat(c, hr, 32, pal.Info)
					if mn != "" {
						kit.MonoStat(c, ":"+mn, 24, pal.Info)
					}
					if secStr != "" {
						kit.MonoStat(c, ":"+secStr, 13.5, pal.Muted)
					}
				})
				kit.SafeCol(c, key+"-day", 3).AlignItems(ui.End).Children(func() {
					kit.Eyebrow(c, sig.Interpolate(n.Badge), pal.Success)
					kit.Line(c, sig.Interpolate(n.Subtitle), kit.TextSpec{Font: kit.FontDisplay, Size: 12, Weight: 600, Color: pal.Warning})
				})
			})
		})

	case KindKPI:
		renderCardContainer(c, key, n, sig, pal, sheet, rt, func() {
			kit.SafeRow(c, key+"-row", 12).Fill().AlignItems(ui.Center).Children(func() {
				if n.Icon != "" {
					kit.IconBadge(c, n.Icon, 40, 18, pal.ToneColor(n.Tone))
				}
				kit.SafeCol(c, key+"-txt", 2).Grow(1).Children(func() {
					if val := sig.Interpolate(n.Value); val != "" {
						kit.MonoStat(c, val, 17.5, pal.Ink)
					}
					if title := sig.Interpolate(n.Title); title != "" {
						kit.Line(c, title, kit.TextSpec{Font: kit.FontDisplay, Size: 12.5, Weight: 600, Color: pal.ToneColor(n.Tone)})
					}
					if sub := sig.Interpolate(n.Subtitle); sub != "" {
						kit.Caption(c, sub, pal.Muted)
					}
				})
			})
		})

	case KindHero:
		renderCardContainer(c, key, n, sig, pal, sheet, rt, func() {
			kit.SafeCol(c, key+"-body", 8).Fill().Justify(ui.SpaceBetween).Children(func() {
				kit.SafeRow(c, key+"-top", 10).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
					kit.IconBadge(c, defaultStr(n.Icon, "sparkles"), 42, 20, pal.Accent)
					kit.MonoStat(c, sig.Interpolate(n.Value), 26, pal.Info)
				})
				kit.SafeCol(c, key+"-sub", 3).AlignItems(ui.Start).Children(func() {
					kit.Headline(c, sig.Interpolate(n.Title), 15.5, pal.ToneColor(n.Tone))
					if sub := sig.Interpolate(n.Subtitle); sub != "" {
						kit.Caption(c, sub, pal.Ink)
					}
					if body := sig.Interpolate(n.Body); body != "" {
						kit.Caption(c, body, pal.Muted)
					}
				})
			})
		})

	case KindTaskList:
		renderTaskListCard(c, key, n, sig, pal, sheet, rt)

	case KindComposer, KindControl:
		renderComposerCard(c, key, n, cardRad, sig, pal, sheet, rt)

	case KindGauge:
		targetPct := float32(sig.Float(n.Progress, 0.35))
		var onClick func()
		if n.OnClick != "" && rt.OnAction != nil {
			act := sig.Interpolate(n.OnClick)
			onClick = func() { rt.OnAction(act) }
		}
		kit.SparkGauge(c, key, defaultStr(n.Icon, "check"), sig.Interpolate(n.Title), sig.Interpolate(n.Value), targetPct, pal.ToneColor(n.Tone), pal, opts.Motion, onClick)

	case KindTrend:
		kit.MiniTrendBars(c, key, rt.ActivitySeries, n.Height, pal.ToneColor(n.Tone))

	case KindTileGrid:
		renderTileGrid(c, key, n, sig, pal, sheet, rt)

	case KindActionTile:
		renderActionTile(c, key, n, cardRad, sig, pal, sheet, rt)

	case KindBadgeCard:
		renderBadgeCard(c, key, n, cardRad, sig, pal, sheet, rt)

	case KindLinkList:
		renderLinkList(c, key, n, sig, pal, sheet, rt)

	case KindText:
		txt := sig.Interpolate(defaultStr(n.Value, n.Title))
		kit.Line(c, txt, kit.TextSpec{Font: kit.FontDisplay, Size: 13.5, Weight: 500, Color: pal.ToneColor(n.Tone)})
	}
}

func renderCardContainer(
	c *ui.Context,
	key string,
	n *Node,
	sig *Signals,
	pal kit.Palette,
	sheet *style.Sheet,
	rt *Runtime,
	body func(),
) {
	classes := append([]string{"bento-card"}, n.Classes...)
	gap := n.Gap
	if gap <= 0 {
		gap = 10
	}
	opts := surfOpts(rt)
	if n.OnClick != "" {
		btn := ui.ButtonBase(c.Key(key)).Label(sig.Interpolate(defaultStr(n.Title, key))).
			MinWidth(0).MinHeight(0).Padding(16, 18).Clip().
			AlignItems(ui.Stretch).Justify(ui.Center)
		applySizing(btn, n)
		kit.ApplyCardSurface(btn, classes, btn.Hovered(), pal, sheet, opts)
		btn.Children(func() {
			kit.SafeCol(c, key+"-in", gap).Fill().AlignItems(ui.Stretch).Justify(ui.Center).Children(body)
		})
		if btn.Clicked() && rt.OnAction != nil {
			rt.OnAction(sig.Interpolate(n.OnClick))
		}
		return
	}
	card := kit.SafeCol(c, key, gap).Padding(16, 18).AlignItems(ui.Stretch).Justify(ui.Center)
	applySizing(card, n)
	kit.ApplyCardSurface(card, classes, false, pal, sheet, opts)
	card.Children(body)
}

func renderTaskListCard(c *ui.Context, key string, n *Node, sig *Signals, pal kit.Palette, sheet *style.Sheet, rt *Runtime) {
	card := kit.SafeCol(c, key, 10).Padding(16, 18).AlignItems(ui.Stretch)
	applySizing(card, n)
	kit.ApplyCardSurface(card, append([]string{"bento-card"}, n.Classes...), false, pal, sheet, surfOpts(rt))

	var addNow bool
	var cycleID string

	card.Children(func() {
		kit.SafeRow(c, key+"-hdr", 8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			kit.SafeRow(c, key+"-hl", 10).AlignItems(ui.Center).Children(func() {
				kit.IconBadge(c, defaultStr(n.Icon, "check"), 30, 14, pal.Success)
				kit.Headline(c, sig.Interpolate(defaultStr(n.Title, "Agent Tasks")), 15, pal.Ink)
			})
			kit.SafeRow(c, key+"-hr", 8).AlignItems(ui.Center).Children(func() {
				kit.MonoStat(c, sig.Interpolate(n.Value), 13.5, pal.Info)
				if n.OnClick != "" {
					openBtn := ui.ButtonBase(c.Key(key+"-open")).Label("Room").
						Padding(4, 10).Radius(kit.RadiusPill).Background(kit.WithAlpha(pal.Bar, 0.72)).AlignItems(ui.Center)
					if openBtn.Hovered() {
						openBtn.Background(pal.Hover)
					}
					openBtn.Children(func() {
						kit.Line(c, "Room", kit.TextSpec{Font: kit.FontDisplay, Size: 11.5, Weight: 600, Color: pal.Accent})
					})
					if openBtn.Clicked() && rt.OnAction != nil {
						rt.OnAction(sig.Interpolate(n.OnClick))
					}
				}
			})
		})

		ui.Scroll(c.Key(key+"-scroll")).Grow(1).MinHeight(0).Children(func() {
			kit.SafeCol(c, key+"-list", 6).AlignItems(ui.Stretch).Children(func() {
				if len(rt.Tasks) == 0 {
					kit.Caption(c, "No tasks yet — add one below to dispatch to Agent Room", pal.Muted)
				}
				for _, item := range rt.Tasks {
					t := item
					col := pal.Info
					iconName := "clock"
					switch t.Status {
					case "IN_PROGRESS":
						col = pal.Warning
						iconName = "bolt"
					case "DONE":
						col = pal.Success
						iconName = "check"
					case "BLOCKED":
						col = pal.Danger
						iconName = "host"
					}
					row := ui.ButtonBase(c.Key(key+"-task-"+t.ID)).Label(t.Title).
						Height(32).Padding(0, 10).Gap(9).Radius(10).Clip().
						Background(kit.WithAlpha(pal.Bar, 0.56)).AlignItems(ui.Center).Justify(ui.Start)
					if row.Hovered() {
						row.Background(kit.WithAlpha(pal.Hover, 0.85))
					}
					row.Children(func() {
						kit.Symbol(c, iconName, 14, col)
						kit.FlexLine(c, t.Title, kit.TextSpec{Font: kit.FontDisplay, Size: 12.5, Weight: 500, Color: pal.Ink})
						ui.Row(c).Padding(2, 7).Radius(kit.RadiusPill).Background(kit.WithAlpha(col, 0.16)).Children(func() {
							kit.Line(c, t.Status, kit.TextSpec{Font: kit.FontMono, Size: 10, Weight: 700, Color: col})
						})
					})
					if row.Clicked() {
						cycleID = t.ID
					}
				}
			})
		})

		if rt.NewTaskDraft != nil {
			ui.Row(c).Radius(11).Background(kit.WithAlpha(pal.Bar, 0.72)).Padding(5, 10).Gap(8).AlignItems(ui.Center).Clip().Children(func() {
				inp := ui.TextInputBase(c.Key(key+"-add-inp"), rt.NewTaskDraft).
					Placeholder("+ Add live task (Enter)...").Font(kit.FontDisplay).FontSize(12.5).TextColor(pal.Ink).Grow(1)
				if inp.Submitted() {
					addNow = true
				}
				btn := ui.ButtonBase(c.Key(key+"-add-btn")).Label("Add Task").
					Padding(4, 11).Radius(8).Background(pal.Success).AlignItems(ui.Center)
				btn.Children(func() {
					kit.Line(c, "Add", kit.TextSpec{Font: kit.FontDisplay, Size: 11.5, Weight: 700, Color: pal.Canvas})
				})
				if btn.Clicked() {
					addNow = true
				}
			})
		}
	})

	if cycleID != "" && rt.OnCycleTask != nil {
		rt.OnCycleTask(cycleID)
	}
	if addNow && rt.NewTaskDraft != nil && strings.TrimSpace(*rt.NewTaskDraft) != "" && rt.OnAddTask != nil {
		title := strings.TrimSpace(*rt.NewTaskDraft)
		*rt.NewTaskDraft = ""
		rt.OnAddTask(title)
	}
}

func renderComposerCard(c *ui.Context, key string, n *Node, cardRad float32, sig *Signals, pal kit.Palette, sheet *style.Sheet, rt *Runtime) {
	opts := surfOpts(rt)
	card := kit.SafeCol(c, key, 10).Padding(16, 18).AlignItems(ui.Stretch).Justify(ui.SpaceBetween)
	applySizing(card, n)
	kit.ApplyCardSurface(card, append([]string{"bento-card"}, n.Classes...), false, pal, sheet, opts)

	var sendNow bool
	var quickPrompt string

	card.Children(func() {
		kit.ActiveEdgeGlow(c, key, cardRad, rt.AgentWorking && opts.Motion, pal)

		kit.SafeRow(c, key+"-hdr", 8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			kit.SafeRow(c, key+"-hl", 9).Grow(1).AlignItems(ui.Center).Children(func() {
				kit.IconBadge(c, defaultStr(n.Icon, "bolt"), 30, 14, pal.Warning)
				kit.FlexLine(c, sig.Interpolate(n.Title), kit.TextSpec{Font: kit.FontDisplay, Size: 14.5, Weight: 700, Color: pal.Ink})
				if rt.AgentWorking {
					kit.LoadingIndicator(c, 16, pal.Accent, !opts.Motion)
				}
			})
			kit.SafeRow(c, key+"-hr", 6).AlignItems(ui.Center).Children(func() {
				if n.PlayAction != "" {
					b := ui.ButtonBase(c.Key(key+"-chat")).Label("Open Chat").
						Padding(4, 10).Radius(kit.RadiusPill).Background(kit.WithAlpha(pal.Bar, 0.72)).AlignItems(ui.Center)
					if b.Hovered() {
						b.Background(pal.Hover)
					}
					b.Children(func() {
						kit.Line(c, "Chat", kit.TextSpec{Font: kit.FontDisplay, Size: 11.5, Weight: 600, Color: pal.Info})
					})
					if b.Clicked() && rt.OnAction != nil {
						rt.OnAction(n.PlayAction)
					}
				}
			})
		})

		if strings.TrimSpace(rt.LastReply) != "" {
			ui.Row(c).Radius(10).Background(kit.WithAlpha(pal.Bar, 0.54)).Padding(8, 12).Clip().Children(func() {
				ui.Text(c, rt.LastReply).Font(kit.FontDisplay).FontSize(12.5).TextColor(pal.Ink).MinWidth(0).Clip().Ellipsis("...").MaxLines(2)
			})
		} else {
			kit.SafeRow(c, key+"-chips", 8).AlignItems(ui.Center).Children(func() {
				chips := []struct {
					label  string
					prompt string
					tone   ui.Color
				}{
					{"Summarize workspace", "Summarize the current workspace status and open tasks.", pal.Info},
					{"Check host health", "Check connected Marlin targets and active processes.", pal.Success},
				}
				for _, ch := range chips {
					item := ch
					b := ui.ButtonBase(c.Key(key+"-chip-"+item.label)).Label(item.label).
						Padding(5, 11).Radius(kit.RadiusPill).Background(kit.WithAlpha(pal.Bar, 0.65)).AlignItems(ui.Center)
					if b.Hovered() {
						b.Background(pal.Hover)
					}
					b.Children(func() {
						kit.Line(c, item.label, kit.TextSpec{Font: kit.FontDisplay, Size: 11.5, Weight: 600, Color: item.tone})
					})
					if b.Clicked() {
						quickPrompt = item.prompt
					}
				}
			})
		}

		if rt.PromptDraft != nil {
			ui.Row(c).Radius(12).Background(kit.WithAlpha(pal.Bar, 0.76)).Padding(7, 12).Gap(10).AlignItems(ui.Center).Clip().Children(func() {
				inp := ui.TextInputBase(c.Key(key+"-prompt"), rt.PromptDraft).
					Placeholder("Ask Marlin anything...").
					Font(kit.FontDisplay).FontSize(13).TextColor(pal.Ink).Grow(1)
				if inp.Submitted() {
					sendNow = true
				}
				sb := ui.ButtonBase(c.Key(key+"-send")).Label("Send").
					Padding(5, 13).Radius(9).AlignItems(ui.Center).Gap(6).
					LinearGradient(ui.LinearGradient{From: kit.Lighten(pal.Accent, 0.16), To: pal.Accent, Angle: 135, Oklab: true})
				sb.Children(func() {
					kit.Symbol(c, "send", 12, ui.RGB(255, 255, 255))
					kit.Line(c, "Send", kit.TextSpec{Font: kit.FontDisplay, Size: 12, Weight: 700, Color: ui.RGB(255, 255, 255)})
				})
				if sb.Clicked() {
					sendNow = true
				}
			})
		}
	})

	if quickPrompt != "" && rt.OnSubmitPrompt != nil {
		rt.OnSubmitPrompt(quickPrompt)
		return
	}
	if sendNow && rt.PromptDraft != nil && strings.TrimSpace(*rt.PromptDraft) != "" && rt.OnSubmitPrompt != nil {
		text := strings.TrimSpace(*rt.PromptDraft)
		*rt.PromptDraft = ""
		rt.OnSubmitPrompt(text)
	}
}

func renderTileGrid(c *ui.Context, key string, n *Node, sig *Signals, pal kit.Palette, sheet *style.Sheet, rt *Runtime) {
	opts := surfOpts(rt)
	card := kit.SafeCol(c, key, 8).Padding(12, 12).Justify(ui.Center).AlignItems(ui.Stretch)
	applySizing(card, n)
	kit.ApplyCardSurface(card, append([]string{"bento-card"}, n.Classes...), false, pal, sheet, opts)

	card.Children(func() {
		for rowStart := 0; rowStart < len(n.Tiles); rowStart += 3 {
			end := rowStart + 3
			if end > len(n.Tiles) {
				end = len(n.Tiles)
			}
			slice := n.Tiles[rowStart:end]
			kit.SafeRow(c, fmt.Sprintf("%s-r%d", key, rowStart), 8).Grow(1).AlignItems(ui.Stretch).Justify(ui.Center).Children(func() {
				for _, item := range slice {
					t := item
					col := pal.ToneColor(t.Tone)
					tile := ui.ButtonBase(c.Key(key+"-"+t.ID)).Label(t.Label).
						Grow(1).MinWidth(0).MinHeight(0).Radius(14).Clip().
						Background(kit.WithAlpha(pal.Bar, 0.58)).
						Justify(ui.Center).AlignItems(ui.Center)
					if tile.Hovered() {
						tile.Background(kit.WithAlpha(col, 0.22))
						if opts.Motion {
							tile.Scale(1.06, 1.06)
						}
					}
					tile.Children(func() {
						kit.SafeCol(c, key+"-"+t.ID+"-in", 4).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
							kit.Symbol(c, t.Icon, 16, col)
							kit.Line(c, t.Label, kit.TextSpec{Font: kit.FontDisplay, Size: 10, Weight: 600, Color: pal.Muted})
						})
					})
					if tile.Clicked() && rt.OnAction != nil && t.Action != "" {
						rt.OnAction(sig.Interpolate(t.Action))
					}
				}
			})
		}
	})
}

func renderActionTile(c *ui.Context, key string, n *Node, cardRad float32, sig *Signals, pal kit.Palette, sheet *style.Sheet, rt *Runtime) {
	opts := surfOpts(rt)
	tone := pal.ToneColor(n.Tone)
	btn := ui.ButtonBase(c.Key(key)).Label(sig.Interpolate(defaultStr(n.Title, key))).
		MinWidth(0).MinHeight(0).Radius(cardRad).Clip().Justify(ui.Center).AlignItems(ui.Center)
	applySizing(btn, n)

	iconCol := tone
	if n.SolidBg {
		iconCol = ui.RGB(255, 255, 255)
		btn.LinearGradient(ui.LinearGradient{
			From:  kit.Lighten(tone, 0.16),
			To:    tone,
			Angle: 145,
			Oklab: true,
		})
		btn.InsetShadow(0, 1, 1.5, 0, kit.WithAlpha(ui.RGB(255, 255, 255), 0.35))
		btn.Shadow(0, 8, 20, -6, kit.WithAlpha(tone, 0.44))
		if opts.Motion && btn.Hovered() {
			btn.Translate(0, btn.Spring("kit-lift", -2.5))
		}
	} else {
		kit.ApplyCardSurface(btn, append([]string{"bento-tile"}, n.Classes...), btn.Hovered(), pal, sheet, opts)
	}

	badge := strings.TrimSpace(sig.Interpolate(n.Badge))
	btn.Children(func() {
		kit.SafeCol(c, key+"-in", 4).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			if n.SolidBg {
				kit.Symbol(c, n.Icon, 20, iconCol)
			} else {
				kit.IconBadge(c, n.Icon, 32, 15, tone)
			}
			if title := sig.Interpolate(n.Title); title != "" {
				lblCol := pal.Ink
				if n.SolidBg {
					lblCol = ui.RGB(255, 255, 255)
				}
				kit.Line(c, title, kit.TextSpec{Font: kit.FontDisplay, Size: 11.5, Weight: 600, Color: lblCol})
			}
			if badge != "" {
				bCol := pal.Muted
				if strings.EqualFold(badge, "ON") {
					bCol = pal.Success
				}
				kit.Line(c, badge, kit.TextSpec{Font: kit.FontMono, Size: 10, Weight: 700, Color: bCol})
			}
		})
	})
	if btn.Clicked() && rt.OnAction != nil && n.OnClick != "" {
		rt.OnAction(sig.Interpolate(n.OnClick))
	}
}

func renderBadgeCard(c *ui.Context, key string, n *Node, cardRad float32, sig *Signals, pal kit.Palette, sheet *style.Sheet, rt *Runtime) {
	opts := surfOpts(rt)
	cs := sheet.Resolve(append([]string{"bento-badge"}, n.Classes...), style.StateNone)
	bg := ui.RGB(236, 239, 244)
	fg := ui.RGB(36, 41, 51)
	if cs.HasBackground {
		bg = cs.Background
	}
	if cs.HasColor {
		fg = cs.Color
	}
	btn := ui.ButtonBase(c.Key(key)).Label(sig.Interpolate(defaultStr(n.Title, "Tasks"))).
		MinWidth(0).MinHeight(0).Radius(cardRad).Padding(12, 16).Background(bg).Gap(12).Clip().
		Shadow(0, 10, 24, -6, kit.WithAlpha(ui.RGB(0, 0, 0), 0.26)).
		Justify(ui.Center).AlignItems(ui.Center)
	applySizing(btn, n)
	if opts.Motion && btn.Hovered() {
		btn.Translate(0, btn.Spring("kit-lift", -2.5))
	}
	btn.Children(func() {
		kit.IconBadge(c, defaultStr(n.Icon, "check"), 34, 16, pal.ToneColor(n.Tone))
		ui.Row(c).Padding(4, 10).Radius(9).Background(ui.RGBA(36, 41, 51, 0.10)).AlignItems(ui.Center).Children(func() {
			kit.MonoStat(c, sig.Interpolate(n.Value), 18, fg)
		})
		kit.Line(c, sig.Interpolate(defaultStr(n.Title, "Tasks")), kit.TextSpec{Font: kit.FontDisplay, Size: 13, Weight: 700, Color: fg})
	})
	if btn.Clicked() && rt.OnAction != nil && n.OnClick != "" {
		rt.OnAction(sig.Interpolate(n.OnClick))
	}
}

func renderLinkList(c *ui.Context, key string, n *Node, sig *Signals, pal kit.Palette, sheet *style.Sheet, rt *Runtime) {
	card := kit.SafeCol(c, key, 10).Padding(14, 14).AlignItems(ui.Stretch)
	applySizing(card, n)
	kit.ApplyCardSurface(card, append([]string{"bento-card"}, n.Classes...), false, pal, sheet, surfOpts(rt))

	card.Children(func() {
		ui.Row(c).Height(52).Padding(0, 14).Radius(12).Background(kit.WithAlpha(pal.Bar, 0.68)).
			AlignItems(ui.Center).Justify(ui.SpaceBetween).Clip().Children(func() {
			kit.IconBadge(c, defaultStr(n.Icon, "folder"), 32, 15, pal.Info)
			kit.SafeCol(c, key+"-sum", 1).AlignItems(ui.End).Children(func() {
				kit.Headline(c, sig.Interpolate(n.Value), 14.5, pal.Ink)
				if b := sig.Interpolate(n.Badge); b != "" {
					kit.Caption(c, b, pal.Muted)
				}
			})
		})

		ui.Scroll(c.Key(key+"-scroll")).Grow(1).MinHeight(0).Children(func() {
			kit.SafeCol(c, key+"-links", 3).AlignItems(ui.Stretch).Children(func() {
				for _, item := range n.Links {
					lk := item
					col := pal.ToneColor(lk.Tone)
					lbl := sig.Interpolate(lk.Label)
					row := ui.ButtonBase(c.Key(key+"-"+lk.ID)).Label(lbl).
						Height(31).Padding(0, 9).Gap(10).Radius(9).Clip().
						AlignItems(ui.Center).Justify(ui.Start)
					if row.Hovered() {
						row.Background(kit.WithAlpha(pal.Hover, 0.72))
					}
					row.Children(func() {
						kit.Symbol(c, lk.Icon, 14, col)
						kit.FlexLine(c, lbl, kit.TextSpec{Font: kit.FontDisplay, Size: 12.5, Weight: 500, Color: pal.Ink})
					})
					if row.Clicked() && rt.OnAction != nil && lk.Action != "" {
						rt.OnAction(sig.Interpolate(lk.Action))
					}
				}
			})
		})
	})
}

func applySizing(el ui.Element, n *Node) {
	if n.Width > 0 {
		el.Width(n.Width)
	}
	if n.Height > 0 {
		el.Height(n.Height)
	}
	if n.Flex > 0 {
		el.Grow(n.Flex)
	}
}

func defaultStr(s, fb string) string {
	if strings.TrimSpace(s) == "" {
		return fb
	}
	return s
}
