// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/status"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	appv1 "github.com/tao3k/marlin-mygo/gen/marlin/app/v1"
	commonv1 "github.com/tao3k/marlin-mygo/gen/marlin/common/v1"
	devicev1 "github.com/tao3k/marlin-mygo/gen/marlin/device/v1"
	gatev1 "github.com/tao3k/marlin-mygo/gen/marlin/gate/v1"
	projectionv1 "github.com/tao3k/marlin-mygo/gen/marlin/projection/v1"
	uiv1 "github.com/tao3k/marlin-mygo/gen/marlin/ui/v1"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/device"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

// faultInjectingHost simulates transient mid-stream drops and server restarts
// across all orthogonal marlin.*.v1 streaming services.
type faultInjectingHost struct {
	appv1.UnimplementedAppServerServer
	uiv1.UnimplementedUiWorkbenchServiceServer
	gatev1.UnimplementedApprovalGateServiceServer
	devicev1.UnimplementedDeviceBridgeServiceServer
	projectionv1.UnimplementedProjectionQueryServiceServer

	generation   uint64
	gateID       string
	nodeID       string
	diffRev      uint64
	procRev      uint64
	watchDrops   atomic.Int32
	unauth       bool
	deviceInvoke bool

	mu               sync.Mutex
	seenAfterEventID []string
	seenAfterGen     []uint64
	deviceHeartbeats int
}

func (h *faultInjectingHost) GetCapabilities(context.Context, *appv1.GetCapabilitiesRequest) (*appv1.GetCapabilitiesResponse, error) {
	return &appv1.GetCapabilitiesResponse{
		HostTargetKind:    commonv1.TargetKind_TARGET_KIND_DESKTOP_MACOS,
		SupportedServices: []string{"marlin.app.v1", "marlin.ui.v1", "marlin.gate.v1", "marlin.device.v1", "marlin.projection.v1"},
	}, nil
}

func (h *faultInjectingHost) WatchSessionTurn(req *appv1.WatchSessionTurnRequest, stream grpc.ServerStreamingServer[appv1.SessionTurnEvent]) error {
	if h.unauth {
		return status.Error(codes.Unauthenticated, "expired bearer token")
	}
	after := req.GetAfterEventId()
	h.mu.Lock()
	h.seenAfterEventID = append(h.seenAfterEventID, after)
	h.mu.Unlock()

	type evSpec struct {
		id    string
		delta string
		done  bool
	}
	all := []evSpec{
		{id: "ev-1", delta: "Alpha "},
		{id: "ev-2", delta: "Beta "},
		{id: "ev-3", delta: "Gamma "},
		{id: "ev-4", delta: "Delta "},
		{id: "ev-5", delta: "Epsilon "},
		{id: "ev-6", delta: "Zeta", done: true},
	}

	startIdx := 0
	if after != "" {
		for i, e := range all {
			if e.id == after {
				startIdx = i + 1
				break
			}
		}
	}

	dropAt := -1
	switch h.watchDrops.Add(1) {
	case 1:
		dropAt = 2 // drop right after ev-2
	case 2:
		dropAt = 4 // drop right after ev-4
	}

	for i := startIdx; i < len(all); i++ {
		if dropAt >= 0 && i == dropAt {
			return status.Error(codes.Unavailable, "simulated HTTP/2 transport reset")
		}
		if err := stream.Send(&appv1.SessionTurnEvent{
			EventId: all[i].id,
			Event: &appv1.SessionTurnEvent_TextCheckpoint{
				TextCheckpoint: &appv1.TextCheckpoint{Text: all[i].delta},
			},
		}); err != nil {
			return err
		}
		if all[i].done {
			return stream.Send(&appv1.SessionTurnEvent{
				EventId: "ev-outcome",
				Event: &appv1.SessionTurnEvent_Outcome{
					Outcome: appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_COMPLETED,
				},
			})
		}
	}
	return nil
}

func (h *faultInjectingHost) OpenWorkbench(_ context.Context, req *uiv1.OpenWorkbenchRequest) (*uiv1.OpenWorkbenchResponse, error) {
	return &uiv1.OpenWorkbenchResponse{Workbench: h.makeWorkbench(req.GetSessionId(), h.generation)}, nil
}

func (h *faultInjectingHost) WatchWorkbench(req *uiv1.WatchWorkbenchRequest, stream grpc.ServerStreamingServer[uiv1.WatchWorkbenchResponse]) error {
	h.mu.Lock()
	h.seenAfterGen = append(h.seenAfterGen, req.GetAfterGeneration())
	h.mu.Unlock()
	if h.generation > req.GetAfterGeneration() {
		if err := stream.Send(&uiv1.WatchWorkbenchResponse{
			Workbench: h.makeWorkbench(req.GetSessionId(), h.generation),
		}); err != nil {
			return err
		}
	}
	<-stream.Context().Done()
	return stream.Context().Err()
}

