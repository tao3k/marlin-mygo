// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	DefaultKeychainService = "dev.tao3k.marlin.auth"
	DefaultKeychainAccount = "vault-master-key-v1"
)

var (
	ErrVaultLocked      = errors.New("auth: credential vault is locked")
	ErrKeychainNotFound = errors.New("auth: secret not found in OS keychain")
)

// NativeSecretStore abstracts the operating system's native secret storage
// (macOS Keychain Services, Linux Freedesktop Secret Service, Windows DPAPI).
// Implementations MUST NOT pass secret values on command-line arguments.
type NativeSecretStore interface {
	Name() string
	Available() bool
	GetSecret(ctx context.Context, service, account string) ([]byte, error)
	SetSecret(ctx context.Context, service, account string, secret []byte) error
	DeleteSecret(ctx context.Context, service, account string) error
}

// VaultSecurityInfo is a secret-free telemetry summary safe for Tier-0 Auth UI.
type VaultSecurityInfo struct {
	BackendName       string
	CipherSuite       string
	OSProtected       bool
	Persistent        bool
	Locked            bool
	VaultPath         string
	FallbackReason    string
}

// NativeVaultConfig configures OpenNativeVault.
type NativeVaultConfig struct {
	Service   string            // defaults to DefaultKeychainService
	Account   string            // defaults to DefaultKeychainAccount
	VaultPath string            // defaults to <os.UserConfigDir>/marlin/auth-vault.enc
	Store     NativeSecretStore // defaults to platform NativeSecretStore
}

// OSProtectedVault wraps EncryptedFileVault (AES-256-GCM, 0600) with a 32-byte
// master wrapping key stored strictly inside the OS Native Keychain.
// Supports Lock() (scrubs RAM master key) and Unlock(ctx) (re-fetches from OS).
type OSProtectedVault struct {
	store     NativeSecretStore
	service   string
	account   string
	vaultPath string

	mu     sync.Mutex
	inner  *EncryptedFileVault
	locked bool
	info   VaultSecurityInfo
}

// OpenNativeVault opens an OS-keychain-backed AES-256-GCM CredentialVault.
// If the OS keychain is unavailable (e.g. WebAssembly or headless container),
// it falls back gracefully to a scrubbed MemoryVault and reports exact status
// in VaultSecurityInfo (never writing unencrypted tokens to disk).
func OpenNativeVault(ctx context.Context, cfg NativeVaultConfig) (CredentialVault, VaultSecurityInfo) {
	service := strings.TrimSpace(cfg.Service)
	if service == "" {
		service = DefaultKeychainService
	}
	account := strings.TrimSpace(cfg.Account)
	if account == "" {
		account = DefaultKeychainAccount
	}
	store := cfg.Store
	if store == nil {
		store = defaultNativeSecretStore()
	}
	if store == nil || !store.Available() {
		reason := "OS keychain unavailable on this platform"
		if store != nil {
			reason = store.Name() + " unavailable"
		}
		return NewMemoryVault(), VaultSecurityInfo{
			BackendName:    "Memory Vault (Ephemeral)",
			CipherSuite:    "In-RAM Zero-Copy Scrubbed",
			OSProtected:    false,
			Persistent:     false,
			FallbackReason: reason,
		}
	}

	vaultPath := strings.TrimSpace(cfg.VaultPath)
	if vaultPath == "" {
		if dir, err := os.UserConfigDir(); err == nil && dir != "" {
			vaultPath = filepath.Join(dir, "marlin", "auth-vault.enc")
		} else {
			return NewMemoryVault(), VaultSecurityInfo{
				BackendName:    "Memory Vault (Ephemeral)",
				CipherSuite:    "In-RAM Zero-Copy Scrubbed",
				OSProtected:    false,
				Persistent:     false,
				FallbackReason: "user config directory unavailable",
			}
		}
	}

	v := &OSProtectedVault{
		store:     store,
		service:   service,
		account:   account,
		vaultPath: vaultPath,
		info: VaultSecurityInfo{
			BackendName: store.Name(),
			CipherSuite: "AES-256-GCM",
			OSProtected: true,
			Persistent:  true,
			VaultPath:   vaultPath,
		},
	}
	if err := v.Unlock(ctx); err != nil {
		return NewMemoryVault(), VaultSecurityInfo{
			BackendName:    "Memory Vault (Ephemeral)",
			CipherSuite:    "In-RAM Zero-Copy Scrubbed",
			OSProtected:    false,
			Persistent:     false,
			FallbackReason: fmt.Sprintf("%s init failed: %v", store.Name(), err),
		}
	}
	return v, v.SecurityInfo()
}

