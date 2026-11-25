// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// CredentialVault persists sensitive TokenSet values behind a pluggable
// platform boundary (OS Keychain, AES-256-GCM local vault, or scrubbed memory).
type CredentialVault interface {
	Store(ctx context.Context, accountID AccountID, tokens TokenSet) error
	Load(ctx context.Context, accountID AccountID) (TokenSet, bool, error)
	Delete(ctx context.Context, accountID AccountID) error
	ListAccountIDs(ctx context.Context) ([]AccountID, error)
}

// MemoryVault is a cross-platform (Desktop, WebAssembly, iOS, Android) thread-safe
// credential store that clones values on read/write and clears entries on Delete.
type MemoryVault struct {
	mu     sync.RWMutex
	tokens map[AccountID]TokenSet
}

func NewMemoryVault() *MemoryVault {
	return &MemoryVault{
		tokens: make(map[AccountID]TokenSet),
	}
}

func (v *MemoryVault) Store(_ context.Context, accountID AccountID, tokens TokenSet) error {
	if accountID == "" {
		return fmt.Errorf("auth: empty AccountID")
	}
	v.mu.Lock()
	v.tokens[accountID] = tokens.Clone()
	v.mu.Unlock()
	return nil
}

func (v *MemoryVault) Load(_ context.Context, accountID AccountID) (TokenSet, bool, error) {
	v.mu.RLock()
	tok, ok := v.tokens[accountID]
	v.mu.RUnlock()
	if !ok {
		return TokenSet{}, false, nil
	}
	return tok.Clone(), true, nil
}

func (v *MemoryVault) Delete(_ context.Context, accountID AccountID) error {
	v.mu.Lock()
	if tok, ok := v.tokens[accountID]; ok {
		tok.AccessToken = ""
		tok.RefreshToken = ""
		tok.IDToken = ""
		delete(v.tokens, accountID)
	}
	v.mu.Unlock()
	return nil
}

func (v *MemoryVault) ListAccountIDs(_ context.Context) ([]AccountID, error) {
	v.mu.RLock()
	ids := make([]AccountID, 0, len(v.tokens))
	for id := range v.tokens {
		ids = append(ids, id)
	}
	v.mu.RUnlock()
	slices.Sort(ids)
	return ids, nil
}

// EncryptedFileVault stores TokenSets on disk using AES-256-GCM authenticated
// encryption with strict 0600 file permissions and atomic temp-file replacement.
// Plaintext tokens are never written to disk.
type EncryptedFileVault struct {
	path string
	key  [32]byte
	mu   sync.Mutex
}

// NewEncryptedFileVault creates an AES-256-GCM vault at filePath keyed by masterKey
// (derived via SHA-256 if len(masterKey) != 32; rejects empty masterKey fail-closed).
func NewEncryptedFileVault(filePath string, masterKey []byte) (*EncryptedFileVault, error) {
	if filePath == "" {
		return nil, fmt.Errorf("auth: vault file path is required")
	}
	if len(masterKey) < 16 {
		return nil, fmt.Errorf("auth: vault master key must be at least 16 bytes")
	}
	var derived [32]byte
	if len(masterKey) == 32 {
		copy(derived[:], masterKey)
	} else {
		derived = sha256.Sum256(masterKey)
	}
	return &EncryptedFileVault{
		path: filePath,
		key:  derived,
	}, nil
}

func (v *EncryptedFileVault) Store(_ context.Context, accountID AccountID, tokens TokenSet) error {
	if accountID == "" {
		return fmt.Errorf("auth: empty AccountID")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	m, err := v.readMapLocked()
	if err != nil {
		return err
	}
	m[accountID] = tokens.Clone()
	return v.writeMapLocked(m)
}

func (v *EncryptedFileVault) Load(_ context.Context, accountID AccountID) (TokenSet, bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	m, err := v.readMapLocked()
	if err != nil {
		return TokenSet{}, false, err
	}
	tok, ok := m[accountID]
	if !ok {
		return TokenSet{}, false, nil
	}
	return tok.Clone(), true, nil
}

func (v *EncryptedFileVault) Delete(_ context.Context, accountID AccountID) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	m, err := v.readMapLocked()
	if err != nil {
		return err
	}
	delete(m, accountID)
	return v.writeMapLocked(m)
}

func (v *EncryptedFileVault) ListAccountIDs(_ context.Context) ([]AccountID, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	m, err := v.readMapLocked()
	if err != nil {
		return nil, err
	}
	ids := make([]AccountID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

var vaultMagic = []byte("MRLV1")

func (v *EncryptedFileVault) readMapLocked() (map[AccountID]TokenSet, error) {
	raw, err := os.ReadFile(v.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return make(map[AccountID]TokenSet), nil
		}
		return nil, err
	}
	if len(raw) < len(vaultMagic)+12+16 || string(raw[:len(vaultMagic)]) != string(vaultMagic) {
		return nil, fmt.Errorf("auth: corrupted or unrecognized encrypted vault header")
	}
	block, err := aes.NewCipher(v.key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	payload := raw[len(vaultMagic):]
	nonceSize := gcm.NonceSize()
	nonce, ciphertext := payload[:nonceSize], payload[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, vaultMagic)
	if err != nil {
		return nil, fmt.Errorf("auth: vault decryption/authentication failed: %w", err)
	}
	defer zeroBytes(plaintext)

	m := make(map[AccountID]TokenSet)
	if err := json.Unmarshal(plaintext, &m); err != nil {
		return nil, fmt.Errorf("auth: decode vault payload: %w", err)
	}
	return m, nil
}

func (v *EncryptedFileVault) writeMapLocked(m map[AccountID]TokenSet) error {
	if err := os.MkdirAll(filepath.Dir(v.path), 0o700); err != nil {
		return err
	}
	plaintext, err := json.Marshal(m)
	if err != nil {
		return err
	}
	defer zeroBytes(plaintext)

	block, err := aes.NewCipher(v.key[:])
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, vaultMagic)

	out := make([]byte, 0, len(vaultMagic)+len(nonce)+len(ciphertext))
	out = append(out, vaultMagic...)
	out = append(out, nonce...)
	out = append(out, ciphertext...)

	tmpPath := v.path + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, v.path)
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
