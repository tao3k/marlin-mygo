// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"maps"

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

// WorkbenchFromProto losslessly projects marlin.ui.v1.UiWorkbenchSnapshot
// (Scheme-authored frame, layout tree, 13 IR primitives, status items,
// extension buffers, commands, Meow keymap, Hydra transients, and slots)
// into schema.UIWorkbenchSnapshot after validating IR bounds.
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
	var roleRegions map[string]string
	if lay := pb.GetLayout(); lay != nil {
		if lay.GetFrameTree() != nil {
			tree = uiElementFromProto(lay.GetFrameTree())
		} else {
			tree = schema.UIElement{Kind: schema.ElemRows}
		}
		if len(lay.GetRoleRegions()) > 0 {
			roleRegions = make(map[string]string, len(lay.GetRoleRegions()))
			maps.Copy(roleRegions, lay.GetRoleRegions())
		}
	} else {
		tree = schema.UIElement{Kind: schema.ElemRows}
	}
	if err := tree.ValidateBounds(schema.MaxUITreeNodes, schema.MaxUITreeDepth); err != nil {
		return schema.UIWorkbenchSnapshot{}, err
	}

	var frame schema.UIFrame
	if fr := pb.GetFrame(); fr != nil {
		wins := make([]schema.UIWindow, 0, len(fr.GetWindows()))
		for _, w := range fr.GetWindows() {
			wins = append(wins, schema.UIWindow{ID: w.GetId(), BufferID: w.GetBufferId()})
		}
		frame = schema.UIFrame{
			ID:             fr.GetId(),
			Windows:        wins,
			SelectedWindow: fr.GetSelectedWindow(),
			Minibuffer:     fr.GetMinibuffer(),
			BufferIDs:      append([]string(nil), fr.GetBufferIds()...),
		}
	}

	var statusItems []schema.UIStatusItem
	var extensions []schema.UIExtensionRegion
	var regionCmds []schema.UICommandSpec
	for _, r := range pb.GetRegions() {
		switch k := r.Kind.(type) {
		case *uiv1.UiRegion_Status:
			for _, it := range k.Status.GetItems() {
				statusItems = append(statusItems, schema.UIStatusItem{
					ID:           it.GetId(),
					Label:        it.GetLabel(),
					CompactLabel: it.GetCompactLabel(),
					Tone:         toneFromProto(it.GetTone()),
					ActiveTone:   toneFromProto(it.GetActiveTone()),
					MinWidth:     it.GetMinWidth(),
					CompactBelow: it.GetCompactBelow(),
					KeepPriority: it.GetKeepPriority(),
				})
			}
		case *uiv1.UiRegion_Extension:
			ext := k.Extension
			content := uiElementFromProto(ext.GetContent())
			if err := content.ValidateBounds(schema.MaxUITreeNodes, schema.MaxUITreeDepth); err != nil {
				return schema.UIWorkbenchSnapshot{}, err
			}
			extensions = append(extensions, schema.UIExtensionRegion{
				ID:        ext.GetId(),
				Label:     ext.GetLabel(),
				Focusable: ext.GetFocusable(),
				Content:   content,
			})
		case *uiv1.UiRegion_Commands:
			for _, c := range k.Commands.GetCommands() {
				regionCmds = append(regionCmds, commandFromProto(c))
			}
		}
	}

	commands := make([]schema.UICommandSpec, 0, len(pb.GetCommands())+len(regionCmds))
	seenCmd := make(map[string]struct{}, len(pb.GetCommands()))
	for _, c := range pb.GetCommands() {
		spec := commandFromProto(c)
		if spec.Name != "" {
			seenCmd[spec.Name] = struct{}{}
		}
		commands = append(commands, spec)
	}
	for _, spec := range regionCmds {
		if _, dup := seenCmd[spec.Name]; !dup {
			seenCmd[spec.Name] = struct{}{}
			commands = append(commands, spec)
		}
	}

	keymap := make([]schema.UIKeyBindingSpec, 0, len(pb.GetKeymap()))
	for _, kb := range pb.GetKeymap() {
		keymap = append(keymap, schema.UIKeyBindingSpec{
			Scope:       keyScopeFromProto(kb.GetScope()),
			Sequence:    append([]string(nil), kb.GetSequence()...),
			ActionKind:  kb.GetActionKind(),
			CommandName: kb.GetCommandName(),
		})
	}

	transients := make([]schema.UITransient, 0, len(pb.GetTransients()))
	for _, tr := range pb.GetTransients() {
		heads := make([]schema.UITransientHead, 0, len(tr.GetHeads()))
		for _, h := range tr.GetHeads() {
			heads = append(heads, schema.UITransientHead{
				Key:         h.GetKey(),
				CommandName: h.GetCommandName(),
				Label:       h.GetLabel(),
				Exit:        h.GetExit(),
			})
		}
		transients = append(transients, schema.UITransient{
			ID:          tr.GetId(),
			Hint:        uiElementFromProto(tr.GetHint()),
			WarningHint: uiElementFromProto(tr.GetWarningHint()),
			Foreign:     transientForeignFromProto(tr.GetForeign()),
			Heads:       heads,
		})
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
		Frame:        frame,
		RoleRegions:  roleRegions,
		FrameTree:    tree,
		StatusItems:  statusItems,
		Extensions:   extensions,
		Commands:     commands,
		Keymap:       keymap,
		Transients:   transients,
		Slots:        slots,
	}, nil
}

