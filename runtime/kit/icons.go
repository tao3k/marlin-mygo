// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package kit

import (
	"sync"

	"github.com/egoist/mygo/plugins/sfsymbols"
	"github.com/egoist/mygo/ui"
)

type iconEntry struct {
	sf  string
	svg string
}

// Semantic icon catalog mapping Marlin icon IDs to native Apple SF Symbols
// (rendered by AppKit CoreGraphics at Retina scale on macOS) with crisp vector
// SVG fallbacks on Linux, Windows, and Web WASM.
var iconCatalog = map[string]iconEntry{
	"sparkles": {"sparkles", `<path d="M8 1.5l1.4 4.1 4.1 1.4-4.1 1.4L8 12.5 6.6 8.4 2.5 7l4.1-1.4z"/><path d="M13 10.5l.6 1.7 1.7.6-1.7.6-.6 1.7-.6-1.7-1.7-.6 1.7-.6z"/>`},
	"check":    {"checkmark.circle.fill", `<circle cx="8" cy="8" r="6"/><path d="M5.2 8.2l2 2 3.8-4.2"/>`},
	"chat":     {"bubble.left.and.bubble.right.fill", `<path d="M2.5 4.2A2.2 2.2 0 0 1 4.7 2h6.6a2.2 2.2 0 0 1 2.2 2.2v4.6a2.2 2.2 0 0 1-2.2 2.2H7L4 13.8V11h-.3a1.2 1.2 0 0 1-1.2-1.2z"/>`},
	"agents":   {"person.2.fill", `<circle cx="6" cy="5.5" r="2.3"/><path d="M2 13c.4-2.3 2-3.5 4-3.5s3.6 1.2 4 3.5"/><circle cx="11.5" cy="6" r="1.8"/><path d="M10.8 10c1.4.3 2.5 1.3 3 3"/>`},
	"search":   {"magnifyingglass", `<circle cx="7" cy="7" r="4.5"/><path d="M10.4 10.4L14 14"/>`},
	"command":  {"command", `<path d="M5.5 5.5v5M10.5 5.5v5M5.5 5.5h5M5.5 10.5h5"/><circle cx="4" cy="4" r="1.5"/><circle cx="12" cy="4" r="1.5"/><circle cx="4" cy="12" r="1.5"/><circle cx="12" cy="12" r="1.5"/>`},
	"sessions": {"tray.full.fill", `<rect x="2" y="3" width="12" height="10" rx="2"/><path d="M2 9h3.5l1 1.5h3l1-1.5H14"/>`},
	"host":     {"server.rack", `<rect x="2" y="2.5" width="12" height="4" rx="1.2"/><rect x="2" y="9.5" width="12" height="4" rx="1.2"/><circle cx="4.5" cy="4.5" r=".7" fill="currentColor"/><circle cx="4.5" cy="11.5" r=".7" fill="currentColor"/>`},
	"cpu":      {"cpu", `<rect x="4" y="4" width="8" height="8" rx="1.5"/><path d="M6 1.5V4M10 1.5V4M6 12v2.5M10 12v2.5M1.5 6H4M1.5 10H4M12 6h2.5M12 10h2.5"/>`},
	"cube":     {"cube.fill", `<path d="M8 1.8L13.5 5v6L8 14.2 2.5 11V5z"/><path d="M8 8v6.2M8 8l5.5-3M8 8L2.5 5"/>`},
	"settings": {"gearshape.fill", `<circle cx="8" cy="8" r="2.2"/><path d="M8 2v1.6M8 12.4V14M2 8h1.6M12.4 8H14M3.8 3.8l1.1 1.1M11.1 11.1l1.1 1.1M12.2 3.8l-1.1 1.1M4.9 11.1l-1.1 1.1"/>`},
	"zen":      {"moon.stars.fill", `<path d="M12.5 10.4A5.2 5.2 0 0 1 6 3.5a5.5 5.5 0 1 0 6.5 6.9z"/>`},
	"reload":   {"arrow.clockwise", `<path d="M13 8a5 5 0 1 1-1.5-3.5L13 6"/><path d="M13 2.8V6H9.8"/>`},
	"theme":    {"paintpalette.fill", `<circle cx="8" cy="8" r="6"/><circle cx="6" cy="6" r=".8" fill="currentColor"/><circle cx="9.8" cy="5.8" r=".8" fill="currentColor"/><circle cx="10.5" cy="9.2" r=".8" fill="currentColor"/>`},
	"glass":    {"drop.halffull", `<path d="M8 2C8 2 3.5 7 3.5 10a4.5 4.5 0 0 0 9 0C12.5 7 8 2 8 2z"/>`},
	"motion":   {"waveform.path.ecg", `<path d="M1.5 8h3l1.5-4 2.5 8 1.5-4h4.5"/>`},
	"bolt":     {"bolt.fill", `<path d="M9 1.8L3.8 8.8H8L7 14.2l5.2-7H8z"/>`},
	"clock":    {"clock.fill", `<circle cx="8" cy="8" r="6"/><path d="M8 4.5V8l2.5 1.6"/>`},
	"timer":    {"timer", `<circle cx="8" cy="9" r="5"/><path d="M8 6.5V9l1.8 1.2M6.2 2h3.6"/>`},
	"drive":    {"externaldrive.fill", `<rect x="2" y="3.5" width="12" height="9" rx="2"/><circle cx="5" cy="9.5" r=".9" fill="currentColor"/><path d="M8 9.5h3.5"/>`},
	"sidebar":  {"sidebar.left", `<rect x="2" y="2.5" width="12" height="11" rx="2"/><path d="M6 2.5v11"/>`},
	"dashboard": {"square.grid.2x2.fill", `<rect x="2.2" y="2.2" width="4.8" height="4.8" rx="1.2"/><rect x="9" y="2.2" width="4.8" height="4.8" rx="1.2"/><rect x="2.2" y="9" width="4.8" height="4.8" rx="1.2"/><rect x="9" y="9" width="4.8" height="4.8" rx="1.2"/>`},
	"terminal": {"terminal.fill", `<rect x="2" y="3" width="12" height="10" rx="2"/><path d="M4.5 6.5L6.8 8.5L4.5 10.5M8 10.5h3"/>`},
	"plus":     {"plus", `<path d="M8 3v10M3 8h10"/>`},
	"folder":   {"folder.fill", `<path d="M2 4.2A1.5 1.5 0 0 1 3.5 2.7H6l1.4 1.5h5.1A1.5 1.5 0 0 1 14 5.7v6.1a1.5 1.5 0 0 1-1.5 1.5h-9A1.5 1.5 0 0 1 2 11.8z"/>`},
	"send":     {"paperplane.fill", `<path d="M2 7.5l12-5-4.5 11.5-2-4.5z"/>`},
}

