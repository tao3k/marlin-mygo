// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package shell

import "github.com/tao3k/marlin-mygo/engine/input"

// Canonical mode transition, Studio navigation, and Emacs Insert/Edit commands.
const (
	// Modal transitions between Layout 1 (SPC Normal/Leader) and Layout 2 (Emacs Insert/Edit).
	CmdModeInsert = "mode.insert"
	CmdModeNormal = "mode.normal"

	// Studio navigation commands (SPC leader layout).
	CmdStudioOpenMarketplace = "studio.open-marketplace"
	CmdStudioOpenProjection  = "studio.open-projection"
	CmdStudioOpenPalette     = "studio.open-palette"
	CmdStudioOpenGate        = "studio.open-gate"
	CmdStudioCycleTheme      = "studio.cycle-theme"

	// Classic Emacs editing & navigation commands active in Insert/Edit mode (ScopeInsert).
	CmdEditNextLine           = "edit.next-line"
	CmdEditPrevLine           = "edit.prev-line"
	CmdEditForwardChar        = "edit.forward-char"
	CmdEditBackwardChar       = "edit.backward-char"
	CmdEditBeginningOfLine    = "edit.beginning-of-line"
	CmdEditEndOfLine          = "edit.end-of-line"
	CmdEditForwardWord        = "edit.forward-word"
	CmdEditBackwardWord       = "edit.backward-word"
	CmdEditDeleteChar         = "edit.delete-char"
	CmdEditBackwardDeleteChar = "edit.backward-delete-char"
	CmdEditKillLine           = "edit.kill-line"
	CmdEditYank               = "edit.yank"
	CmdEditBackwardKillWord   = "edit.backward-kill-word"
	CmdEditUndo               = "edit.undo"
)

// NormalLeaderBindings returns Keymap Layout 1: SPC-guided Meow/Vim-style
// Normal mode bindings for window composition, perspectives, and studio control.
func NormalLeaderBindings() []input.Binding {
	return []input.Binding{
		// Window splits & pane management (SPC w ...)
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "w", "v"}, ActionKind: "window", CommandName: CmdWindowSplitRight},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "w", "s"}, ActionKind: "window", CommandName: CmdWindowSplitBelow},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "w", "d"}, ActionKind: "window", CommandName: CmdWindowDelete},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "w", "z"}, ActionKind: "window", CommandName: CmdWindowToggleZoom},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "w", "o"}, ActionKind: "window", CommandName: CmdWindowFocusNext},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "w", "="}, ActionKind: "window", CommandName: CmdWindowBalance},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "w", "b"}, ActionKind: "window", CommandName: CmdWindowCycleBuffer},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "w", "x"}, ActionKind: "window", CommandName: CmdWindowSwapNext},

		// Perspectives (SPC p ...)
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "p", "1"}, ActionKind: "perspective", CommandName: CmdPerspectiveFocus},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "p", "2"}, ActionKind: "perspective", CommandName: CmdPerspectiveSplit},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "p", "3"}, ActionKind: "perspective", CommandName: CmdPerspectiveCockpit},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "p", "4"}, ActionKind: "perspective", CommandName: CmdPerspectiveZen},

		// Studio drawers & theme (SPC ...)
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "m"}, ActionKind: "studio", CommandName: CmdStudioOpenMarketplace},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "i"}, ActionKind: "studio", CommandName: CmdStudioOpenProjection},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "g"}, ActionKind: "studio", CommandName: CmdStudioOpenGate},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", "t", "t"}, ActionKind: "studio", CommandName: CmdStudioCycleTheme},
		{Scope: input.ScopeNormal, Sequence: []string{"SPC", ":"}, ActionKind: "studio", CommandName: CmdStudioOpenPalette},

		// Transition from Normal/Leader mode into Emacs Insert/Edit mode
		{Scope: input.ScopeNormal, Sequence: []string{"i"}, ActionKind: "mode", CommandName: CmdModeInsert},
		{Scope: input.ScopeNormal, Sequence: []string{"a"}, ActionKind: "mode", CommandName: CmdModeInsert},
	}
}

