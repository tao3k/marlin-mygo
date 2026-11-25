// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build darwin

package composer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func pickFilesPlatform(ctx context.Context, opts NativePickerOptions) ([]string, error) {
	if _, err := exec.LookPath("osascript"); err != nil {
		return nil, ErrNativePickerUnavailable
	}
	script := `on run argv
set dialogTitle to item 1 of argv
set allowMulti to item 2 of argv
if allowMulti is "true" then
	set picked to choose file with prompt dialogTitle with multiple selections allowed
	set outLines to {}
	repeat with f in picked
		set end of outLines to POSIX path of f
	end repeat
	set AppleScript's text item delimiters to linefeed
	return outLines as text
else
	set picked to choose file with prompt dialogTitle
	return POSIX path of picked
end if
end run`
	multi := "false"
	if opts.AllowMultiple {
		multi = "true"
	}
	cmd := exec.CommandContext(ctx, "osascript", "-e", script, "--", opts.Title, multi)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// AppleScript error -128 indicates normal user cancellation.
		if strings.Contains(stderr.String(), "(-128)") || strings.Contains(stderr.String(), "User canceled") {
			return nil, nil
		}
		return nil, fmt.Errorf("composer: macOS file picker failed: %w", err)
	}
	return splitNonEmptyLines(stdout.String()), nil
}

func readClipboardPlatform(ctx context.Context) (AttachmentInput, bool, error) {
	// 1. Prefer PNG image on the macOS pasteboard (e.g. Cmd+Ctrl+Shift+4 screenshots).
	if _, err := exec.LookPath("osascript"); err == nil {
		tmp, err := os.CreateTemp("", "marlin-clip-*.png")
		if err == nil {
			tmpPath := tmp.Name()
			_ = tmp.Chmod(0o600)
			_ = tmp.Close()
			defer os.Remove(tmpPath)

			script := `on run argv
set outPath to POSIX file (item 1 of argv)
try
	set imgData to the clipboard as «class PNGf»
	set fRef to open for access outPath with write permission
	set eof of fRef to 0
	write imgData to fRef
	close access fRef
	return "ok"
on error
	try
		close access outPath
	end try
	return "empty"
end try
end run`
			out, runErr := exec.CommandContext(ctx, "osascript", "-e", script, "--", tmpPath).Output()
			if runErr == nil && strings.TrimSpace(string(out)) == "ok" {
				if data, rErr := os.ReadFile(tmpPath); rErr == nil && len(data) > 0 {
					return AttachmentInput{
						Filename: "clipboard-screenshot.png",
						MIMEType: "image/png",
						Data:     data,
					}, true, nil
				}
			}
		}
	}

	// 2. Fallback to UTF-8 text snippet via pbpaste when user explicitly triggers Paste Attachment.
	if _, err := exec.LookPath("pbpaste"); err == nil {
		out, err := exec.CommandContext(ctx, "pbpaste").Output()
		if err == nil && len(bytes.TrimSpace(out)) > 0 {
			return AttachmentInput{
				Filename: "clipboard-snippet.txt",
				MIMEType: "text/plain",
				Data:     out,
			}, true, nil
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
