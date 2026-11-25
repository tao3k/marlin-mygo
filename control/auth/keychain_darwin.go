// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build darwin && !js

package auth

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type darwinKeychainStore struct{}

func defaultNativeSecretStore() NativeSecretStore { return darwinKeychainStore{} }

func (darwinKeychainStore) Name() string { return "macOS Keychain" }

func (darwinKeychainStore) Available() bool {
	_, err := exec.LookPath("/usr/bin/security")
	return err == nil
}

func (darwinKeychainStore) GetSecret(ctx context.Context, service, account string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", service, "-a", account, "-w")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.ToLower(stderr.String())
		if strings.Contains(msg, "could not be found") || strings.Contains(msg, "item could not be found") {
			return nil, ErrKeychainNotFound
		}
		return nil, fmt.Errorf("auth: macOS Keychain lookup failed: %w", err)
	}
	out := bytes.TrimSpace(stdout.Bytes())
	if len(out) == 0 {
		return nil, ErrKeychainNotFound
	}
	res := append([]byte(nil), out...)
	zeroBytes(stdout.Bytes())
	return res, nil
}

// SetSecret writes the secret strictly via stdin (`security -i`) so the secret
// never appears in OS process arguments (`ps aux`).
func (darwinKeychainStore) SetSecret(ctx context.Context, service, account string, secret []byte) error {
	if strings.ContainsAny(service+account, " \t\r\n\"'") {
		return fmt.Errorf("auth: invalid keychain service/account characters")
	}
	if strings.ContainsAny(string(secret), " \t\r\n\"'") {
		return fmt.Errorf("auth: invalid keychain secret characters for security -i stdin")
	}
	line := fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s\n", service, account, string(secret))
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "-i")
	cmd.Stdin = strings.NewReader(line)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("auth: macOS Keychain store failed: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (darwinKeychainStore) DeleteSecret(ctx context.Context, service, account string) error {
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "delete-generic-password", "-s", service, "-a", account)
	_ = cmd.Run()
	return nil
}
