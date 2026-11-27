// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package schema

import "fmt"

// RGB represents an 8-bit sRGB color admitted from Scheme UiThemesScope.
type RGB struct {
	R uint8 `json:"r"`
	G uint8 `json:"g"`
	B uint8 `json:"b"`
}

func (c RGB) Hex() string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// UITheme mirrors crates/marlin-agent-ui/src/model/frame.rs::UiTheme.
type UITheme struct {
	Canvas  RGB `json:"canvas"`
	Ink     RGB `json:"ink"`
	Bar     RGB `json:"bar"`
	Accent  RGB `json:"accent"`
	Info    RGB `json:"info"`
	Success RGB `json:"success"`
	Warning RGB `json:"warning"`
	Muted   RGB `json:"muted"`
}

// DefaultUITheme matches Marlin's default dark semantic palette.
func DefaultUITheme() UITheme {
	return UITheme{
		Canvas:  RGB{40, 44, 52},
		Ink:     RGB{187, 194, 207},
		Bar:     RGB{28, 31, 36},
		Accent:  RGB{198, 120, 221},
		Info:    RGB{81, 175, 239},
		Success: RGB{152, 190, 101},
		Warning: RGB{236, 190, 123},
		Muted:   RGB{115, 121, 126},
	}
}

// CSSVariables projects the semantic palette into :root custom properties
// injected by the MyGo host into Untrusted plugin WebViews.
func (t UITheme) CSSVariables() map[string]string {
	return map[string]string{
		"--marlin-canvas":  t.Canvas.Hex(),
		"--marlin-ink":     t.Ink.Hex(),
		"--marlin-bar":     t.Bar.Hex(),
		"--marlin-accent":  t.Accent.Hex(),
		"--marlin-info":    t.Info.Hex(),
		"--marlin-success": t.Success.Hex(),
		"--marlin-warning": t.Warning.Hex(),
		"--marlin-muted":   t.Muted.Hex(),
	}
}

// UITone mirrors marlin.ui.v1.UiTone.
type UITone string

const (
	ToneNormal  UITone = "normal"
	ToneCanvas  UITone = "canvas"
	ToneAccent  UITone = "accent"
	ToneInfo    UITone = "info"
	ToneSuccess UITone = "success"
	ToneWarning UITone = "warning"
	ToneMuted   UITone = "muted"
)

// UISpan is a styled run of text inside a host-neutral UiElement.
type UISpan struct {
	Text string `json:"text"`
	Tone UITone `json:"tone"`
}

// UIElementKind discriminates the 13 primitives from crates/marlin-agent-ui.
type UIElementKind string

const (
	ElemText     UIElementKind = "text"
	ElemSlot     UIElementKind = "slot"
	ElemPanel    UIElementKind = "panel"
	ElemTemplate UIElementKind = "template"
	ElemPrompt   UIElementKind = "prompt"
	ElemBar      UIElementKind = "bar"
	ElemList     UIElementKind = "list"
	ElemInput    UIElementKind = "input"
	ElemRows     UIElementKind = "rows"
	ElemColumns  UIElementKind = "columns"
	ElemChoice   UIElementKind = "choice"
	ElemSized    UIElementKind = "sized"
	ElemRegion   UIElementKind = "region"
	ElemButton   UIElementKind = "button"
)

// UIElement is the bounded declarative UI IR shared by Scheme workbenches,
// MyGo native layout mapping, and wazero WASM-IR plugins.
type UIElement struct {
	Kind       UIElementKind `json:"kind"`
	ID         ComponentID   `json:"id,omitempty"`
	Classes    []string      `json:"classes,omitempty"`
	SlotID     SlotID        `json:"slotId,omitempty"`
	SlotSource UISlotSource  `json:"slotSource,omitempty"`
	Title      string        `json:"title,omitempty"`
	Tone       UITone        `json:"tone,omitempty"`
	Background UIBackground  `json:"background,omitempty"`
	Flex       uint16        `json:"flex,omitempty"`
	Cells      uint16        `json:"cells,omitempty"`
	Action     string        `json:"action,omitempty"`
	ActionArgs string        `json:"actionArgs,omitempty"`
	FieldID    string        `json:"fieldId,omitempty"`
	ActiveID   string        `json:"activeId,omitempty"`
	Bordered   bool          `json:"bordered,omitempty"`
	Spans      []UISpan      `json:"spans,omitempty"`
	// Rich marlin.ui.v1 fields populated losslessly by grpcwire.WorkbenchFromProto.
	TemplateSpans []UITemplateSpan `json:"templateSpans,omitempty"`
	BarSegments   []UIBarSegment   `json:"barSegments,omitempty"`
	ListSource    UIListSourceKind `json:"listSource,omitempty"`
	InlineRows    []UIListRowValue `json:"inlineRows,omitempty"`
	ScrollTarget  *UIScrollTarget  `json:"scrollTarget,omitempty"`
	Children      []UIElement      `json:"children,omitempty"`
}

// ValidateBounds enforces MAR-UI-R03 bounded tree depth and node count before
// admitting any Scheme or WASM-IR plugin tree into the MyGo renderer.
func (e *UIElement) ValidateBounds(maxNodes, maxDepth int) error {
	if maxNodes <= 0 {
		maxNodes = MaxUITreeNodes
	}
	if maxDepth <= 0 {
		maxDepth = MaxUITreeDepth
	}
	count := 0
	var walk func(node *UIElement, depth int) error
	walk = func(node *UIElement, depth int) error {
		if depth > maxDepth {
			return fmt.Errorf("%w: depth %d exceeds %d", ErrUITreeBounds, depth, maxDepth)
		}
		count++
		if count > maxNodes {
			return fmt.Errorf("%w: node count exceeds %d", ErrUITreeBounds, maxNodes)
		}
		if node.Kind == "" {
			return fmt.Errorf("%w: empty element kind", ErrUITreeBounds)
		}
		for i := range node.Children {
			if err := walk(&node.Children[i], depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(e, 1)
}

// SlotDeclaration advertises an extensible slot in the Scheme workbench.
type SlotDeclaration struct {
	SlotID               SlotID          `json:"slotId"`
	Cardinality          SlotCardinality `json:"cardinality"`
	FallbackRenderer     string          `json:"fallbackRenderer"`
	AllowUntrustedPlugin bool            `json:"allowUntrustedPlugin"`
}

// UIWorkbenchSnapshot is an admitted generation of the Scheme-composed workbench,
// carrying 100% of marlin.ui.v1.UiWorkbenchSnapshot losslessly.
type UIWorkbenchSnapshot struct {
	Generation   uint64              `json:"generation,string"`
	ProfileName  string              `json:"profileName"`
	ModelLabel   string              `json:"modelLabel"`
	SessionLabel string              `json:"sessionLabel"`
	FocusID      ComponentID         `json:"focusId"`
	Theme        UITheme             `json:"theme"`
	Frame        UIFrame             `json:"frame,omitempty"`
	RoleRegions  map[string]string   `json:"roleRegions,omitempty"`
	FrameTree    UIElement           `json:"frameTree"`
	StatusItems  []UIStatusItem      `json:"statusItems,omitempty"`
	Extensions   []UIExtensionRegion `json:"extensions,omitempty"`
	Commands     []UICommandSpec     `json:"commands,omitempty"`
	Keymap       []UIKeyBindingSpec  `json:"keymap,omitempty"`
	Transients   []UITransient       `json:"transients,omitempty"`
	Slots        []SlotDeclaration   `json:"slots"`
}
