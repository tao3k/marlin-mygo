// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mapper

import (
	"fmt"
	"sort"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// ResolvedStatusItem is one width-admitted Scheme modeline item ready for painting.
type ResolvedStatusItem struct {
	ID    string
	Text  string
	Tone  schema.UITone
	Order int
}

// ProjectStatusItems evaluates Scheme's UIStatusItem width-aware priority rules
// (MinWidth, CompactBelow, KeepPriority) matching marlin-agent-tui's Rust modeline.
func ProjectStatusItems(items []schema.UIStatusItem, widthCols uint32, activeIDs map[string]bool) []ResolvedStatusItem {
	type indexed struct {
		idx  int
		item schema.UIStatusItem
		text string
		tone schema.UITone
	}
	admitted := make([]indexed, 0, len(items))
	for i, it := range items {
		if it.MinWidth > 0 && widthCols > 0 && widthCols < it.MinWidth {
			continue
		}
		txt := it.Label
		if it.CompactLabel != "" && it.CompactBelow > 0 && widthCols > 0 && widthCols < it.CompactBelow {
			txt = it.CompactLabel
		}
		tone := it.Tone
		if activeIDs[it.ID] && it.ActiveTone != "" {
			tone = it.ActiveTone
		}
		admitted = append(admitted, indexed{idx: i, item: it, text: txt, tone: tone})
	}
	if widthCols > 0 && len(admitted) > 1 {
		// Drop lowest KeepPriority items first if cumulative width exceeds budget.
		byPrio := append([]indexed(nil), admitted...)
		sort.SliceStable(byPrio, func(i, j int) bool {
			return byPrio[i].item.KeepPriority > byPrio[j].item.KeepPriority
		})
		used := uint32(0)
		keep := make(map[int]bool, len(byPrio))
		for _, entry := range byPrio {
			need := uint32(len(entry.text)) + 3
			if used == 0 || used+need <= widthCols {
				keep[entry.idx] = true
				used += need
			}
		}
		filtered := admitted[:0]
		for _, entry := range admitted {
			if keep[entry.idx] {
				filtered = append(filtered, entry)
			}
		}
		admitted = filtered
	}
	out := make([]ResolvedStatusItem, 0, len(admitted))
	for _, a := range admitted {
		out = append(out, ResolvedStatusItem{ID: a.item.ID, Text: a.text, Tone: a.tone, Order: a.idx})
	}
	return out
}

// MapStatusBar paints a Scheme-authored UIStatusItem bar onto mygo/ui using the injected Skin.
func MapStatusBar(c *ui.Context, items []schema.UIStatusItem, widthCols uint32, activeIDs map[string]bool, skin Skin) {
	resolved := ProjectStatusItems(items, widthCols, activeIDs)
	ui.Row(c.Key("mapper-status-bar")).Height(skin.BarHeight).Background(skin.Bar).
		Padding(0, 12).Gap(12).AlignItems(ui.Center).Children(func() {
		for _, it := range resolved {
			ui.Text(c.Key("st-"+it.ID), it.Text).Font(skin.Font).FontSize(skin.FontSizeMeta).
				FontWeight(600).TextColor(skin.ToneColor(it.Tone)).NoWrap()
		}
	})
}

// MapTransientPopup renders a Scheme-authored UITransient Hydra directly onto
// mygo/ui using the injected Skin, dispatching CommandName on head activation.
func MapTransientPopup(
	c *ui.Context,
	tr schema.UITransient,
	skin Skin,
	onCommand func(commandName string),
	onDismiss func(),
) {
	ui.Column(c.Key("mapper-transient-"+tr.ID)).Background(skin.Bar).
		BorderWidth(1, 0, 0, 0).BorderColor(skin.Border).
		Padding(10, 16).Gap(10).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Row(c).Padding(2, 8).Radius(skin.RadiusSmall).Background(skin.SurfaceElevated).Children(func() {
				ui.Text(c, tr.ID).Font(skin.Font).FontSize(skin.FontSizeMeta).FontWeight(700).TextColor(skin.Accent).NoWrap()
			})
			ui.Spacer(c)
			quitBtn := ui.ButtonBase(c.Key("tr-quit-"+tr.ID)).Label("C-g quit").
				Padding(2, 8).Radius(skin.RadiusSmall).AlignItems(ui.Center)
			if quitBtn.Hovered() {
				quitBtn.Background(skin.SurfaceHover)
			}
			quitBtn.Children(func() {
				ui.Text(c, "C-g quit").Font(skin.Font).FontSize(skin.FontSizeMeta).TextColor(skin.InkMuted).NoWrap()
			})
			if quitBtn.Clicked() && onDismiss != nil {
				onDismiss()
			}
		})

		// Render Scheme Hydra heads in balanced 2-column rows.
		for i := 0; i < len(tr.Heads); i += 2 {
			ui.Row(c.Key(fmt.Sprintf("tr-row-%s-%d", tr.ID, i))).Gap(16).AlignItems(ui.Center).Children(func() {
				for j := i; j < i+2 && j < len(tr.Heads); j++ {
					hd := tr.Heads[j]
					lbl := hd.Label
					if lbl == "" {
						lbl = hd.CommandName
					}
					keyCol := skin.Accent
					if !hd.Exit {
						keyCol = skin.Danger // repeatable Hydra red head
					}
					btn := ui.ButtonBase(c.Key("tr-head-"+tr.ID+"-"+hd.Key)).Label(lbl).
						Grow(1).Height(skin.RowHeight).Padding(0, 8).Gap(8).Radius(skin.RadiusSmall).
						AlignItems(ui.Center).Justify(ui.Start)
					if btn.Hovered() {
						btn.Background(skin.SurfaceHover)
					}
					btn.Children(func() {
						ui.Row(c).Width(38).Padding(2, 6).Radius(skin.RadiusSmall).Background(skin.SurfaceElevated).
							Justify(ui.Center).AlignItems(ui.Center).Children(func() {
							ui.Text(c, hd.Key).Font(skin.Font).FontSize(12).FontWeight(700).TextColor(keyCol).NoWrap()
						})
						ui.Text(c, "→").Font(skin.Font).FontSize(12).TextColor(skin.InkMuted).NoWrap()
						ui.Text(c, lbl).Font(skin.Font).FontSize(skin.FontSizeUI).TextColor(skin.Ink).Grow(1).NoWrap().MaxLines(1)
					})
					if btn.Clicked() {
						if hd.CommandName != "" && onCommand != nil {
							onCommand(hd.CommandName)
						}
						if hd.Exit && onDismiss != nil {
							onDismiss()
						}
					}
				}
			})
		}
	})
}
