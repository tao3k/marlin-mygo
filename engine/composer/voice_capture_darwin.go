// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build darwin

package composer

import (
	"context"
	"os"
	"os/exec"
)

func buildAudioCaptureCmd(ctx context.Context, outPath string) (*exec.Cmd, error) {
	if rec, err := exec.LookPath("rec"); err == nil {
		return exec.CommandContext(ctx, rec, "-q", "-c", "1", "-r", "16000", "-b", "16", outPath), nil
	}
	if ff, err := exec.LookPath("ffmpeg"); err == nil {
		return exec.CommandContext(ctx, ff, "-y", "-loglevel", "error",
			"-f", "avfoundation", "-i", ":0", "-ac", "1", "-ar", "16000", outPath), nil
	}
	return nil, ErrAudioCaptureUnavailable
}

func signalGracefulAudioStop(cmd *exec.Cmd) error {
	return cmd.Process.Signal(os.Interrupt)
}
