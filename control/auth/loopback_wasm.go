// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build js

package auth

import "context"

// LoopbackCallback holds the parsed query parameters from an OAuth redirect.
type LoopbackCallback struct {
	Code             string
	State            string
	Error            string
	ErrorDescription string
}

// LoopbackReceiver is unavailable in browser WebAssembly sandboxes; browser
// builds use standard redirect URIs or RFC 8628 Device Authorization instead.
type LoopbackReceiver struct{}

func StartLoopbackReceiver() (*LoopbackReceiver, error) {
	return nil, ErrNotSupported
}

func (r *LoopbackReceiver) RedirectURI() string { return "" }

func (r *LoopbackReceiver) Wait(context.Context) (LoopbackCallback, error) {
	return LoopbackCallback{}, ErrNotSupported
}

func (r *LoopbackReceiver) Close() {}
