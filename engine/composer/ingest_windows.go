// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build windows

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
	ps, err := exec.LookPath("powershell")
	if err != nil {
		return nil, ErrNativePickerUnavailable
	}
	multi := "$false"
	if opts.AllowMultiple {
		multi = "$true"
	}
	script := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms
$d = New-Object System.Windows.Forms.OpenFileDialog
$d.Multiselect = %s
$d.Title = %q
if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
  $d.FileNames | ForEach-Object { Write-Output $_ }
}`, multi, opts.Title)
	out, err := exec.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive", "-STA", "-Command", script).Output()
	if err != nil {
		return nil, fmt.Errorf("composer: Windows file picker failed: %w", err)
	}
	return splitNonEmptyLines(string(out)), nil
}

func readClipboardPlatform(ctx context.Context) (AttachmentInput, bool, error) {
	ps, err := exec.LookPath("powershell")
	if err != nil {
		return AttachmentInput{}, false, fmt.Errorf("composer: Windows clipboard requires powershell: %w", err)
	}
	tmp, err := os.CreateTemp("", "marlin-clip-*.png")
	if err == nil {
		tmpPath := tmp.Name()
		_ = tmp.Close()
		defer os.Remove(tmpPath)

		script := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$img = [System.Windows.Forms.Clipboard]::GetImage()
if ($null -ne $img) {
  $img.Save(%q, [System.Drawing.Imaging.ImageFormat]::Png)
  Write-Output "ok"
}`, tmpPath)
		out, runErr := exec.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive", "-STA", "-Command", script).Output()
		if runErr == nil && strings.TrimSpace(string(out)) == "ok" {
			if data, rErr := os.ReadFile(tmpPath); rErr == nil && len(data) > 0 {
				return AttachmentInput{Filename: "clipboard-screenshot.png", MIMEType: "image/png", Data: data}, true, nil
			}
		}
	}

	out, err := exec.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive", "-STA", "-Command", "Get-Clipboard -Raw").Output()
	if err != nil {
		if ctx.Err() != nil {
			return AttachmentInput{}, false, ctx.Err()
		}
		return AttachmentInput{}, false, fmt.Errorf("composer: Windows clipboard read failed: %w", err)
	}
	if len(bytes.TrimSpace(out)) > 0 {
		return AttachmentInput{Filename: "clipboard-snippet.txt", MIMEType: "text/plain", Data: out}, true, nil
	}
	return AttachmentInput{}, false, nil
}

func splitNonEmptyLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
