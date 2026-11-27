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

// Render paints a declarative Control Center Bento *Node tree onto mygo/ui
// using live Signals, framework-level runtime/kit GPU primitives (floating
// frosted islands, native SF Pro system-ui typography, Retina SF Symbols,
// Metal Kage ThinkingOrb, vector RingGauges, Spotlight Ask Marlin composer,
// Apple squircle Quick Launch, and Android 16 / iOS Quick-Action capsules).
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
	return kit.SurfaceOptions{
		Glass:        rt.Modules.Glass,
		GlassOpacity: rt.Modules.GlassOpacity,
		Motion:       rt.Modules.Motion,
	}
}

func renderNode(c *ui.Context, n *Node, sig *Signals, pal kit.Palette, sheet *style.Sheet, rt *Runtime, idx int) {
	if n == nil || !evalVisibleWhen(n.VisibleWhen, sig) {
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
			gap = 12
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
			gap = 12
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
			if title := strings.TrimSpace(sig.Interpolate(n.Title)); title != "" {
				kit.SafeRow(c, key+"-hdr", 8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
					kit.Eyebrow(c, title, pal.Muted)
					if b := strings.TrimSpace(sig.Interpolate(n.Badge)); b != "" {
						ui.Row(c).Padding(2, 8).Radius(kit.RadiusPill).
							Background(kit.WithAlpha(pal.Success, 0.16)).AlignItems(ui.Center).Gap(5).Children(func() {
							ui.Box(c).Width(6).Height(6).Radius(kit.RadiusPill).Background(pal.Success)
							kit.Line(c, b, kit.TextSpec{Font: kit.FontDisplay, Size: 10.5, Weight: 600, Color: pal.Success})
						})
					}
				})
			}
			for i, ch := range n.Children {
				renderNode(c, ch, sig, pal, sheet, rt, i)
			}
		})

	case KindProfile:
		// Control Center identity & live host card: anchored top-to-bottom with
		// status eyebrow, 3D GPU orb focal point, and inset model/endpoint bar.
		renderCardContainer(c, key, n, sig, pal, sheet, rt, func() {
			kit.SafeCol(c, key+"-body", 10).Fill().Justify(ui.SpaceBetween).AlignItems(ui.Stretch).Children(func() {
				kit.SafeRow(c, key+"-top", 6).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
					kit.Eyebrow(c, "WORKSPACE", pal.Muted)
					if badge := sig.Interpolate(n.Badge); badge != "" {
						ui.Row(c).Padding(3, 9).Radius(kit.RadiusPill).
							Background(kit.WithAlpha(pal.Success, 0.16)).AlignItems(ui.Center).Gap(5).Children(func() {
							dot := ui.Box(c.Key(key+"-dot")).Width(6).Height(6).Radius(kit.RadiusPill).Background(pal.Success)
							if opts.Motion {
								pulse := float32(0.55)
								if time.Now().Second()%2 == 0 {
									pulse = 1.0
								}
								dot.Opacity(dot.AnimateTo("pulse", pulse, ui.Smooth))
							}
							kit.Line(c, badge, kit.TextSpec{Font: kit.FontDisplay, Size: 10.5, Weight: 700, Color: pal.Success})
						})
					}
				})

				kit.SafeCol(c, key+"-center", 10).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
					kit.ThinkingOrb(c, key+"-orb", 84, rt.AgentWorking, opts.Motion, pal)
					kit.SafeCol(c, key+"-titles", 2).AlignItems(ui.Center).Children(func() {
						kit.Headline(c, sig.Interpolate(n.Title), 18, pal.Ink)
						if sub := sig.Interpolate(n.Subtitle); sub != "" {
							kit.Caption(c, sub, pal.Muted)
						}
					})
				})

				// Anchored bottom telemetry strip with concentric inner corner radius.
				modelVal := strings.TrimSpace(sig.Interpolate(n.Value))
				hostVal := strings.TrimSpace(sig.Interpolate(n.Body))
				if modelVal != "" || hostVal != "" {
					ui.Column(c).Radius(kit.ConcentricRadius(cardRad, 8)).Background(kit.WithAlpha(pal.Bar, 0.62)).
						BorderWidth(1).BorderColor(kit.WithAlpha(ui.RGB(255, 255, 255), 0.08)).
						InsetShadow(0, 1, 2, 0, kit.WithAlpha(ui.RGB(0, 0, 0), 0.24)).
						Padding(8, 10).Gap(4).AlignItems(ui.Stretch).Children(func() {
						if modelVal != "" {
							kit.SafeRow(c, key+"-mod", 6).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
								kit.Caption(c, "Model", pal.Muted)
								kit.Line(c, modelVal, kit.TextSpec{Font: kit.FontMono, Size: 11, Weight: 600, Color: pal.Info})
							})
						}
						if hostVal != "" {
							kit.SafeRow(c, key+"-host", 6).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
								kit.Caption(c, "Endpoint", pal.Muted)
								kit.Line(c, hostVal, kit.TextSpec{Font: kit.FontMono, Size: 10.5, Weight: 500, Color: pal.Ink})
							})
						}
					})
				}
			})
		})

	case KindClock:
		// Crisp iOS / macOS Lock Screen & Control Center clock card:
		// top eyebrow + period pill, bold baseline-aligned numerals, anchored date line.
		renderCardContainer(c, key, n, sig, pal, sheet, rt, func() {
			tStr := sig.Interpolate(n.Title)
			secStr := sig.Interpolate(n.Value)
			kit.SafeCol(c, key+"-col", 6).Fill().Justify(ui.SpaceBetween).AlignItems(ui.Stretch).Children(func() {
				kit.SafeRow(c, key+"-top", 6).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
					kit.Eyebrow(c, "LOCAL TIME", pal.Muted)
					if ampm := strings.TrimSpace(sig.Interpolate(n.Badge)); ampm != "" {
						ui.Row(c).Padding(2, 7).Radius(kit.RadiusPill).Background(kit.WithAlpha(pal.Info, 0.16)).Children(func() {
							kit.Line(c, ampm, kit.TextSpec{Font: kit.FontDisplay, Size: 10.5, Weight: 700, Color: pal.Info})
						})
					}
				})

				kit.SafeRow(c, key+"-hms", 4).AlignItems(ui.End).Children(func() {
					kit.DisplayFigure(c, tStr, 40, pal.Ink)
					if secStr != "" {
						ui.Row(c).Padding(0, 0, 6, 0).Children(func() {
							kit.MonoStat(c, ":"+secStr, 13.5, pal.Muted)
						})
					}
				})

				kit.SafeRow(c, key+"-foot", 6).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
					kit.Line(c, sig.Interpolate(n.Subtitle), kit.TextSpec{Font: kit.FontDisplay, Size: 12.5, Weight: 600, Color: pal.Warning})
					kit.Caption(c, time.Now().Format("Jan 02"), pal.Muted)
				})
			})
		})

	case KindKPI:
		// Compact iOS / Android 16 Control Center connectivity/metric tile.
		renderCardContainer(c, key, n, sig, pal, sheet, rt, func() {
			kit.SafeRow(c, key+"-row", 12).Fill().AlignItems(ui.Center).Children(func() {
				if n.Icon != "" {
					kit.IconBadge(c, n.Icon, 40, 18, pal.ToneColor(n.Tone))
				}
				kit.SafeCol(c, key+"-txt", 2).Grow(1).Children(func() {
					if title := sig.Interpolate(n.Title); title != "" {
						kit.Eyebrow(c, title, pal.Muted)
					}
					if val := sig.Interpolate(n.Value); val != "" {
						kit.Line(c, val, kit.TextSpec{Font: kit.FontDisplay, Size: 18, Weight: 700, Color: pal.Ink})
					}
					if sub := sig.Interpolate(n.Subtitle); sub != "" {
						kit.Caption(c, sub, pal.Info)
					}
				})
			})
		})

	case KindHero:
		renderCardContainer(c, key, n, sig, pal, sheet, rt, func() {
			kit.SafeCol(c, key+"-body", 8).Fill().Justify(ui.SpaceBetween).Children(func() {
				kit.SafeRow(c, key+"-top", 10).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
					kit.IconBadge(c, defaultStr(n.Icon, "sparkles"), 38, 18, pal.Accent)
					kit.MonoStat(c, sig.Interpolate(n.Value), 28, pal.Info)
				})
				kit.SafeCol(c, key+"-sub", 3).AlignItems(ui.Start).Children(func() {
					kit.Headline(c, sig.Interpolate(n.Title), 16, pal.ToneColor(n.Tone))
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
		// GPU vector Activity Ring Gauge (iOS / macOS Control Center style):
		// always labeled with clear numeric readout + semantic category.
		targetPct := float32(sig.Float(n.Progress, 0.35))
		var onClick func()
		if n.OnClick != "" && rt.OnAction != nil {
			act := sig.Interpolate(n.OnClick)
			onClick = func() { rt.OnAction(act) }
		}
		kit.RingGauge(c, key, defaultStr(n.Icon, "check"), sig.Interpolate(n.Title), sig.Interpolate(n.Value), targetPct, pal.ToneColor(n.Tone), pal, opts.Motion, onClick)

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

	case KindWebIsland:
		kit.WebIslandFrame(c, kit.WebIslandConfig{
			Key:        key,
			IslandID:   sig.Interpolate(defaultStr(n.IslandID, key)),
			Title:      sig.Interpolate(n.Title),
			Subtitle:   sig.Interpolate(n.Subtitle),
			Height:     n.Height,
			Borderless: n.Borderless,
			Palette:    pal,
			Sheet:      sheet,
			Surface:    opts,
			Host:       rt.WebHost,
		})

	case KindScroll:
		gap := n.Gap
		if gap <= 0 {
			gap = 8
		}
		sc := ui.Scroll(c.Key(key)).MinHeight(0)
		applySizing(sc, n)
		if n.Flex == 0 && n.Height == 0 {
			sc.Grow(1)
		}
		sc.Children(func() {
			kit.SafeCol(c, key+"-in", gap).AlignItems(ui.Stretch).Children(func() {
				for i, ch := range n.Children {
					renderNode(c, ch, sig, pal, sheet, rt, i)
				}
			})
		})

	case KindSection:
		gap := n.Gap
		if gap <= 0 {
			gap = 6
		}
		open := !rt.isSectionCollapsed(key)
		kit.SafeCol(c, key, gap).AlignItems(ui.Stretch).Children(func() {
			hdr := kit.ActionControl(ui.ButtonBase(c.Key(key+"-toggle")).Label(sig.Interpolate(defaultStr(n.Title, "Section"))).
				Height(24).Padding(0, 6).Radius(7).AlignItems(ui.Center).Justify(ui.SpaceBetween))
			if hdr.Hovered() {
				hdr.Background(kit.WithAlpha(pal.Hover, 0.42))
			}
			chevron := "▾"
			if !open {
				chevron = "▸"
			}
			hdr.Children(func() {
				kit.Eyebrow(c, sig.Interpolate(defaultStr(n.Title, "SECTION")), pal.Muted)
				kit.Line(c, chevron, kit.TextSpec{Font: kit.FontMono, Size: 11, Weight: 700, Color: pal.Muted})
			})
			if hdr.Clicked() {
				rt.toggleSection(key)
			}
			if open {
				for i, ch := range n.Children {
					renderNode(c, ch, sig, pal, sheet, rt, i)
				}
			}
		})

	case KindDivider:
		ui.Box(c.Key(key)).Height(1).Background(kit.WithAlpha(pal.Ink, 0.10))

	case KindText:
		txt := sig.Interpolate(defaultStr(n.Value, n.Title))
		kit.Line(c, txt, kit.TextSpec{Font: kit.FontDisplay, Size: 13.5, Weight: 500, Color: pal.ToneColor(n.Tone)})
	}
}

func evalVisibleWhen(expr string, sig *Signals) bool {
	e := strings.TrimSpace(expr)
	if e == "" {
		return true
	}
	if sig == nil {
		return false
	}
	if lhs, rhs, ok := strings.Cut(e, "!="); ok {
		return strings.TrimSpace(sig.Get(strings.TrimSpace(lhs))) != strings.TrimSpace(rhs)
	}
	if lhs, rhs, ok := strings.Cut(e, "=="); ok {
		return strings.TrimSpace(sig.Get(strings.TrimSpace(lhs))) == strings.TrimSpace(rhs)
	}
	neg := false
	if strings.HasPrefix(e, "!") {
		neg = true
		e = strings.TrimSpace(strings.TrimPrefix(e, "!"))
	}
	v := strings.ToLower(strings.TrimSpace(sig.Get(e)))
	truthy := v != "" && v != "0" && v != "false" && v != "off"
	if neg {
		return !truthy
	}
	return truthy
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
		gap = 8
	}
	opts := surfOpts(rt)
	if n.OnClick != "" {
		btn := kit.ActionControl(ui.ButtonBase(c.Key(key)).Label(sig.Interpolate(defaultStr(n.Title, key))).
			MinWidth(0).MinHeight(0).Padding(14, 16).Clip().
			AlignItems(ui.Stretch).Justify(ui.Center))
		applySizing(btn, n)
		kit.ApplyCardSurface(btn, classes, btn.Hovered(), pal, sheet, opts)
		btn.Children(func() {
			kit.SafeCol(c, key+"-in", gap).Fill().AlignItems(ui.Stretch).Justify(ui.SpaceBetween).Children(body)
		})
		if btn.Clicked() && rt.OnAction != nil {
			rt.OnAction(sig.Interpolate(n.OnClick))
		}
		return
	}
	card := kit.SafeCol(c, key, gap).Padding(14, 16).AlignItems(ui.Stretch).Justify(ui.SpaceBetween)
	applySizing(card, n)
	kit.ApplyCardSurface(card, classes, false, pal, sheet, opts)
	card.Children(body)
}

func renderTaskListCard(c *ui.Context, key string, n *Node, sig *Signals, pal kit.Palette, sheet *style.Sheet, rt *Runtime) {
	card := kit.SafeCol(c, key, 10).Padding(14, 16).AlignItems(ui.Stretch)
	applySizing(card, n)
	kit.ApplyCardSurface(card, append([]string{"bento-card"}, n.Classes...), false, pal, sheet, surfOpts(rt))

	var addNow bool
	var cycleID string

	card.Children(func() {
		kit.SafeRow(c, key+"-hdr", 8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			kit.SafeRow(c, key+"-hl", 8).AlignItems(ui.Center).Children(func() {
				kit.IconBadge(c, defaultStr(n.Icon, "check"), 26, 13, pal.Success)
				kit.Headline(c, sig.Interpolate(defaultStr(n.Title, "Agent Tasks")), 15, pal.Ink)
			})
			kit.SafeRow(c, key+"-hr", 8).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Padding(2, 8).Radius(kit.RadiusPill).Background(kit.WithAlpha(pal.Info, 0.16)).Children(func() {
					kit.MonoStat(c, sig.Interpolate(n.Value), 11.5, pal.Info)
				})
				if n.OnClick != "" {
					openBtn := kit.ActionControl(ui.ButtonBase(c.Key(key+"-open")).Label("Room").
						Padding(3, 10).Radius(kit.RadiusPill).
						Background(kit.WithAlpha(pal.Bar, 0.72)).
						BorderWidth(1).BorderColor(kit.WithAlpha(ui.RGB(255, 255, 255), 0.10)).
						AlignItems(ui.Center))
					if openBtn.Hovered() {
						openBtn.Background(pal.Hover)
					}
					openBtn.Children(func() {
						kit.Line(c, "Room", kit.TextSpec{Font: kit.FontDisplay, Size: 11.5, Weight: 600, Color: pal.Ink})
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
					row := kit.ActionControl(ui.ButtonBase(c.Key(key+"-task-"+t.ID)).Label(t.Title).
						Height(34).Padding(0, 11).Gap(9).Radius(11).Clip().
						Background(kit.WithAlpha(pal.Bar, 0.56)).AlignItems(ui.Center).Justify(ui.Start))
					if row.Hovered() {
						row.Background(kit.WithAlpha(pal.Hover, 0.85))
					}
					row.Children(func() {
						kit.Symbol(c, iconName, 14, col)
						kit.FlexLine(c, t.Title, kit.TextSpec{Font: kit.FontDisplay, Size: 13, Weight: 500, Color: pal.Ink})
						ui.Row(c).Padding(2, 7).Radius(kit.RadiusPill).Background(kit.WithAlpha(col, 0.16)).Children(func() {
							kit.Line(c, t.Status, kit.TextSpec{Font: kit.FontMono, Size: 9.5, Weight: 700, Color: col})
						})
					})
					if row.Clicked() {
						cycleID = t.ID
					}
				}
			})
		})

		if rt.NewTaskDraft != nil {
			ui.Row(c).Radius(11).Background(kit.WithAlpha(pal.Bar, 0.72)).
				BorderWidth(1).BorderColor(kit.WithAlpha(ui.RGB(255, 255, 255), 0.08)).
				Padding(5, 10).Gap(8).AlignItems(ui.Center).Clip().Children(func() {
				inp := ui.TextInputBase(c.Key(key+"-add-inp"), rt.NewTaskDraft).
					Placeholder("+ Add live task (Enter)...").Font(kit.FontDisplay).FontSize(12.5).TextColor(pal.Ink).Grow(1)
				if inp.Submitted() {
					addNow = true
				}
				btn := kit.ActionControl(ui.ButtonBase(c.Key(key+"-add-btn")).Label("Add Task").
					Padding(4, 12).Radius(8).Background(pal.Success).AlignItems(ui.Center))
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
	// Apple Spotlight / iOS Intelligence style composer: compact artwork header,
	// full-width frosted prompt bar, and tight 2x2 quick-prompt suggestions (zero empty void).
	opts := surfOpts(rt)
	card := kit.SafeCol(c, key, 10).Padding(14, 16).Justify(ui.SpaceBetween).AlignItems(ui.Stretch)
	applySizing(card, n)
	kit.ApplyCardSurface(card, append([]string{"bento-card"}, n.Classes...), false, pal, sheet, opts)

	var sendNow bool
	var quickPrompt string

	card.Children(func() {
		kit.ActiveEdgeGlow(c, key, cardRad, rt.AgentWorking && opts.Motion, pal)

		kit.SafeRow(c, key+"-hdr", 10).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			kit.SafeRow(c, key+"-hl", 10).Grow(1).AlignItems(ui.Center).Children(func() {
				kit.AmbientArtwork(c, key+"-art", 34, 10, rt.AgentWorking, opts.Motion, pal)
				kit.SafeCol(c, key+"-ht", 1).Grow(1).Children(func() {
					kit.Headline(c, sig.Interpolate(defaultStr(n.Title, "Ask Marlin")), 15, pal.Ink)
					if b := strings.TrimSpace(sig.Interpolate(n.Badge)); b != "" {
						kit.Caption(c, b, pal.Muted)
					}
				})
				if rt.AgentWorking {
					kit.LoadingIndicator(c, 15, pal.Accent, !opts.Motion)
				}
			})
			if n.PlayAction != "" {
				b := kit.ActionControl(ui.ButtonBase(c.Key(key+"-chat")).Label("Open Chat").
					Padding(4, 11).Radius(kit.RadiusPill).
					Background(kit.WithAlpha(pal.Bar, 0.72)).
					BorderWidth(1).BorderColor(kit.WithAlpha(ui.RGB(255, 255, 255), 0.10)).
					AlignItems(ui.Center))
				if b.Hovered() {
					b.Background(pal.Hover)
				}
				b.Children(func() {
					kit.Line(c, "Open Chat", kit.TextSpec{Font: kit.FontDisplay, Size: 11.5, Weight: 600, Color: pal.Info})
				})
				if b.Clicked() && rt.OnAction != nil {
					rt.OnAction(n.PlayAction)
				}
			}
		})

		if rt.PromptDraft != nil {
			ui.Row(c).Height(42).Radius(kit.ConcentricRadius(cardRad, 8)).Background(kit.WithAlpha(pal.Bar, 0.80)).
				BorderWidth(1).BorderColor(kit.WithAlpha(ui.RGB(255, 255, 255), 0.12)).
				InsetShadow(0, 1, 3, 0, kit.WithAlpha(ui.RGB(0, 0, 0), 0.28)).
				Padding(5, 12).Gap(10).AlignItems(ui.Center).Clip().Children(func() {
				kit.Symbol(c, "sparkles", 14, pal.Accent)
				inp := ui.TextInputBase(c.Key(key+"-prompt"), rt.PromptDraft).
					Placeholder("Ask Marlin anything or @mention a workspace file...").
					Font(kit.FontDisplay).FontSize(13).TextColor(pal.Ink).Grow(1)
				if inp.Submitted() {
					sendNow = true
				}
				sb := kit.ActionControl(ui.ButtonBase(c.Key(key+"-send")).Label("Send").
					Padding(5, 13).Radius(9).AlignItems(ui.Center).Gap(5).
					LinearGradient(ui.LinearGradient{From: kit.Lighten(pal.Accent, 0.18), To: pal.Accent, Angle: 135, Oklab: true}).
					Shadow(0, 4, 12, -2, kit.WithAlpha(pal.Accent, 0.45)))
				sb.Children(func() {
					kit.Symbol(c, "send", 11.5, ui.RGB(255, 255, 255))
					kit.Line(c, "Send", kit.TextSpec{Font: kit.FontDisplay, Size: 11.5, Weight: 700, Color: ui.RGB(255, 255, 255)})
				})
				if sb.Clicked() {
					sendNow = true
				}
			})
		}

		if strings.TrimSpace(rt.LastReply) != "" {
			ui.Row(c).Radius(11).Background(kit.WithAlpha(pal.Bar, 0.54)).Padding(9, 12).Clip().Children(func() {
				ui.Text(c, rt.LastReply).Font(kit.FontDisplay).FontSize(12.5).TextColor(pal.Ink).MinWidth(0).Clip().Ellipsis("...").MaxLines(2)
			})
		} else {
			type suggestion struct {
				icon   string
				label  string
				prompt string
				tone   ui.Color
			}
			rows := [][]suggestion{
				{
					{"sparkles", "Summarize workspace", "Summarize the current workspace status and open tasks.", pal.Info},
					{"host", "Check host health", "Check connected Marlin targets and active processes.", pal.Success},
				},
				{
					{"check", "Review open tasks", "Review all open tasks in Agent Room and suggest priorities.", pal.Warning},
					{"cube", "Inspect active models", "List available models and active routing configuration.", pal.Accent},
				},
			}
			kit.SafeCol(c, key+"-sugs", 7).AlignItems(ui.Stretch).Children(func() {
				for ri, r := range rows {
					kit.SafeRow(c, fmt.Sprintf("%s-sr-%d", key, ri), 8).AlignItems(ui.Stretch).Children(func() {
						for _, ch := range r {
							item := ch
							b := kit.ActionControl(ui.ButtonBase(c.Key(key+"-chip-"+item.label)).Label(item.label).
								Grow(1).MinWidth(0).Height(32).Padding(0, 11).Gap(8).Radius(10).Clip().
								Background(kit.WithAlpha(pal.Bar, 0.56)).
								BorderWidth(1).BorderColor(kit.WithAlpha(ui.RGB(255, 255, 255), 0.07)).
								AlignItems(ui.Center).Justify(ui.Start))
							if b.Hovered() {
								b.Background(kit.WithAlpha(pal.Hover, 0.82))
							}
							b.Children(func() {
								kit.Symbol(c, item.icon, 13, item.tone)
								kit.FlexLine(c, item.label, kit.TextSpec{Font: kit.FontDisplay, Size: 12, Weight: 500, Color: pal.Ink})
							})
							if b.Clicked() {
								quickPrompt = item.prompt
							}
						}
					})
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
	// iOS / macOS Control Center Quick Launch card: crisp section header +
	// tight 3x3 Apple squircle icon tiles with legible 1-word captions.
	opts := surfOpts(rt)
	card := kit.SafeCol(c, key, 10).Padding(14, 14).Justify(ui.SpaceBetween).AlignItems(ui.Stretch)
	applySizing(card, n)
	kit.ApplyCardSurface(card, append([]string{"bento-card"}, n.Classes...), false, pal, sheet, opts)

	card.Children(func() {
		if title := strings.TrimSpace(sig.Interpolate(n.Title)); title != "" {
			kit.SafeRow(c, key+"-hdr", 6).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				kit.Eyebrow(c, title, pal.Muted)
				if b := strings.TrimSpace(sig.Interpolate(n.Badge)); b != "" {
					kit.Line(c, b, kit.TextSpec{Font: kit.FontMono, Size: 10, Weight: 700, Color: pal.Info})
				}
			})
		}
		kit.SafeCol(c, key+"-grid", 8).Grow(1).Justify(ui.SpaceBetween).AlignItems(ui.Stretch).Children(func() {
			for rowStart := 0; rowStart < len(n.Tiles); rowStart += 3 {
				end := rowStart + 3
				if end > len(n.Tiles) {
					end = len(n.Tiles)
				}
				slice := n.Tiles[rowStart:end]
				kit.SafeRow(c, fmt.Sprintf("%s-r%d", key, rowStart), 8).Grow(1).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
					for _, item := range slice {
						t := item
						fromCol, toCol := kit.AppIconColors(t.ID)
						cell := kit.ActionControl(ui.ButtonBase(c.Key(key+"-"+t.ID)).Label(t.Label).
							Grow(1).MinWidth(0).Padding(4, 2).Radius(12).Clip().
							Justify(ui.Center).AlignItems(ui.Center))
						if cell.Hovered() {
							cell.Background(kit.WithAlpha(pal.Hover, 0.52))
						}
						cell.Children(func() {
							kit.SafeCol(c, key+"-"+t.ID+"-in", 4).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
								ui.Row(c).Width(36).Height(36).Radius(10).Clip().
									Justify(ui.Center).AlignItems(ui.Center).
									LinearGradient(ui.LinearGradient{From: fromCol, To: toCol, Angle: 142, Oklab: true}).
									BorderWidth(1).BorderColor(kit.WithAlpha(ui.RGB(255, 255, 255), 0.22)).
									InsetShadow(0, 1, 1, 0, kit.WithAlpha(ui.RGB(255, 255, 255), 0.36)).
									Shadow(0, 4, 12, -3, kit.WithAlpha(toCol, 0.45)).
									Children(func() {
										kit.Symbol(c, t.Icon, 17, ui.RGB(255, 255, 255))
									})
								kit.Line(c, t.Label, kit.TextSpec{Font: kit.FontDisplay, Size: 10.5, Weight: 500, Color: pal.Ink})
							})
						})
						if cell.Clicked() && rt.OnAction != nil && t.Action != "" {
							rt.OnAction(sig.Interpolate(t.Action))
						}
					}
				})
			}
		})
	})
}

func renderActionTile(c *ui.Context, key string, n *Node, cardRad float32, sig *Signals, pal kit.Palette, sheet *style.Sheet, rt *Runtime) {
	opts := surfOpts(rt)
	tone := pal.ToneColor(n.Tone)
	title := sig.Interpolate(defaultStr(n.Title, key))
	sub := strings.TrimSpace(sig.Interpolate(n.Subtitle))
	badge := strings.TrimSpace(sig.Interpolate(n.Badge))
	isOn := strings.EqualFold(badge, "ON")

	btn := kit.ActionControl(ui.ButtonBase(c.Key(key)).Label(title).
		MinWidth(0).MinHeight(0).Radius(cardRad).Clip().Justify(ui.Center).AlignItems(ui.Center))
	applySizing(btn, n)

	if n.SolidBg {
		// Android 16 / iOS Control Center horizontal quick-action capsule:
		// frosted glass pill + 34px vibrant gradient squircle icon + crisp title & shortcut.
		kit.ApplyCardSurface(btn, append([]string{"bento-tile"}, n.Classes...), btn.Hovered(), pal, sheet, opts)
		btn.Padding(10, 14).Justify(ui.Start)
		btn.Children(func() {
			kit.SafeRow(c, key+"-in", 11).Fill().AlignItems(ui.Center).Children(func() {
				kit.IconBadge(c, n.Icon, 34, 16, tone)
				kit.SafeCol(c, key+"-txt", 1).Grow(1).AlignItems(ui.Start).Children(func() {
					kit.Line(c, title, kit.TextSpec{Font: kit.FontDisplay, Size: 13, Weight: 600, Color: pal.Ink})
					if sub != "" {
						kit.Line(c, sub, kit.TextSpec{Font: kit.FontMono, Size: 10.5, Weight: 500, Color: pal.Muted})
					}
				})
			})
		})
	} else {
		// iOS Control Center 2x2 module switch tile: glowing active tint + SF Symbol + On/Off caption.
		kit.ApplyCardSurface(btn, append([]string{"bento-tile"}, n.Classes...), btn.Hovered(), pal, sheet, opts)
		if isOn {
			btn.LinearGradient(ui.LinearGradient{
				From:  kit.WithAlpha(tone, 0.28),
				To:    kit.WithAlpha(pal.Surface, 0.72),
				Angle: 145,
				Oklab: true,
			})
			btn.BorderWidth(1).BorderColor(kit.WithAlpha(tone, 0.52))
		}
		btn.Children(func() {
			kit.SafeCol(c, key+"-in", 5).Fill().Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				if isOn {
					ui.Box(c.Key(key+"-led")).Absolute().Top(10).Right(10).Width(7).Height(7).
						Radius(kit.RadiusPill).Background(tone).
						Shadow(0, 0, 8, 1, kit.WithAlpha(tone, 0.85))
				}
				kit.Symbol(c, n.Icon, 24, tone)
				kit.SafeCol(c, key+"-lbls", 1).AlignItems(ui.Center).Children(func() {
					if title != "" {
						kit.Line(c, title, kit.TextSpec{Font: kit.FontDisplay, Size: 12, Weight: 600, Color: pal.Ink})
					}
					if badge != "" {
						bCol := pal.Muted
						if isOn {
							bCol = tone
						}
						kit.Line(c, badge, kit.TextSpec{Font: kit.FontDisplay, Size: 10, Weight: 600, Color: bCol})
					}
				})
			})
		})
	}

	if btn.Clicked() && rt.OnAction != nil && n.OnClick != "" {
		rt.OnAction(sig.Interpolate(n.OnClick))
	}
}

func renderBadgeCard(c *ui.Context, key string, n *Node, cardRad float32, sig *Signals, pal kit.Palette, sheet *style.Sheet, rt *Runtime) {
	opts := surfOpts(rt)
	btn := kit.ActionControl(ui.ButtonBase(c.Key(key)).Label(sig.Interpolate(defaultStr(n.Title, "Tasks"))).
		MinWidth(0).MinHeight(0).Radius(cardRad).Padding(10, 14).Gap(12).Clip().
		Justify(ui.Start).AlignItems(ui.Center))
	applySizing(btn, n)
	kit.ApplyCardSurface(btn, append([]string{"bento-badge"}, n.Classes...), btn.Hovered(), pal, sheet, opts)
	btn.Children(func() {
		kit.IconBadge(c, defaultStr(n.Icon, "check"), 34, 16, pal.ToneColor(n.Tone))
		kit.SafeCol(c, key+"-txt", 1).Grow(1).AlignItems(ui.Start).Children(func() {
			kit.Line(c, sig.Interpolate(defaultStr(n.Title, "Tasks")), kit.TextSpec{Font: kit.FontDisplay, Size: 13, Weight: 600, Color: pal.Ink})
			kit.MonoStat(c, sig.Interpolate(n.Value)+" done", 11, pal.Muted)
		})
	})
	if btn.Clicked() && rt.OnAction != nil && n.OnClick != "" {
		rt.OnAction(sig.Interpolate(n.OnClick))
	}
}

func renderLinkList(c *ui.Context, key string, n *Node, sig *Signals, pal kit.Palette, sheet *style.Sheet, rt *Runtime) {
	// Apple HIG workspace buffer list: tinted semantic icons with crisp monochrome
	// ink titles (zero rainbow text clutter) and clean inset summary header.
	card := kit.SafeCol(c, key, 10).Padding(14, 14).AlignItems(ui.Stretch)
	applySizing(card, n)
	kit.ApplyCardSurface(card, append([]string{"bento-card"}, n.Classes...), false, pal, sheet, surfOpts(rt))

	card.Children(func() {
		ui.Row(c).Height(52).Padding(0, 14).Radius(13).
			Background(kit.WithAlpha(pal.Bar, 0.70)).
			BorderWidth(1).BorderColor(kit.WithAlpha(ui.RGB(255, 255, 255), 0.08)).
			AlignItems(ui.Center).Gap(12).Clip().Children(func() {
			kit.IconBadge(c, defaultStr(n.Icon, "drive"), 32, 15, pal.Info)
			kit.SafeCol(c, key+"-sum", 1).Grow(1).AlignItems(ui.Start).Children(func() {
				kit.Headline(c, sig.Interpolate(n.Value), 15, pal.Ink)
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
					leftLbl, rightBadge := lbl, ""
					if parts := strings.SplitN(lbl, " · ", 2); len(parts) == 2 {
						leftLbl, rightBadge = parts[0], parts[1]
					}
					row := kit.ActionControl(ui.ButtonBase(c.Key(key+"-"+lk.ID)).Label(lbl).
						Height(32).Padding(0, 10).Gap(10).Radius(10).Clip().
						AlignItems(ui.Center).Justify(ui.Start))
					if row.Hovered() {
						row.Background(kit.WithAlpha(pal.Hover, 0.72))
					}
					row.Children(func() {
						kit.Symbol(c, lk.Icon, 15, col)
						kit.FlexLine(c, leftLbl, kit.TextSpec{Font: kit.FontDisplay, Size: 12.5, Weight: 500, Color: pal.Ink})
						if rightBadge != "" {
							kit.Line(c, rightBadge, kit.TextSpec{Font: kit.FontMono, Size: 10.5, Weight: 600, Color: pal.Muted})
						}
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
