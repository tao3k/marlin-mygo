// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build js && wasm

package main

import (
	"strings"
	"syscall/js"

	"github.com/egoist/mygo"

	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/resilience"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

const (
	webRecoveryStorageKey = "marlin.recovery.v1"
	webDefaultChatScope   = "chat:web-session-1"
)

type webContinuityHandle struct {
	saveNow        func()
	onDraftChanged func(scopeID, text string)
	setWindow      func(*mygo.Window)
}

// initWebDurableContinuity restores crash/reload-persisted drafts (Desktop chat
// + @ mentions, Mobile chat/capture drafts, and opt-in ChatRoom drafts),
// pending offline outbox turns, and stream resume cursors from browser
// localStorage before the first frame renders.
func initWebDurableContinuity(adaptive *mygoshell.AdaptiveShell) *webContinuityHandle {
	if adaptive == nil || adaptive.Desktop == nil || adaptive.Desktop.Transcript == nil || adaptive.Desktop.Transcript.Resilience == nil {
		return &webContinuityHandle{
			saveNow:        func() {},
			onDraftChanged: func(string, string) {},
			setWindow:      func(*mygo.Window) {},
		}
	}
	transcript := adaptive.Desktop.Transcript
	res := transcript.Resilience
	var winRef *mygo.Window

	invalidate := func() {
		if winRef != nil {
			winRef.Invalidate()
		}
	}

	// Restore last persisted snapshot fail-closed from browser localStorage.
	if ls := js.Global().Get("localStorage"); ls.Truthy() {
		raw := ls.Call("getItem", webRecoveryStorageKey)
		if raw.Type() == js.TypeString && strings.TrimSpace(raw.String()) != "" {
			if snap, err := resilience.DecodeRecoverySnapshot([]byte(raw.String())); err == nil {
				res.ImportSnapshot(snap)
			}
		}
	}

	transcript.RestoreDraftFromResilience(webDefaultChatScope)
	if adaptive.Mobile != nil {
		adaptive.Mobile.RestoreDraftsFromResilience()
	}

	saveNow := func() {
		ls := js.Global().Get("localStorage")
		if !ls.Truthy() {
			return
		}
		data, err := resilience.EncodeRecoverySnapshot(res.ExportSnapshot())
		if err != nil {
			return
		}
		ls.Call("setItem", webRecoveryStorageKey, string(data))
	}

	onDraftChanged := func(_, _ string) {
		saveNow()
	}
	transcript.OnDraftChanged = onDraftChanged
	transcript.OnDiscardOutbox = func(_ string) {
		saveNow()
		invalidate()
	}

	flushOutbox := func() {
		res.Outbox.Flush(webDefaultChatScope, func(it resilience.OutboxItem) error {
			if transcript.OnSubmitTurn != nil {
				transcript.OnSubmitTurn(composer.TurnSubmission{Prompt: it.Text})
			} else {
				transcript.AppendUser(it.Text)
			}
			return nil
		})
		res.MarkOnline()
		saveNow()
		invalidate()
	}
	transcript.OnRetryOutbox = flushOutbox

	// Initialize browser network state from navigator.onLine.
	if nav := js.Global().Get("navigator"); nav.Truthy() {
		if onLine := nav.Get("onLine"); onLine.Type() == js.TypeBoolean && !onLine.Bool() {
			res.MarkOffline("browser offline")
		}
	}

	h := &webContinuityHandle{
		saveNow:        saveNow,
		onDraftChanged: onDraftChanged,
		setWindow: func(w *mygo.Window) {
			winRef = w
			attachWebBrowserContinuityEvents(adaptive, res, saveNow, flushOutbox, invalidate)
		},
	}
	return h
}

func attachWebBrowserContinuityEvents(
	adaptive *mygoshell.AdaptiveShell,
	res *resilience.SessionResilience,
	saveNow func(),
	flushOutbox func(),
	invalidate func(),
) {
	winJS := js.Global().Get("window")
	if !winJS.Truthy() || !winJS.Get("addEventListener").Truthy() {
		return
	}

	onOffline := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		res.MarkOffline("browser offline")
		saveNow()
		invalidate()
		return nil
	})
	onOnline := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		flushOutbox()
		return nil
	})
	onPageHide := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		saveNow()
		return nil
	})
	onKeyDown := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 || adaptive == nil || adaptive.Desktop == nil {
			return nil
		}
		ev := args[0]
		mod := ev.Get("metaKey").Bool() || ev.Get("ctrlKey").Bool()
		if !mod {
			return nil
		}
		key := strings.ToLower(ev.Get("key").String())
		switch {
		case key == "k":
			ev.Call("preventDefault")
			adaptive.Desktop.OpenCommandPalette()
			invalidate()
		case key == "p":
			ev.Call("preventDefault")
			adaptive.Desktop.OpenHelmSearch()
			invalidate()
		case key == "f":
			ev.Call("preventDefault")
			if adaptive.Desktop.Transcript != nil {
				adaptive.Desktop.Transcript.OpenSearch()
				invalidate()
			}
		case key == "g" && ev.Get("shiftKey").Bool():
			ev.Call("preventDefault")
			adaptive.Desktop.OpenGovernance()
			invalidate()
		}
		return nil
	})

	winJS.Call("addEventListener", "offline", onOffline)
	winJS.Call("addEventListener", "online", onOnline)
	winJS.Call("addEventListener", "pagehide", onPageHide)
	winJS.Call("addEventListener", "beforeunload", onPageHide)
	winJS.Call("addEventListener", "keydown", onKeyDown)
}
