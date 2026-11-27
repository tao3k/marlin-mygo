// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	appv1 "github.com/tao3k/marlin-mygo/gen/marlin/app/v1"
	commonv1 "github.com/tao3k/marlin-mygo/gen/marlin/common/v1"
	devicev1 "github.com/tao3k/marlin-mygo/gen/marlin/device/v1"
	gatev1 "github.com/tao3k/marlin-mygo/gen/marlin/gate/v1"
	pluginv1 "github.com/tao3k/marlin-mygo/gen/marlin/plugin/v1"
	uiv1 "github.com/tao3k/marlin-mygo/gen/marlin/ui/v1"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

type fakeMarlinHost struct {
	appv1.UnimplementedAppServerServer
	gatev1.UnimplementedApprovalGateServiceServer
	uiv1.UnimplementedUiWorkbenchServiceServer
	pluginv1.UnimplementedPluginServiceServer
	devicev1.UnimplementedDeviceBridgeServiceServer
	token string
}

func (s *fakeMarlinHost) checkAuth(ctx context.Context) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok || len(md.Get("authorization")) == 0 || md.Get("authorization")[0] != s.token {
		return status.Error(codes.Unauthenticated, "missing or invalid token")
	}
	return nil
}

func (s *fakeMarlinHost) GetCapabilities(ctx context.Context, _ *appv1.GetCapabilitiesRequest) (*appv1.GetCapabilitiesResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	return &appv1.GetCapabilitiesResponse{
		MaxRequestBytes:    256 * 1024,
		MaxMessages:        128,
		MaxOutputTextBytes: 1024 * 1024,
		HostTargetKind:     commonv1.TargetKind_TARGET_KIND_REMOTE_HEADLESS,
		SupportedServices:  []string{"marlin.app.v1", "marlin.gate.v1", "marlin.ui.v1", "marlin.plugin.v1", "marlin.device.v1"},
	}, nil
}

func (s *fakeMarlinHost) OpenWorkbench(ctx context.Context, req *uiv1.OpenWorkbenchRequest) (*uiv1.OpenWorkbenchResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	slotID := "workbench.main"
	return &uiv1.OpenWorkbenchResponse{
		Workbench: &uiv1.UiWorkbenchSnapshot{
			Generation:   2,
			ProfileName:  "macos-dev",
			SessionLabel: req.GetSessionId(),
			Theme: &uiv1.UiTheme{
				Canvas:  &uiv1.UiRgb{Red: 40, Green: 44, Blue: 52},
				Ink:     &uiv1.UiRgb{Red: 187, Green: 194, Blue: 207},
				Bar:     &uiv1.UiRgb{Red: 28, Green: 31, Blue: 36},
				Accent:  &uiv1.UiRgb{Red: 198, Green: 120, Blue: 221},
				Info:    &uiv1.UiRgb{Red: 81, Green: 175, Blue: 239},
				Success: &uiv1.UiRgb{Red: 152, Green: 190, Blue: 101},
				Warning: &uiv1.UiRgb{Red: 236, Green: 190, Blue: 123},
				Muted:   &uiv1.UiRgb{Red: 115, Green: 121, Blue: 126},
			},
			Layout: &uiv1.UiLayoutPolicy{
				FrameTree: &uiv1.UiElement{
					Node: &uiv1.UiElement_Rows{
						Rows: &uiv1.UiElementSequence{
							Children: []*uiv1.UiElement{
								{Node: &uiv1.UiElement_Slot{Slot: &uiv1.UiSlotNode{
									Source: uiv1.UiSlotSource_UI_SLOT_SOURCE_NAMED_PLUGIN_SLOT,
									SlotId: &slotID,
								}}},
							},
						},
					},
				},
			},
			Slots: []*uiv1.SlotDeclaration{
				{SlotId: slotID, Cardinality: uiv1.SlotCardinality_SLOT_CARDINALITY_SINGLE, FallbackRenderer: "host.main", AllowUntrustedPlugin: true},
			},
		},
	}, nil
}

func (s *fakeMarlinHost) SubmitToolReviewDecision(ctx context.Context, req *gatev1.SubmitToolReviewDecisionRequest) (*gatev1.SubmitToolReviewDecisionResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	return &gatev1.SubmitToolReviewDecisionResponse{
		Receipt: &gatev1.GateDecisionReceipt{
			GateId:  req.GetGateId(),
			Kind:    gatev1.GateKind_GATE_KIND_MODEL_TOOL_REVIEW,
			Claimed: true,
			Receipt: &commonv1.ReceiptRef{ReceiptId: "rcpt-gate-100"},
		},
	}, nil
}

func (s *fakeMarlinHost) SubmitScopedPanelAction(ctx context.Context, req *pluginv1.SubmitScopedPanelActionRequest) (*pluginv1.SubmitScopedPanelActionResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	return &pluginv1.SubmitScopedPanelActionResponse{
		Receipt: &pluginv1.ScopedPanelActionReceipt{
			Admitted:   true,
			ResultJson: []byte(`{"moved":true}`),
			Receipt:    &commonv1.ReceiptRef{ReceiptId: "rcpt-panel-200"},
		},
	}, nil
}

