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

// ValidateBrowserURL strictly validates that rawURL is a safe HTTPS browser
// navigation target before invoking OS launchers (no leading-dash flag
// injection, no whitespace/control/quote/shell metacharacters, while allowing
// standard RFC 3986 query parameter separators such as '&' and '=').
func ValidateBrowserURL(rawURL string) error {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" || strings.HasPrefix(trimmed, "-") || strings.ContainsAny(trimmed, " \t\r\n\"'|;<>`") {
		return fmt.Errorf("%w: URL contains forbidden characters or leading '-'", ErrInsecureEndpoint)
	}
	return ValidateSecureURL(trimmed, false)
}

// OpenSystemBrowser launches an HTTPS authorization URL in the operating system's
// default external browser per RFC 8252 (never an embedded untrusted WebView).
// Fail-closed: strictly rejects any non-HTTPS URL or leading-dash flag injection.
func OpenSystemBrowser(ctx context.Context, rawURL string) error {
	trimmed := strings.TrimSpace(rawURL)
	if err := ValidateBrowserURL(trimmed); err != nil {
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
