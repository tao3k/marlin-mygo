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

// DriverConfig wires one remote/local gRPC Target (or hot-swappable Provider)
// into the live MyGo shell, FederationSupervisor, pluginhost.Host, and runtime/device.Bridge.
type DriverConfig struct {
	Target                  *Target
	Provider                TargetProvider
	TargetKind              schema.TargetKind
	SessionID               schema.SessionID
	PreferredProfile        string
	Workbench               *shell.WorkbenchController
	Federation              *transport.FederationSupervisor
	PluginHost              *pluginhost.Host
	DeviceBridge            *device.Bridge
	DeviceID                string
	DisplayName             string
	ReconnectInitialBackoff time.Duration // defaults to 50ms
	ReconnectMaxBackoff     time.Duration // defaults to 2s
	DeviceHeartbeatInterval time.Duration // defaults to 15s
	OnWorkbench             func(schema.UIWorkbenchSnapshot)
	OnPendingGate           func(*gatev1.PendingGate)
	OnDeviceInvoked         func(req device.InvocationRequest, res device.InvocationResult)
	OnStreamState           func(streamName string, connected bool, err error)
}

// SessionDriver pumps real-time HTTP/2 gRPC streams (WatchWorkbench,
// WatchPendingGates, OpenDeviceChannel, OpenScopedPanelStream) into Marlin's
// local engine planes with automatic bounded-backoff reconnection and target
// hot-swap support without blocking UI rendering.
type SessionDriver struct {
	cfg    DriverConfig
	mu     sync.Mutex
	gates  map[string]*gatev1.PendingGate
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewSessionDriver(cfg DriverConfig) *SessionDriver {
	if cfg.ReconnectInitialBackoff <= 0 {
		cfg.ReconnectInitialBackoff = 50 * time.Millisecond
	}
	if cfg.ReconnectMaxBackoff <= 0 {
		cfg.ReconnectMaxBackoff = 2 * time.Second
	}
	if cfg.DeviceHeartbeatInterval <= 0 {
		cfg.DeviceHeartbeatInterval = 15 * time.Second
	}
	return &SessionDriver{
		cfg:   cfg,
		gates: make(map[string]*gatev1.PendingGate),
	}
}

func (d *SessionDriver) activeTarget() (*Target, uint64) {
	return resolveProviderTarget(d.cfg.Provider, d.cfg.Target)
}

func (d *SessionDriver) targetID() string {
	if t, _ := d.activeTarget(); t != nil {
		return t.Meta().ID
	}
	return ""
}

func (d *SessionDriver) notifyStreamState(name string, connected bool, err error) {
	if d.cfg.OnStreamState != nil {
		d.cfg.OnStreamState(name, connected, err)
	}
}

// Start performs initial OpenWorkbench + ListPendingGates hydration and spawns
// auto-reconnecting background stream pumps until Stop() or ctx cancellation.
func (d *SessionDriver) Start(ctx context.Context) (*schema.UIWorkbenchSnapshot, error) {
	target, _ := d.activeTarget()
	if target == nil {
		return nil, fmt.Errorf("grpcwire: DriverConfig.Target or Provider is required")
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
	wbResp, err := target.UI().OpenWorkbench(runCtx, openReq)
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
	if listResp, err := target.Gate().ListPendingGates(runCtx, &gatev1.ListPendingGatesRequest{}); err == nil {
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
// active plugin generation and feeds every host-filtered event into PluginHost.RouteEvent,
// auto-reconnecting on transient HTTP/2 stream interruptions.
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
	target, seenGen := d.activeTarget()
	if target == nil {
		return fmt.Errorf("grpcwire: SessionDriver has no active Target")
	}
	req := &pluginv1.OpenScopedPanelStreamRequest{
		ProjectId:            projectID,
		SessionId:            string(d.cfg.SessionID),
		PluginId:             string(pluginID),
		ContributionId:       string(contributionID),
		ActivationGeneration: activationGen,
	}
	stream, err := target.Plugin().OpenScopedPanelStream(ctx, req)
	if err != nil {
		return err
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		curStream := stream
		curGen := seenGen
		backoff := d.cfg.ReconnectInitialBackoff
		for {
			if curStream == nil {
				t, gen := d.activeTarget()
				curGen = gen
				if t != nil {
					s, openErr := t.Plugin().OpenScopedPanelStream(ctx, req)
					if openErr == nil {
						curStream = s
						backoff = d.cfg.ReconnectInitialBackoff
						d.notifyStreamState("plugin:"+string(pluginID), true, nil)
					} else if !isRetryableStreamError(ctx, openErr) {
						d.notifyStreamState("plugin:"+string(pluginID), false, openErr)
						return
					} else {
						d.notifyStreamState("plugin:"+string(pluginID), false, openErr)
						if !waitReconnectOrSwap(ctx, d.cfg.Provider, curGen, backoff) {
							return
						}
						backoff = min(backoff*2, d.cfg.ReconnectMaxBackoff)
						continue
					}
				} else {
					if !waitReconnectOrSwap(ctx, d.cfg.Provider, curGen, backoff) {
						return
					}
					backoff = min(backoff*2, d.cfg.ReconnectMaxBackoff)
					continue
				}
			}
			resp, recvErr := curStream.Recv()
			if recvErr != nil {
				curStream = nil
				if !isRetryableStreamError(ctx, recvErr) {
					d.notifyStreamState("plugin:"+string(pluginID), false, recvErr)
					return
				}
				d.notifyStreamState("plugin:"+string(pluginID), false, recvErr)
				if !waitReconnectOrSwap(ctx, d.cfg.Provider, curGen, backoff) {
					return
				}
				backoff = min(backoff*2, d.cfg.ReconnectMaxBackoff)
				continue
			}
			backoff = d.cfg.ReconnectInitialBackoff
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
	return d.SubmitToolReviewWithRationale(ctx, gateID, callID, true, "", idempotencyKey)
}

// RejectGate submits a fail-closed rejection decision to Rust ApprovalGateService
// and clears the local pending gate badge upon claim.
func (d *SessionDriver) RejectGate(
	ctx context.Context,
	gateID string,
	callID string,
	idempotencyKey string,
) (*gatev1.GateDecisionReceipt, error) {
	return d.SubmitToolReviewWithRationale(ctx, gateID, callID, false, "", idempotencyKey)
}

// SubmitToolReviewWithRationale submits a one-shot tool-review decision carrying
// an optional human rationale string to Rust ApprovalGateService.
func (d *SessionDriver) SubmitToolReviewWithRationale(
	ctx context.Context,
	gateID string,
	callID string,
	approve bool,
	rationale string,
	idempotencyKey string,
) (*gatev1.GateDecisionReceipt, error) {
	decision := gatev1.ToolReviewDecision_TOOL_REVIEW_DECISION_REJECT
	if approve {
		decision = gatev1.ToolReviewDecision_TOOL_REVIEW_DECISION_APPROVE_ONCE
	}
	target, _ := d.activeTarget()
	if target == nil {
		return nil, fmt.Errorf("grpcwire: SessionDriver has no active Target")
	}
	req := &gatev1.SubmitToolReviewDecisionRequest{
		GateId:         gateID,
		CallId:         callID,
		Decision:       decision,
		IdempotencyKey: idempotencyKey,
	}
	if rationale != "" {
		r := rationale
		req.Rationale = &r
	}
	resp, err := target.Gate().SubmitToolReviewDecision(ctx, req)
	if err != nil {
		return nil, err
	}
	rcpt := resp.GetReceipt()
	if rcpt != nil && rcpt.GetClaimed() {
		d.clearGate(gateID)
	}
	return rcpt, nil
}

// SubmitGraphHumanDecision submits a structured graph human-gate decision
// (Approve, Reject, AskQuestion, or RequestPatch) with an optional note.
func (d *SessionDriver) SubmitGraphHumanDecision(
	ctx context.Context,
	gateID string,
	decision gatev1.GraphHumanDecision,
	note string,
	idempotencyKey string,
) (*gatev1.GateDecisionReceipt, error) {
	target, _ := d.activeTarget()
	if target == nil {
		return nil, fmt.Errorf("grpcwire: SessionDriver has no active Target")
	}
	req := &gatev1.SubmitGraphHumanDecisionRequest{
		GateId:         gateID,
		Decision:       decision,
		IdempotencyKey: idempotencyKey,
	}
	if note != "" {
		n := note
		req.Note = &n
	}
	resp, err := target.Gate().SubmitGraphHumanDecision(ctx, req)
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
		if id := d.targetID(); id != "" {
			d.cfg.Federation.UpdateMetrics(id, count, 1, 0)
		}
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
		if id := d.targetID(); id != "" {
			d.cfg.Federation.UpdateMetrics(id, count, 1, 0)
		}
	}
}

func (d *SessionDriver) pumpWorkbench(ctx context.Context, afterGen uint64) {
	defer d.wg.Done()
	lastGen := afterGen
	backoff := d.cfg.ReconnectInitialBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		target, seenGen := d.activeTarget()
		if target == nil {
			if !waitReconnectOrSwap(ctx, d.cfg.Provider, seenGen, backoff) {
				return
			}
			backoff = min(backoff*2, d.cfg.ReconnectMaxBackoff)
			continue
		}
		stream, err := target.UI().WatchWorkbench(ctx, &uiv1.WatchWorkbenchRequest{
			SessionId:       string(d.cfg.SessionID),
			AfterGeneration: lastGen,
		})
		if err != nil {
			d.notifyStreamState("workbench", false, err)
			if !isRetryableStreamError(ctx, err) {
				return
			}
			if !waitReconnectOrSwap(ctx, d.cfg.Provider, seenGen, backoff) {
				return
			}
			backoff = min(backoff*2, d.cfg.ReconnectMaxBackoff)
			continue
		}
		d.notifyStreamState("workbench", true, nil)
		backoff = d.cfg.ReconnectInitialBackoff
		for {
			resp, recvErr := stream.Recv()
			if recvErr != nil {
				d.notifyStreamState("workbench", false, recvErr)
				if !isRetryableStreamError(ctx, recvErr) {
					return
				}
				break
			}
			snap, convErr := WorkbenchFromProto(resp.GetWorkbench())
			if convErr != nil {
				continue
			}
			if snap.Generation > lastGen {
				lastGen = snap.Generation
			}
			d.admitWorkbench(snap)
		}
		if !waitReconnectOrSwap(ctx, d.cfg.Provider, seenGen, backoff) {
			return
		}
		backoff = min(backoff*2, d.cfg.ReconnectMaxBackoff)
	}
}

func (d *SessionDriver) pumpPendingGates(ctx context.Context) {
	defer d.wg.Done()
	backoff := d.cfg.ReconnectInitialBackoff
	firstAttempt := true
	for {
		if ctx.Err() != nil {
			return
		}
		target, seenGen := d.activeTarget()
		if target == nil {
			if !waitReconnectOrSwap(ctx, d.cfg.Provider, seenGen, backoff) {
				return
			}
			backoff = min(backoff*2, d.cfg.ReconnectMaxBackoff)
			continue
		}
		if !firstAttempt {
			if listResp, err := target.Gate().ListPendingGates(ctx, &gatev1.ListPendingGatesRequest{}); err == nil {
				for _, g := range listResp.GetGates() {
					d.recordGate(g)
				}
			}
		}
		firstAttempt = false

		stream, err := target.Gate().WatchPendingGates(ctx, &gatev1.WatchPendingGatesRequest{})
		if err != nil {
			d.notifyStreamState("pending_gates", false, err)
			if !isRetryableStreamError(ctx, err) {
				return
			}
			if !waitReconnectOrSwap(ctx, d.cfg.Provider, seenGen, backoff) {
				return
			}
			backoff = min(backoff*2, d.cfg.ReconnectMaxBackoff)
			continue
		}
		d.notifyStreamState("pending_gates", true, nil)
		backoff = d.cfg.ReconnectInitialBackoff
		for {
			resp, recvErr := stream.Recv()
			if recvErr != nil {
				d.notifyStreamState("pending_gates", false, recvErr)
				if !isRetryableStreamError(ctx, recvErr) {
					return
				}
				break
			}
			d.recordGate(resp.GetGate())
		}
		if !waitReconnectOrSwap(ctx, d.cfg.Provider, seenGen, backoff) {
			return
		}
		backoff = min(backoff*2, d.cfg.ReconnectMaxBackoff)
	}
}

func (d *SessionDriver) openDeviceStream(ctx context.Context, target *Target) (devicev1.DeviceBridgeService_OpenDeviceChannelClient, error) {
	descs, err := d.cfg.DeviceBridge.SnapshotDescriptors(ctx)
	if err != nil {
		return nil, err
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
		devID = target.Meta().ID
	}
	regResp, err := target.Device().RegisterDeviceNode(ctx, &devicev1.RegisterDeviceNodeRequest{
		DeviceId:     devID,
		DisplayName:  d.cfg.DisplayName,
		TargetKind:   TargetKindToProto(d.cfg.TargetKind),
		OsVersion:    "marlin-mygo",
		AppVersion:   "0.2.0",
		Capabilities: protoDescs,
	})
	if err != nil {
		return nil, fmt.Errorf("grpcwire: RegisterDeviceNode: %w", err)
	}
	stream, err := target.Device().OpenDeviceChannel(ctx)
	if err != nil {
		return nil, fmt.Errorf("grpcwire: OpenDeviceChannel: %w", err)
	}
	if err := stream.Send(&devicev1.OpenDeviceChannelRequest{
		Frame: &devicev1.OpenDeviceChannelRequest_CapabilityUpdate{
			CapabilityUpdate: &devicev1.DeviceCapabilityUpdate{
				LeaseId:      regResp.GetLeaseId(),
				Capabilities: protoDescs,
			},
		},
	}); err != nil {
		return nil, err
	}
	// Send one initial heartbeat to confirm channel liveness.
	_ = stream.Send(&devicev1.OpenDeviceChannelRequest{
		Frame: &devicev1.OpenDeviceChannelRequest_Heartbeat{
			Heartbeat: &devicev1.DeviceHeartbeat{UnixMs: uint64(time.Now().UnixMilli())},
		},
	})
	return stream, nil
}

func (d *SessionDriver) startDeviceChannel(ctx context.Context) error {
	target, seenGen := d.activeTarget()
	if target == nil {
		return fmt.Errorf("grpcwire: SessionDriver has no active Target for device channel")
	}
	initialStream, err := d.openDeviceStream(ctx, target)
	if err != nil {
		return err
	}
	d.notifyStreamState("device_channel", true, nil)

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		curStream := initialStream
		curGen := seenGen
		backoff := d.cfg.ReconnectInitialBackoff

		for {
			if ctx.Err() != nil {
				return
			}
			if curStream == nil {
				t, gen := d.activeTarget()
				curGen = gen
				if t == nil {
					if !waitReconnectOrSwap(ctx, d.cfg.Provider, curGen, backoff) {
						return
					}
					backoff = min(backoff*2, d.cfg.ReconnectMaxBackoff)
					continue
				}
				s, openErr := d.openDeviceStream(ctx, t)
				if openErr != nil {
					d.notifyStreamState("device_channel", false, openErr)
					if !isRetryableStreamError(ctx, openErr) {
						return
					}
					if !waitReconnectOrSwap(ctx, d.cfg.Provider, curGen, backoff) {
						return
					}
					backoff = min(backoff*2, d.cfg.ReconnectMaxBackoff)
					continue
				}
				curStream = s
				backoff = d.cfg.ReconnectInitialBackoff
				d.notifyStreamState("device_channel", true, nil)
			}

			var sendMu sync.Mutex
			sendFrame := func(frame *devicev1.OpenDeviceChannelRequest) error {
				sendMu.Lock()
				defer sendMu.Unlock()
				return curStream.Send(frame)
			}

			hbCtx, hbCancel := context.WithCancel(ctx)
			hbDone := make(chan struct{})
			go func() {
				defer close(hbDone)
				ticker := time.NewTicker(d.cfg.DeviceHeartbeatInterval)
				defer ticker.Stop()
				for {
					select {
					case <-hbCtx.Done():
						return
					case <-ticker.C:
						_ = sendFrame(&devicev1.OpenDeviceChannelRequest{
							Frame: &devicev1.OpenDeviceChannelRequest_Heartbeat{
								Heartbeat: &devicev1.DeviceHeartbeat{UnixMs: uint64(time.Now().UnixMilli())},
							},
						})
					}
				}
			}()

			var recvErr error
			for {
				var resp *devicev1.OpenDeviceChannelResponse
				resp, recvErr = curStream.Recv()
				if recvErr != nil {
					break
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
				_ = sendFrame(&devicev1.OpenDeviceChannelRequest{
					Frame: &devicev1.OpenDeviceChannelRequest_InvocationResult{
						InvocationResult: out,
					},
				})
			}

			hbCancel()
			<-hbDone
			curStream = nil

			d.notifyStreamState("device_channel", false, recvErr)
			if !isRetryableStreamError(ctx, recvErr) {
				return
			}
			if errors.Is(recvErr, io.EOF) && ctx.Err() != nil {
				return
			}
			if !waitReconnectOrSwap(ctx, d.cfg.Provider, curGen, backoff) {
				return
			}
			backoff = min(backoff*2, d.cfg.ReconnectMaxBackoff)
		}
	}()
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