// Unlock retrieves (or generates and stores) the 32-byte master wrapping key
// in the OS Native Keychain and opens the AES-256-GCM encrypted file vault.
func (v *OSProtectedVault) Unlock(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.inner != nil && !v.locked {
		return nil
	}

	rawKey, err := v.store.GetSecret(ctx, v.service, v.account)
	if err != nil {
		if !errors.Is(err, ErrKeychainNotFound) {
			return err
		}
		// Generate a fresh 32-byte crypto/rand master wrapping key and seal it in OS Keychain.
		var fresh [32]byte
		if _, rErr := rand.Read(fresh[:]); rErr != nil {
			return fmt.Errorf("auth: generate vault master key: %w", rErr)
		}
		encoded := []byte(hex.EncodeToString(fresh[:]))
		defer zeroBytes(fresh[:])
		defer zeroBytes(encoded)
		if sErr := v.store.SetSecret(ctx, v.service, v.account, encoded); sErr != nil {
			return fmt.Errorf("auth: store master key in %s: %w", v.store.Name(), sErr)
		}
		rawKey = append([]byte(nil), encoded...)
	}
	defer zeroBytes(rawKey)

	masterKey, decErr := decodeKeyMaterial(rawKey)
	if decErr != nil {
		return decErr
	}
	defer zeroBytes(masterKey)

	enc, err := NewEncryptedFileVault(v.vaultPath, masterKey)
	if err != nil {
		return err
	}
	v.inner = enc
	v.locked = false
	v.info.Locked = false
	return nil
}

// Lock scrubs the in-memory AES-256-GCM master key immediately. Subsequent
// Store/Load calls fail-closed with ErrVaultLocked until Unlock(ctx) succeeds.
func (v *OSProtectedVault) Lock() {
	v.mu.Lock()
	if v.inner != nil {
		zeroBytes(v.inner.key[:])
		v.inner = nil
	}
	v.locked = true
	v.info.Locked = true
	v.mu.Unlock()
}

// SecurityInfo returns a secret-free status snapshot for Tier-0 UI rendering.
func (v *OSProtectedVault) SecurityInfo() VaultSecurityInfo {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.info
}

func (v *OSProtectedVault) Store(ctx context.Context, accountID AccountID, tokens TokenSet) error {
	v.mu.Lock()
	inner := v.inner
	locked := v.locked
	v.mu.Unlock()
	if locked || inner == nil {
		return ErrVaultLocked
	}
	return inner.Store(ctx, accountID, tokens)
}

func (v *OSProtectedVault) Load(ctx context.Context, accountID AccountID) (TokenSet, bool, error) {
	v.mu.Lock()
	inner := v.inner
	locked := v.locked
	v.mu.Unlock()
	if locked || inner == nil {
		return TokenSet{}, false, ErrVaultLocked
	}
	return inner.Load(ctx, accountID)
}

func (v *OSProtectedVault) Delete(ctx context.Context, accountID AccountID) error {
	v.mu.Lock()
	inner := v.inner
	locked := v.locked
	v.mu.Unlock()
	if locked || inner == nil {
		return ErrVaultLocked
	}
	return inner.Delete(ctx, accountID)
}

func (v *OSProtectedVault) ListAccountIDs(ctx context.Context) ([]AccountID, error) {
	v.mu.Lock()
	inner := v.inner
	locked := v.locked
	v.mu.Unlock()
	if locked || inner == nil {
		return nil, ErrVaultLocked
	}
	return inner.ListAccountIDs(ctx)
}

func (v *OSProtectedVault) StoreProfile(ctx context.Context, profile AccountProfile) error {
	v.mu.Lock()
	inner := v.inner
	locked := v.locked
	v.mu.Unlock()
	if locked || inner == nil {
		return ErrVaultLocked
	}
	return inner.StoreProfile(ctx, profile)
}

func (v *OSProtectedVault) LoadProfiles(ctx context.Context) ([]AccountProfile, AccountID, error) {
	v.mu.Lock()
	inner := v.inner
	locked := v.locked
	v.mu.Unlock()
	if locked || inner == nil {
		return nil, "", ErrVaultLocked
	}
	return inner.LoadProfiles(ctx)
}

func (v *OSProtectedVault) SetActiveAccount(ctx context.Context, accountID AccountID) error {
	v.mu.Lock()
	inner := v.inner
	locked := v.locked
	v.mu.Unlock()
	if locked || inner == nil {
		return ErrVaultLocked
	}
	return inner.SetActiveAccount(ctx, accountID)
}

func (v *OSProtectedVault) DeleteProfile(ctx context.Context, accountID AccountID) error {
	v.mu.Lock()
	inner := v.inner
	locked := v.locked
	v.mu.Unlock()
	if locked || inner == nil {
		return ErrVaultLocked
	}
	return inner.DeleteProfile(ctx, accountID)
}

func decodeKeyMaterial(raw []byte) ([]byte, error) {
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) == 64 {
		if decoded, err := hex.DecodeString(trimmed); err == nil && len(decoded) == 32 {
			return decoded, nil
		}
	}
	if len(raw) < 16 {
		return nil, fmt.Errorf("auth: OS keychain master key too short (%d bytes)", len(raw))
	}
	return append([]byte(nil), raw...), nil
}
