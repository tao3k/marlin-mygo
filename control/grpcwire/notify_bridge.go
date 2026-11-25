// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"fmt"
	"sync"

	"google.golang.org/grpc/connectivity"

	"github.com/tao3k/marlin-mygo/control/auth"
	"github.com/tao3k/marlin-mygo/engine/notify"
	gatev1 "github.com/tao3k/marlin-mygo/gen/marlin/gate/v1"
)

// NotifyBridge connects Marlin's live gRPC lifecycle events (ChannelManager,
// ResilientSidecarTarget, SessionDriver Approval Gates) and Tier-0 AuthManager
// transitions into engine/notify.Center with deduplication and actionable CTAs.
type NotifyBridge struct {
	Center *notify.Center

	mu          sync.Mutex
	lastChanSt  map[string]connectivity.State
	lastChanGen map[string]uint64
	lastAuthPh  auth.AuthPhase
	lastAcctID  auth.AccountID
	lastAuthErr string
}

// NewNotifyBridge constructs a deduplicating telemetry bridge into center.
func NewNotifyBridge(center *notify.Center) *NotifyBridge {
	return &NotifyBridge{
		Center:      center,
		lastChanSt:  make(map[string]connectivity.State),
		lastChanGen: make(map[string]uint64),
	}
}

// AttachChannelManager subscribes to all managed HTTP/2 gRPC channel state
// transitions and atomic target swaps, returning an unsubscribe function.
func (b *NotifyBridge) AttachChannelManager(mgr *ChannelManager) func() {
	if b == nil || b.Center == nil || mgr == nil {
		return func() {}
	}
	return mgr.Subscribe(func(st ChannelStatus) {
		b.handleChannelStatus(st)
	})
}

func (b *NotifyBridge) handleChannelStatus(st ChannelStatus) {
	b.mu.Lock()
	prevSt, seen := b.lastChanSt[st.ID]
	prevGen := b.lastChanGen[st.ID]
	b.lastChanSt[st.ID] = st.State
	b.lastChanGen[st.ID] = st.Generation
	b.mu.Unlock()

	dedupe := "grpc:channel:" + st.ID
	if seen && st.Generation > prevGen {
		_, _ = b.Center.Publish(notify.NotificationSpec{
			Severity:  notify.SeveritySuccess,
			Source:    notify.SourceGRPC,
			Title:     fmt.Sprintf("Channel %s swapped to generation #%d", st.ID, st.Generation),
			Message:   fmt.Sprintf("All live streams seamlessly moved to %s", st.Address),
			DedupeKey: dedupe,
		})
		return
	}

	switch st.State {
	case connectivity.TransientFailure:
		_, _ = b.Center.Publish(notify.NotificationSpec{
			Severity:  notify.SeverityWarning,
			Source:    notify.SourceGRPC,
			Title:     fmt.Sprintf("gRPC channel %s reconnecting", st.ID),
			Message:   fmt.Sprintf("Transient transport interruption on %s — automatic backoff active", st.Address),
			DedupeKey: dedupe,
			Actions: []notify.Action{
				{ID: "wake-channels", Label: "Reconnect Now", Primary: true},
			},
		})
	case connectivity.Ready:
		if seen && prevSt == connectivity.TransientFailure {
			_, _ = b.Center.Publish(notify.NotificationSpec{
				Severity:  notify.SeveritySuccess,
				Source:    notify.SourceGRPC,
				Title:     fmt.Sprintf("gRPC channel %s restored", st.ID),
				Message:   fmt.Sprintf("HTTP/2 stream healthy on %s", st.Address),
				DedupeKey: dedupe,
			})
		}
	}
}

// OnSidecarCrash records a deduplicated warning when the supervised Rust App Server exits unexpectedly.
func (b *NotifyBridge) OnSidecarCrash(name string, attempt int, exitErr error) {
	if b == nil || b.Center == nil {
		return
	}
	msg := fmt.Sprintf("Auto-restarting with rotated ephemeral Bearer token (attempt #%d)", attempt)
	if exitErr != nil {
		msg = fmt.Sprintf("Attempt #%d (%v) — rotating ephemeral Bearer token", attempt, exitErr)
	}
	_, _ = b.Center.Publish(notify.NotificationSpec{
		Severity:  notify.SeverityWarning,
		Source:    notify.SourceGRPC,
		Title:     fmt.Sprintf("%s exited unexpectedly", name),
		Message:   msg,
		DedupeKey: "grpc:sidecar:" + name,
	})
}

