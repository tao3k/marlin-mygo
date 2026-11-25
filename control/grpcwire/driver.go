// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	commonv1 "github.com/tao3k/marlin-mygo/gen/marlin/common/v1"
	devicev1 "github.com/tao3k/marlin-mygo/gen/marlin/device/v1"
	gatev1 "github.com/tao3k/marlin-mygo/gen/marlin/gate/v1"
	pluginv1 "github.com/tao3k/marlin-mygo/gen/marlin/plugin/v1"
	uiv1 "github.com/tao3k/marlin-mygo/gen/marlin/ui/v1"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/device"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

// CaptureSinkAdapter forwards local device.Bridge.SyncCaptures payloads to
// marlin.device.v1.DeviceBridgeService.IngestCapture over HTTP/2 gRPC.
type CaptureSinkAdapter struct {
	Target *Target
}

func (a *CaptureSinkAdapter) IngestCapture(
	ctx context.Context,
	deviceID string,
	sessionID string,
	payload device.CapturePayload,
	idempotencyKey string,
) error {
	if a.Target == nil {
		return fmt.Errorf("grpcwire: nil Target in CaptureSinkAdapter")
	}
	req := &devicev1.IngestCaptureRequest{
		SessionId:      sessionID,
		DeviceId:       deviceID,
		SourceChannel:  payload.SourceChannel,
		Metadata:       payload.Metadata,
		IdempotencyKey: idempotencyKey,
	}
	if payload.Text != "" {
		txt := payload.Text
		req.Text = &txt
	}
	if payload.OCRText != "" {
		ocr := payload.OCRText
		req.OnDeviceOcrText = &ocr
	}
	resp, err := a.Target.Device().IngestCapture(ctx, req)
	if err != nil {
		return err
	}
	if !resp.GetAccepted() {
		return fmt.Errorf("grpcwire: capture rejected: %s", resp.GetDenial().GetMessage())
	}
	return nil
}

// DriverConfig wires one remote/local gRPC Target into the live MyGo shell,
// FederationSupervisor, pluginhost.Host, and runtime/device.Bridge.
type DriverConfig struct {
	Target           *Target
	TargetKind       schema.TargetKind
	SessionID        schema.SessionID
	PreferredProfile string
	Workbench        *shell.WorkbenchController
	Federation       *transport.FederationSupervisor
	PluginHost       *pluginhost.Host
	DeviceBridge     *device.Bridge
	DeviceID         string
	DisplayName      string
	OnWorkbench      func(schema.UIWorkbenchSnapshot)
	OnPendingGate    func(*gatev1.PendingGate)
	OnDeviceInvoked  func(req device.InvocationRequest, res device.InvocationResult)
}

// SessionDriver pumps real-time HTTP/2 gRPC streams (WatchWorkbench,
// WatchPendingGates, OpenDeviceChannel, OpenScopedPanelStream) into Marlin's
// local engine planes without blocking UI rendering.
type SessionDriver struct {
	cfg    DriverConfig
	mu     sync.Mutex
	gates  map[string]*gatev1.PendingGate
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewSessionDriver(cfg DriverConfig) *SessionDriver {
	return &SessionDriver{
		cfg:   cfg,
		gates: make(map[string]*gatev1.PendingGate),
	}
}

// Start performs initial OpenWorkbench + ListPendingGates hydration and spawns
// background stream pumps until Stop() or ctx cancellation.
func (d *SessionDriver) Start(ctx context.Context) (*schema.UIWorkbenchSnapshot, error) {
	if d.cfg.Target == nil {
		return nil, fmt.Errorf("grpcwire: DriverConfig.Target is required")
	}
	runCtx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.cancel = cancel
	d.mu.Unlock()

	openReq := &uiv1.OpenWorkbenchRequest{
		SessionId:        string(d.cfg.SessionID),
		ClientTargetKind: TargetKindToProto(d.cfg.TargetKind),
	}
	if d.cfg.PreferredProfile != "" {
		p := d.cfg.PreferredProfile
		openReq.PreferredProfile = &p
	}
	wbResp, err := d.cfg.Target.UI().OpenWorkbench(runCtx, openReq)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("grpcwire: OpenWorkbench: %w", err)
	}
	snap, err := WorkbenchFromProto(wbResp.GetWorkbench())
	if err != nil {
		cancel()
		return nil, err
	}
	d.admitWorkbench(snap)

	// Hydrate initial pending gates before starting stream watch.
	if listResp, err := d.cfg.Target.Gate().ListPendingGates(runCtx, &gatev1.ListPendingGatesRequest{}); err == nil {
		for _, g := range listResp.GetGates() {
			d.recordGate(g)
		}
	}

	d.wg.Add(2)
	go d.pumpWorkbench(runCtx, snap.Generation)
	go d.pumpPendingGates(runCtx)

	if d.cfg.DeviceBridge != nil {
		if err := d.startDeviceChannel(runCtx); err != nil {
			cancel()
			d.wg.Wait()
			return nil, err
		}
	}
	return &snap, nil
}

