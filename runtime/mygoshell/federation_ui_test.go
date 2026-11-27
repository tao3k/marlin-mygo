// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestFederatedMultiMachineSidebarAndIsolatedWorkbenches(t *testing.T) {
	targets := transport.NewTargetRegistry()
	_ = targets.Register(&testTarget{meta: transport.TargetMeta{
		ID: "local-mac", DisplayName: "Local MacBook", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})
	_ = targets.Register(&testTarget{meta: transport.TargetMeta{
		ID: "remote-gpu", DisplayName: "H100 Agent Node", Mode: transport.ModeRemoteGRPC,
		TargetKind: schema.TargetDesktopLinux, Phase: transport.PhaseReady,
	}})
	_ = targets.Register(&testTarget{meta: transport.TargetMeta{
		ID: "iphone-16", DisplayName: "iPhone Personal", Mode: transport.ModeRemoteGRPC,
		TargetKind: schema.TargetMobileIOS, Phase: transport.PhaseReady,
	}})

	fed := transport.NewFederationSupervisor(targets)
	fed.ProbeAll(context.Background(), time.Second)
	fed.UpdateMetrics("remote-gpu", 1, 2, 1)

	slots := shell.NewSlotRegistry()
	localWB := shell.NewWorkbenchController(slots)
	_ = localWB.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation: 1, ProfileName: "desktop-dev", Theme: schema.DefaultUITheme(),
		FrameTree: schema.UIElement{
			Kind:  schema.ElemPanel,
			Title: "Local MacBook Workspace",
			Tone:  schema.ToneInfo,
			Children: []schema.UIElement{
				{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Editing local repo", Tone: schema.ToneNormal}}},
			},
		},
	})
	remoteWB := shell.NewWorkbenchController(slots)
	_ = remoteWB.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation: 1, ProfileName: "remote-gpu", Theme: schema.DefaultUITheme(),
		FrameTree: schema.UIElement{
			Kind:  schema.ElemPanel,
			Title: "H100 Remote Training Run",
			Tone:  schema.ToneWarning,
			Children: []schema.UIElement{
				{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Awaiting human gate approval", Tone: schema.ToneWarning}}},
			},
		},
	})

	app := NewWorkbenchApp(targets, slots, localWB)
	app.Federation = fed
	app.TargetWorkbenches = map[string]*shell.WorkbenchController{
		"local-mac":  localWB,
		"remote-gpu": remoteWB,
	}

	tt := ui.NewTester(app.View, 1100, 700)
	if !tt.HasText("LOCAL WORKSTATION") || !tt.HasText("REMOTE AGENTS") || !tt.HasText("MOBILE COMPANIONS") {
		t.Fatalf("expected all 3 federated sidebar role sections, got %q", tt.Texts())
	}
	if !tt.HasText("Local MacBook Workspace") {
		t.Fatalf("expected local-mac workbench active initially, got %q", tt.Texts())
	}
	if !tt.HasText("Jump to Gate (remote-gpu)") {
		t.Fatalf("expected cross-machine Jump to Gate button for remote-gpu, got %q", tt.Texts())
	}

	// Click "Jump to Gate (remote-gpu)" -> switches target and renders isolated remote workbench!
	if err := tt.Click("Jump to Gate (remote-gpu)"); err != nil {
		t.Fatalf("clicking Jump to Gate failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("H100 Remote Training Run") {
		t.Fatalf("expected remote-gpu workbench after jumping to gate, got %q", tt.Texts())
	}
}
