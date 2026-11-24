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

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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

// DialConfig configures one authenticated HTTP/2 gRPC Target connection.
type DialConfig struct {
	ID          string
	DisplayName string
	Mode        transport.Mode
	Address     string
	AuthToken   string
	// TransportCredentials defaults to loopback insecure when nil; supply TLS/mTLS for remote hosts.
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
	opts := append([]grpc.DialOption{}, cfg.DialOptions...)
	if len(opts) == 0 {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
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

// PanelActionDispatcher adapts Target.Plugin() to pluginhost.ActionDispatcher
// for one authorized project and session scope.
type PanelActionDispatcher struct {
	Target    *Target
	ProjectID string
	SessionID schema.SessionID
}

func (d *PanelActionDispatcher) SubmitScopedPanelAction(ctx context.Context, req pluginhost.ScopedActionRequest) (pluginhost.ScopedActionReceipt, error) {
	resp, err := d.Target.Plugin().SubmitScopedPanelAction(ctx, &pluginv1.SubmitScopedPanelActionRequest{
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
