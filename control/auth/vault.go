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

// ProfileVault is an optional extension implemented by vaults capable of
// persisting secret-free AccountProfile metadata inside the same encrypted
// envelope so signed-in sessions rehydrate seamlessly across app restarts.
type ProfileVault interface {
	StoreProfile(ctx context.Context, profile AccountProfile) error
	LoadProfiles(ctx context.Context) ([]AccountProfile, AccountID, error)
	SetActiveAccount(ctx context.Context, accountID AccountID) error
	DeleteProfile(ctx context.Context, accountID AccountID) error
}

// MemoryVault is a cross-platform (Desktop, WebAssembly, iOS, Android) thread-safe
// credential store that clones values on read/write and clears entries on Delete.
type MemoryVault struct {
	mu       sync.RWMutex
	tokens   map[AccountID]TokenSet
	profiles map[AccountID]AccountProfile
	activeID AccountID
}

func NewMemoryVault() *MemoryVault {
	return &MemoryVault{
		tokens:   make(map[AccountID]TokenSet),
		profiles: make(map[AccountID]AccountProfile),
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

func (v *MemoryVault) StoreProfile(_ context.Context, profile AccountProfile) error {
	if profile.ID == "" {
		return fmt.Errorf("auth: empty AccountID")
	}
	v.mu.Lock()
	cp := profile
	if len(profile.Scopes) > 0 {
		cp.Scopes = append([]string(nil), profile.Scopes...)
	}
	v.profiles[profile.ID] = cp
	v.activeID = profile.ID
	v.mu.Unlock()
	return nil
}

func (v *MemoryVault) LoadProfiles(_ context.Context) ([]AccountProfile, AccountID, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	ids := make([]AccountID, 0, len(v.profiles))
	for id := range v.profiles {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]AccountProfile, 0, len(ids))
	for _, id := range ids {
		p := v.profiles[id]
		if len(p.Scopes) > 0 {
			p.Scopes = append([]string(nil), p.Scopes...)
		}
		out = append(out, p)
	}
	return out, v.activeID, nil
}

func (v *MemoryVault) SetActiveAccount(_ context.Context, accountID AccountID) error {
	v.mu.Lock()
	v.activeID = accountID
	v.mu.Unlock()
	return nil
}

func (v *MemoryVault) DeleteProfile(_ context.Context, accountID AccountID) error {
	v.mu.Lock()
	delete(v.profiles, accountID)
	if v.activeID == accountID {
		v.activeID = ""
	}
	v.mu.Unlock()
	return nil
}

// EncryptedFileVault stores TokenSets on disk using AES-256-GCM authenticated
// encryption with strict 0600 file permissions and atomic temp-file replacement.
// Plaintext tokens are never written to disk.
type EncryptedFileVault struct {
	path        string
	key         [32]byte
	mu          sync.Mutex
	minRevision uint64
}

// Revision returns the highest monotonic vault revision observed or written.
func (v *EncryptedFileVault) Revision() uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.minRevision
}

