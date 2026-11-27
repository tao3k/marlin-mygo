// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build js

package auth

import "context"

// OpenSystemBrowser is unavailable in browser WebAssembly builds; callers use
// standard top-level navigation or RFC 8628 Device Authorization instead.
func OpenSystemBrowser(context.Context, string) error {
	return ErrNotSupported
}
