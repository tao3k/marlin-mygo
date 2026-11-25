// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build js && wasm

package composer

import "context"

func pickFilesPlatform(_ context.Context, _ NativePickerOptions) ([]string, error) {
	return nil, ErrNativePickerUnavailable
}

func readClipboardPlatform(_ context.Context) (AttachmentInput, bool, error) {
	return AttachmentInput{}, false, nil
}