func (h *faultInjectingHost) makeWorkbench(sessionID string, gen uint64) *uiv1.UiWorkbenchSnapshot {
	slotID := "workbench.main"
	return &uiv1.UiWorkbenchSnapshot{
		Generation:   gen,
		ProfileName:  fmt.Sprintf("gen-%d", gen),
		SessionLabel: sessionID,
		Theme: &uiv1.UiTheme{
			Canvas:  &uiv1.UiRgb{Red: 15, Green: 17, Blue: 23},
			Ink:     &uiv1.UiRgb{Red: 230, Green: 237, Blue: 243},
			Bar:     &uiv1.UiRgb{Red: 22, Green: 27, Blue: 34},
			Accent:  &uiv1.UiRgb{Red: 88, Green: 166, Blue: 255},
			Info:    &uiv1.UiRgb{Red: 88, Green: 166, Blue: 255},
			Success: &uiv1.UiRgb{Red: 63, Green: 185, Blue: 80},
			Warning: &uiv1.UiRgb{Red: 210, Green: 153, Blue: 34},
			Muted:   &uiv1.UiRgb{Red: 139, Green: 148, Blue: 158},
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
	}
}

func (h *faultInjectingHost) ListPendingGates(context.Context, *gatev1.ListPendingGatesRequest) (*gatev1.ListPendingGatesResponse, error) {
	if h.gateID == "" {
		return &gatev1.ListPendingGatesResponse{}, nil
	}
	return &gatev1.ListPendingGatesResponse{
		Gates: []*gatev1.PendingGate{{GateId: h.gateID, ToolOrNodeName: "device.ios.notify.send", Prompt: "Approve after swap"}},
	}, nil
}

func (h *faultInjectingHost) WatchPendingGates(_ *gatev1.WatchPendingGatesRequest, stream grpc.ServerStreamingServer[gatev1.WatchPendingGatesResponse]) error {
	if h.gateID != "" {
		if err := stream.Send(&gatev1.WatchPendingGatesResponse{
			Gate: &gatev1.PendingGate{GateId: h.gateID, ToolOrNodeName: "device.ios.notify.send", Prompt: "Approve after swap"},
		}); err != nil {
			return err
		}
	}
	<-stream.Context().Done()
	return stream.Context().Err()
}

func (h *faultInjectingHost) RegisterDeviceNode(_ context.Context, req *devicev1.RegisterDeviceNodeRequest) (*devicev1.RegisterDeviceNodeResponse, error) {
	return &devicev1.RegisterDeviceNodeResponse{
		LeaseId:              "lease-" + req.GetDeviceId(),
		LeaseTtlMs:           30000,
		ActiveProfileOverlay: "desktop-dev",
	}, nil
}

func (h *faultInjectingHost) OpenDeviceChannel(stream grpc.BidiStreamingServer[devicev1.OpenDeviceChannelRequest, devicev1.OpenDeviceChannelResponse]) error {
	invoked := false
	for {
		req, err := stream.Recv()
		if err != nil {
			return err
		}
		if req.GetHeartbeat() != nil {
			h.mu.Lock()
			h.deviceHeartbeats++
			h.mu.Unlock()
		}
		if h.deviceInvoke && !invoked && req.GetCapabilityUpdate() != nil {
			invoked = true
			_ = stream.Send(&devicev1.OpenDeviceChannelResponse{
				Frame: &devicev1.OpenDeviceChannelResponse_InvocationRequest{
					InvocationRequest: &devicev1.DeviceInvocationRequest{
						InvocationId: "inv-after-swap",
						SessionId:    "sess-swap",
						CapabilityId: "device.ios.notify.send",
						GateReceipt:  &commonv1.ReceiptRef{ReceiptId: "rcpt-swap-1"},
						ArgsJson:     []byte(`{"title":"Reconnected","body":"Channel healed"}`),
					},
				},
			})
		}
	}
}

func (h *faultInjectingHost) QueryProjectGraph(context.Context, *projectionv1.QueryProjectGraphRequest) (*projectionv1.QueryProjectGraphResponse, error) {
	return &projectionv1.QueryProjectGraphResponse{}, nil
}

func (h *faultInjectingHost) WatchGraphTrajectory(_ *projectionv1.WatchGraphTrajectoryRequest, stream grpc.ServerStreamingServer[projectionv1.WatchGraphTrajectoryResponse]) error {
	if h.nodeID != "" {
		if err := stream.Send(&projectionv1.WatchGraphTrajectoryResponse{
			Event: &projectionv1.GraphTrajectoryEvent{
				NodeId:      h.nodeID,
				NodeKind:    "agent-node",
				Status:      "succeeded",
				NodeReceipt: &commonv1.ReceiptRef{ReceiptId: "rcpt-" + h.nodeID},
			},
		}); err != nil {
			return err
		}
	}
	<-stream.Context().Done()
	return stream.Context().Err()
}

func (h *faultInjectingHost) WatchWorkspaceDiff(_ *projectionv1.WatchWorkspaceDiffRequest, stream grpc.ServerStreamingServer[projectionv1.WatchWorkspaceDiffResponse]) error {
	if h.diffRev > 0 {
		if err := stream.Send(&projectionv1.WatchWorkspaceDiffResponse{
			Snapshot: &projectionv1.WorkspaceDiffSnapshot{
				Revision: h.diffRev,
				Branch:   "feat/marlin-mygo-frontend-engine",
				Files: []*projectionv1.FileDiffEntry{
					{Path: "control/grpcwire/channel_manager.go", Status: "modified", AddedLines: 42, RemovedLines: 3},
				},
			},
		}); err != nil {
			return err
		}
	}
	<-stream.Context().Done()
	return stream.Context().Err()
}

func (h *faultInjectingHost) WatchManagedProcesses(_ *projectionv1.WatchManagedProcessesRequest, stream grpc.ServerStreamingServer[projectionv1.WatchManagedProcessesResponse]) error {
	if h.procRev > 0 {
		if err := stream.Send(&projectionv1.WatchManagedProcessesResponse{
			Snapshot: &projectionv1.ManagedProcessSnapshot{
				Revision: h.procRev,
				Processes: []*projectionv1.ManagedProcessView{
					{ProcessId: fmt.Sprintf("proc-rev-%d", h.procRev), Command: "marlin-mygo-host", State: "running"},
				},
			},
		}); err != nil {
			return err
		}
	}
	<-stream.Context().Done()
	return stream.Context().Err()
}

func startFaultHost(t *testing.T, host *faultInjectingHost) (string, *grpc.Server) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	appv1.RegisterAppServerServer(srv, host)
	uiv1.RegisterUiWorkbenchServiceServer(srv, host)
	gatev1.RegisterApprovalGateServiceServer(srv, host)
	devicev1.RegisterDeviceBridgeServiceServer(srv, host)
	projectionv1.RegisterProjectionQueryServiceServer(srv, host)
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr().String(), srv
}

