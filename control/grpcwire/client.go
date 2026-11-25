// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package grpcwire connects marlin-mygo's transport-agnostic pkg/* interfaces
// to the generated marlin.*.v1 HTTP/2 gRPC stubs under gen/marlin/*/v1.
package grpcwire

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"

	"github.com/tao3k/marlin-mygo/control/transport"
	appv1 "github.com/tao3k/marlin-mygo/gen/marlin/app/v1"
	devicev1 "github.com/tao3k/marlin-mygo/gen/marlin/device/v1"
	gatev1 "github.com/tao3k/marlin-mygo/gen/marlin/gate/v1"
	pluginv1 "github.com/tao3k/marlin-mygo/gen/marlin/plugin/v1"
	projectionv1 "github.com/tao3k/marlin-mygo/gen/marlin/projection/v1"
	uiv1 "github.com/tao3k/marlin-mygo/gen/marlin/ui/v1"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

// ChannelPolicy tunes HTTP/2 keepalive, flow-control windows, message bounds,
// and reconnect backoff so Go <-> Rust streams remain fluent and resilient
// across long-lived sessions, NAT/VPN hops, and laptop sleep-wake cycles.
type ChannelPolicy struct {
	KeepaliveTime         time.Duration // default 30s
	KeepaliveTimeout      time.Duration // default 10s
	PermitWithoutStream   bool          // default false (pings while streams active)
	DisableKeepalive      bool
	InitialWindowSize     int32         // default 1 MiB per stream
	InitialConnWindowSize int32         // default 4 MiB per connection
	MaxRecvMsgSize        int           // default 32 MiB
	ConnectBaseDelay      time.Duration // default 100ms
	ConnectMaxDelay       time.Duration // default 5s
	MinConnectTimeout     time.Duration // default 3s
}

// DefaultChannelPolicy returns tuned HTTP/2 defaults for Marlin Rust <-> Go channels.
func DefaultChannelPolicy() ChannelPolicy {
	return ChannelPolicy{
		KeepaliveTime:         30 * time.Second,
		KeepaliveTimeout:      10 * time.Second,
		PermitWithoutStream:   false,
		InitialWindowSize:     1 << 20,  // 1 MiB stream window
		InitialConnWindowSize: 4 << 20,  // 4 MiB conn window
		MaxRecvMsgSize:        32 << 20, // 32 MiB max snapshot/diff message
		ConnectBaseDelay:      100 * time.Millisecond,
		ConnectMaxDelay:       5 * time.Second,
		MinConnectTimeout:     3 * time.Second,
	}
}

func (p ChannelPolicy) withDefaults() ChannelPolicy {
	def := DefaultChannelPolicy()
	if p.KeepaliveTime <= 0 {
		p.KeepaliveTime = def.KeepaliveTime
	}
	if p.KeepaliveTimeout <= 0 {
		p.KeepaliveTimeout = def.KeepaliveTimeout
	}
	if p.InitialWindowSize <= 0 {
		p.InitialWindowSize = def.InitialWindowSize
	}
	if p.InitialConnWindowSize <= 0 {
		p.InitialConnWindowSize = def.InitialConnWindowSize
	}
	if p.MaxRecvMsgSize <= 0 {
		p.MaxRecvMsgSize = def.MaxRecvMsgSize
	}
	if p.ConnectBaseDelay <= 0 {
		p.ConnectBaseDelay = def.ConnectBaseDelay
	}
	if p.ConnectMaxDelay <= 0 {
		p.ConnectMaxDelay = def.ConnectMaxDelay
	}
	if p.MinConnectTimeout <= 0 {
		p.MinConnectTimeout = def.MinConnectTimeout
	}
	return p
}

// DialConfig configures one authenticated HTTP/2 gRPC Target connection.
type DialConfig struct {
	ID          string
	DisplayName string
	Mode        transport.Mode
	Address     string
	AuthToken   string
	// TLS configures TLS 1.3 or mutual TLS for remote targets.
	TLS *TLSConfig
	// Policy optionally overrides HTTP/2 keepalive, flow control, and connect backoff.
	Policy *ChannelPolicy
	// Optional low-level gRPC dial options (overrides default credentials when supplied).
	DialOptions []grpc.DialOption
}

