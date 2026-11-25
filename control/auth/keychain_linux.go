// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build linux && !js

package auth

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

type linuxSecretServiceStore struct{}

func defaultNativeSecretStore() NativeSecretStore { return linuxSecretServiceStore{} }

func (linuxSecretServiceStore) Name() string { return "Linux Secret Service" }

func (linuxSecretServiceStore) Available() bool {
	_, err := exec.LookPath("secret-tool")
	return err == nil
}

func (linuxSecretServiceStore) GetSecret(ctx context.Context, service, account string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "secret-tool", "lookup", "service", service, "account", account)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, ErrKeychainNotFound
	}
	out := bytes.TrimSpace(stdout.Bytes())
	if len(out) == 0 {
		return nil, ErrKeychainNotFound
	}
	res := append([]byte(nil), out...)
	zeroBytes(stdout.Bytes())
	return res, nil
}

// SetSecret writes the secret strictly via stdin to `secret-tool store`.
func (linuxSecretServiceStore) SetSecret(ctx context.Context, service, account string, secret []byte) error {
	cmd := exec.CommandContext(ctx, "secret-tool", "store", "--label=Marlin Auth Vault", "service", service, "account", account)
	cmd.Stdin = bytes.NewReader(secret)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("auth: Linux Secret Service store failed: %w", err)
	}
	return nil
}

func (linuxSecretServiceStore) DeleteSecret(ctx context.Context, service, account string) error {
	cmd := exec.CommandContext(ctx, "secret-tool", "clear", "service", service, "account", account)
	_ = cmd.Run()
	return nil
}