var (
	svgMu    sync.RWMutex
	svgCache = make(map[string]*ui.SVG, len(iconCatalog))
)

func fallbackSVG(name string) *ui.SVG {
	svgMu.RLock()
	if s, ok := svgCache[name]; ok {
		svgMu.RUnlock()
		return s
	}
	svgMu.RUnlock()

	ent, ok := iconCatalog[name]
	if !ok {
		ent = iconCatalog["sparkles"]
	}
	raw := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">` + ent.svg + `</svg>`
	parsed := ui.MustParseSVG([]byte(raw))

	svgMu.Lock()
	svgCache[name] = parsed
	svgMu.Unlock()
	return parsed
}

// Symbol renders a native Apple SF Symbol on macOS with automatic crisp vector
// SVG fallback on Linux, Windows, and Web WASM.
func Symbol(c *ui.Context, name string, size float32, col ui.Color) ui.Element {
	if size <= 0 {
		size = 16
	}
	sfName := name
	if ent, ok := iconCatalog[name]; ok {
		sfName = ent.sf
	}
	return sfsymbols.Symbol(c, sfName).
		Size(size).
		Color(col).
		Weight(sfsymbols.Semibold).
		Fallback(fallbackSVG(name)).
		Element
}

// IconBadge renders an iOS Control Center / macOS glowing gradient squircle
// badge hosting a crisp white SF Symbol inside.
func IconBadge(c *ui.Context, name string, boxSize, iconSize float32, tone ui.Color) ui.Element {
	if boxSize <= 0 {
		boxSize = 36
	}
	if iconSize <= 0 {
		iconSize = boxSize * 0.50
	}
	rad := boxSize * 0.28
	badge := ui.Row(c).Width(boxSize).Height(boxSize).Radius(rad).
		Justify(ui.Center).AlignItems(ui.Center).
		LinearGradient(ui.LinearGradient{
			From:  Lighten(tone, 0.24),
			To:    tone,
			Angle: 142,
			Oklab: true,
		}).
		BorderWidth(1).BorderColor(WithAlpha(ui.RGB(255, 255, 255), 0.22)).
		InsetShadow(0, 1, 1.5, 0, WithAlpha(ui.RGB(255, 255, 255), 0.40)).
		Shadow(0, 6, 16, -3, WithAlpha(tone, 0.48))
	badge.Children(func() {
		Symbol(c, name, iconSize, ui.RGB(255, 255, 255))
	})
	return badge
}

