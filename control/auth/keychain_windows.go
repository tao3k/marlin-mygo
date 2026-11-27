// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build windows && !js

package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

const cryptProtectUIForbidden = 0x01

var (
	modCrypt32             = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtectData   = modCrypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = modCrypt32.NewProc("CryptUnprotectData")

	modKernel32   = syscall.NewLazyDLL("kernel32.dll")
	procLocalFree = modKernel32.NewProc("LocalFree")
)

type windowsDataBlob struct {
	cbData uint32
	pbData *byte
}

type windowsDPAPIStore struct{}

func defaultNativeSecretStore() NativeSecretStore { return windowsDPAPIStore{} }

func (windowsDPAPIStore) Name() string { return "Windows DPAPI" }

func (windowsDPAPIStore) Available() bool {
	return procCryptProtectData.Find() == nil && procCryptUnprotectData.Find() == nil
}

func (w windowsDPAPIStore) blobPath(service, account string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return "", fmt.Errorf("auth: user config directory unavailable")
	}
	sum := sha256.Sum256([]byte(service + "\x00" + account))
	name := "key-" + hex.EncodeToString(sum[:12]) + ".dpapi"
	return filepath.Join(dir, "marlin", name), nil
}

func (w windowsDPAPIStore) GetSecret(_ context.Context, service, account string) ([]byte, error) {
	path, err := w.blobPath(service, account)
	if err != nil {
		return nil, err
	}
	protected, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrKeychainNotFound
		}
		return nil, err
	}
	if len(protected) == 0 {
		return nil, ErrKeychainNotFound
	}
	return dpapiUnprotect(protected)
}

func (w windowsDPAPIStore) SetSecret(_ context.Context, service, account string, secret []byte) error {
	path, err := w.blobPath(service, account)
	if err != nil {
		return err
	}
	protected, err := dpapiProtect(secret)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, protected, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (w windowsDPAPIStore) DeleteSecret(_ context.Context, service, account string) error {
	path, err := w.blobPath(service, account)
	if err != nil {
		return nil
	}
	_ = os.Remove(path)
	return nil
}

func dpapiProtect(plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, fmt.Errorf("auth: empty DPAPI secret")
	}
	in := windowsDataBlob{
		cbData: uint32(len(plaintext)),
		pbData: &plaintext[0],
	}
	var out windowsDataBlob
	r1, _, callErr := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0,
		0,
		0,
		0,
		uintptr(cryptProtectUIForbidden),
		uintptr(unsafe.Pointer(&out)),
	)
	if r1 == 0 {
		return nil, fmt.Errorf("auth: CryptProtectData failed: %w", callErr)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	res := make([]byte, out.cbData)
	copy(res, unsafe.Slice(out.pbData, out.cbData))
	return res, nil
}

func dpapiUnprotect(protected []byte) ([]byte, error) {
	in := windowsDataBlob{
		cbData: uint32(len(protected)),
		pbData: &protected[0],
	}
	var out windowsDataBlob
	r1, _, callErr := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0,
		0,
		0,
		0,
		uintptr(cryptProtectUIForbidden),
		uintptr(unsafe.Pointer(&out)),
	)
	if r1 == 0 {
		return nil, fmt.Errorf("auth: CryptUnprotectData failed: %w", callErr)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	res := make([]byte, out.cbData)
	copy(res, unsafe.Slice(out.pbData, out.cbData))
	return res, nil
}
