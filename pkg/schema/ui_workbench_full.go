// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package schema

// UIBackgroundKind mirrors marlin.ui.v1.UiBackgroundKind.
type UIBackgroundKind string

const (
	BgNone   UIBackgroundKind = "none"
	BgCanvas UIBackgroundKind = "canvas"
	BgBar    UIBackgroundKind = "bar"
	BgTone   UIBackgroundKind = "tone"
)

// UIBackground mirrors marlin.ui.v1.UiBackground.
type UIBackground struct {
	Kind UIBackgroundKind `json:"kind,omitempty"`
	Tone UITone           `json:"tone,omitempty"`
}

// UISlotSource mirrors marlin.ui.v1.UiSlotSource.
type UISlotSource string

const (
	SlotSourceSelectedBuffer   UISlotSource = "selected_buffer"
	SlotSourceFocusedExtension UISlotSource = "focused_extension"
	SlotSourceRuntimePolicy    UISlotSource = "runtime_policy"
	SlotSourceTransientHint    UISlotSource = "transient_hint"
	SlotSourceNamedPluginSlot  UISlotSource = "named_plugin_slot"
)

// UIListSourceKind mirrors marlin.ui.v1.UiListSourceKind.
type UIListSourceKind string

const (
	ListSourceConversation UIListSourceKind = "conversation"
	ListSourceMessages     UIListSourceKind = "messages"
	ListSourceWorkspace    UIListSourceKind = "workspace"
	ListSourceReview       UIListSourceKind = "review"
	ListSourceCandidates   UIListSourceKind = "candidates"
	ListSourceExtensions   UIListSourceKind = "extensions"
	ListSourceInlineRows   UIListSourceKind = "inline_rows"
)

// UIBarSide mirrors marlin.ui.v1.UiBarSide.
type UIBarSide string

const (
	BarSideLeft  UIBarSide = "left"
	BarSideRight UIBarSide = "right"
)

// UITemplateSpan mirrors marlin.ui.v1.UiTemplateSpan.
type UITemplateSpan struct {
	Literal   string `json:"literal,omitempty"`
	BindingID string `json:"bindingId,omitempty"`
	ColumnID  string `json:"columnId,omitempty"`
	TailID    string `json:"tailId,omitempty"`
	Width     uint32 `json:"width,omitempty"`
	Tone      UITone `json:"tone,omitempty"`
	Bold      bool   `json:"bold,omitempty"`
}

// UIBarSegment mirrors marlin.ui.v1.UiBarSegment (Doom Modeline / tab-bar segment).
type UIBarSegment struct {
	Side        UIBarSide  `json:"side"`
	Priority    uint32     `json:"priority,omitempty"`
	MinWidth    uint32     `json:"minWidth,omitempty"`
	ConditionID string     `json:"conditionId,omitempty"`
	Child       *UIElement `json:"child,omitempty"`
	CommandName string     `json:"commandName,omitempty"`
}

// UIListRowValue mirrors marlin.ui.v1.UiListRowValue.
type UIListRowValue struct {
	ID     string            `json:"id,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
}

// UIScrollTarget mirrors marlin.ui.v1.UiScrollTarget.
type UIScrollTarget struct {
	ID   string `json:"id"`
	Step uint32 `json:"step,omitempty"`
}

// UIWindow mirrors marlin.ui.v1.UiWindow.
type UIWindow struct {
	ID       string `json:"id"`
	BufferID string `json:"bufferId"`
}

// UIFrame mirrors marlin.ui.v1.UiFrame.
type UIFrame struct {
	ID             string     `json:"id,omitempty"`
	Windows        []UIWindow `json:"windows,omitempty"`
	SelectedWindow string     `json:"selectedWindow,omitempty"`
	Minibuffer     string     `json:"minibuffer,omitempty"`
	BufferIDs      []string   `json:"bufferIds,omitempty"`
}

// UIStatusItem mirrors marlin.ui.v1.UiStatusItem (width-aware Doom Modeline item).
type UIStatusItem struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	CompactLabel string `json:"compactLabel,omitempty"`
	Tone         UITone `json:"tone,omitempty"`
	ActiveTone   UITone `json:"activeTone,omitempty"`
	MinWidth     uint32 `json:"minWidth,omitempty"`
	CompactBelow uint32 `json:"compactBelow,omitempty"`
	KeepPriority uint32 `json:"keepPriority,omitempty"`
}

// UIExtensionRegion mirrors marlin.ui.v1.UiExtensionRegion (Scheme-authored buffer/pane).
type UIExtensionRegion struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Focusable bool      `json:"focusable,omitempty"`
	Content   UIElement `json:"content"`
}

// UICommandSpec mirrors marlin.ui.v1.UiCommand.
type UICommandSpec struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Arguments   string `json:"arguments,omitempty"` // none | required | optional
	Capability  string `json:"capability,omitempty"`
	FixedValue  string `json:"fixedValue,omitempty"`
}

// UIKeyScope mirrors marlin.ui.v1.UiKeyScope.
type UIKeyScope string

const (
	KeyScopeGlobal UIKeyScope = "global"
	KeyScopeInsert UIKeyScope = "insert"
	KeyScopeNormal UIKeyScope = "normal"
)

// UIKeyBindingSpec mirrors marlin.ui.v1.UiKeyBinding (Scheme Meow/Emacs keymap).
type UIKeyBindingSpec struct {
	Scope       UIKeyScope `json:"scope"`
	Sequence    []string   `json:"sequence"`
	ActionKind  string     `json:"actionKind,omitempty"`
	CommandName string     `json:"commandName,omitempty"`
}

// UITransientForeign mirrors marlin.ui.v1.UiTransientForeign (Scheme Hydra foreign policy).
type UITransientForeign string

const (
	TransientForeignExit UITransientForeign = "exit"
	TransientForeignWarn UITransientForeign = "warn"
	TransientForeignRun  UITransientForeign = "run"
)

// UITransientHead mirrors marlin.ui.v1.UiTransientHead (one Scheme Hydra head).
type UITransientHead struct {
	Key         string `json:"key"`
	CommandName string `json:"commandName,omitempty"`
	Label       string `json:"label,omitempty"`
	Exit        bool   `json:"exit,omitempty"`
}

// UITransient mirrors marlin.ui.v1.UiTransient (one Scheme Hydra body).
type UITransient struct {
	ID          string             `json:"id"`
	Hint        UIElement          `json:"hint"`
	WarningHint UIElement          `json:"warningHint"`
	Foreign     UITransientForeign `json:"foreign,omitempty"`
	Heads       []UITransientHead  `json:"heads,omitempty"`
}
