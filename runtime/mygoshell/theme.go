// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package mygoshell binds marlin-mygo's transport-agnostic shell, UI IR, and
// pluginhost contracts onto github.com/egoist/mygo's native GPU UI and
// isolated Untrusted WebView primitives.
package mygoshell

import (
	"fmt"
	"math"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// ThemeAppearance indicates whether a derived theme targets dark or light surfaces.
type ThemeAppearance string

const (
	AppearanceDark  ThemeAppearance = "dark"
	AppearanceLight ThemeAppearance = "light"
)

// ThemePreset defines a curated, Emacs-deftheme-style semantic palette.
type ThemePreset struct {
	ID         string
	Name       string
	Appearance ThemeAppearance
	Base       schema.UITheme
}

// DerivedTheme is the complete 34-token semantic face & surface projection
// derived deterministically from any 8-color schema.UITheme. It serves as the
// single source of truth for BOTH MyGo's native GPU chrome (including Emacs
// active/inactive window borders & modelines) and every Untrusted WebView panel.
type DerivedTheme struct {
	ID         string
	Name       string
	Appearance ThemeAppearance
	Base       schema.UITheme

	// Surface elevation hierarchy
	Canvas          schema.RGB
	Surface         schema.RGB
	SurfaceElevated schema.RGB
	SurfaceRecessed schema.RGB
	SurfaceHover    schema.RGB
	SurfaceActive   schema.RGB

	// Emacs-style window frame & modeline faces
	ActiveWindowBorder   schema.RGB
	InactiveWindowBorder schema.RGB
	ModelineActiveBg     schema.RGB
	ModelineInactiveBg   schema.RGB
	ModelineActiveInk    schema.RGB
	ModelineInactiveInk  schema.RGB

	// Hairline border & focus hierarchy
	BorderSubtle schema.RGB
	Border       schema.RGB
	BorderStrong schema.RGB

	// Typography & ink hierarchy (WCAG contrast-guarded)
	Ink          schema.RGB
	InkSecondary schema.RGB
	InkMuted     schema.RGB
	InkOnAccent  schema.RGB

	// Semantic roles
	Accent      schema.RGB
	AccentHover schema.RGB
	Info        schema.RGB
	Success     schema.RGB
	Warning     schema.RGB
	Danger      schema.RGB
}

func toColor(c schema.RGB) ui.Color {
	return ui.RGB(c.R, c.G, c.B)
}

func hexRGB(c schema.RGB) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

func rgbaCSS(c schema.RGB, alpha float64) string {
	return fmt.Sprintf("rgba(%d, %d, %d, %.2f)", c.R, c.G, c.B, alpha)
}

// mixRGB linearly blends a toward b by t in [0, 1].
func mixRGB(a, b schema.RGB, t float64) schema.RGB {
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	lerp := func(x, y uint8) uint8 {
		v := math.Round(float64(x)*(1-t) + float64(y)*t)
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v)
	}
	return schema.RGB{
		R: lerp(a.R, b.R),
		G: lerp(a.G, b.G),
		B: lerp(a.B, b.B),
	}
}