// WatchPluginStream subscribes to marlin.plugin.v1.OpenScopedPanelStream for one
// active plugin generation and feeds every host-filtered event into PluginHost.RouteEvent.
func (d *SessionDriver) WatchPluginStream(
	ctx context.Context,
	projectID string,
	pluginID schema.PluginID,
	contributionID schema.ContributionID,
	activationGen uint64,
) error {
	if d.cfg.PluginHost == nil {
		return fmt.Errorf("grpcwire: PluginHost not configured on SessionDriver")
	}
	stream, err := d.cfg.Target.Plugin().OpenScopedPanelStream(ctx, &pluginv1.OpenScopedPanelStreamRequest{
		ProjectId:            projectID,
		SessionId:            string(d.cfg.SessionID),
		PluginId:             string(pluginID),
		ContributionId:       string(contributionID),
		ActivationGeneration: activationGen,
	})
	if err != nil {
		return err
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		for {
			resp, err := stream.Recv()
			if err != nil {
				return
			}
			ev := resp.GetEvent()
			if ev == nil {
				continue
			}
			_ = d.cfg.PluginHost.RouteEvent(pluginhost.ScopedEvent{
				EventID:              ev.GetEventId(),
				PluginID:             schema.PluginID(ev.GetPluginId()),
				ActivationGeneration: ev.GetActivationGeneration(),
				Topic:                ev.GetTopic(),
				Payload:              json.RawMessage(ev.GetPayloadJson()),
			})
		}
	}()
	return nil
}