func (s *fakeMarlinHost) RegisterDeviceNode(ctx context.Context, req *devicev1.RegisterDeviceNodeRequest) (*devicev1.RegisterDeviceNodeResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	return &devicev1.RegisterDeviceNodeResponse{
		LeaseId:              "lease-" + req.GetDeviceId(),
		LeaseTtlMs:           60000,
		ActiveProfileOverlay: "+ios-personal",
	}, nil
}

func TestLoopbackHTTP2UnifiedContractRoundTrip(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	host := &fakeMarlinHost{token: "secret-loopback-token"}
	appv1.RegisterAppServerServer(srv, host)
	gatev1.RegisterApprovalGateServiceServer(srv, host)
	uiv1.RegisterUiWorkbenchServiceServer(srv, host)
	pluginv1.RegisterPluginServiceServer(srv, host)
	devicev1.RegisterDeviceBridgeServiceServer(srv, host)
	go func() { _ = srv.Serve(ln) }()
	defer srv.Stop()

	ctx := context.Background()
	target, err := Dial(DialConfig{
		ID:          "remote-1",
		DisplayName: "Remote Marlin",
		Mode:        transport.ModeRemoteGRPC,
		Address:     ln.Addr().String(),
		AuthToken:   "secret-loopback-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	// 1. Capabilities handshake via TargetRegistry
	reg := transport.NewTargetRegistry()
	if err := reg.Register(target); err != nil {
		t.Fatal(err)
	}
	if err := target.Ping(ctx); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
	if got := target.Meta().TargetKind; got != schema.TargetRemoteHeadless {
		t.Fatalf("expected remote-headless, got %q", got)
	}

	// 2. marlin.ui.v1 workbench snapshot -> shell.WorkbenchController
	wbResp, err := target.UI().OpenWorkbench(ctx, &uiv1.OpenWorkbenchRequest{
		SessionId:        "sess-1",
		ClientTargetKind: commonv1.TargetKind_TARGET_KIND_MOBILE_IOS,
	})
	if err != nil {
		t.Fatal(err)
	}
	wbSnap, err := WorkbenchFromProto(wbResp.GetWorkbench())
	if err != nil {
		t.Fatal(err)
	}
	ctrl := shell.NewWorkbenchController(shell.NewSlotRegistry())
	if err := ctrl.AdmitSnapshot(wbSnap); err != nil {
		t.Fatal(err)
	}

	// 3. marlin.gate.v1 one-shot approval
	gateResp, err := target.Gate().SubmitToolReviewDecision(ctx, &gatev1.SubmitToolReviewDecisionRequest{
		Turn:           &appv1.SessionTurnRef{SessionId: "sess-1", Ordinal: 1},
		GateId:         "gate-1",
		CallId:         "call-1",
		Decision:       gatev1.ToolReviewDecision_TOOL_REVIEW_DECISION_APPROVE_ONCE,
		IdempotencyKey: "idem-gate-1",
	})
	gateRcpt := gateResp.GetReceipt()
	if err != nil || !gateRcpt.GetClaimed() || gateRcpt.GetReceipt().GetReceiptId() != "rcpt-gate-100" {
		t.Fatalf("unexpected gate receipt: %+v, err=%v", gateRcpt, err)
	}

	// 4. marlin.plugin.v1 scoped panel action through pluginhost.ScopedProxy
	proxy := pluginhost.NewScopedProxy(pluginhost.ScopedScope{
		PluginID:             "tao3k.org-kanban",
		ContributionID:       "kanban-panel",
		ActivationGeneration: 1,
		DeclaredActions:      []string{"org.task.move-state"},
	}, &PanelActionDispatcher{Target: target, ProjectID: "proj-1", SessionID: "sess-1"})
	panelRcpt, err := proxy.SubmitAction(ctx, "org.task.move-state", json.RawMessage(`{"id":"T-1"}`), "idem-panel-1")
	if err != nil || !panelRcpt.Admitted || panelRcpt.ReceiptID != "rcpt-panel-200" {
		t.Fatalf("unexpected panel receipt: %+v, err=%v", panelRcpt, err)
	}

	// 5. marlin.device.v1 iOS node registration -> Scheme overlay selection
	devResp, err := target.Device().RegisterDeviceNode(ctx, &devicev1.RegisterDeviceNodeRequest{
		DeviceId:   "iphone-17",
		TargetKind: commonv1.TargetKind_TARGET_KIND_MOBILE_IOS,
		Capabilities: []*devicev1.DeviceCapabilityDescriptor{
			{CapabilityId: "ios.photos.recent", ProtocolFamily: "photos", Permission: devicev1.OsPermissionState_OS_PERMISSION_STATE_GRANTED, ReadOnly: true},
		},
	})
	if err != nil || devResp.GetActiveProfileOverlay() != "+ios-personal" {
		t.Fatalf("unexpected device registration: %+v, err=%v", devResp, err)
	}
}