// Target implements transport.TargetClient over real HTTP/2 gRPC and exposes
// all six orthogonal marlin.*.v1 service clients.
type Target struct {
	cc         *grpc.ClientConn
	app        appv1.AppServerClient
	gate       gatev1.ApprovalGateServiceClient
	ui         uiv1.UiWorkbenchServiceClient
	plugin     pluginv1.PluginServiceClient
	device     devicev1.DeviceBridgeServiceClient
	projection projectionv1.ProjectionQueryServiceClient

	mu   sync.RWMutex
	meta transport.TargetMeta
}

// Dial opens a gRPC channel to a local sidecar, mobile embedded host, or remote machine.
func Dial(cfg DialConfig) (*Target, error) {
	if cfg.ID == "" || cfg.Address == "" {
		return nil, fmt.Errorf("grpcwire: ID and Address are required")
	}
	policy := DefaultChannelPolicy()
	if cfg.Policy != nil {
		policy = cfg.Policy.withDefaults()
	}
	baseOpts := []grpc.DialOption{
		grpc.WithInitialWindowSize(policy.InitialWindowSize),
		grpc.WithInitialConnWindowSize(policy.InitialConnWindowSize),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(policy.MaxRecvMsgSize)),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  policy.ConnectBaseDelay,
				Multiplier: 1.6,
				Jitter:     0.2,
				MaxDelay:   policy.ConnectMaxDelay,
			},
			MinConnectTimeout: policy.MinConnectTimeout,
		}),
	}
	if !policy.DisableKeepalive {
		baseOpts = append(baseOpts, grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                policy.KeepaliveTime,
			Timeout:             policy.KeepaliveTimeout,
			PermitWithoutStream: policy.PermitWithoutStream,
		}))
	}

	opts := make([]grpc.DialOption, 0, len(baseOpts)+len(cfg.DialOptions)+3)
	opts = append(opts, baseOpts...)
	if len(cfg.DialOptions) == 0 {
		creds, err := BuildTransportCredentials(cfg.Mode, cfg.Address, cfg.TLS)
		if err != nil {
			return nil, err
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, cfg.DialOptions...)
	}
	if cfg.AuthToken != "" {
		opts = append(opts,
			grpc.WithUnaryInterceptor(unaryAuthInterceptor(cfg.AuthToken)),
			grpc.WithStreamInterceptor(streamAuthInterceptor(cfg.AuthToken)),
		)
	}
	cc, err := grpc.NewClient(cfg.Address, opts...)
	if err != nil {
		return nil, fmt.Errorf("grpcwire: dial %s: %w", cfg.Address, err)
	}
	return &Target{
		cc:         cc,
		app:        appv1.NewAppServerClient(cc),
		gate:       gatev1.NewApprovalGateServiceClient(cc),
		ui:         uiv1.NewUiWorkbenchServiceClient(cc),
		plugin:     pluginv1.NewPluginServiceClient(cc),
		device:     devicev1.NewDeviceBridgeServiceClient(cc),
		projection: projectionv1.NewProjectionQueryServiceClient(cc),
		meta: transport.TargetMeta{
			ID:          cfg.ID,
			DisplayName: cfg.DisplayName,
			Mode:        cfg.Mode,
			Address:     cfg.Address,
			Phase:       transport.PhaseConnecting,
		},
	}, nil
}

func (t *Target) Meta() transport.TargetMeta {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.meta
}

// Ping probes AppServer.GetCapabilities and refreshes TargetKind and SupportedSvcs.
func (t *Target) Ping(ctx context.Context) error {
	caps, err := t.app.GetCapabilities(ctx, &appv1.GetCapabilitiesRequest{})
	t.mu.Lock()
	defer t.mu.Unlock()
	if err != nil {
		t.meta.Phase = transport.PhaseError
		t.meta.LastErrorBrief = err.Error()
		return err
	}
	t.meta.Phase = transport.PhaseReady
	t.meta.LastErrorBrief = ""
	t.meta.TargetKind = TargetKindFromProto(caps.GetHostTargetKind())
	t.meta.SupportedSvcs = append([]string(nil), caps.GetSupportedServices()...)
	return nil
}

// CurrentTarget implements TargetProvider so a static *Target can be passed
// anywhere a hot-swappable TargetProvider is accepted.
func (t *Target) CurrentTarget() *Target { return t }

// ConnectivityState returns the live gRPC HTTP/2 channel connectivity state.
func (t *Target) ConnectivityState() connectivity.State {
	if t == nil || t.cc == nil {
		return connectivity.Shutdown
	}
	return t.cc.GetState()
}

// Wake resets connect backoff and initiates an immediate HTTP/2 reconnect
// attempt (useful after laptop wake-from-sleep or network interface changes).
func (t *Target) Wake() {
	if t == nil || t.cc == nil {
		return
	}
	t.cc.ResetConnectBackoff()
	t.cc.Connect()
}

