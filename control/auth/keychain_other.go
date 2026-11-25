// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build (!darwin && !linux && !windows) || js

package auth

import "context"

type unsupportedKeychainStore struct{}

func defaultNativeSecretStore() NativeSecretStore { return unsupportedKeychainStore{} }

func (unsupportedKeychainStore) Name() string                                          { return "Platform Keychain" }
func (unsupportedKeychainStore) Available() bool                                       { return false }
func (unsupportedKeychainStore) GetSecret(context.Context, string, string) ([]byte, error) { return nil, ErrNotSupported }
func (unsupportedKeychainStore) SetSecret(context.Context, string, string, []byte) error   { return ErrNotSupported }
func (unsupportedKeychainStore) DeleteSecret(context.Context, string, string) error        { return ErrNotSupported }