// AppIconColors returns curated saturated macOS / iOS multi-stop Oklab gradient
// pairs for rich colorful launcher dock icons.
func AppIconColors(key string) (from, to ui.Color) {
	switch key {
	case "t-chat", "chat":
		return ui.RGB(72, 198, 255), ui.RGB(37, 118, 235)
	case "t-room", "agents":
		return ui.RGB(181, 126, 255), ui.RGB(108, 72, 230)
	case "t-helm", "search":
		return ui.RGB(255, 164, 62), ui.RGB(234, 88, 32)
	case "t-mx", "command":
		return ui.RGB(74, 222, 152), ui.RGB(16, 155, 102)
	case "t-sess", "sessions":
		return ui.RGB(96, 165, 250), ui.RGB(59, 98, 224)
	case "t-targ", "host":
		return ui.RGB(251, 113, 133), ui.RGB(218, 48, 86)
	case "t-proc", "cpu":
		return ui.RGB(253, 200, 74), ui.RGB(217, 119, 16)
	case "t-mod", "cube":
		return ui.RGB(232, 121, 249), ui.RGB(147, 51, 234)
	default:
		return ui.RGB(120, 138, 162), ui.RGB(62, 74, 94)
	}
}

// AppIconTile renders a flagship macOS / iOS / EWW dock app icon button:
// saturated Oklab gradient circle or squircle, crisp top specular catchlight,
// colored ambient glow shadow, large centered white SF Symbol, focus isolation
// (ActionControl), tooltip label, and 120Hz spring hover pop.
func AppIconTile(
	c *ui.Context,
	key, iconName, label string,
	boxSize, iconSize float32,
	fromCol, toCol ui.Color,
	circular, motionOn bool,
) ui.Element {
	if boxSize <= 0 {
		boxSize = 44
	}
	if iconSize <= 0 {
		iconSize = boxSize * 0.48
	}
	rad := boxSize * 0.28
	if circular {
		rad = RadiusPill
	}
	btn := ActionControl(ui.ButtonBase(c.Key(key)).Label(label).
		Width(boxSize).Height(boxSize).Radius(rad).Clip().
		Justify(ui.Center).AlignItems(ui.Center).
		LinearGradient(ui.LinearGradient{
			From:  fromCol,
			To:    toCol,
			Angle: 142,
			Oklab: true,
		}).
		BorderWidth(1).BorderColor(WithAlpha(ui.RGB(255, 255, 255), 0.22)).
		InsetShadow(0, 1, 1.5, 0, WithAlpha(ui.RGB(255, 255, 255), 0.42)).
		Shadow(0, 7, 18, -4, WithAlpha(toCol, 0.52)))
	if label != "" {
		btn.Tooltip(label)
	}
	if motionOn {
		target := float32(1.0)
		if btn.Hovered() {
			target = 1.12
		}
		sc := btn.Spring("app-pop", target)
		if sc != 1.0 {
			btn.Scale(sc, sc)
		}
	} else if btn.Hovered() {
		btn.Scale(1.06, 1.06)
	}
	btn.Children(func() {
		Symbol(c, iconName, iconSize, ui.RGB(255, 255, 255))
	})
	return btn
}
