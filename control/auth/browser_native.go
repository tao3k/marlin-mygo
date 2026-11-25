// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package auth

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// OpenSystemBrowser launches an HTTPS authorization URL in the operating system's
// default external browser per RFC 8252 (never an embedded untrusted WebView).
// Fail-closed: strictly rejects any non-HTTPS URL or leading-dash flag injection.
func OpenSystemBrowser(ctx context.Context, rawURL string) error {
	trimmed := strings.TrimSpace(rawURL)
	if strings.HasPrefix(trimmed, "-") {
		return fmt.Errorf("%w: URL cannot start with '-'", ErrInsecureEndpoint)
	}
	if err := ValidateSecureURL(trimmed, false); err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", trimmed)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", trimmed)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", trimmed)
	}
	return cmd.Start()
}
