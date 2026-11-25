// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"

	"github.com/tao3k/marlin-mygo/engine/shell"
)

// DefaultOfficialMarketplaceThemes returns the built-in curated Official UI
// Marketplace theme packs projected into the Marketplace Themes stream.
func DefaultOfficialMarketplaceThemes() []MarketplaceThemeItem {
	presets := BuiltinThemes()
	out := make([]MarketplaceThemeItem, 0, len(presets))
	for i, p := range presets {
		out = append(out, MarketplaceThemeItem{
			ThemeID:     "tao3k.theme-" + p.ID,
			Name:        p.Name,
			Appearance:  string(p.Appearance),
			PublisherID: "tao3k.official",
			Summary:     fmt.Sprintf("Official 8-color semantic palette (%s) projected into 34 --marlin-* tokens.", p.Appearance),
			TrustTier:   "official",
			Base:        p.Base,
			Active:      i == 0,
		})
	}
	return out
}

// DefaultOfficialMarketplaceLayouts returns the built-in curated Official UI
// Marketplace Emacs LayoutSpec packs projected into the Marketplace Layouts stream.
func DefaultOfficialMarketplaceLayouts() []MarketplaceLayoutItem {
	return []MarketplaceLayoutItem{
		{
			LayoutID:    "tao3k.layout-focus",
			Name:        "Deep Focus",
			Perspective: "focus",
			PublisherID: "tao3k.official",
			Summary:     "Clean 2-pane Emacs workspace pairing primary plugin with resident GPU dock.",
			TrustTier:   "official",
			Active:      true,
			Spec: shell.LayoutSpec{
				Perspective: "focus",
				Root: &shell.LayoutNodeSpec{
					Axis:  shell.SplitRow,
					Ratio: 0.72,
					First: &shell.LayoutNodeSpec{
						BufferID: "slot:workbench.main",
						Title:    "Workspace Panel",
						Kind:     "webview",
					},
					Second: &shell.LayoutNodeSpec{
						BufferID: "slot:workbench.inspector",
						Title:    "Focus Clock",
						Kind:     "wasm-gpu",
					},
				},
			},
		},
		{
			LayoutID:    "tao3k.layout-cockpit",
			Name:        "AI Engineering Cockpit",
			Perspective: "cockpit",
			PublisherID: "tao3k.official",
			Summary:     "3-pane Emacs layout combining Workspace Panel, AI Transcript, and WASI Focus Clock.",
			TrustTier:   "official",
			Spec: shell.LayoutSpec{
				Perspective: "cockpit",
				Root: &shell.LayoutNodeSpec{
					Axis:  shell.SplitRow,
					Ratio: 0.62,
					First: &shell.LayoutNodeSpec{
						BufferID: "slot:workbench.main",
						Title:    "Workspace Panel",
						Kind:     "webview",
					},
					Second: &shell.LayoutNodeSpec{
						Axis:  shell.SplitColumn,
						Ratio: 0.66,
						First: &shell.LayoutNodeSpec{
							BufferID: "builtin:transcript",
							Title:    "AI Transcript",
							Kind:     "native-gpu",
						},
						Second: &shell.LayoutNodeSpec{
							BufferID: "slot:workbench.inspector",
							Title:    "Focus Clock",
							Kind:     "wasm-gpu",
						},
					},
				},
			},
		},
		{
			LayoutID:    "tao3k.layout-review-triage",
			Name:        "Review & Triage Studio",
			Perspective: "review",
			PublisherID: "tao3k.official",
			Summary:     "3-pane review studio combining Workspace Panel, Projection Inspector, and AI Transcript.",
			TrustTier:   "official",
			Spec: shell.LayoutSpec{
				Perspective: "review",
				Root: &shell.LayoutNodeSpec{
					Axis:  shell.SplitRow,
					Ratio: 0.56,
					First: &shell.LayoutNodeSpec{
						BufferID: "slot:workbench.main",
						Title:    "Workspace Panel",
						Kind:     "webview",
					},
					Second: &shell.LayoutNodeSpec{
						Axis:  shell.SplitColumn,
						Ratio: 0.52,
						First: &shell.LayoutNodeSpec{
							BufferID: "builtin:projection",
							Title:    "Projection Inspector",
							Kind:     "native-gpu",
						},
						Second: &shell.LayoutNodeSpec{
							BufferID: "builtin:transcript",
							Title:    "AI Transcript",
							Kind:     "native-gpu",
						},
					},
				},
			},
		},
		{
			LayoutID:    "tao3k.layout-dev-git-github",
			Name:        "Git + GitHub Composable Studio",
			Perspective: "dev-git-github",
			PublisherID: "tao3k.official",
			Summary:     "4-pane composable studio combining Primary (Git), Secondary (GitHub), AI Transcript, and WASI Focus Clock.",
			TrustTier:   "official",
			Spec: shell.LayoutSpec{
				Perspective: "dev-git-github",
				Root: &shell.LayoutNodeSpec{
					Axis:  shell.SplitRow,
					Ratio: 0.58,
					First: &shell.LayoutNodeSpec{
						Axis:  shell.SplitColumn,
						Ratio: 0.50,
						First: &shell.LayoutNodeSpec{
							BufferID: "slot:workbench.main",
							Title:    "Git Workbench",
							Kind:     "webview",
						},
						Second: &shell.LayoutNodeSpec{
							BufferID: "slot:workbench.secondary",
							Title:    "GitHub Workbench",
							Kind:     "webview",
						},
					},
					Second: &shell.LayoutNodeSpec{
						Axis:  shell.SplitColumn,
						Ratio: 0.65,
						First: &shell.LayoutNodeSpec{
							BufferID: "builtin:transcript",
							Title:    "AI Transcript",
							Kind:     "native-gpu",
						},
						Second: &shell.LayoutNodeSpec{
							BufferID: "slot:workbench.inspector",
							Title:    "Focus Clock",
							Kind:     "wasm-gpu",
						},
					},
				},
			},
		},
	}
}
