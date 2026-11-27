// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package style implements Marlin MyGo's pure-Go (CGO_ENABLED=0, Web WASM
// compatible), governed CSS-subset stylesheet engine inspired by elkowar/eww.
//
// Unlike unconstrained browser CSS, every stylesheet is admitted fail-closed
// against a typed Contract (allowlisted semantic classes, allowlisted visual
// properties, bounded bytes/rules/variables, zero @import traversal, zero
// external url() fetches). Admitted sheets compile into O(1) cached rules for
// zero-allocation 120Hz GPU rendering.
package style

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// Default governance bounds preventing unbounded theme payloads.
const (
	DefaultMaxBytes     = 64 * 1024
	DefaultMaxRules     = 256
	DefaultMaxVariables = 64
)

// PseudoState is a bitmask of interactive widget states matching CSS pseudo-classes.
type PseudoState uint8

const (
	StateNone     PseudoState = 0
	StateHover    PseudoState = 1 << iota // :hover
	StateActive                           // :active
	StateSelected                         // :selected
	StateFocus                            // :focus
)

// Diagnostic reports an exact line-numbered admission error when a stylesheet
// violates syntax or governance Contract rules.
type Diagnostic struct {
	Line    int
	Message string
}

func (d Diagnostic) Error() string {
	if d.Line > 0 {
		return fmt.Sprintf("style:%d: %s", d.Line, d.Message)
	}
	return "style: " + d.Message
}

// Contract defines the fail-closed admission boundary for user and built-in
// Marlin UI stylesheets. Empty AllowedClasses admits any valid identifier class;
// non-empty AllowedClasses restricts rules strictly to declared semantic slots.
type Contract struct {
	MaxBytes          int
	MaxRules          int
	MaxVariables      int
	AllowedClasses    map[string]bool
	AllowedProperties map[string]bool
}

// DefaultAllowedProperties enumerates the bounded pure-Go GPU visual properties
// admitted across Desktop, Mobile, and Web WASM surfaces.
func DefaultAllowedProperties() map[string]bool {
	return map[string]bool{
		"background":       true,
		"background-color": true,
		"color":            true,
		"border-radius":    true,
		"border-width":     true,
		"border-color":     true,
		"padding":          true,
		"padding-x":        true,
		"padding-y":        true,
		"gap":              true,
		"width":            true,
		"height":           true,
		"min-width":        true,
		"min-height":       true,
		"font-family":      true,
		"font-size":        true,
		"font-weight":      true,
	}
}

// DefaultContract returns a fail-closed Contract with bounded limits and the
// standard visual property allowlist. Downstream apps (such as marlin-ui)
// supply their exact AllowedClasses map declared in OpenSysML v2.
func DefaultContract(allowedClasses ...string) Contract {
	var cls map[string]bool
	if len(allowedClasses) > 0 {
		cls = make(map[string]bool, len(allowedClasses))
		for _, c := range allowedClasses {
			cls[c] = true
		}
	}
	return Contract{
		MaxBytes:          DefaultMaxBytes,
		MaxRules:          DefaultMaxRules,
		MaxVariables:      DefaultMaxVariables,
		AllowedClasses:    cls,
		AllowedProperties: DefaultAllowedProperties(),
	}
}

// ComputedStyle holds resolved, ready-to-paint visual properties for one widget.
type ComputedStyle struct {
	HasBackground bool
	Background    ui.Color

	HasColor bool
	Color    ui.Color

	HasBorderRadius bool
	BorderRadius    float32

	HasBorderWidth bool
	BorderWidth    float32

	HasBorderColor bool
	BorderColor    ui.Color

	HasPaddingX bool
	PaddingX    float32

	HasPaddingY bool
	PaddingY    float32

	HasGap bool
	Gap    float32

	HasWidth bool
	Width    float32

	HasHeight bool
	Height    float32

	HasMinWidth bool
	MinWidth    float32

	HasMinHeight bool
	MinHeight    float32

	HasFontFamily bool
	FontFamily    string

	HasFontSize bool
	FontSize    float32

	HasFontWeight bool
	FontWeight    int
}

// ApplyToElement applies all set box/layout/surface properties of ComputedStyle
// directly onto a MyGo ui.Element.
func (st ComputedStyle) ApplyToElement(el ui.Element) {
	if st.HasBackground {
		el.Background(st.Background)
	}
	if st.HasBorderRadius {
		el.Radius(st.BorderRadius)
	}
	if st.HasBorderWidth && st.HasBorderColor {
		el.Border(st.BorderWidth, st.BorderColor)
	} else if st.HasBorderColor {
		el.BorderColor(st.BorderColor)
	}
	if st.HasPaddingX || st.HasPaddingY {
		el.Padding(st.PaddingY, st.PaddingX)
	}
	if st.HasGap {
		el.Gap(st.Gap)
	}
	if st.HasWidth {
		el.Width(st.Width)
	}
	if st.HasHeight {
		el.Height(st.Height)
	}
	if st.HasMinWidth {
		el.MinWidth(st.MinWidth)
	}
	if st.HasMinHeight {
		el.MinHeight(st.MinHeight)
	}
}

// ApplyToText applies resolved typography and foreground color onto a MyGo text element.
func (st ComputedStyle) ApplyToText(el ui.Element) {
	if st.HasFontFamily {
		el.Font(st.FontFamily)
	}
	if st.HasFontSize {
		el.FontSize(st.FontSize)
	}
	if st.HasFontWeight {
		el.FontWeight(st.FontWeight)
	}
	if st.HasColor {
		el.TextColor(st.Color)
	}
}

type selector struct {
	classes     []string
	pseudo      PseudoState
	specificity int
}

type declaration struct {
	prop  string
	value string
	line  int
}

type rule struct {
	sel   selector
	decls []declaration
	order int
}