func commandFromProto(c *uiv1.UiCommand) schema.UICommandSpec {
	args := "none"
	switch c.GetArguments() {
	case uiv1.UiCommandArguments_UI_COMMAND_ARGUMENTS_REQUIRED:
		args = "required"
	case uiv1.UiCommandArguments_UI_COMMAND_ARGUMENTS_OPTIONAL:
		args = "optional"
	}
	return schema.UICommandSpec{
		Name:        c.GetName(),
		Description: c.GetDescription(),
		Arguments:   args,
		Capability:  c.GetCapability(),
		FixedValue:  c.GetFixedValue(),
	}
}

func keyScopeFromProto(s uiv1.UiKeyScope) schema.UIKeyScope {
	switch s {
	case uiv1.UiKeyScope_UI_KEY_SCOPE_INSERT:
		return schema.KeyScopeInsert
	case uiv1.UiKeyScope_UI_KEY_SCOPE_NORMAL:
		return schema.KeyScopeNormal
	default:
		return schema.KeyScopeGlobal
	}
}

func transientForeignFromProto(f uiv1.UiTransientForeign) schema.UITransientForeign {
	switch f {
	case uiv1.UiTransientForeign_UI_TRANSIENT_FOREIGN_WARN:
		return schema.TransientForeignWarn
	case uiv1.UiTransientForeign_UI_TRANSIENT_FOREIGN_RUN:
		return schema.TransientForeignRun
	default:
		return schema.TransientForeignExit
	}
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

func bgFromProto(bg *uiv1.UiBackground) schema.UIBackground {
	if bg == nil {
		return schema.UIBackground{}
	}
	kind := schema.BgNone
	switch bg.GetKind() {
	case uiv1.UiBackgroundKind_UI_BACKGROUND_KIND_CANVAS:
		kind = schema.BgCanvas
	case uiv1.UiBackgroundKind_UI_BACKGROUND_KIND_BAR:
		kind = schema.BgBar
	case uiv1.UiBackgroundKind_UI_BACKGROUND_KIND_TONE:
		kind = schema.BgTone
	}
	return schema.UIBackground{Kind: kind, Tone: toneFromProto(bg.GetTone())}
}

func slotSourceFromProto(s uiv1.UiSlotSource) schema.UISlotSource {
	switch s {
	case uiv1.UiSlotSource_UI_SLOT_SOURCE_SELECTED_BUFFER:
		return schema.SlotSourceSelectedBuffer
	case uiv1.UiSlotSource_UI_SLOT_SOURCE_FOCUSED_EXTENSION:
		return schema.SlotSourceFocusedExtension
	case uiv1.UiSlotSource_UI_SLOT_SOURCE_RUNTIME_POLICY:
		return schema.SlotSourceRuntimePolicy
	case uiv1.UiSlotSource_UI_SLOT_SOURCE_TRANSIENT_HINT:
		return schema.SlotSourceTransientHint
	case uiv1.UiSlotSource_UI_SLOT_SOURCE_NAMED_PLUGIN_SLOT:
		return schema.SlotSourceNamedPluginSlot
	default:
		return ""
	}
}

func listSourceFromProto(k uiv1.UiListSourceKind) schema.UIListSourceKind {
	switch k {
	case uiv1.UiListSourceKind_UI_LIST_SOURCE_KIND_CONVERSATION:
		return schema.ListSourceConversation
	case uiv1.UiListSourceKind_UI_LIST_SOURCE_KIND_MESSAGES:
		return schema.ListSourceMessages
	case uiv1.UiListSourceKind_UI_LIST_SOURCE_KIND_WORKSPACE:
		return schema.ListSourceWorkspace
	case uiv1.UiListSourceKind_UI_LIST_SOURCE_KIND_REVIEW:
		return schema.ListSourceReview
	case uiv1.UiListSourceKind_UI_LIST_SOURCE_KIND_CANDIDATES:
		return schema.ListSourceCandidates
	case uiv1.UiListSourceKind_UI_LIST_SOURCE_KIND_EXTENSIONS:
		return schema.ListSourceExtensions
	case uiv1.UiListSourceKind_UI_LIST_SOURCE_KIND_INLINE_ROWS:
		return schema.ListSourceInlineRows
	default:
		return ""
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
		return schema.UIElement{
			Kind:       schema.ElemSlot,
			SlotSource: slotSourceFromProto(n.Slot.GetSource()),
			SlotID:     schema.SlotID(n.Slot.GetSlotId()),
		}

	case *uiv1.UiElement_Panel:
		var children []schema.UIElement
		if n.Panel.GetChild() != nil {
			children = append(children, uiElementFromProto(n.Panel.GetChild()))
		}
		title := ""
		if n.Panel.GetTitle() != nil {
			title = extractPlainText(uiElementFromProto(n.Panel.GetTitle()))
		}
		return schema.UIElement{
			Kind:     schema.ElemPanel,
			Title:    title,
			Tone:     toneFromProto(n.Panel.GetBorderTone()),
			Children: children,
		}

	case *uiv1.UiElement_Template:
		tspans := make([]schema.UITemplateSpan, 0, len(n.Template.GetSpans()))
		for _, sp := range n.Template.GetSpans() {
			ts := schema.UITemplateSpan{Tone: toneFromProto(sp.GetTone()), Bold: sp.GetBold()}
			switch v := sp.Value.(type) {
			case *uiv1.UiTemplateSpan_Literal:
				ts.Literal = v.Literal
			case *uiv1.UiTemplateSpan_BindingId:
				ts.BindingID = v.BindingId
			case *uiv1.UiTemplateSpan_Column:
				ts.ColumnID = v.Column.GetId()
				ts.Width = sizeCellsFromProto(v.Column.GetWidth())
			case *uiv1.UiTemplateSpan_Tail:
				ts.TailID = v.Tail.GetId()
				ts.Width = sizeCellsFromProto(v.Tail.GetWidth())
			}
			tspans = append(tspans, ts)
		}
		return schema.UIElement{
			Kind:          schema.ElemTemplate,
			Background:    bgFromProto(n.Template.GetBackground()),
			TemplateSpans: tspans,
		}

	case *uiv1.UiElement_Prompt:
		var children []schema.UIElement
		if n.Prompt.GetTemplate() != nil {
			children = append(children, uiElementFromProto(n.Prompt.GetTemplate()))
		}
		return schema.UIElement{
			Kind:     schema.ElemPrompt,
			FieldID:  n.Prompt.GetFieldId(),
			Children: children,
		}

	case *uiv1.UiElement_Bar:
		segs := make([]schema.UIBarSegment, 0, len(n.Bar.GetSegments()))
		for _, sg := range n.Bar.GetSegments() {
			side := schema.BarSideLeft
			if sg.GetSide() == uiv1.UiBarSide_UI_BAR_SIDE_RIGHT {
				side = schema.BarSideRight
			}
			var ch *schema.UIElement
			if sg.GetChild() != nil {
				c := uiElementFromProto(sg.GetChild())
				ch = &c
			}
			segs = append(segs, schema.UIBarSegment{
				Side:        side,
				Priority:    sg.GetPriority(),
				MinWidth:    sg.GetMinWidth(),
				ConditionID: sg.GetConditionId(),
				Child:       ch,
				CommandName: sg.GetCommandName(),
			})
		}
		return schema.UIElement{
			Kind:        schema.ElemBar,
			Background:  bgFromProto(n.Bar.GetBackground()),
			BarSegments: segs,
		}

	case *uiv1.UiElement_List:
		rows := make([]schema.UIListRowValue, 0, len(n.List.GetInlineRows()))
		for _, r := range n.List.GetInlineRows() {
			fields := make(map[string]string, len(r.GetFields()))
			maps.Copy(fields, r.GetFields())
			rows = append(rows, schema.UIListRowValue{ID: r.GetId(), Fields: fields})
		}
		var children []schema.UIElement
		if n.List.GetChild() != nil {
			children = append(children, uiElementFromProto(n.List.GetChild()))
		}
		return schema.UIElement{
			Kind:       schema.ElemList,
			ID:         schema.ComponentID(n.List.GetId()),
			ListSource: listSourceFromProto(n.List.GetSourceKind()),
			InlineRows: rows,
			Children:   children,
		}

	case *uiv1.UiElement_Input:
		var children []schema.UIElement
		if n.Input.GetPrefix() != nil {
			children = append(children, uiElementFromProto(n.Input.GetPrefix()))
		}
		if tr := n.Input.GetTrailing(); tr != nil && tr.GetView() != nil {
			children = append(children, uiElementFromProto(tr.GetView()))
		}
		return schema.UIElement{
			Kind:       schema.ElemInput,
			Title:      n.Input.GetTitleId(),
			ActiveID:   n.Input.GetActiveId(),
			Bordered:   n.Input.GetBordered(),
			Background: bgFromProto(n.Input.GetBackground()),
			Children:   children,
		}

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

	case *uiv1.UiElement_Choice:
		children := make([]schema.UIElement, 0, len(n.Choice.GetCases())+1)
		for _, cs := range n.Choice.GetCases() {
			ch := uiElementFromProto(cs.GetView())
			if ch.ID == "" {
				ch.ID = schema.ComponentID(cs.GetFactId())
			}
			children = append(children, ch)
		}
		if n.Choice.GetFallback() != nil {
			children = append(children, uiElementFromProto(n.Choice.GetFallback()))
		}
		return schema.UIElement{Kind: schema.ElemChoice, Children: children}

	case *uiv1.UiElement_Sized:
		var children []schema.UIElement
		if n.Sized.GetChild() != nil {
			children = append(children, uiElementFromProto(n.Sized.GetChild()))
		}
		cells, flex := sizeConstraintsFromProto(n.Sized.GetSize())
		return schema.UIElement{
			Kind:     schema.ElemSized,
			Cells:    cells,
			Flex:     flex,
			Children: children,
		}

	case *uiv1.UiElement_Region:
		var children []schema.UIElement
		if n.Region.GetChild() != nil {
			children = append(children, uiElementFromProto(n.Region.GetChild()))
		}
		var st *schema.UIScrollTarget
		if sc := n.Region.GetScrollTarget(); sc != nil {
			st = &schema.UIScrollTarget{ID: sc.GetId(), Step: sc.GetStep()}
		}
		return schema.UIElement{
			Kind:         schema.ElemRegion,
			ID:           schema.ComponentID(n.Region.GetId()),
			ScrollTarget: st,
			Children:     children,
		}

	default:
		return schema.UIElement{Kind: schema.ElemRows}
	}
}

func sizeCellsFromProto(sz *uiv1.UiSize) uint32 {
	if sz == nil {
		return 0
	}
	if c, ok := sz.Expr.(*uiv1.UiSize_Cells); ok {
		return c.Cells
	}
	return 0
}

func sizeConstraintsFromProto(sz *uiv1.UiSize) (cells, flex uint16) {
	if sz == nil {
		return 0, 0
	}
	switch v := sz.Expr.(type) {
	case *uiv1.UiSize_Cells:
		return uint16(min(v.Cells, 65535)), 0
	case *uiv1.UiSize_Flex:
		return 0, uint16(min(v.Flex, 65535))
	default:
		return 0, 0
	}
}

func extractPlainText(el schema.UIElement) string {
	if len(el.Spans) > 0 {
		out := ""
		for _, sp := range el.Spans {
			out += sp.Text
		}
		return out
	}
	if len(el.TemplateSpans) > 0 {
		out := ""
		for _, sp := range el.TemplateSpans {
			if sp.Literal != "" {
				out += sp.Literal
			} else if sp.BindingID != "" {
				out += "{{" + sp.BindingID + "}}"
			}
		}
		return out
	}
	return el.Title
}
