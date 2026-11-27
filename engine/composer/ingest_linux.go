// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build linux

package composer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func pickFilesPlatform(ctx context.Context, opts NativePickerOptions) ([]string, error) {
	if _, err := exec.LookPath("zenity"); err == nil {
		args := []string{"--file-selection", "--title", opts.Title, "--separator=\n"}
		if opts.AllowMultiple {
			args = append(args, "--multiple")
		}
		cmd := exec.CommandContext(ctx, "zenity", args...)
		out, err := cmd.Output()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
				return nil, nil // user cancelled
			}
			return nil, fmt.Errorf("composer: zenity file picker failed: %w", err)
		}
		return splitNonEmptyLines(string(out)), nil
	}
	if _, err := exec.LookPath("kdialog"); err == nil {
		args := []string{"--getopenfilename", ".", "--title", opts.Title}
		if opts.AllowMultiple {
			args = append(args, "--multiple", "--separate-output")
		}
		cmd := exec.CommandContext(ctx, "kdialog", args...)
		out, err := cmd.Output()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
				return nil, nil
			}
			return nil, fmt.Errorf("composer: kdialog file picker failed: %w", err)
		}
		return splitNonEmptyLines(string(out)), nil
	}
	return nil, ErrNativePickerUnavailable
}

func readClipboardPlatform(ctx context.Context) (AttachmentInput, bool, error) {
	// 1. Wayland wl-paste image/png -> X11 xclip image/png.
	if _, err := exec.LookPath("wl-paste"); err == nil {
		if data, err := exec.CommandContext(ctx, "wl-paste", "--no-newline", "--type", "image/png").Output(); err == nil && len(data) > 0 {
			return AttachmentInput{Filename: "clipboard-screenshot.png", MIMEType: "image/png", Data: data}, true, nil
		}
		if data, err := exec.CommandContext(ctx, "wl-paste", "--no-newline").Output(); err == nil && len(bytes.TrimSpace(data)) > 0 {
			return AttachmentInput{Filename: "clipboard-snippet.txt", MIMEType: "text/plain", Data: data}, true, nil
		}
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		if data, err := exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", "image/png", "-o").Output(); err == nil && len(data) > 0 {
			return AttachmentInput{Filename: "clipboard-screenshot.png", MIMEType: "image/png", Data: data}, true, nil
		}
		if data, err := exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-o").Output(); err == nil && len(bytes.TrimSpace(data)) > 0 {
			return AttachmentInput{Filename: "clipboard-snippet.txt", MIMEType: "text/plain", Data: data}, true, nil
		}
	}
	return AttachmentInput{}, false, nil
}

func splitNonEmptyLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
