// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package device bridges per-target platform.* capability adapters (iOS,
// Android, Desktop) into marlin.device.v1.DeviceBridgeService under the
// uniform device.* capability namespace.
package device

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

var (
	ErrCapabilityUnknown   = errors.New("device: capability not registered on this target")
	ErrPermissionDenied    = errors.New("device: OS permission denied or unsupported")
	ErrGateReceiptMissing  = errors.New("device: non-read-only capability requires Rust gate receipt")
	ErrGateReceiptReplayed = errors.New("device: one-shot Rust gate receipt already consumed")
)

// OsPermissionState mirrors marlin.device.v1.OsPermissionState.
type OsPermissionState string

const (
	PermissionGranted     OsPermissionState = "granted"
	PermissionLimited     OsPermissionState = "limited"
	PermissionPrompt      OsPermissionState = "prompt"
	PermissionDenied      OsPermissionState = "denied"
	PermissionUnsupported OsPermissionState = "unsupported"
)

// CapabilityDescriptor mirrors marlin.device.v1.DeviceCapabilityDescriptor.
type CapabilityDescriptor struct {
	CapabilityID   schema.CapabilityID `json:"capabilityId"`
	ProtocolFamily string              `json:"protocolFamily"`
	Permission     OsPermissionState   `json:"permission"`
	InputSchemaID  schema.SchemaID     `json:"inputSchemaId"`
	OutputSchemaID schema.SchemaID     `json:"outputSchemaId"`
	ReadOnly       bool                `json:"readOnly"`
}

// InvocationRequest mirrors marlin.device.v1.DeviceInvocationRequest.
type InvocationRequest struct {
	InvocationID  string              `json:"invocationId"`
	SessionID     schema.SessionID    `json:"sessionId"`
	CapabilityID  schema.CapabilityID `json:"capabilityId"`
	GateReceiptID string              `json:"gateReceiptId,omitempty"`
	Args          json.RawMessage     `json:"args"`
}

// InvocationResult mirrors marlin.device.v1.DeviceInvocationResult.
type InvocationResult struct {
	InvocationID string          `json:"invocationId"`
	OK           bool            `json:"ok"`
	Result       json.RawMessage `json:"result,omitempty"`
	ErrorMessage string          `json:"errorMessage,omitempty"`
}

// CapabilityProvider is implemented per platform protocol family (platform.ios,
// platform.android, platform.desktop) behind clean device.* capability IDs.
type CapabilityProvider interface {
	Family() string
	Descriptors(ctx context.Context) ([]CapabilityDescriptor, error)
	Invoke(ctx context.Context, req InvocationRequest) (InvocationResult, error)
}

const defaultMaxConsumedReceipts = 4096

// Bridge validates OS permissions and one-shot Rust gate receipts before
// dispatching any host-originated device.* invocation to platform adapters.
// Consumed receipt IDs are retained in a bounded FIFO ring (default 4,096)
// so long-lived desktop processes never grow memory monotonically; Rust
// authority gate receipts are short-lived and bound to session generation.
type Bridge struct {
	targetKind          schema.TargetKind
	mu                  sync.RWMutex
	providers           map[schema.CapabilityID]CapabilityProvider
	consumedReceipts    map[string]struct{}
	receiptRing         []string
	receiptHead         int
	maxConsumedReceipts int
}

func NewBridge(kind schema.TargetKind) *Bridge {
	return &Bridge{
		targetKind:          kind,
		providers:           make(map[schema.CapabilityID]CapabilityProvider),
		consumedReceipts:    make(map[string]struct{}),
		maxConsumedReceipts: defaultMaxConsumedReceipts,
	}
}

