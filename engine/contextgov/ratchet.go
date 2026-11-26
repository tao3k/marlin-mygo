// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package contextgov

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrEpochKeyZeroized   = errors.New("contextgov: epoch key has been zeroized (forward secrecy enforced)")
	ErrRatchetDecryptFail = errors.New("contextgov: failed to authenticate/decrypt message under epoch key")
	ErrRatchetEpochMismatch = errors.New("contextgov: ciphertext epoch does not match key epoch")
)

// ChannelEpochCiphertext carries an authenticated AES-256-GCM payload bound to
// its exact MLS ratchet epoch and key generation.
type ChannelEpochCiphertext struct {
	Epoch      uint64
	KeyGen     uint64
	Nonce      []byte
	Ciphertext []byte
}

// ChannelKeyRatchet implements MLS-style (RFC 9420) symmetric epoch key
// evolution for shared-context channels:
//   - Forward Secrecy (FS): advancing an epoch derives a new 32-byte key and
//     explicitly zeroes out the previous epoch key bytes in memory.
//   - Post-Compromise Security (PCS): HealPCS mixes fresh OS CSPRNG entropy so
//     an attacker holding a compromised prior key state cannot derive future keys.
//   - Member Removal Rotation: RemoveMemberAndRotate atomically revokes channel
//     membership in GovernanceStore (when attached) and rotates to a fresh epoch key
//     so removed members cannot decrypt any subsequent channel context.
type ChannelKeyRatchet struct {
	mu          sync.Mutex
	tenantID    string
	channelID   string
	store       *GovernanceStore
	epoch       uint64
	keyGen      uint64
	currentKey  [32]byte
	memberKeys  map[string][32]byte // per-member active key copy (frozen upon removal)
	memberGen   map[string]uint64
}

// NewChannelKeyRatchet initializes epoch 1 with fresh cryptographic randomness.
func NewChannelKeyRatchet(tenantID, channelID string, store *GovernanceStore, initialMembers ...string) (*ChannelKeyRatchet, error) {
	var initKey [32]byte
	if _, err := rand.Read(initKey[:]); err != nil {
		return nil, fmt.Errorf("contextgov: ratchet entropy: %w", err)
	}
	r := &ChannelKeyRatchet{
		tenantID:   tenantID,
		channelID:  channelID,
		store:      store,
		epoch:      1,
		keyGen:     1,
		currentKey: initKey,
		memberKeys: make(map[string][32]byte),
		memberGen:  make(map[string]uint64),
	}
	for _, m := range initialMembers {
		r.memberKeys[m] = initKey
		r.memberGen[m] = 1
		if store != nil {
			store.RegisterChannelMember(tenantID, channelID, m)
		}
	}
	return r, nil
}