// OnSidecarRecovered replaces the crash warning with a green recovery notice once respawned.
func (b *NotifyBridge) OnSidecarRecovered(name, address string, restarts int) {
	if b == nil || b.Center == nil {
		return
	}
	_, _ = b.Center.Publish(notify.NotificationSpec{
		Severity:  notify.SeveritySuccess,
		Source:    notify.SourceGRPC,
		Title:     fmt.Sprintf("%s auto-recovered", name),
		Message:   fmt.Sprintf("Restart #%d live on %s with rotated Bearer token", restarts, address),
		DedupeKey: "grpc:sidecar:" + name,
	})
}

// OnPendingGate publishes a persistent DND-breakthrough Tier-0 Approval Gate alert.
func (b *NotifyBridge) OnPendingGate(g *gatev1.PendingGate) {
	if b == nil || b.Center == nil || g == nil || g.GetGateId() == "" {
		return
	}
	tool := g.GetToolOrNodeName()
	if tool == "" {
		tool = g.GetGateId()
	}
	_, _ = b.Center.Publish(notify.NotificationSpec{
		Severity:   notify.SeverityWarning,
		Source:     notify.SourceGate,
		Title:      fmt.Sprintf("Approval Gate: %s", tool),
		Message:    g.GetPrompt(),
		DedupeKey:  "gate:" + g.GetGateId(),
		Persistent: true,
		Actions: []notify.Action{
			{ID: "review-gate", Label: "Review Gate", Primary: true},
		},
	})
}

// AttachAuthManager subscribes to secret-free AuthManager transitions and emits
// deduplicated sign-in, sign-out, and auth-error notifications (never tokens!).
func (b *NotifyBridge) AttachAuthManager(mgr *auth.AuthManager) func() {
	if b == nil || b.Center == nil || mgr == nil {
		return func() {}
	}
	initSnap := mgr.Snapshot()
	b.mu.Lock()
	b.lastAuthPh = initSnap.Phase
	b.lastAcctID = initSnap.ActiveAccountID
	b.lastAuthErr = initSnap.LastError
	b.mu.Unlock()

	return mgr.Subscribe(func(snap auth.AuthSnapshot) {
		b.handleAuthSnapshot(snap)
	})
}

func (b *NotifyBridge) handleAuthSnapshot(snap auth.AuthSnapshot) {
	b.mu.Lock()
	prevPh := b.lastAuthPh
	prevAcct := b.lastAcctID
	prevErr := b.lastAuthErr
	b.lastAuthPh = snap.Phase
	b.lastAcctID = snap.ActiveAccountID
	b.lastAuthErr = snap.LastError
	b.mu.Unlock()

	if snap.Phase == auth.PhaseSignedIn && snap.ActiveAccountID != "" && (prevAcct != snap.ActiveAccountID || prevPh != auth.PhaseSignedIn) {
		if prof, ok := snap.ActiveProfile(); ok {
			_, _ = b.Center.Publish(notify.NotificationSpec{
				Severity:  notify.SeveritySuccess,
				Source:    notify.SourceAuth,
				Title:     fmt.Sprintf("Signed in as %s", prof.DisplayName),
				Message:   fmt.Sprintf("%s account active (%s)", prof.ProviderName, prof.Email),
				DedupeKey: "auth:session",
			})
		}
		return
	}
	if snap.Phase == auth.PhaseError && snap.LastError != "" && snap.LastError != prevErr {
		_, _ = b.Center.Publish(notify.NotificationSpec{
			Severity:  notify.SeverityError,
			Source:    notify.SourceAuth,
			Title:     "Authentication attention required",
			Message:   snap.LastError,
			DedupeKey: "auth:error",
			Actions: []notify.Action{
				{ID: "open-auth", Label: "Open Sign-In", Primary: true},
			},
		})
		return
	}
	if snap.Phase == auth.PhaseSignedOut && prevPh == auth.PhaseSignedIn {
		_, _ = b.Center.Publish(notify.NotificationSpec{
			Severity:  notify.SeverityInfo,
			Source:    notify.SourceAuth,
			Title:     "Signed out",
			Message:   "Vaulted credentials cleared for active session",
			DedupeKey: "auth:session",
		})
	}
}
