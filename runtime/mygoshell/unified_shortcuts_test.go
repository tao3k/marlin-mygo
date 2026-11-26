// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell_test

import (
	"testing"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

func TestUnifiedCommandPaletteAndEmacsSPCShortcuts(t *testing.T) {
	targets := transport.NewTargetRegistry()
	fed := transport.NewFederationSupervisor(targets)
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)
	app.Federation = fed
	app.Transcript = mygoshell.NewTranscriptController()

	var newSessions, voiceStarts, voiceStops int
	app.OnNewSession = func() { newSessions++ }
	app.Transcript.OnStartVoice = func() { voiceStarts++ }
	app.Transcript.OnStopVoice = func() { voiceStops++ }

	if err := app.EnableEmacsWindowKeymap(); err != nil {
		t.Fatalf("EnableEmacsWindowKeymap failed: %v", err)
	}

	runChord := func(tokens ...string) string {
		var kind input.MatchKind
		var cmd string
		for _, tok := range tokens {
			kind, cmd = app.HandleActiveKeyToken(tok)
		}
		if kind != input.MatchExact {
			t.Fatalf("expected MatchExact for chord %v, got kind=%v cmd=%q", tokens, kind, cmd)
		}
		return cmd
	}

	// 1. SPC f -> opens Federation Cockpit, ESC closes it.
	if cmd := runChord("SPC", "f"); cmd != shell.CmdStudioOpenFederation || !app.IsFederationOpen() {
		t.Fatalf("expected SPC f to open Federation Cockpit, cmd=%q open=%v", cmd, app.IsFederationOpen())
	}
	app.DismissTopOverlay()

	// 2. SPC M -> toggles Model Picker open/closed.
	if cmd := runChord("SPC", "M"); cmd != shell.CmdStudioOpenModelPicker || !app.Transcript.IsModelPickerOpen() {
		t.Fatalf("expected SPC M to open Model Picker, cmd=%q open=%v", cmd, app.Transcript.IsModelPickerOpen())
	}
	runChord("SPC", "M")
	if app.Transcript.IsModelPickerOpen() {
		t.Fatal("expected second SPC M to close Model Picker")
	}

	// 3. SPC v -> starts live Voice Recording; second SPC v -> stops/attaches Voice Recording.
	if cmd := runChord("SPC", "v"); cmd != shell.CmdStudioToggleVoice || voiceStarts != 1 || app.Transcript.Voice.Snapshot().Phase != composer.VoiceRecording {
		t.Fatalf("expected first SPC v to start voice, cmd=%q starts=%d phase=%s", cmd, voiceStarts, app.Transcript.Voice.Snapshot().Phase)
	}
	if cmd := runChord("SPC", "v"); cmd != shell.CmdStudioToggleVoice || voiceStops != 1 || app.Transcript.Voice.Snapshot().Phase != composer.VoiceProcessing {
		t.Fatalf("expected second SPC v to stop voice, cmd=%q stops=%d phase=%s", cmd, voiceStops, app.Transcript.Voice.Snapshot().Phase)
	}

	// 4. SPC / -> opens Session Search bar; SPC n -> creates new session; SPC K -> opens Keymap Inspector.
	if cmd := runChord("SPC", "/"); cmd != shell.CmdStudioOpenSearch || !app.Transcript.IsSearchOpen() {
		t.Fatalf("expected SPC / to open session search, cmd=%q open=%v", cmd, app.Transcript.IsSearchOpen())
	}
	if cmd := runChord("SPC", "n"); cmd != shell.CmdStudioNewSession || newSessions != 1 {
		t.Fatalf("expected SPC n to trigger OnNewSession, cmd=%q count=%d", cmd, newSessions)
	}
	if cmd := runChord("SPC", "K"); cmd != shell.CmdStudioOpenKeymap || !app.IsKeymapInspectorOpen() {
		t.Fatalf("expected SPC K to open Keymap Inspector, cmd=%q open=%v", cmd, app.IsKeymapInspectorOpen())
	}
}
