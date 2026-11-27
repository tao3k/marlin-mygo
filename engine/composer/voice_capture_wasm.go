// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build js && wasm

package composer

import (
	"context"
	"os/exec"
)

func buildAudioCaptureCmd(_ context.Context, _ string) (*exec.Cmd, error) {
	return nil, ErrAudioCaptureUnavailable
}

func signalGracefulAudioStop(_ *exec.Cmd) error {
	return nil
}
