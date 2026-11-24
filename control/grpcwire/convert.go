// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	commonv1 "github.com/tao3k/marlin-mygo/gen/marlin/common/v1"
	uiv1 "github.com/tao3k/marlin-mygo/gen/marlin/ui/v1"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TargetKindFromProto(k commonv1.TargetKind) schema.TargetKind {
	switch k {
	case commonv1.TargetKind_TARGET_KIND_DESKTOP_MACOS:
		return schema.TargetDesktopMacOS
	case commonv1.TargetKind_TARGET_KIND_DESKTOP_LINUX:
		return schema.TargetDesktopLinux
	case commonv1.TargetKind_TARGET_KIND_DESKTOP_WINDOWS:
		return schema.TargetDesktopWindows
	case commonv1.TargetKind_TARGET_KIND_MOBILE_IOS:
		return schema.TargetMobileIOS
	case commonv1.TargetKind_TARGET_KIND_MOBILE_ANDROID:
		return schema.TargetMobileAndroid
	case commonv1.TargetKind_TARGET_KIND_REMOTE_HEADLESS:
		return schema.TargetRemoteHeadless
	default:
		return ""
	}
}

func WorkbenchFromProto(pb *uiv1.UiWorkbenchSnapshot) (schema.UIWorkbenchSnapshot, error) {
	theme := schema.DefaultUITheme()
	if t := pb.GetTheme(); t != nil {
		theme = schema.UITheme{
			Canvas:  rgbFromProto(t.GetCanvas()),
			Ink:     rgbFromProto(t.GetInk()),
			Bar:     rgbFromProto(t.GetBar()),
			Accent:  rgbFromProto(t.GetAccent()),
			Info:    rgbFromProto(t.GetInfo()),
			Success: rgbFromProto(t.GetSuccess()),
			Warning: rgbFromProto(t.GetWarning()),
			Muted:   rgbFromProto(t.GetMuted()),
		}
	}
	var tree schema.UIElement
	if pb.GetLayout() != nil && pb.GetLayout().GetFrameTree() != nil {
		tree = uiElementFromProto(pb.GetLayout().GetFrameTree())
	} else {
		tree = schema.UIElement{Kind: schema.ElemRows}
	}
	if err := tree.ValidateBounds(schema.MaxUITreeNodes, schema.MaxUITreeDepth); err != nil {
		return schema.UIWorkbenchSnapshot{}, err
	}
	slots := make([]schema.SlotDeclaration, 0, len(pb.GetSlots()))
	for _, s := range pb.GetSlots() {
		slots = append(slots, schema.SlotDeclaration{
			SlotID:               schema.SlotID(s.GetSlotId()),
			Cardinality:          cardinalityFromProto(s.GetCardinality()),
			FallbackRenderer:     s.GetFallbackRenderer(),
			AllowUntrustedPlugin: s.GetAllowUntrustedPlugin(),
		})
	}
	return schema.UIWorkbenchSnapshot{
		Generation:   pb.GetGeneration(),
		ProfileName:  pb.GetProfileName(),
		ModelLabel:   pb.GetModelLabel(),
		SessionLabel: pb.GetSessionLabel(),
		FocusID:      schema.ComponentID(pb.GetFocusComponentId()),
		Theme:        theme,
		FrameTree:    tree,
		Slots:        slots,
	}, nil
}

func rgbFromProto(c *uiv1.UiRgb) schema.RGB {
	if c == nil {
		return schema.RGB{}
	}
	return schema.RGB{R: uint8(c.GetRed()), G: uint8(c.GetGreen()), B: uint8(c.GetBlue())}
}

func toneFromProto(t uiv1.UiTone) schema.UITone {
	switch t {
	case uiv1.UiTone_UI_TONE_CANVAS:
		return schema.ToneCanvas
	case uiv1.UiTone_UI_TONE_ACCENT:
		return schema.ToneAccent
	case uiv1.UiTone_UI_TONE_INFO:
		return schema.ToneInfo
	case uiv1.UiTone_UI_TONE_SUCCESS:
		return schema.ToneSuccess
	case uiv1.UiTone_UI_TONE_WARNING:
		return schema.ToneWarning
	case uiv1.UiTone_UI_TONE_MUTED:
		return schema.ToneMuted
	default:
		return schema.ToneNormal
	}
}

func cardinalityFromProto(c uiv1.SlotCardinality) schema.SlotCardinality {
	switch c {
	case uiv1.SlotCardinality_SLOT_CARDINALITY_LIST:
		return schema.SlotList
	case uiv1.SlotCardinality_SLOT_CARDINALITY_KEYED:
		return schema.SlotKeyed
	default:
		return schema.SlotSingle
	}
}

func uiElementFromProto(pb *uiv1.UiElement) schema.UIElement {
	if pb == nil {
		return schema.UIElement{Kind: schema.ElemRows}
	}
	switch n := pb.Node.(type) {
	case *uiv1.UiElement_Text:
		spans := make([]schema.UISpan, 0, len(n.Text.GetSpans()))
		for _, sp := range n.Text.GetSpans() {
			spans = append(spans, schema.UISpan{Text: sp.GetText(), Tone: toneFromProto(sp.GetTone())})
		}
		return schema.UIElement{Kind: schema.ElemText, Spans: spans}
	case *uiv1.UiElement_Slot:
		return schema.UIElement{Kind: schema.ElemSlot, SlotID: schema.SlotID(n.Slot.GetSlotId())}
	case *uiv1.UiElement_Rows:
		children := make([]schema.UIElement, 0, len(n.Rows.GetChildren()))
		for _, ch := range n.Rows.GetChildren() {
			children = append(children, uiElementFromProto(ch))
		}
		return schema.UIElement{Kind: schema.ElemRows, Children: children}
	case *uiv1.UiElement_Columns:
		children := make([]schema.UIElement, 0, len(n.Columns.GetChildren()))
		for _, ch := range n.Columns.GetChildren() {
			children = append(children, uiElementFromProto(ch))
		}
		return schema.UIElement{Kind: schema.ElemColumns, Children: children}
	case *uiv1.UiElement_Panel:
		return schema.UIElement{
			Kind:     schema.ElemPanel,
			Tone:     toneFromProto(n.Panel.GetBorderTone()),
			Children: []schema.UIElement{uiElementFromProto(n.Panel.GetChild())},
		}
	case *uiv1.UiElement_Region:
		return schema.UIElement{
			Kind:     schema.ElemRegion,
			ID:       schema.ComponentID(n.Region.GetId()),
			Children: []schema.UIElement{uiElementFromProto(n.Region.GetChild())},
		}
	default:
		return schema.UIElement{Kind: schema.ElemRows}
	}
}
