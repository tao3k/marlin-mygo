// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell_test

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

func TestFederationCockpitInspectorAndMobileVoiceModelSheet(t *testing.T) {
	// 1. Test Tier-0 Federation Cockpit modal: KPI summary, role filter, Probe All, Switch, Reconnect, and ESC dismiss.
	targets := transport.NewTargetRegistry()
	_ = targets.Register(&authUITestTarget{meta: transport.TargetMeta{
		ID: "local-mac", DisplayName: "Local Mac", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Address: "127.0.0.1:50051", Phase: transport.PhaseReady,
	}})
	_ = targets.Register(&authUITestTarget{meta: transport.TargetMeta{
		ID: "remote-h100", DisplayName: "Remote H100", Mode: transport.ModeRemoteGRPC,
		TargetKind: schema.TargetDesktopLinux, Address: "10.0.0.42:50051", Phase: transport.PhaseReady,
	}})
	fed := transport.NewFederationSupervisor(targets)
	fed.UpdateMetrics("remote-h100", 2, 3, 1)

	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	app := mygoshell.NewWorkbenchApp(targets, slots, wb)
	app.Federation = fed

	var probeCalls int
	var reconnectedID, switchedID string
	fedUI := mygoshell.NewFederationController(fed, targets)
	fedUI.OnProbeAll = func() { probeCalls++ }
	fedUI.OnReconnectTarget = func(id string) { reconnectedID = id }
	fedUI.OnSelectTarget = func(id string) { switchedID = id }
	app.FederationUI = fedUI

	app.OpenFederation()
	tt := ui.NewTester(app.View, 1240, 820)
	tt.Frame()

	for _, want := range []string{
		"TIER-0 FEDERATION", "Multi-Target Federation Cockpit",
		"Targets: 2", "Pending Gates: 2", "Local Mac", "Remote H100",
	} {
		if !tt.HasText(want) {
			t.Fatalf("expected Federation Cockpit to render %q, got %q", want, strings.Join(tt.Texts(), " | "))
		}
	}

	if err := tt.Click("Probe All"); err != nil {
		t.Fatalf("click Probe All failed: %v", err)
	}
	if err := tt.Click("Remote"); err != nil {
		t.Fatalf("click Remote filter failed: %v", err)
	}
	tt.Frame()
	if tt.HasText("127.0.0.1:50051") || !tt.HasText("10.0.0.42:50051") {
		t.Fatalf("expected Remote filter to hide Local Mac card (127.0.0.1:50051) and show Remote H100 (10.0.0.42:50051), got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Switch"); err != nil {
		t.Fatalf("click Switch on Remote H100 failed: %v", err)
	}
	if err := tt.Click("Reconnect"); err != nil {
		t.Fatalf("click Reconnect on Remote H100 failed: %v", err)
	}
	if probeCalls != 1 || switchedID != "remote-h100" || reconnectedID != "remote-h100" {
		t.Fatalf("unexpected Federation Cockpit actions: probe=%d switch=%q reconn=%q", probeCalls, switchedID, reconnectedID)
	}
	if !app.DismissTopOverlay() || app.IsFederationOpen() {
		t.Fatal("expected global ESC DismissTopOverlay to close Federation Cockpit")
	}

	// 2. Test Mobile Companion Voice Recording Bar + Model & Reasoning Bottom Sheet.
	mob := mygoshell.NewMobileCompanionAppForPlatform(mygoshell.MobilePlatformIOS, "iphone16", "")
	tc := mob.Transcript
	var mobVoiceStarts, mobVoiceStops int
	tc.OnStartVoice = func() { mobVoiceStarts++ }
	tc.OnStopVoice = func() { mobVoiceStops++ }

	mtt := ui.NewTester(mob.View, 393, 852)
	mtt.Frame()

	if err := mtt.Click("Voice"); err != nil {
		t.Fatalf("click mobile Voice button failed: %v", err)
	}
	tc.Voice.PushLevel(0.8)
	mtt.Frame()
	if mobVoiceStarts != 1 || !mtt.HasText("REC") || !mtt.HasText("Attach Voice") {
		t.Fatalf("expected mobile REC voice bar, got %q", strings.Join(mtt.Texts(), " | "))
	}
	if err := mtt.Click("Attach Voice"); err != nil {
		t.Fatalf("click mobile Attach Voice failed: %v", err)
	}
	if _, err := tc.CompleteVoiceAudio("iphone-note.wav", makeTestWAV()); err != nil {
		t.Fatalf("CompleteVoiceAudio failed: %v", err)
	}
	mtt.Frame()
	if mobVoiceStops != 1 || !mtt.HasText("iphone-note.wav") {
		t.Fatalf("expected iphone-note.wav staged on mobile, got %q", strings.Join(mtt.Texts(), " | "))
	}

	// Tap active model pill on mobile -> opens Model & Reasoning bottom sheet -> select Marlin Pro + Deep!
	if err := mtt.Click("Marlin Core"); err != nil {
		t.Fatalf("click mobile model pill failed: %v", err)
	}
	mtt.Frame()
	if !mtt.HasText("Model & Reasoning") || !mtt.HasText("Marlin Pro") {
		t.Fatalf("expected mobile Model & Reasoning bottom sheet, got %q", strings.Join(mtt.Texts(), " | "))
	}
	if err := mtt.Click("Deep"); err != nil {
		t.Fatalf("click Deep effort on mobile sheet failed: %v", err)
	}
	if err := mtt.Click("Marlin Pro"); err != nil {
		t.Fatalf("click Marlin Pro on mobile sheet failed: %v", err)
	}
	mtt.Frame()
	if !tc.DeepThinkEnabled() || tc.Composer.Snapshot().ActiveModel.ID != "marlin-pro" || tc.IsModelPickerOpen() {
		t.Fatalf("expected mobile sheet selection synced: think=%v model=%s open=%v",
			tc.DeepThinkEnabled(), tc.Composer.Snapshot().ActiveModel.ID, tc.IsModelPickerOpen())
	}
}