// SetMinRevision pins the minimum acceptable monotonic vault revision so
// replaying an older encrypted vault snapshot fails closed with ErrVaultRollback.
func (v *EncryptedFileVault) SetMinRevision(rev uint64) {
	v.mu.Lock()
	if rev > v.minRevision {
		v.minRevision = rev
	}
	v.mu.Unlock()
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

type encryptedVaultDoc struct {
	Version         int                          `json:"v"`
	Revision        uint64                       `json:"rev,omitempty"`
	Tokens          map[AccountID]TokenSet       `json:"tokens"`
	Profiles        map[AccountID]AccountProfile `json:"profiles,omitempty"`
	ActiveAccountID AccountID                    `json:"active_account_id,omitempty"`
}

func (v *EncryptedFileVault) Store(_ context.Context, accountID AccountID, tokens TokenSet) error {
	if accountID == "" {
		return fmt.Errorf("auth: empty AccountID")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	doc, err := v.readDocLocked()
	if err != nil {
		return err
	}
	doc.Tokens[accountID] = tokens.Clone()
	return v.writeDocLocked(doc)
}

func (v *EncryptedFileVault) Load(_ context.Context, accountID AccountID) (TokenSet, bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	doc, err := v.readDocLocked()
	if err != nil {
		return TokenSet{}, false, err
	}
	tok, ok := doc.Tokens[accountID]
	if !ok {
		return TokenSet{}, false, nil
	}
	return tok.Clone(), true, nil
}

func (v *EncryptedFileVault) Delete(_ context.Context, accountID AccountID) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	doc, err := v.readDocLocked()
	if err != nil {
		return err
	}
	delete(doc.Tokens, accountID)
	delete(doc.Profiles, accountID)
	if doc.ActiveAccountID == accountID {
		doc.ActiveAccountID = ""
	}
	return v.writeDocLocked(doc)
}

func (v *EncryptedFileVault) ListAccountIDs(_ context.Context) ([]AccountID, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	doc, err := v.readDocLocked()
	if err != nil {
		return nil, err
	}
	ids := make([]AccountID, 0, len(doc.Tokens))
	for id := range doc.Tokens {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

func (v *EncryptedFileVault) StoreProfile(_ context.Context, profile AccountProfile) error {
	if profile.ID == "" {
		return fmt.Errorf("auth: empty AccountID")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	doc, err := v.readDocLocked()
	if err != nil {
		return err
	}
	cp := profile
	if len(profile.Scopes) > 0 {
		cp.Scopes = append([]string(nil), profile.Scopes...)
	}
	doc.Profiles[profile.ID] = cp
	doc.ActiveAccountID = profile.ID
	return v.writeDocLocked(doc)
}

func (v *EncryptedFileVault) LoadProfiles(_ context.Context) ([]AccountProfile, AccountID, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	doc, err := v.readDocLocked()
	if err != nil {
		return nil, "", err
	}
	ids := make([]AccountID, 0, len(doc.Profiles))
	for id := range doc.Profiles {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]AccountProfile, 0, len(ids))
	for _, id := range ids {
		p := doc.Profiles[id]
		if len(p.Scopes) > 0 {
			p.Scopes = append([]string(nil), p.Scopes...)
		}
		out = append(out, p)
	}
	return out, doc.ActiveAccountID, nil
}

func (v *EncryptedFileVault) SetActiveAccount(_ context.Context, accountID AccountID) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	doc, err := v.readDocLocked()
	if err != nil {
		return err
	}
	doc.ActiveAccountID = accountID
	return v.writeDocLocked(doc)
}

func (v *EncryptedFileVault) DeleteProfile(_ context.Context, accountID AccountID) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	doc, err := v.readDocLocked()
	if err != nil {
		return err
	}
	delete(doc.Profiles, accountID)
	if doc.ActiveAccountID == accountID {
		doc.ActiveAccountID = ""
	}
	return v.writeDocLocked(doc)
}

var vaultMagic = []byte("MRLV1")

func (v *EncryptedFileVault) readDocLocked() (*encryptedVaultDoc, error) {
	emptyDoc := func() *encryptedVaultDoc {
		return &encryptedVaultDoc{
			Version:  2,
			Tokens:   make(map[AccountID]TokenSet),
			Profiles: make(map[AccountID]AccountProfile),
		}
	}
	raw, err := os.ReadFile(v.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyDoc(), nil
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

	var doc encryptedVaultDoc
	if err := json.Unmarshal(plaintext, &doc); err == nil && doc.Version >= 2 {
		if v.minRevision > 0 && doc.Revision < v.minRevision {
			return nil, fmt.Errorf("%w: got %d want >= %d", ErrVaultRollback, doc.Revision, v.minRevision)
		}
		if doc.Revision > v.minRevision {
			v.minRevision = doc.Revision
		}
		if doc.Tokens == nil {
			doc.Tokens = make(map[AccountID]TokenSet)
		}
		if doc.Profiles == nil {
			doc.Profiles = make(map[AccountID]AccountProfile)
		}
		return &doc, nil
	}

	// Legacy v1 vault fallback (plain map[AccountID]TokenSet).
	m := make(map[AccountID]TokenSet)
	if err := json.Unmarshal(plaintext, &m); err != nil {
		return nil, fmt.Errorf("auth: decode vault payload: %w", err)
	}
	out := emptyDoc()
	out.Tokens = m
	return out, nil
}

func (v *EncryptedFileVault) writeDocLocked(doc *encryptedVaultDoc) error {
	if err := os.MkdirAll(filepath.Dir(v.path), 0o700); err != nil {
		return err
	}
	doc.Version = 2
	if doc.Revision < v.minRevision {
		doc.Revision = v.minRevision
	}
	doc.Revision++
	v.minRevision = doc.Revision
	plaintext, err := json.Marshal(doc)
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