// EmacsInsertBindings returns Keymap Layout 2: authentic Emacs cursor motion
// and line-editing bindings active in Insert / Edit mode (ScopeInsert), such
// as C-n, C-p, C-f, C-b, C-a, C-e, C-k, C-y, plus ESC / C-g to return to Normal.
func EmacsInsertBindings() []input.Binding {
	return []input.Binding{
		{Scope: input.ScopeInsert, Sequence: []string{"C-n"}, ActionKind: "edit", CommandName: CmdEditNextLine},
		{Scope: input.ScopeInsert, Sequence: []string{"C-p"}, ActionKind: "edit", CommandName: CmdEditPrevLine},
		{Scope: input.ScopeInsert, Sequence: []string{"C-f"}, ActionKind: "edit", CommandName: CmdEditForwardChar},
		{Scope: input.ScopeInsert, Sequence: []string{"C-b"}, ActionKind: "edit", CommandName: CmdEditBackwardChar},
		{Scope: input.ScopeInsert, Sequence: []string{"C-a"}, ActionKind: "edit", CommandName: CmdEditBeginningOfLine},
		{Scope: input.ScopeInsert, Sequence: []string{"C-e"}, ActionKind: "edit", CommandName: CmdEditEndOfLine},
		{Scope: input.ScopeInsert, Sequence: []string{"M-f"}, ActionKind: "edit", CommandName: CmdEditForwardWord},
		{Scope: input.ScopeInsert, Sequence: []string{"M-b"}, ActionKind: "edit", CommandName: CmdEditBackwardWord},
		{Scope: input.ScopeInsert, Sequence: []string{"C-d"}, ActionKind: "edit", CommandName: CmdEditDeleteChar},
		{Scope: input.ScopeInsert, Sequence: []string{"C-h"}, ActionKind: "edit", CommandName: CmdEditBackwardDeleteChar},
		{Scope: input.ScopeInsert, Sequence: []string{"C-k"}, ActionKind: "edit", CommandName: CmdEditKillLine},
		{Scope: input.ScopeInsert, Sequence: []string{"C-y"}, ActionKind: "edit", CommandName: CmdEditYank},
		{Scope: input.ScopeInsert, Sequence: []string{"C-w"}, ActionKind: "edit", CommandName: CmdEditBackwardKillWord},
		{Scope: input.ScopeInsert, Sequence: []string{"C-/"}, ActionKind: "edit", CommandName: CmdEditUndo},
		// Return from Insert/Edit mode to SPC Normal mode
		{Scope: input.ScopeInsert, Sequence: []string{"ESC"}, ActionKind: "mode", CommandName: CmdModeNormal},
		{Scope: input.ScopeInsert, Sequence: []string{"C-g"}, ActionKind: "mode", CommandName: CmdModeNormal},
	}
}

// GlobalEmacsBindings returns classic Emacs C-x window chords available in
// both Normal and Insert/Edit layouts.
func GlobalEmacsBindings() []input.Binding {
	return []input.Binding{
		{Scope: input.ScopeGlobal, Sequence: []string{"C-x", "3"}, ActionKind: "window", CommandName: CmdWindowSplitRight},
		{Scope: input.ScopeGlobal, Sequence: []string{"C-x", "2"}, ActionKind: "window", CommandName: CmdWindowSplitBelow},
		{Scope: input.ScopeGlobal, Sequence: []string{"C-x", "0"}, ActionKind: "window", CommandName: CmdWindowDelete},
		{Scope: input.ScopeGlobal, Sequence: []string{"C-x", "1"}, ActionKind: "window", CommandName: CmdWindowToggleZoom},
		{Scope: input.ScopeGlobal, Sequence: []string{"C-x", "o"}, ActionKind: "window", CommandName: CmdWindowFocusNext},
		{Scope: input.ScopeGlobal, Sequence: []string{"C-x", "+"}, ActionKind: "window", CommandName: CmdWindowBalance},
		{Scope: input.ScopeGlobal, Sequence: []string{"C-x", "x"}, ActionKind: "window", CommandName: CmdWindowSwapNext},
	}
}

// DualModalBindings merges Layout 1 (SPC Normal), Layout 2 (Emacs Insert/Edit),
// and Global C-x chords into one complete conflict-free binding set.
func DualModalBindings() []input.Binding {
	n := NormalLeaderBindings()
	ins := EmacsInsertBindings()
	g := GlobalEmacsBindings()
	out := make([]input.Binding, 0, len(n)+len(ins)+len(g))
	out = append(out, n...)
	out = append(out, ins...)
	out = append(out, g...)
	return out
}

// CompileDualModalKeymap compiles both modal keymap layouts into one O(chord_len) KeyTrie.
func CompileDualModalKeymap() (*input.KeyTrie, error) {
	return input.CompileKeyTrie(DualModalBindings())
}

// DualModalCommands returns Command Palette entries covering both layouts.
func DualModalCommands() []input.CommandEntry {
	base := DefaultEmacsWindowCommands()
	extra := []input.CommandEntry{
		{Name: CmdModeInsert, Description: "Enter Insert/Edit mode with Emacs keybindings (i / a)"},
		{Name: CmdModeNormal, Description: "Return to Normal SPC leader mode (ESC / C-g)"},
		{Name: CmdStudioOpenMarketplace, Description: "Open Official Marketplace drawer (SPC m)"},
		{Name: CmdStudioOpenProjection, Description: "Open Projection Inspector (SPC i)"},
		{Name: CmdStudioCycleTheme, Description: "Cycle active Studio theme preset (SPC t t)"},
		{Name: CmdEditNextLine, Description: "Emacs Insert: move to next line (C-n)"},
		{Name: CmdEditPrevLine, Description: "Emacs Insert: move to previous line (C-p)"},
		{Name: CmdEditBeginningOfLine, Description: "Emacs Insert: beginning of line (C-a)"},
		{Name: CmdEditEndOfLine, Description: "Emacs Insert: end of line (C-e)"},
		{Name: CmdEditKillLine, Description: "Emacs Insert: kill to end of line (C-k)"},
		{Name: CmdEditYank, Description: "Emacs Insert: yank killed text (C-y)"},
	}
	return append(base, extra...)
}