// SetMaxConsumedReceipts bounds the FIFO consumed-receipt deduplication ring.
func (b *Bridge) SetMaxConsumedReceipts(limit int) {
	if limit < 1 {
		limit = 1
	}
	b.mu.Lock()
	b.maxConsumedReceipts = limit
	for len(b.receiptRing) > limit {
		oldest := b.receiptRing[b.receiptHead]
		delete(b.consumedReceipts, oldest)
		b.receiptRing = append(b.receiptRing[:b.receiptHead], b.receiptRing[b.receiptHead+1:]...)
		if b.receiptHead >= len(b.receiptRing) {
			b.receiptHead = 0
		}
	}
	b.mu.Unlock()
}

// ConsumedReceiptCount reports the current size of the consumed-receipt cache.
func (b *Bridge) ConsumedReceiptCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.consumedReceipts)
}

func (b *Bridge) TargetKind() schema.TargetKind { return b.targetKind }

func (b *Bridge) RegisterProvider(ctx context.Context, p CapabilityProvider) error {
	descs, err := p.Descriptors(ctx)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, d := range descs {
		if err := schema.ValidateDotIdentifier(string(d.CapabilityID)); err != nil {
			return fmt.Errorf("device: invalid capability %q: %w", d.CapabilityID, err)
		}
		b.providers[d.CapabilityID] = p
	}
	return nil
}

// SnapshotDescriptors collects fresh OS permission states across all registered providers.
func (b *Bridge) SnapshotDescriptors(ctx context.Context) ([]CapabilityDescriptor, error) {
	b.mu.RLock()
	seen := make(map[CapabilityProvider]struct{})
	list := make([]CapabilityProvider, 0)
	for _, p := range b.providers {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			list = append(list, p)
		}
	}
	b.mu.RUnlock()

	var out []CapabilityDescriptor
	for _, p := range list {
		descs, err := p.Descriptors(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, descs...)
	}
	return out, nil
}

// Dispatch verifies capability ownership, live OS permission state, and
// mandatory Rust gate receipt on mutating capabilities before invoking native code.
func (b *Bridge) Dispatch(ctx context.Context, req InvocationRequest) (InvocationResult, error) {
	b.mu.RLock()
	provider, ok := b.providers[req.CapabilityID]
	b.mu.RUnlock()
	if !ok {
		return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: ErrCapabilityUnknown.Error()}, ErrCapabilityUnknown
	}
	descs, err := provider.Descriptors(ctx)
	if err != nil {
		return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: err.Error()}, err
	}
	var matched *CapabilityDescriptor
	for i := range descs {
		if descs[i].CapabilityID == req.CapabilityID {
			matched = &descs[i]
			break
		}
	}
	if matched == nil {
		return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: ErrCapabilityUnknown.Error()}, ErrCapabilityUnknown
	}
	if matched.Permission == PermissionDenied || matched.Permission == PermissionUnsupported {
		return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: ErrPermissionDenied.Error()}, ErrPermissionDenied
	}
	if !matched.ReadOnly {
		if req.GateReceiptID == "" {
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: ErrGateReceiptMissing.Error()}, ErrGateReceiptMissing
		}
		b.mu.Lock()
		if _, used := b.consumedReceipts[req.GateReceiptID]; used {
			b.mu.Unlock()
			return InvocationResult{InvocationID: req.InvocationID, OK: false, ErrorMessage: ErrGateReceiptReplayed.Error()}, ErrGateReceiptReplayed
		}
		limit := b.maxConsumedReceipts
		if limit <= 0 {
			limit = defaultMaxConsumedReceipts
		}
		if len(b.receiptRing) >= limit {
			evicted := b.receiptRing[b.receiptHead]
			delete(b.consumedReceipts, evicted)
			b.receiptRing[b.receiptHead] = req.GateReceiptID
			b.receiptHead++
			if b.receiptHead >= len(b.receiptRing) {
				b.receiptHead = 0
			}
		} else {
			b.receiptRing = append(b.receiptRing, req.GateReceiptID)
		}
		b.consumedReceipts[req.GateReceiptID] = struct{}{}
		b.mu.Unlock()
	}
	return provider.Invoke(ctx, req)
}
