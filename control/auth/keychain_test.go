// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/tao3k/marlin-mygo/control/auth"
)

type fakeNativeSecretStore struct {
	name      string
	available bool
	mu        sync.Mutex
	secrets   map[string][]byte
	setCalls  int
}

func (f *fakeNativeSecretStore) Name() string    { return f.name }
func (f *fakeNativeSecretStore) Available() bool { return f.available }

func (f *fakeNativeSecretStore) GetSecret(_ context.Context, service, account string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.secrets[service+"/"+account]
	if !ok {
		return nil, auth.ErrKeychainNotFound
	}
	return append([]byte(nil), v...), nil
}

func (f *fakeNativeSecretStore) SetSecret(_ context.Context, service, account string, secret []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.secrets == nil {
		f.secrets = make(map[string][]byte)
	}
	f.setCalls++
	f.secrets[service+"/"+account] = append([]byte(nil), secret...)
	return nil
}

func (f *fakeNativeSecretStore) DeleteSecret(_ context.Context, service, account string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.secrets, service+"/"+account)
	return nil
}

func TestOSProtectedVaultKeychainWrapLockUnlockAndPersistence(t *testing.T) {
	ctx := context.Background()
	store := &fakeNativeSecretStore{name: "macOS Keychain (Mock)", available: true}
	vaultPath := filepath.Join(t.TempDir(), "auth-vault.enc")

	// 1. OpenNativeVault generates a 32-byte wrapping key in OS keychain + creates AES-256-GCM file vault.
	vault, info := auth.OpenNativeVault(ctx, auth.NativeVaultConfig{
		Store:     store,
		VaultPath: vaultPath,
	})
	if !info.OSProtected || !info.Persistent || info.CipherSuite != "AES-256-GCM" || info.Locked {
		t.Fatalf("unexpected VaultSecurityInfo: %+v", info)
	}
	if store.setCalls != 1 {
		t.Fatalf("expected 1 OS keychain master key generation, got %d", store.setCalls)
	}

	const secretTok = "ya29.os-keychain-wrapped-secret"
	if err := vault.Store(ctx, "google:sub-1", auth.TokenSet{
		AccessToken:  secretTok,
		RefreshToken: "1//os-keychain-refresh",
		TokenType:    "Bearer",
	}); err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	// Disk ciphertext MUST contain zero plaintext token.
	diskBytes, err := os.ReadFile(vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(diskBytes, []byte(secretTok)) {
		t.Fatal("plaintext access token leaked to encrypted vault file!")
	}

	// 2. Lock() scrubs in-memory AES key and fails closed with ErrVaultLocked; Unlock(ctx) restores access!
	osVault, ok := vault.(*auth.OSProtectedVault)
	if !ok {
		t.Fatalf("expected *OSProtectedVault, got %T", vault)
	}
	osVault.Lock()
	if !osVault.SecurityInfo().Locked {
		t.Fatal("expected Locked=true after Lock()")
	}
	if _, _, err := osVault.Load(ctx, "google:sub-1"); !errors.Is(err, auth.ErrVaultLocked) {
		t.Fatalf("expected ErrVaultLocked while locked, got %v", err)
	}
	if err := osVault.Unlock(ctx); err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}
	loaded, found, err := osVault.Load(ctx, "google:sub-1")
	if err != nil || !found || loaded.AccessToken != secretTok {
		t.Fatalf("expected restored token after Unlock, got found=%v err=%v", found, err)
	}

	// 3. Re-opening from a fresh process with the same OS keychain reuses the existing master key!
	vault2, info2 := auth.OpenNativeVault(ctx, auth.NativeVaultConfig{
		Store:     store,
		VaultPath: vaultPath,
	})
	if !info2.OSProtected || store.setCalls != 1 {
		t.Fatalf("expected fresh OpenNativeVault to reuse existing keychain key (setCalls=%d)", store.setCalls)
	}
	reopened, found2, err := vault2.Load(ctx, "google:sub-1")
	if err != nil || !found2 || reopened.AccessToken != secretTok {
		t.Fatalf("expected persisted token across restart, got found=%v err=%v", found2, err)
	}

	// 4. Unavailable OS keychain degrades gracefully to ephemeral MemoryVault (never plaintext disk!).
	unavailStore := &fakeNativeSecretStore{name: "Headless", available: false}
	fallbackVault, fbInfo := auth.OpenNativeVault(ctx, auth.NativeVaultConfig{Store: unavailStore})
	if fbInfo.OSProtected || fbInfo.Persistent || fbInfo.FallbackReason == "" {
		t.Fatalf("expected ephemeral MemoryVault fallback info, got %+v", fbInfo)
	}
	if err := fallbackVault.Store(ctx, "email:u", auth.TokenSet{AccessToken: "mem-tok"}); err != nil {
		t.Fatalf("fallback MemoryVault Store failed: %v", err)
	}
}