// ApproveGateOnce submits a one-shot human approval decision to Rust ApprovalGateService
// and clears the local pending gate badge upon claim.
func (d *SessionDriver) ApproveGateOnce(
	ctx context.Context,
	gateID string,
	callID string,
	idempotencyKey string,
) (*gatev1.GateDecisionReceipt, error) {
	resp, err := d.cfg.Target.Gate().SubmitToolReviewDecision(ctx, &gatev1.SubmitToolReviewDecisionRequest{
		GateId:         gateID,
		CallId:         callID,
		Decision:       gatev1.ToolReviewDecision_TOOL_REVIEW_DECISION_APPROVE_ONCE,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	rcpt := resp.GetReceipt()
	if rcpt != nil && rcpt.GetClaimed() {
		d.clearGate(gateID)
	}
	return rcpt, nil
}

// Stop cancels all background HTTP/2 streams and waits for clean goroutine shutdown.
func (d *SessionDriver) Stop() {
	d.mu.Lock()
	cancel := d.cancel
	d.cancel = nil
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	d.wg.Wait()
}

func (d *SessionDriver) admitWorkbench(snap schema.UIWorkbenchSnapshot) {
	if d.cfg.Workbench != nil {
		_ = d.cfg.Workbench.AdmitSnapshot(snap)
	}
	if d.cfg.OnWorkbench != nil {
		d.cfg.OnWorkbench(snap)
	}
}

func (d *SessionDriver) recordGate(g *gatev1.PendingGate) {
	if g == nil || g.GetGateId() == "" {
		return
	}
	d.mu.Lock()
	d.gates[g.GetGateId()] = g
	count := len(d.gates)
	d.mu.Unlock()
	if d.cfg.Federation != nil {
		d.cfg.Federation.UpdateMetrics(d.cfg.Target.Meta().ID, count, 1, 0)
	}
	if d.cfg.OnPendingGate != nil {
		d.cfg.OnPendingGate(g)
	}
}

func (d *SessionDriver) clearGate(gateID string) {
	d.mu.Lock()
	delete(d.gates, gateID)
	count := len(d.gates)
	d.mu.Unlock()
	if d.cfg.Federation != nil {
		d.cfg.Federation.UpdateMetrics(d.cfg.Target.Meta().ID, count, 1, 0)
	}
}

func (d *SessionDriver) pumpWorkbench(ctx context.Context, afterGen uint64) {
	defer d.wg.Done()
	stream, err := d.cfg.Target.UI().WatchWorkbench(ctx, &uiv1.WatchWorkbenchRequest{
		SessionId:       string(d.cfg.SessionID),
		AfterGeneration: afterGen,
	})
	if err != nil {
		return
	}
	for {
		resp, err := stream.Recv()
		if err != nil {
			return
		}
		snap, err := WorkbenchFromProto(resp.GetWorkbench())
		if err != nil {
			continue
		}
		d.admitWorkbench(snap)
	}
}

func (d *SessionDriver) pumpPendingGates(ctx context.Context) {
	defer d.wg.Done()
	stream, err := d.cfg.Target.Gate().WatchPendingGates(ctx, &gatev1.WatchPendingGatesRequest{})
	if err != nil {
		return
	}
	for {
		resp, err := stream.Recv()
		if err != nil {
			return
		}
		d.recordGate(resp.GetGate())
	}
}

func (d *SessionDriver) startDeviceChannel(ctx context.Context) error {
	descs, err := d.cfg.DeviceBridge.SnapshotDescriptors(ctx)
	if err != nil {
		return err
	}
	protoDescs := make([]*devicev1.DeviceCapabilityDescriptor, 0, len(descs))
	for _, desc := range descs {
		protoDescs = append(protoDescs, &devicev1.DeviceCapabilityDescriptor{
			CapabilityId:   string(desc.CapabilityID),
			ProtocolFamily: desc.ProtocolFamily,
			Permission:     osPermissionToProto(desc.Permission),
			InputSchemaId:  string(desc.InputSchemaID),
			OutputSchemaId: string(desc.OutputSchemaID),
			ReadOnly:       desc.ReadOnly,
		})
	}
	devID := d.cfg.DeviceID
	if devID == "" {
		devID = d.cfg.Target.Meta().ID
	}
	regResp, err := d.cfg.Target.Device().RegisterDeviceNode(ctx, &devicev1.RegisterDeviceNodeRequest{
		DeviceId:     devID,
		DisplayName:  d.cfg.DisplayName,
		TargetKind:   TargetKindToProto(d.cfg.TargetKind),
		OsVersion:    "marlin-mygo",
		AppVersion:   "0.2.0",
		Capabilities: protoDescs,
	})
	if err != nil {
		return fmt.Errorf("grpcwire: RegisterDeviceNode: %w", err)
	}
	stream, err := d.cfg.Target.Device().OpenDeviceChannel(ctx)
	if err != nil {
		return fmt.Errorf("grpcwire: OpenDeviceChannel: %w", err)
	}
	if err := stream.Send(&devicev1.OpenDeviceChannelRequest{
		Frame: &devicev1.OpenDeviceChannelRequest_CapabilityUpdate{
			CapabilityUpdate: &devicev1.DeviceCapabilityUpdate{
				LeaseId:      regResp.GetLeaseId(),
				Capabilities: protoDescs,
			},
		},
	}); err != nil {
		return err
	}

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		for {
			resp, err := stream.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) || ctx.Err() != nil {
					return
				}
				return
			}
			inv := resp.GetInvocationRequest()
			if inv == nil {
				continue
			}
			req := device.InvocationRequest{
				InvocationID:  inv.GetInvocationId(),
				SessionID:     schema.SessionID(inv.GetSessionId()),
				CapabilityID:  schema.CapabilityID(inv.GetCapabilityId()),
				GateReceiptID: inv.GetGateReceipt().GetReceiptId(),
				Args:          json.RawMessage(inv.GetArgsJson()),
			}
			res, dispatchErr := d.cfg.DeviceBridge.Dispatch(ctx, req)
			if d.cfg.OnDeviceInvoked != nil {
				d.cfg.OnDeviceInvoked(req, res)
			}
			out := &devicev1.DeviceInvocationResult{
				InvocationId: req.InvocationID,
				Ok:           dispatchErr == nil && res.OK,
				ResultJson:   []byte(res.Result),
			}
			if dispatchErr != nil || !res.OK {
				msg := res.ErrorMessage
				if msg == "" && dispatchErr != nil {
					msg = dispatchErr.Error()
				}
				out.Denial = &commonv1.TypedDenial{
					Code:    commonv1.DenialCode_DENIAL_CODE_CAPABILITY_NOT_GRANTED,
					Message: msg,
				}
			}
			_ = stream.Send(&devicev1.OpenDeviceChannelRequest{
				Frame: &devicev1.OpenDeviceChannelRequest_InvocationResult{
					InvocationResult: out,
				},
			})
		}
	}()
	// Send one initial heartbeat to confirm channel liveness.
	_ = stream.Send(&devicev1.OpenDeviceChannelRequest{
		Frame: &devicev1.OpenDeviceChannelRequest_Heartbeat{
			Heartbeat: &devicev1.DeviceHeartbeat{UnixMs: uint64(time.Now().UnixMilli())},
		},
	})
	return nil
}

