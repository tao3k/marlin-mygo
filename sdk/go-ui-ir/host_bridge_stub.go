// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !wasip1

package gouir

// EmitActionMock lets native Go unit tests intercept EmitAction calls outside wasip1.
var EmitActionMock func(action string, args []byte, idempotencyKey string) bool

// EmitAction forwards to EmitActionMock on non-WASM hosts for fast native testing.
func EmitAction(action string, args []byte, idempotencyKey string) bool {
	if EmitActionMock != nil {
		return EmitActionMock(action, args, idempotencyKey)
	}
	return false
}
