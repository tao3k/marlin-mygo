// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build windows

package composer

import (
	"context"
	"os/exec"
)

func buildAudioCaptureCmd(ctx context.Context, outPath string) (*exec.Cmd, error) {
	if ff, err := exec.LookPath("ffmpeg"); err == nil {
		return exec.CommandContext(ctx, ff, "-y", "-loglevel", "error",
			"-f", "dshow", "-i", "audio=default", "-ac", "1", "-ar", "16000", outPath), nil
	}
	return nil, ErrAudioCaptureUnavailable
}

func signalGracefulAudioStop(cmd *exec.Cmd) error {
	return cmd.Process.Kill()
}