// WaitForReady initiates connection if idle and blocks until the underlying
// HTTP/2 channel reaches connectivity.Ready, enters Shutdown, or ctx ends.
func (t *Target) WaitForReady(ctx context.Context) error {
	if t == nil || t.cc == nil {
		return fmt.Errorf("grpcwire: nil Target")
	}
	for {
		st := t.cc.GetState()
		switch st {
		case connectivity.Ready:
			return nil
		case connectivity.Shutdown:
			return fmt.Errorf("grpcwire: target %s channel is shut down", t.Meta().ID)
		case connectivity.Idle:
			t.cc.Connect()
		}
		if !t.cc.WaitForStateChange(ctx, st) {
			return ctx.Err()
		}
	}
}

// WatchConnectivity monitors gRPC channel connectivity transitions until ctx is
// cancelled or the channel enters connectivity.Shutdown, keeping Target.Meta().Phase
// synchronized with the live HTTP/2 transport state.
func (t *Target) WatchConnectivity(ctx context.Context, onChange func(prev, next connectivity.State)) {
	if t == nil || t.cc == nil {
		return
	}
	prev := t.cc.GetState()
	for {
		if prev == connectivity.Shutdown {
			return
		}
		if !t.cc.WaitForStateChange(ctx, prev) {
			return
		}
		next := t.cc.GetState()
		t.mu.Lock()
		t.meta.Phase = connectivityStateToPhase(next)
		if next == connectivity.Ready {
			t.meta.LastErrorBrief = ""
		}
		t.mu.Unlock()
		if onChange != nil {
			onChange(prev, next)
		}
		prev = next
	}
}

func (t *Target) App() appv1.AppServerClient                            { return t.app }
func (t *Target) Gate() gatev1.ApprovalGateServiceClient                { return t.gate }
func (t *Target) UI() uiv1.UiWorkbenchServiceClient                     { return t.ui }
func (t *Target) Plugin() pluginv1.PluginServiceClient                  { return t.plugin }
func (t *Target) Device() devicev1.DeviceBridgeServiceClient            { return t.device }
func (t *Target) Projection() projectionv1.ProjectionQueryServiceClient { return t.projection }

func (t *Target) Close() error {
	t.mu.Lock()
	t.meta.Phase = transport.PhaseDisconnected
	t.mu.Unlock()
	return t.cc.Close()
}

// PanelActionDispatcher adapts Target.Plugin() (or a hot-swappable Provider)
// to pluginhost.ActionDispatcher for one authorized project and session scope.
type PanelActionDispatcher struct {
	Target    *Target
	Provider  TargetProvider
	ProjectID string
	SessionID schema.SessionID
}

func (d *PanelActionDispatcher) SubmitScopedPanelAction(ctx context.Context, req pluginhost.ScopedActionRequest) (pluginhost.ScopedActionReceipt, error) {
	target, _ := resolveProviderTarget(d.Provider, d.Target)
	if target == nil {
		err := fmt.Errorf("grpcwire: PanelActionDispatcher has no active Target")
		return pluginhost.ScopedActionReceipt{Admitted: false, Denial: err.Error()}, err
	}
	resp, err := target.Plugin().SubmitScopedPanelAction(ctx, &pluginv1.SubmitScopedPanelActionRequest{
		ProjectId:            d.ProjectID,
		SessionId:            string(d.SessionID),
		PluginId:             string(req.PluginID),
		ContributionId:       string(req.ContributionID),
		ActivationGeneration: req.ActivationGeneration,
		ActionName:           req.ActionName,
		ArgsJson:             []byte(req.Args),
		IdempotencyKey:       req.IdempotencyKey,
	})
	if err != nil {
		return pluginhost.ScopedActionReceipt{Admitted: false, Denial: err.Error()}, err
	}
	rcpt := resp.GetReceipt()
	out := pluginhost.ScopedActionReceipt{
		Admitted: rcpt.GetAdmitted(),
		Result:   json.RawMessage(rcpt.GetResultJson()),
	}
	if r := rcpt.GetReceipt(); r != nil {
		out.ReceiptID = r.GetReceiptId()
	}
	if den := rcpt.GetDenial(); den != nil {
		out.Denial = den.GetMessage()
	}
	return out, nil
}

func unaryAuthInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", token)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func streamAuthInterceptor(token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", token)
		return streamer(ctx, desc, cc, method, opts...)
	}
}