func TestChannelManagerLifecycleAndAtomicSwap(t *testing.T) {
	addr1, srv1 := startFaultHost(t, &faultInjectingHost{generation: 1})
	defer srv1.Stop()
	addr2, srv2 := startFaultHost(t, &faultInjectingHost{generation: 2})
	defer srv2.Stop()

	mgr := grpcwire.NewChannelManager()
	defer func() {
		if err := mgr.CloseAll(); err != nil {
			t.Errorf("CloseAll failed: %v", err)
		}
	}()

	var eventsMu sync.Mutex
	var events []grpcwire.ChannelStatus
	unsub := mgr.Subscribe(func(st grpcwire.ChannelStatus) {
		eventsMu.Lock()
		events = append(events, st)
		eventsMu.Unlock()
	})
	defer unsub()

	policy := grpcwire.DefaultChannelPolicy()
	policy.KeepaliveTime = 10 * time.Second
	policy.KeepaliveTimeout = 2 * time.Second

	t1, err := mgr.DialAndRegister(grpcwire.DialConfig{
		ID:          "rust-primary",
		DisplayName: "Rust Authority",
		Mode:        transport.ModeLocalSidecar,
		Address:     addr1,
		Policy:      &policy,
	})
	if err != nil {
		t.Fatalf("DialAndRegister primary failed: %v", err)
	}
	if _, err := mgr.DialAndRegister(grpcwire.DialConfig{
		ID:          "aux-indexer",
		DisplayName: "Auxiliary gRPC Service",
		Mode:        transport.ModeLocalSidecar,
		Address:     addr2,
	}); err != nil {
		t.Fatalf("DialAndRegister aux failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := t1.WaitForReady(ctx); err != nil {
		t.Fatalf("WaitForReady failed: %v", err)
	}
	if got := t1.ConnectivityState(); got != connectivity.Ready {
		t.Fatalf("expected Ready connectivity state, got %s", got)
	}

	snap := mgr.Snapshot()
	if len(snap) != 2 || snap[0].ID != "rust-primary" || snap[1].ID != "aux-indexer" {
		t.Fatalf("unexpected deterministic ChannelManager snapshot: %+v", snap)
	}

	// Verify atomic SwapTarget immediately wakes WaitSwap waiters with generation 2!
	provider := mgr.Provider("rust-primary")
	swapResultCh := make(chan uint64, 1)
	go func() {
		gen, ok := provider.WaitSwap(ctx, 1)
		if ok {
			swapResultCh <- gen
		}
	}()

	t2, err := grpcwire.Dial(grpcwire.DialConfig{
		ID:          "rust-primary",
		DisplayName: "Rust Authority Respawned",
		Mode:        transport.ModeLocalSidecar,
		Address:     addr2,
	})
	if err != nil {
		t.Fatalf("Dial respawned target failed: %v", err)
	}
	newGen, err := mgr.SwapTarget("rust-primary", t2)
	if err != nil || newGen != 2 {
		t.Fatalf("SwapTarget expected gen 2, got gen=%d err=%v", newGen, err)
	}
	_ = t1.Close()

	select {
	case wokenGen := <-swapResultCh:
		if wokenGen != 2 {
			t.Fatalf("expected WaitSwap to wake on gen 2, got %d", wokenGen)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for WaitSwap notification")
	}

	if provider.CurrentTarget() != t2 {
		t.Fatal("expected ManagedChannel.CurrentTarget() to return swapped target")
	}
	mgr.WakeAll()
	if err := t2.WaitForReady(ctx); err != nil {
		t.Fatalf("swapped target WaitForReady failed: %v", err)
	}

	st, ok := mgr.Status("rust-primary")
	if !ok || st.Generation != 2 || st.Address != addr2 {
		t.Fatalf("unexpected swapped channel status: ok=%v st=%+v", ok, st)
	}

	if err := mgr.CloseAll(); err != nil {
		t.Fatalf("CloseAll failed: %v", err)
	}
	if got := t2.ConnectivityState(); got != connectivity.Shutdown {
		t.Fatalf("expected Shutdown after CloseAll, got %s", got)
	}
}

func TestWatchTurnResilientMidStreamDropResumesStrictlyFromCursor(t *testing.T) {
	host := &faultInjectingHost{generation: 1}
	addr, srv := startFaultHost(t, host)
	defer srv.Stop()

	target, err := grpcwire.Dial(grpcwire.DialConfig{
		ID:      "fault-host",
		Mode:    transport.ModeLocalSidecar,
		Address: addr,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ctrl := grpcwire.NewTurnStreamController(target)
	var (
		assembled  strings.Builder
		reconnects int
		cursors    []string
	)
	lastEv, outcome, err := ctrl.WatchTurnResilient(
		ctx,
		&appv1.SessionTurnRef{SessionId: "sess-resilient", Ordinal: 1},
		"",
		grpcwire.WatchRetryPolicy{
			InitialBackoff: 5 * time.Millisecond,
			MaxBackoff:     20 * time.Millisecond,
			MaxRetries:     5,
			OnReconnect: func(_ int, afterEventID string, _ error) {
				reconnects++
				cursors = append(cursors, afterEventID)
			},
		},
		func(upd grpcwire.TurnStreamUpdate) {
			assembled.WriteString(upd.TextDelta)
		},
	)
	if err != nil {
		t.Fatalf("WatchTurnResilient failed: %v", err)
	}
	if outcome != appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_COMPLETED {
		t.Fatalf("expected COMPLETED outcome, got %v", outcome)
	}
	if assembled.String() != "Alpha Beta Gamma Delta Epsilon Zeta" {
		t.Fatalf("expected exact lossless reassembled text, got %q", assembled.String())
	}
	if lastEv != "ev-outcome" {
		t.Fatalf("expected final event ID ev-outcome, got %q", lastEv)
	}
	if reconnects != 2 || len(cursors) != 2 || cursors[0] != "ev-2" || cursors[1] != "ev-4" {
		t.Fatalf("expected 2 cursor-resumed reconnects at [ev-2 ev-4], got reconnects=%d cursors=%v", reconnects, cursors)
	}

	// Non-retryable Unauthenticated MUST fail fast with 0 retries!
	unauthHost := &faultInjectingHost{unauth: true}
	uAddr, uSrv := startFaultHost(t, unauthHost)
	defer uSrv.Stop()
	uTarget, err := grpcwire.Dial(grpcwire.DialConfig{ID: "unauth", Mode: transport.ModeLocalSidecar, Address: uAddr})
	if err != nil {
		t.Fatal(err)
	}
	defer uTarget.Close()

	unauthRetries := 0
	_, _, uErr := grpcwire.NewTurnStreamController(uTarget).WatchTurnResilient(
		ctx,
		&appv1.SessionTurnRef{SessionId: "s", Ordinal: 1},
		"",
		grpcwire.WatchRetryPolicy{
			InitialBackoff: 5 * time.Millisecond,
			MaxRetries:     5,
			OnReconnect:    func(int, string, error) { unauthRetries++ },
		},
		nil,
	)
	if status.Code(uErr) != codes.Unauthenticated || unauthRetries != 0 {
		t.Fatalf("expected immediate fail-fast Unauthenticated with 0 retries, got err=%v retries=%d", uErr, unauthRetries)
	}
}

func TestSessionDriverAndProjectionBridgeAutoReconnectAcrossTargetSwap(t *testing.T) {
	host1 := &faultInjectingHost{generation: 1, gateID: "gate-gen-1", nodeID: "node-gen-1", diffRev: 1, procRev: 1}
	addr1, srv1 := startFaultHost(t, host1)

	host2 := &faultInjectingHost{generation: 2, gateID: "gate-gen-2", nodeID: "node-gen-2", diffRev: 2, procRev: 2, deviceInvoke: true}
	addr2, srv2 := startFaultHost(t, host2)
	defer srv2.Stop()

	mgr := grpcwire.NewChannelManager()
	defer mgr.CloseAll()

	t1, err := mgr.DialAndRegister(grpcwire.DialConfig{
		ID:      "rust-swap",
		Mode:    transport.ModeLocalSidecar,
		Address: addr1,
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := mgr.Provider("rust-swap")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wbGen2 := make(chan schema.UIWorkbenchSnapshot, 1)
	gate2Seen := make(chan string, 1)
	deviceInvoked := make(chan device.InvocationResult, 1)

	notify := &e2eNotify{}
	devBridge := device.NewBridge(schema.TargetMobileIOS)
	if err := devBridge.RegisterProvider(ctx, &device.IOSPersonalProvider{Notify: notify}); err != nil {
		t.Fatal(err)
	}

	driver := grpcwire.NewSessionDriver(grpcwire.DriverConfig{
		Provider:                provider,
		TargetKind:              schema.TargetDesktopMacOS,
		SessionID:               "sess-swap",
		Workbench:               shell.NewWorkbenchController(shell.NewSlotRegistry()),
		DeviceBridge:            devBridge,
		DeviceID:                "desktop-swap-test",
		ReconnectInitialBackoff: 15 * time.Millisecond,
		ReconnectMaxBackoff:     60 * time.Millisecond,
		DeviceHeartbeatInterval: 40 * time.Millisecond,
		OnWorkbench: func(snap schema.UIWorkbenchSnapshot) {
			if snap.Generation == 2 {
				select {
				case wbGen2 <- snap:
				default:
				}
			}
		},
		OnPendingGate: func(g *gatev1.PendingGate) {
			if g.GetGateId() == "gate-gen-2" {
				select {
				case gate2Seen <- g.GetGateId():
				default:
				}
			}
		},
		OnDeviceInvoked: func(_ device.InvocationRequest, res device.InvocationResult) {
			select {
			case deviceInvoked <- res:
			default:
			}
		},
	})
	if _, err := driver.Start(ctx); err != nil {
		t.Fatalf("driver.Start failed: %v", err)
	}
	defer driver.Stop()

	projCtrl := mygoshell.NewProjectionController(mygoshell.ProjectionSnapshot{})
	projGen2Ready := make(chan struct{}, 1)
	projBridge := grpcwire.NewProjectionStreamBridge(grpcwire.ProjectionBridgeConfig{
		Provider:                provider,
		ProjectID:               "proj-swap",
		SessionID:               "sess-swap",
		Controller:              projCtrl,
		ReconnectInitialBackoff: 15 * time.Millisecond,
		ReconnectMaxBackoff:     60 * time.Millisecond,
		OnUpdate: func(snap mygoshell.ProjectionSnapshot) {
			hasNode2, hasProc2 := false, false
			for _, n := range snap.GraphNodes {
				if n.NodeID == "node-gen-2" {
					hasNode2 = true
				}
			}
			for _, p := range snap.Processes {
				if p.ProcessID == "proc-rev-2" {
					hasProc2 = true
				}
			}
			if hasNode2 && hasProc2 && len(snap.DiffFiles) > 0 {
				select {
				case projGen2Ready <- struct{}{}:
				default:
				}
			}
		},
	})
	if err := projBridge.Start(ctx); err != nil {
		t.Fatalf("projBridge.Start failed: %v", err)
	}
	defer projBridge.Stop()

	// Kill Server #1 and atomically swap to Server #2 (simulating Rust sidecar respawn)!
	srv1.Stop()
	t2, err := grpcwire.Dial(grpcwire.DialConfig{
		ID:      "rust-swap",
		Mode:    transport.ModeLocalSidecar,
		Address: addr2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.SwapTarget("rust-swap", t2); err != nil {
		t.Fatal(err)
	}
	_ = t1.Close()

	// ALL 6 long-lived streams (Workbench, PendingGates, DeviceChannel, Graph, Diff, Processes)
	// MUST auto-reconnect to Server #2 with ZERO manual driver/bridge recreation!
	select {
	case snap := <-wbGen2:
		if snap.Generation != 2 {
			t.Fatalf("expected workbench gen 2, got %d", snap.Generation)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for WatchWorkbench auto-reconnect to gen 2")
	}
	select {
	case gid := <-gate2Seen:
		if gid != "gate-gen-2" {
			t.Fatalf("unexpected gate ID after reconnect: %q", gid)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for WatchPendingGates auto-reconnect")
	}
	select {
	case res := <-deviceInvoked:
		if !res.OK {
			t.Fatalf("expected OK device invocation after OpenDeviceChannel reconnect, got %+v", res)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for OpenDeviceChannel auto-reconnect and invocation")
	}
	select {
	case <-projGen2Ready:
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for ProjectionStreamBridge auto-reconnect")
	}

	// Verify WatchWorkbench resumed strictly with AfterGeneration == 1!
	host2.mu.Lock()
	afterGens := append([]uint64(nil), host2.seenAfterGen...)
	host2.mu.Unlock()
	if len(afterGens) == 0 || afterGens[0] != 1 {
		t.Fatalf("expected WatchWorkbench reconnect to send AfterGeneration=1, got %v", afterGens)
	}
}

func TestFrameCoalescer120HzBurstProtection(t *testing.T) {
	var flushed atomic.Int64
	var latestSeq atomic.Int64
	var renderedSeq atomic.Int64

	c := grpcwire.NewFrameCoalescer(8*time.Millisecond, func() {
		flushed.Add(1)
		renderedSeq.Store(latestSeq.Load())
	})

	// Simulate 4 concurrent gRPC stream pumps firing 2,500 updates in a tight burst.
	const workers = 4
	const perWorker = 625
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				latestSeq.Add(1)
				c.MarkDirty()
			}
		}()
	}
	wg.Wait()
	c.Close()

	stats := c.Stats()
	if stats.Signals != workers*perWorker {
		t.Fatalf("expected %d signals, got %d", workers*perWorker, stats.Signals)
	}
	if stats.Flushes == 0 || stats.Flushes > 25 {
		t.Fatalf("expected burst coalescing to bound flushes in [1..25], got %d flushes for %d signals", stats.Flushes, stats.Signals)
	}
	if renderedSeq.Load() != workers*perWorker {
		t.Fatalf("expected final trailing state %d to be rendered with zero loss, got %d", workers*perWorker, renderedSeq.Load())
	}
}

// TestRealRustMultiChannelProviderSelfHealing runs live against the compiled
// Rust `marlin-mygo-host` binary (when MARLIN_RUST_SIDECAR_BIN is set):
// wires SessionDriver + ProjectionStreamBridge + TurnStreamController + 120Hz
// FrameCoalescer through ONE TargetProvider (ResilientSidecarTarget), abruptly
// SIGKILLs the Rust process mid-session, and proves every long-lived stream and
// WatchTurnResilient automatically follows the rotated-token respawned Rust PID
// with zero manual rewiring.
func TestRealRustMultiChannelProviderSelfHealing(t *testing.T) {
	binPath := os.Getenv("MARLIN_RUST_SIDECAR_BIN")
	if binPath == "" {
		t.Skip("set MARLIN_RUST_SIDECAR_BIN to run live Rust lifecycle channel E2E")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tursoPath := filepath.Join(t.TempDir(), "channel-lifecycle.turso")
	const sessionID = "channel-lifecycle-e2e"

	recoveredCh := make(chan struct{}, 2)
	var stderr bytes.Buffer
	resilient, err := grpcwire.LaunchResilientSidecar(ctx, grpcwire.ResilientSidecarConfig{
		ID:             "live-lifecycle-sidecar",
		DisplayName:    "Live Lifecycle Rust Host",
		InitialBackoff: 40 * time.Millisecond,
		MaxBackoff:     200 * time.Millisecond,
		MaxRestarts:    3,
		Sidecar: transport.SidecarConfig{
			BinaryPath: binPath,
			ExtraEnv: []string{
				"MARLIN_BRIDGE_REAL_SESSION=1",
				"MARLIN_BRIDGE_TURSO_PATH=" + tursoPath,
				"MARLIN_BRIDGE_CHUNK_DELAY_MS=2",
			},
			StartTimeout: 8 * time.Second,
			Stderr:       &stderr,
		},
		OnRecovered: func(_ context.Context, _ *grpcwire.Target, _ int) {
			select {
			case recoveredCh <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("LaunchResilientSidecar failed: %v\nstderr:\n%s", err, stderr.String())
	}
	defer func() { _ = resilient.Close() }()

	// Verify tuned HTTP/2 channel readiness immediately.
	if err := resilient.CurrentTarget().WaitForReady(ctx); err != nil {
		t.Fatalf("WaitForReady on real Rust marlin-mygo-host failed: %v", err)
	}

	coalescer := grpcwire.NewFrameCoalescer(grpcwire.DefaultFrameInterval, func() {})
	defer coalescer.Close()

	wb := shell.NewWorkbenchController(shell.NewSlotRegistry())
	driver := grpcwire.NewSessionDriver(grpcwire.DriverConfig{
		Provider:                resilient,
		TargetKind:              schema.TargetDesktopMacOS,
		SessionID:               sessionID,
		Workbench:               wb,
		ReconnectInitialBackoff: 20 * time.Millisecond,
		ReconnectMaxBackoff:     100 * time.Millisecond,
		OnWorkbench:             func(schema.UIWorkbenchSnapshot) { coalescer.MarkDirty() },
	})
	if _, err := driver.Start(ctx); err != nil {
		t.Fatalf("SessionDriver.Start against real Rust host failed: %v", err)
	}
	defer driver.Stop()

	projCtrl := mygoshell.NewProjectionController(mygoshell.ProjectionSnapshot{})
	projBridge := grpcwire.NewProjectionStreamBridge(grpcwire.ProjectionBridgeConfig{
		Provider:                resilient,
		ProjectID:               "default",
		SessionID:               sessionID,
		Controller:              projCtrl,
		ReconnectInitialBackoff: 20 * time.Millisecond,
		ReconnectMaxBackoff:     100 * time.Millisecond,
		OnUpdate:                func(mygoshell.ProjectionSnapshot) { coalescer.MarkDirty() },
	})
	if err := projBridge.Start(ctx); err != nil {
		t.Fatalf("ProjectionStreamBridge.Start against real Rust host failed: %v", err)
	}
	defer projBridge.Stop()

	// Single provider-backed TurnStreamController spans BOTH pre-SIGKILL and post-SIGKILL Rust PIDs!
	turnCtrl := grpcwire.NewTurnStreamControllerWithProvider(resilient)
	if _, err := turnCtrl.OpenSession(ctx, sessionID, true); err != nil {
		t.Fatalf("OpenSession(Create) failed: %v", err)
	}

	transcript := mygoshell.NewTranscriptController()
	turn1, err := turnCtrl.StartTurn(ctx, sessionID, "Turn 1 over tuned HTTP/2", 0, "lc-sub-1")
	if err != nil {
		t.Fatalf("Turn #1 StartTurn failed: %v", err)
	}
	_, out1, err := turnCtrl.WatchTurnResilient(ctx, turn1, "", grpcwire.WatchRetryPolicy{}, func(upd grpcwire.TurnStreamUpdate) {
		if upd.Started {
			transcript.BeginAssistantTurn(upd.TurnOrdinal)
		}
		if upd.TextDelta != "" {
			transcript.ApplyCheckpoint(upd.TurnOrdinal, upd.EventID, upd.TextDelta)
			coalescer.MarkDirty()
		}
		if upd.HasOutcome {
			transcript.CompleteTurn(upd.TurnOrdinal, upd.Outcome.String())
			coalescer.MarkDirty()
		}
	})
	if err != nil || out1 != appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_COMPLETED {
		t.Fatalf("Turn #1 WatchTurnResilient failed: outcome=%v err=%v", out1, err)
	}

	// Abruptly SIGKILL Rust PID #1 while driver, projection bridge, and turnCtrl are all live!
	pid1 := resilient.PID()
	p1, err := os.FindProcess(pid1)
	if err != nil {
		t.Fatalf("FindProcess(%d): %v", pid1, err)
	}
	if err := p1.Kill(); err != nil {
		t.Fatalf("SIGKILL(%d): %v", pid1, err)
	}

	select {
	case <-recoveredCh:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for Rust sidecar respawn\nstderr:\n%s", stderr.String())
	}

	pid2 := resilient.PID()
	if pid2 == 0 || pid2 == pid1 {
		t.Fatalf("expected fresh Rust PID after SIGKILL, pid1=%d pid2=%d", pid1, pid2)
	}

	// Using the EXACT SAME turnCtrl (no reconstruction!) rehydrate & stream Turn #2 on PID #2!
	if _, err := turnCtrl.OpenSession(ctx, sessionID, false); err != nil {
		t.Fatalf("OpenSession(Existing) on respawned PID #2 failed: %v", err)
	}
	turn2, err := turnCtrl.StartTurn(ctx, sessionID, "Turn 2 after automatic channel swap", 1, "lc-sub-2")
	if err != nil {
		t.Fatalf("Turn #2 StartTurn on PID #2 failed: %v", err)
	}
	_, out2, err := turnCtrl.WatchTurnResilient(ctx, turn2, "", grpcwire.WatchRetryPolicy{}, func(upd grpcwire.TurnStreamUpdate) {
		if upd.Started {
			transcript.BeginAssistantTurn(upd.TurnOrdinal)
		}
		if upd.TextDelta != "" {
			transcript.ApplyCheckpoint(upd.TurnOrdinal, upd.EventID, upd.TextDelta)
			coalescer.MarkDirty()
		}
		if upd.HasOutcome {
			transcript.CompleteTurn(upd.TurnOrdinal, upd.Outcome.String())
			coalescer.MarkDirty()
		}
	})
	if err != nil || out2 != appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_COMPLETED {
		t.Fatalf("Turn #2 WatchTurnResilient on PID #2 failed: outcome=%v err=%v", out2, err)
	}

	// Approve the freshly seeded gate on PID #2 using the EXACT SAME SessionDriver!
	rcpt, err := driver.ApproveGateOnce(ctx, "gate-e2e-1", "call-e2e-1", "lc-approve-after-swap")
	if err != nil || !rcpt.GetClaimed() {
		t.Fatalf("ApproveGateOnce on respawned PID #2 via existing SessionDriver failed: rcpt=%+v err=%v", rcpt, err)
	}

	stats := coalescer.Stats()
	if stats.Signals == 0 || stats.Flushes == 0 {
		t.Fatalf("expected non-zero 120Hz coalescer activity, got %+v", stats)
	}
}

// TestAutoDiscoverAndHookRealRustAppServer verifies the zero-flag desktop boot
// architecture: DiscoverAppServerBinary automatically locates the local Rust
// App Server (`marlin-app-server` or legacy `marlin-mygo-host`) without manual
// CLI flags, hooks it via LaunchResilientSidecar, probes all 6 services over
// HTTP/2 gRPC, streams a durable turn, and exits cleanly with code 0 on Close.
func TestAutoDiscoverAndHookRealRustAppServer(t *testing.T) {
	binPath, source, found := transport.DiscoverAppServerBinary(transport.SidecarDiscoverOptions{})
	if !found {
		t.Skip("no local Rust App Server binary discovered in env/PATH/workspace target")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	resilient, err := grpcwire.LaunchResilientSidecar(ctx, grpcwire.ResilientSidecarConfig{
		ID:          "auto-hooked-app-server",
		DisplayName: "Marlin App Server",
		Sidecar: transport.SidecarConfig{
			BinaryPath:   binPath,
			ExtraEnv:     []string{"MARLIN_BRIDGE_REAL_SESSION=1", "MARLIN_BRIDGE_CHUNK_DELAY_MS=1"},
			StartTimeout: 8 * time.Second,
		},
	})
	if err != nil {
		t.Fatalf("auto-hook LaunchResilientSidecar(%s via %s) failed: %v", binPath, source, err)
	}

	if err := resilient.Ping(ctx); err != nil {
		_ = resilient.Close()
		t.Fatalf("Ping auto-hooked Rust App Server (%s) failed: %v", source, err)
	}
	if resilient.Meta().Phase != transport.PhaseReady {
		_ = resilient.Close()
		t.Fatalf("expected PhaseReady from auto-hooked App Server, got %s", resilient.Meta().Phase)
	}

	turnCtrl := grpcwire.NewTurnStreamControllerWithProvider(resilient)
	if _, err := turnCtrl.OpenSession(ctx, "auto-hook-session", true); err != nil {
		_ = resilient.Close()
		t.Fatalf("OpenSession on auto-hooked App Server failed: %v", err)
	}
	turn, err := turnCtrl.StartTurn(ctx, "auto-hook-session", "Hello auto-hooked App Server", 0, "auto-sub-1")
	if err != nil {
		_ = resilient.Close()
		t.Fatalf("StartTurn on auto-hooked App Server failed: %v", err)
	}
	_, outcome, err := turnCtrl.WatchTurnResilient(ctx, turn, "", grpcwire.WatchRetryPolicy{}, nil)
	if err != nil || outcome != appv1.SessionTurnOutcome_SESSION_TURN_OUTCOME_COMPLETED {
		_ = resilient.Close()
		t.Fatalf("WatchTurnResilient on auto-hooked App Server failed: outcome=%v err=%v", outcome, err)
	}

	sidecar := resilient.Sidecar()
	if err := resilient.Close(); err != nil {
		t.Fatalf("resilient.Close failed: %v", err)
	}
	if exitErr := sidecar.ExitErr(); exitErr != nil {
		t.Fatalf("expected clean exit code 0 from auto-hooked App Server, got %v", exitErr)
	}
}