func relativeLuminance(c schema.RGB) float64 {
	ch := func(v uint8) float64 {
		s := float64(v) / 255.0
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
}

// ContrastRatioRGB computes WCAG 2.1 contrast ratio between two RGB colors.
func ContrastRatioRGB(a, b schema.RGB) float64 {
	la := relativeLuminance(a)
	lb := relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// StudioDarkPalette returns the default Midnight Graphite + Indigo palette.
func StudioDarkPalette() schema.UITheme {
	return schema.UITheme{
		Canvas:  schema.RGB{R: 15, G: 17, B: 23},    // #0f1117
		Bar:     schema.RGB{R: 22, G: 26, B: 35},    // #161a23
		Ink:     schema.RGB{R: 235, G: 239, B: 247}, // #ebeff7
		Accent:  schema.RGB{R: 99, G: 82, B: 232},   // #6352e8 rich indigo (5.48:1 contrast with white)
		Info:    schema.RGB{R: 96, G: 165, B: 250},  // #60a5fa
		Success: schema.RGB{R: 74, G: 222, B: 128},  // #4ade80
		Warning: schema.RGB{R: 251, G: 191, B: 36},  // #fbbf24
		Muted:   schema.RGB{R: 139, G: 148, B: 166}, // #8b94a6
	}
}

// StudioLightPalette returns the Paper-Snow + Cobalt-Indigo light theme.
func StudioLightPalette() schema.UITheme {
	return schema.UITheme{
		Canvas:  schema.RGB{R: 246, G: 247, B: 250}, // #f6f7fa
		Bar:     schema.RGB{R: 255, G: 255, B: 255}, // #ffffff
		Ink:     schema.RGB{R: 20, G: 24, B: 34},    // #141822
		Accent:  schema.RGB{R: 88, G: 72, B: 220},   // #5848dc
		Info:    schema.RGB{R: 37, G: 99, B: 235},   // #2563eb
		Success: schema.RGB{R: 22, G: 163, B: 74},   // #16a34a
		Warning: schema.RGB{R: 217, G: 119, B: 6},   // #d97706
		Muted:   schema.RGB{R: 96, G: 106, B: 124},  // #606a7c
	}
}

// DoomOnePalette returns the iconic Emacs Doom One palette.
func DoomOnePalette() schema.UITheme {
	return schema.UITheme{
		Canvas:  schema.RGB{R: 33, G: 36, B: 43},    // #21242b
		Bar:     schema.RGB{R: 40, G: 44, B: 52},    // #282c34
		Ink:     schema.RGB{R: 220, G: 223, B: 228}, // #dcdfe4
		Accent:  schema.RGB{R: 81, G: 175, B: 239},  // #51afef
		Info:    schema.RGB{R: 198, G: 120, B: 221}, // #c678dd
		Success: schema.RGB{R: 152, G: 190, B: 101}, // #98be65
		Warning: schema.RGB{R: 236, G: 190, B: 123}, // #ecbe7b
		Muted:   schema.RGB{R: 138, G: 147, B: 162}, // #8a93a2
	}
}

// TokyoNightPalette returns the Tokyo Night Storm palette.
func TokyoNightPalette() schema.UITheme {
	return schema.UITheme{
		Canvas:  schema.RGB{R: 22, G: 22, B: 30},    // #16161e
		Bar:     schema.RGB{R: 26, G: 27, B: 38},    // #1a1b26
		Ink:     schema.RGB{R: 192, G: 202, B: 245}, // #c0caf5
		Accent:  schema.RGB{R: 122, G: 162, B: 247}, // #7aa2f7
		Info:    schema.RGB{R: 125, G: 207, B: 255}, // #7dcfff
		Success: schema.RGB{R: 158, G: 206, B: 106}, // #9ece6a
		Warning: schema.RGB{R: 224, G: 175, B: 104}, // #e0af68
		Muted:   schema.RGB{R: 132, G: 141, B: 173}, // #848dad
	}
}

// CatppuccinMochaPalette returns the Catppuccin Mocha palette.
func CatppuccinMochaPalette() schema.UITheme {
	return schema.UITheme{
		Canvas:  schema.RGB{R: 24, G: 24, B: 37},    // #181825 (mantle)
		Bar:     schema.RGB{R: 30, G: 30, B: 46},    // #1e1e2e (base)
		Ink:     schema.RGB{R: 205, G: 214, B: 244}, // #cdd6f4 (text)
		Accent:  schema.RGB{R: 203, G: 166, B: 247}, // #cba6f7 (mauve)
		Info:    schema.RGB{R: 137, G: 180, B: 250}, // #89b4fa (blue)
		Success: schema.RGB{R: 166, G: 227, B: 161}, // #a6e3a1 (green)
		Warning: schema.RGB{R: 249, G: 226, B: 175}, // #f9e2af (yellow)
		Muted:   schema.RGB{R: 147, G: 153, B: 178}, // #9399b2 (overlay2)
	}
}

// BuiltinThemes returns the ordered list of curated built-in studio themes.
func BuiltinThemes() []ThemePreset {
	return []ThemePreset{
		{ID: "midnight", Name: "Midnight", Appearance: AppearanceDark, Base: StudioDarkPalette()},
		{ID: "paper", Name: "Paper Light", Appearance: AppearanceLight, Base: StudioLightPalette()},
		{ID: "doom-one", Name: "Doom One", Appearance: AppearanceDark, Base: DoomOnePalette()},
		{ID: "tokyo-night", Name: "Tokyo Night", Appearance: AppearanceDark, Base: TokyoNightPalette()},
		{ID: "catppuccin", Name: "Catppuccin", Appearance: AppearanceDark, Base: CatppuccinMochaPalette()},
	}
}

// ThemeByID resolves a built-in ThemePreset by ID (defaulting to Midnight).
func ThemeByID(id string) ThemePreset {
	norm := strings.ToLower(strings.TrimSpace(id))
	presets := BuiltinThemes()
	for _, p := range presets {
		if p.ID == norm {
			return p
		}
	}
	return presets[0]
}

// NextThemePreset cycles to the next curated theme preset in deterministic order.
func NextThemePreset(currentID string) ThemePreset {
	presets := BuiltinThemes()
	norm := strings.ToLower(strings.TrimSpace(currentID))
	for i, p := range presets {
		if p.ID == norm {
			return presets[(i+1)%len(presets)]
		}
	}
	return presets[0]
}

// DeriveTheme computes the full 34-token semantic face & surface projection
// from any 8-color schema.UITheme with automatic dark/light detection and
// WCAG-checked InkOnAccent derivation.
func DeriveTheme(id, name string, mt schema.UITheme) DerivedTheme {
	if id == "" {
		id = "custom"
	}
	if name == "" {
		name = "Custom"
	}
	isLight := relativeLuminance(mt.Canvas) > 0.42
	app := AppearanceDark
	if isLight {
		app = AppearanceLight
	}

	white := schema.RGB{R: 255, G: 255, B: 255}
	black := schema.RGB{R: 10, G: 12, B: 18}

	var surfaceElev, surfaceRecessed, surfaceHover, surfaceActive schema.RGB
	var borderSubtle, border, borderStrong schema.RGB
	var activeBorder, inactiveBorder schema.RGB
	var mlActiveBg, mlInactiveBg schema.RGB
	var accentHover, danger schema.RGB

	if isLight {
		surfaceElev = white
		surfaceRecessed = mixRGB(mt.Canvas, black, 0.04)
		surfaceHover = mixRGB(mt.Bar, black, 0.05)
		surfaceActive = mixRGB(mt.Bar, mt.Accent, 0.10)
		borderSubtle = mixRGB(mt.Canvas, black, 0.07)
		border = mixRGB(mt.Canvas, black, 0.12)
		borderStrong = mixRGB(mt.Canvas, black, 0.22)
		activeBorder = mixRGB(mt.Accent, mt.Canvas, 0.25)
		inactiveBorder = border
		mlActiveBg = mixRGB(mt.Bar, mt.Accent, 0.08)
		mlInactiveBg = surfaceRecessed
		accentHover = mixRGB(mt.Accent, black, 0.12)
		danger = schema.RGB{R: 220, G: 38, B: 38}
	} else {
		surfaceElev = mixRGB(mt.Bar, white, 0.05)
		surfaceRecessed = mixRGB(mt.Canvas, black, 0.28)
		surfaceHover = mixRGB(mt.Bar, white, 0.08)
		surfaceActive = mixRGB(mt.Bar, mt.Accent, 0.16)
		borderSubtle = mixRGB(mt.Bar, white, 0.06)
		border = mixRGB(mt.Bar, white, 0.11)
		borderStrong = mixRGB(mt.Bar, white, 0.20)
		activeBorder = mixRGB(mt.Accent, white, 0.10)
		inactiveBorder = borderSubtle
		mlActiveBg = mixRGB(mt.Bar, mt.Accent, 0.11)
		mlInactiveBg = mixRGB(mt.Canvas, mt.Bar, 0.45)
		accentHover = mixRGB(mt.Accent, white, 0.12)
		danger = schema.RGB{R: 248, G: 113, B: 113}
	}

	inkOnAccent := white
	if ContrastRatioRGB(white, mt.Accent) < 4.5 && ContrastRatioRGB(black, mt.Accent) > ContrastRatioRGB(white, mt.Accent) {
		inkOnAccent = black
	}

	return DerivedTheme{
		ID:                   id,
		Name:                 name,
		Appearance:           app,
		Base:                 mt,
		Canvas:               mt.Canvas,
		Surface:              mt.Bar,
		SurfaceElevated:      surfaceElev,
		SurfaceRecessed:      surfaceRecessed,
		SurfaceHover:         surfaceHover,
		SurfaceActive:        surfaceActive,
		ActiveWindowBorder:   activeBorder,
		InactiveWindowBorder: inactiveBorder,
		ModelineActiveBg:     mlActiveBg,
		ModelineInactiveBg:   mlInactiveBg,
		ModelineActiveInk:    mt.Ink,
		ModelineInactiveInk:  mt.Muted,
		BorderSubtle:         borderSubtle,
		Border:               border,
		BorderStrong:         borderStrong,
		Ink:                  mt.Ink,
		InkSecondary:         mixRGB(mt.Ink, mt.Muted, 0.35),
		InkMuted:             mt.Muted,
		InkOnAccent:          inkOnAccent,
		Accent:               mt.Accent,
		AccentHover:          accentHover,
		Info:                 mt.Info,
		Success:              mt.Success,
		Warning:              mt.Warning,
		Danger:               danger,
	}
}

// DerivedThemeTokensMap serializes all 34 derived tokens into CSS custom
// properties (`--marlin-*`) delivered to every Untrusted WebView plugin.
func DerivedThemeTokensMap(dt DerivedTheme) map[string]string {
	softAlpha := 0.14
	shadowSm := "0 1px 3px rgba(0, 0, 0, 0.28)"
	shadowMd := "0 6px 20px rgba(0, 0, 0, 0.32)"
	if dt.Appearance == AppearanceLight {
		softAlpha = 0.11
		shadowSm = "0 1px 2px rgba(15, 23, 42, 0.06)"
		shadowMd = "0 6px 18px rgba(15, 23, 42, 0.08)"
	}

	return map[string]string{
		"--marlin-theme-id":               dt.ID,
		"--marlin-appearance":             string(dt.Appearance),
		"--marlin-canvas":                 hexRGB(dt.Canvas),
		"--marlin-bar":                    hexRGB(dt.Surface),
		"--marlin-surface":                hexRGB(dt.Surface),
		"--marlin-surface-elevated":       hexRGB(dt.SurfaceElevated),
		"--marlin-surface-recessed":       hexRGB(dt.SurfaceRecessed),
		"--marlin-surface-hover":          hexRGB(dt.SurfaceHover),
		"--marlin-surface-active":         hexRGB(dt.SurfaceActive),
		"--marlin-border-subtle":          hexRGB(dt.BorderSubtle),
		"--marlin-border":                 hexRGB(dt.Border),
		"--marlin-border-strong":          hexRGB(dt.BorderStrong),
		"--marlin-focus-ring":             rgbaCSS(dt.Accent, 0.28),
		"--marlin-ink":                    hexRGB(dt.Ink),
		"--marlin-ink-secondary":          hexRGB(dt.InkSecondary),
		"--marlin-muted":                  hexRGB(dt.InkMuted),
		"--marlin-ink-on-accent":          hexRGB(dt.InkOnAccent),
		"--marlin-window-border-active":   hexRGB(dt.ActiveWindowBorder),
		"--marlin-window-border-inactive": hexRGB(dt.InactiveWindowBorder),
		"--marlin-modeline-active-bg":     hexRGB(dt.ModelineActiveBg),
		"--marlin-modeline-inactive-bg":   hexRGB(dt.ModelineInactiveBg),
		"--marlin-accent":                 hexRGB(dt.Accent),
		"--marlin-accent-hover":           hexRGB(dt.AccentHover),
		"--marlin-accent-soft":            rgbaCSS(dt.Accent, softAlpha),
		"--marlin-info":                   hexRGB(dt.Info),
		"--marlin-info-soft":              rgbaCSS(dt.Info, softAlpha),
		"--marlin-success":                hexRGB(dt.Success),
		"--marlin-success-soft":           rgbaCSS(dt.Success, softAlpha),
		"--marlin-warning":                hexRGB(dt.Warning),
		"--marlin-warning-soft":           rgbaCSS(dt.Warning, softAlpha),
		"--marlin-danger":                 hexRGB(dt.Danger),
		"--marlin-danger-soft":            rgbaCSS(dt.Danger, softAlpha),
		"--marlin-shadow-sm":              shadowSm,
		"--marlin-shadow-md":              shadowMd,
	}
}

// ThemeTokensMap derives and serializes all 34 CSS custom properties from any schema.UITheme.
func ThemeTokensMap(mt schema.UITheme) map[string]string {
	return DerivedThemeTokensMap(DeriveTheme("runtime", "Runtime", mt))
}

// ApplyMarlinTheme projects Marlin's derived semantic tokens onto a cohesive
// MyGo ui.Theme so GPU controls and WebView plugins share identical surfaces.
func ApplyMarlinTheme(c *ui.Context, mt schema.UITheme) *ui.Theme {
	dt := DeriveTheme("runtime", "Runtime", mt)
	var th *ui.Theme
	if dt.Appearance == AppearanceLight {
		th = ui.LightTheme()
	} else {
		th = ui.DarkTheme()
	}
	th.Background = toColor(dt.Canvas)
	th.Surface = toColor(dt.Surface)
	th.SurfaceHover = toColor(dt.SurfaceHover)
	th.SurfacePressed = toColor(dt.SurfaceActive)
	th.Border = toColor(dt.Border)
	th.InactiveAccent = toColor(dt.BorderStrong)
	th.Text = toColor(dt.Ink)
	th.TextMuted = toColor(dt.InkMuted)
	th.Accent = toColor(dt.Accent)
	th.AccentHover = toColor(dt.AccentHover)
	th.AccentPressed = toColor(dt.ActiveWindowBorder)
	th.AccentText = toColor(dt.InkOnAccent)
	th.Success = toColor(dt.Success)
	th.Warning = toColor(dt.Warning)
	th.Danger = toColor(dt.Danger)
	th.Focus = toColor(dt.Accent)
	th.Radius = 7
	th.Spacing = 4
	th.FontSize = 13
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
