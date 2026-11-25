// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/control/auth"
	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/notify"
	gatev1 "github.com/tao3k/marlin-mygo/gen/marlin/gate/v1"
)

type bridgeEmailBackend struct{}

func (bridgeEmailBackend) RequestChallenge(_ context.Context, _ string, _ auth.AuthMethod) (auth.EmailChallengeResponse, error) {
	return auth.EmailChallengeResponse{ChallengeID: "nb-1", ExpiresIn: 5 * time.Minute, Cooldown: time.Second, MaxAttempts: 3}, nil
}

func (bridgeEmailBackend) VerifyChallenge(_ context.Context, _, email, code string, _ auth.AuthMethod) (auth.TokenSet, auth.AccountProfile, error) {
	if code != "777777" {
		return auth.TokenSet{}, auth.AccountProfile{}, errors.New("wrong verification code")
	}
	return auth.TokenSet{AccessToken: "SECRET_BRIDGE_TOKEN", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)},
		auth.AccountProfile{Email: email, DisplayName: "Bridge User", EmailVerified: true}, nil
}

func TestNotifyBridgeGRPCSwapSidecarGateAndAuthEvents(t *testing.T) {
	center := notify.NewCenter(notify.CenterConfig{})
	bridge := grpcwire.NewNotifyBridge(center)

	// 1. Sidecar crash followed by recovery collapses cleanly via DedupeKey!
	bridge.OnSidecarCrash("Marlin App Server", 1, errors.New("signal: killed"))
	if snap := center.Snapshot(); len(snap.History) != 1 || snap.History[0].Severity != notify.SeverityWarning {
		t.Fatalf("expected 1 sidecar crash warning, got %+v", snap.History)
	}
	bridge.OnSidecarRecovered("Marlin App Server", "127.0.0.1:50123", 1)
	if snap := center.Snapshot(); len(snap.History) != 1 || snap.History[0].Severity != notify.SeveritySuccess || snap.History[0].Count != 2 {
		t.Fatalf("expected crash warning collapsed into recovery success (Count=2), got %+v", snap.History)
	}

	// 2. Pending Gate emits persistent SourceGate alert with Review Gate CTA!
	bridge.OnPendingGate(&gatev1.PendingGate{
		GateId:         "gate-bridge-1",
		ToolOrNodeName: "device.ios.notify.send",
		Prompt:         "Allow notification?",
	})
	foundGate := false
	for _, it := range center.Snapshot().FloatingToasts {
		if it.Source == notify.SourceGate && it.Persistent && len(it.Actions) == 1 && it.Actions[0].ID == "review-gate" {
			foundGate = true
		}
	}
	if !foundGate {
		t.Fatalf("expected persistent SourceGate toast with review-gate CTA, got %+v", center.Snapshot().FloatingToasts)
	}

	// 3. ChannelManager atomic Target swap notifies SourceGRPC cleanly!
	addr1, srv1 := startFaultHost(t, &faultInjectingHost{generation: 1})
	defer srv1.Stop()
	addr2, srv2 := startFaultHost(t, &faultInjectingHost{generation: 2})
	defer srv2.Stop()

	mgr := grpcwire.NewChannelManager()
	defer mgr.CloseAll()
	unsubChan := bridge.AttachChannelManager(mgr)
	defer unsubChan()

	t1, err := mgr.DialAndRegister(grpcwire.DialConfig{ID: "primary", Mode: transport.ModeLocalSidecar, Address: addr1})
	if err != nil {
		t.Fatal(err)
	}
	t2, err := grpcwire.Dial(grpcwire.DialConfig{ID: "primary", Mode: transport.ModeLocalSidecar, Address: addr2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.SwapTarget("primary", t2); err != nil {
		t.Fatal(err)
	}
	_ = t1.Close()

	foundSwap := false
	for _, it := range center.Snapshot().History {
		if it.Source == notify.SourceGRPC && strings.Contains(it.Title, "swapped to generation #2") {
			foundSwap = true
		}
	}
	if !foundSwap {
		t.Fatalf("expected ChannelManager swap notification, got %+v", center.Snapshot().History)
	}

	// 4. AuthManager sign-in -> sign-out emits SourceAuth notices with ZERO secret token exposure!
	authMgr := auth.NewManager(auth.ManagerConfig{EmailBackend: bridgeEmailBackend{}})
	_ = authMgr.RegisterProvider(auth.EmailOTPProvider())
	unsubAuth := bridge.AttachAuthManager(authMgr)
	defer unsubAuth()

	ctx := context.Background()
	_, _ = authMgr.RequestEmailLogin(ctx, "ops@marlin.dev", auth.MethodEmailOTP)
	if _, err := authMgr.VerifyEmailLogin(ctx, "777777"); err != nil {
		t.Fatal(err)
	}
	foundSignIn := false
	for _, it := range center.Snapshot().History {
		if strings.Contains(it.Title+it.Message, "SECRET_BRIDGE_TOKEN") {
			t.Fatalf("SECURITY VIOLATION: raw token leaked into notification: %+v", it)
		}
		if it.Source == notify.SourceAuth && strings.Contains(it.Title, "Signed in as Bridge User") {
			foundSignIn = true
		}
	}
	if !foundSignIn {
		t.Fatalf("expected Signed in notification from AuthManager bridge, got %+v", center.Snapshot().History)
	}
}