// TargetKindToProto converts schema.TargetKind to marlin.common.v1.TargetKind.
func TargetKindToProto(kind schema.TargetKind) commonv1.TargetKind {
	switch kind {
	case schema.TargetDesktopMacOS:
		return commonv1.TargetKind_TARGET_KIND_DESKTOP_MACOS
	case schema.TargetDesktopLinux:
		return commonv1.TargetKind_TARGET_KIND_DESKTOP_LINUX
	case schema.TargetDesktopWindows:
		return commonv1.TargetKind_TARGET_KIND_DESKTOP_WINDOWS
	case schema.TargetMobileIOS:
		return commonv1.TargetKind_TARGET_KIND_MOBILE_IOS
	case schema.TargetMobileAndroid:
		return commonv1.TargetKind_TARGET_KIND_MOBILE_ANDROID
	case schema.TargetRemoteHeadless:
		return commonv1.TargetKind_TARGET_KIND_REMOTE_HEADLESS
	default:
		return commonv1.TargetKind_TARGET_KIND_UNSPECIFIED
	}
}

func osPermissionToProto(p device.OsPermissionState) devicev1.OsPermissionState {
	switch p {
	case device.PermissionGranted:
		return devicev1.OsPermissionState_OS_PERMISSION_STATE_GRANTED
	case device.PermissionLimited:
		return devicev1.OsPermissionState_OS_PERMISSION_STATE_LIMITED
	case device.PermissionPrompt:
		return devicev1.OsPermissionState_OS_PERMISSION_STATE_PROMPT
	case device.PermissionDenied:
		return devicev1.OsPermissionState_OS_PERMISSION_STATE_DENIED
	case device.PermissionUnsupported:
		return devicev1.OsPermissionState_OS_PERMISSION_STATE_UNSUPPORTED
	default:
		return devicev1.OsPermissionState_OS_PERMISSION_STATE_UNSPECIFIED
	}
}
