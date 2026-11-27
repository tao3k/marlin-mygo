// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// KeymapScopeFilter filters entries shown in the Tier-0 Keymap Inspector modal.
type KeymapScopeFilter string

const (
	KeymapFilterAll    KeymapScopeFilter = "all"
	KeymapFilterNormal KeymapScopeFilter = "normal"
	KeymapFilterInsert KeymapScopeFilter = "insert"
	KeymapFilterGlobal KeymapScopeFilter = "global"
)

// KeymapInspectorController drives the interactive, searchable Tier-0 Keymap
// Inspector modal displaying all compiled SPC Normal, Emacs Insert, and Global
// C-x keybindings with 1-click command execution.
type KeymapInspectorController struct {
	mu     sync.Mutex
	query  string
	filter KeymapScopeFilter
}

func NewKeymapInspectorController() *KeymapInspectorController {
	return &KeymapInspectorController{
		filter: KeymapFilterAll,
	}
}

// SetQuery updates the inspector's live filter text.
func (ki *KeymapInspectorController) SetQuery(q string) {
	ki.mu.Lock()
	ki.query = q
	ki.mu.Unlock()
}

// SetFilter updates the active scope filter tab.
func (ki *KeymapInspectorController) SetFilter(f KeymapScopeFilter) {
	ki.mu.Lock()
	ki.filter = f
	ki.mu.Unlock()
}

// Render paints the Tier-0 Keymap Inspector inside a Modal.
func (ki *KeymapInspectorController) Render(
	c *ui.Context,
	dt DerivedTheme,
	trie *input.KeyTrie,
	commands []input.CommandEntry,
	onExecute func(commandName string),
	onClose func(),
) {
	if ki == nil {
		return
	}
	descByCmd := make(map[string]string, len(commands))
	for _, cmd := range commands {
		if cmd.Description != "" {
			descByCmd[cmd.Name] = cmd.Description
		}
	}

	ki.mu.Lock()
	filter := ki.filter
	if filter == "" {
		filter = KeymapFilterAll
		ki.filter = filter
	}
	ki.mu.Unlock()

	all := trie.AllBindings()
	q := strings.ToLower(strings.TrimSpace(ki.query))

	filtered := make([]input.Binding, 0, len(all))
	for _, b := range all {
		if !matchesKeymapScopeFilter(b.Scope, filter) {
			continue
		}
		if q != "" {
			hay := strings.ToLower(strings.Join(b.Sequence, " ") + " " + b.CommandName + " " + b.ActionKind + " " + descByCmd[b.CommandName])
			if !strings.Contains(hay, q) {
				continue
			}
		}
		filtered = append(filtered, b)
	}

	ui.Column(c).Width(620).Gap(12).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			AppleBadgePill(c, "TIER-0 KEYMAP", dt.Accent, dt)
			ui.Text(c, "Keymap Inspector & Shortcut Guide").FontSize(16).FontWeight(700).TextColor(toColor(dt.Ink))
			AppleBadgePill(c, fmt.Sprintf("%d bindings", len(filtered)), dt.Info, dt)
			ui.Spacer(c)
			if onClose != nil {
				if ApplePillButton(c, "km-close", "Close", BtnGlass, dt, svgIconXMark, true).Clicked() {
					onClose()
				}
			}
		})

		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if chosen := AppleSegmentedControl(c, "km-scope", []SegmentedItem{
				{ID: string(KeymapFilterAll), Label: "All"},
				{ID: string(KeymapFilterNormal), Label: "Normal (SPC)"},
				{ID: string(KeymapFilterInsert), Label: "Insert (Emacs)"},
				{ID: string(KeymapFilterGlobal), Label: "Global (C-x)"},
			}, string(filter), dt, true); chosen != "" {
				ki.SetFilter(KeymapScopeFilter(chosen))
			}
			ui.Spacer(c)
			ui.Text(c, "Press ESC to dismiss").FontSize(10.5).TextColor(toColor(dt.InkMuted))
		})

		AuthEmailInputRow(c, dt, "km-search-input", svgIconKey, &ki.query, "Filter by key chord (e.g. SPC w, C-x, C-k) or command...", true)

		if len(filtered) == 0 {
			ui.Column(c).Padding(20).Gap(4).AlignItems(ui.Center).Radius(12).
				Background(toColor(dt.SurfaceRecessed)).Children(func() {
				ui.Text(c, "No matching keybindings").FontSize(13.5).FontWeight(700).TextColor(toColor(dt.Ink))
				ui.Text(c, "Try searching for split, sidebar, theme, or C-k.").FontSize(11.5).TextColor(toColor(dt.InkMuted))
			})
			return
		}

		ui.Column(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
			limit := min(len(filtered), 14)
			for i := 0; i < limit; i++ {
				b := filtered[i]
				chordText := strings.Join(b.Sequence, " ")
				scopeLbl, scopeTone := keymapScopeBadge(b.Scope, dt)
				desc := descByCmd[b.CommandName]

				ui.Row(c.Key(fmt.Sprintf("km-row-%d", i))).Padding(7, 10).Gap(8).Radius(10).
					AlignItems(ui.Center).Background(toColor(dt.SurfaceRecessed)).
					Border(1, glassBorderColor(dt)).Children(func() {
					AppleBadgePill(c, chordText, dt.Accent, dt)
					AppleBadgePill(c, scopeLbl, scopeTone, dt)
					ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
						ui.Text(c, b.CommandName).FontSize(12).FontWeight(700).TextColor(toColor(dt.Ink))
						if desc != "" {
							ui.Text(c, desc).FontSize(10.5).TextColor(toColor(dt.InkMuted))
						}
					})
					if onExecute != nil {
						if ApplePillButton(c, fmt.Sprintf("km-run-%d", i), "Run", BtnTinted, dt, nil, true).Clicked() {
							if onClose != nil {
								onClose()
							}
							onExecute(b.CommandName)
						}
					}
				})
			}
		})
	})
}

func matchesKeymapScopeFilter(scope input.KeyScope, f KeymapScopeFilter) bool {
	switch f {
	case KeymapFilterNormal:
		return scope == input.ScopeNormal
	case KeymapFilterInsert:
		return scope == input.ScopeInsert
	case KeymapFilterGlobal:
		return scope == input.ScopeGlobal
	default:
		return true
	}
}

func keymapScopeBadge(scope input.KeyScope, dt DerivedTheme) (string, schema.RGB) {
	switch scope {
	case input.ScopeNormal:
		return "NORMAL", dt.Info
	case input.ScopeInsert:
		return "INSERT", dt.Success
	case input.ScopeGlobal:
		return "GLOBAL", dt.Warning
	default:
		return "KEY", dt.InkMuted
	}
}
