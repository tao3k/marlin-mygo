// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package mygoshell binds marlin-mygo's transport-agnostic shell, UI IR, and
// pluginhost contracts onto github.com/egoist/mygo's native GPU UI and
// isolated Untrusted WebView primitives.
package mygoshell

import (
	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func toColor(c schema.RGB) ui.Color {
	return ui.RGB(c.R, c.G, c.B)
}

// ApplyMarlinTheme projects Scheme's 8-color semantic palette onto a MyGo ui.Theme.
func ApplyMarlinTheme(c *ui.Context, mt schema.UITheme) *ui.Theme {
	th := ui.DarkTheme()
	th.Background = toColor(mt.Canvas)
	th.Surface = toColor(mt.Bar)
	th.Text = toColor(mt.Ink)
	th.TextMuted = toColor(mt.Muted)
	th.Accent = toColor(mt.Accent)
	th.Success = toColor(mt.Success)
	th.Warning = toColor(mt.Warning)
	th.Focus = toColor(mt.Accent)
	c.SetTheme(th)
	return th
}

// ToneColor maps a semantic UITone from Scheme UI IR onto the active Marlin palette.
func ToneColor(mt schema.UITheme, tone schema.UITone) ui.Color {
	switch tone {
	case schema.ToneCanvas:
		return toColor(mt.Canvas)
	case schema.ToneAccent:
		return toColor(mt.Accent)
	case schema.ToneInfo:
		return toColor(mt.Info)
	case schema.ToneSuccess:
		return toColor(mt.Success)
	case schema.ToneWarning:
		return toColor(mt.Warning)
	case schema.ToneMuted:
		return toColor(mt.Muted)
	default:
		return toColor(mt.Ink)
	}
}