func deriveNextKey(cur [32]byte, epoch uint64, freshEntropy []byte) [32]byte {
	h := sha256.New()
	h.Write([]byte("marlin.contextgov.mls-ratchet.v1\x00"))
	h.Write(cur[:])
	var epBuf [8]byte
	for i := 0; i < 8; i++ {
		epBuf[i] = byte(epoch >> (56 - 8*i))
	}
	h.Write(epBuf[:])
	if len(freshEntropy) > 0 {
		h.Write(freshEntropy)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// AdvanceEpoch ratchets the channel key forward and explicitly zeroizes the old
// key bytes (Forward Secrecy), updating active (non-removed) members.
func (r *ChannelKeyRatchet) AdvanceEpoch() (epoch, keyGen uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.epoch++
	r.keyGen++
	next := deriveNextKey(r.currentKey, r.epoch, nil)
	clear(r.currentKey[:])
	r.currentKey = next
	for m := range r.memberKeys {
		r.memberKeys[m] = next
		r.memberGen[m] = r.keyGen
	}
	return r.epoch, r.keyGen
}

// HealPCS performs a Post-Compromise Security commit by mixing 32 bytes of fresh
// CSPRNG entropy into the epoch key derivation and zeroizing the compromised key.
func (r *ChannelKeyRatchet) HealPCS() (epoch, keyGen uint64, err error) {
	var fresh [32]byte
	if _, err = rand.Read(fresh[:]); err != nil {
		return 0, 0, fmt.Errorf("contextgov: pcs entropy: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.epoch++
	r.keyGen++
	next := deriveNextKey(r.currentKey, r.epoch, fresh[:])
	clear(r.currentKey[:])
	clear(fresh[:])
	r.currentKey = next
	for m := range r.memberKeys {
		r.memberKeys[m] = next
		r.memberGen[m] = r.keyGen
	}
	return r.epoch, r.keyGen, nil
}

// RemoveMemberAndRotate removes memberID from active key distribution (and from
// GovernanceStore ACL if attached), rotating the epoch key with fresh entropy so
// the removed member's frozen key can never decrypt future messages.
func (r *ChannelKeyRatchet) RemoveMemberAndRotate(memberID string) (epoch, keyGen uint64, removedKey [32]byte, err error) {
	var fresh [32]byte
	if _, err = rand.Read(fresh[:]); err != nil {
		return 0, 0, removedKey, fmt.Errorf("contextgov: remove entropy: %w", err)
	}
	r.mu.Lock()
	removedKey = r.memberKeys[memberID]
	delete(r.memberKeys, memberID)
	delete(r.memberGen, memberID)
	r.epoch++
	r.keyGen++
	next := deriveNextKey(r.currentKey, r.epoch, fresh[:])
	clear(r.currentKey[:])
	clear(fresh[:])
	r.currentKey = next
	for m := range r.memberKeys {
		r.memberKeys[m] = next
		r.memberGen[m] = r.keyGen
	}
	ep, kg := r.epoch, r.keyGen
	store := r.store
	tid, cid := r.tenantID, r.channelID
	r.mu.Unlock()

	if store != nil {
		store.RemoveChannelMember(tid, cid, memberID)
	}
	return ep, kg, removedKey, nil
}

// CompromiseCurrentKey returns a copy of the current epoch key (modeling a
// point-in-time state compromise for FS/PCS adversarial tests).
func (r *ChannelKeyRatchet) CompromiseCurrentKey() (key [32]byte, epoch, keyGen uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.currentKey, r.epoch, r.keyGen
}

// Seal encrypts plaintext under the current epoch key using AES-256-GCM with
// epoch/keyGen bound into AAD so ciphertexts cannot be replayed across epochs.
func (r *ChannelKeyRatchet) Seal(plaintext []byte) (ChannelEpochCiphertext, error) {
	r.mu.Lock()
	key := r.currentKey
	ep := r.epoch
	kg := r.keyGen
	r.mu.Unlock()

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return ChannelEpochCiphertext{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ChannelEpochCiphertext{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return ChannelEpochCiphertext{}, err
	}
	aad := []byte(fmt.Sprintf("%s::%s::%d::%d", r.tenantID, r.channelID, ep, kg))
	ct := gcm.Seal(nil, nonce, plaintext, aad)
	return ChannelEpochCiphertext{
		Epoch:      ep,
		KeyGen:     kg,
		Nonce:      nonce,
		Ciphertext: ct,
	}, nil
}

// OpenWithKey attempts to decrypt msg using an arbitrary 32-byte key (e.g. an
// attacker's compromised snapshot or a removed member's frozen key).
func (r *ChannelKeyRatchet) OpenWithKey(key [32]byte, msg ChannelEpochCiphertext) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(msg.Nonce) != gcm.NonceSize() {
		return nil, ErrRatchetDecryptFail
	}
	aad := []byte(fmt.Sprintf("%s::%s::%d::%d", r.tenantID, r.channelID, msg.Epoch, msg.KeyGen))
	pt, err := gcm.Open(nil, msg.Nonce, msg.Ciphertext, aad)
	if err != nil {
		return nil, ErrRatchetDecryptFail
	}
	return pt, nil
}

// OpenAsMember attempts to decrypt msg using memberID's currently held key.
func (r *ChannelKeyRatchet) OpenAsMember(memberID string, msg ChannelEpochCiphertext) ([]byte, error) {
	r.mu.Lock()
	key, ok := r.memberKeys[memberID]
	r.mu.Unlock()
	if !ok {
		return nil, ErrContextAccessRevoked
	}
	return r.OpenWithKey(key, msg)
}

// State returns current (epoch, keyGen).
func (r *ChannelKeyRatchet) State() (epoch, keyGen uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.epoch, r.keyGen
}
