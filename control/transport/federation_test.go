// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package transport_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

type probeTarget struct {
	meta    transport.TargetMeta
	pingErr error
}

func (p *probeTarget) Meta() transport.TargetMeta { return p.meta }
func (p *probeTarget) Ping(context.Context) error { return p.pingErr }
func (p *probeTarget) Close() error               { return nil }

func TestFederationSupervisorGroupingProbesAndCrossMachineGates(t *testing.T) {
	reg := transport.NewTargetRegistry()
	_ = reg.Register(&probeTarget{meta: transport.TargetMeta{
		ID: "iphone-16", DisplayName: "iPhone Personal", Mode: transport.ModeRemoteGRPC,
		TargetKind: schema.TargetMobileIOS, ActiveProfile: "ios-personal", Phase: transport.PhaseReady,
	}})
	_ = reg.Register(&probeTarget{meta: transport.TargetMeta{
		ID: "remote-gpu", DisplayName: "Linux H100 Worker", Mode: transport.ModeRemoteGRPC,
		TargetKind: schema.TargetDesktopLinux, ActiveProfile: "remote-dev", Phase: transport.PhaseConnecting,
	}})
	_ = reg.Register(&probeTarget{meta: transport.TargetMeta{
		ID: "local-mac", DisplayName: "Local MacBook", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, ActiveProfile: "desktop-dev", Phase: transport.PhaseReady,
	}})
	_ = reg.Register(&probeTarget{
		meta: transport.TargetMeta{
			ID: "remote-offline", DisplayName: "Offline Build Box", Mode: transport.ModeRemoteGRPC,
			TargetKind: schema.TargetRemoteHeadless, Phase: transport.PhaseConnecting,
		},
		pingErr: errors.New("connection refused"),
	})
	_, _ = reg.Switch("local-mac")

	sup := transport.NewFederationSupervisor(reg)
	sup.UpdateMetrics("remote-gpu", 2, 3, 1)
	sup.UpdateMetrics("iphone-16", 1, 1, 0)

	snap := sup.ProbeAll(context.Background(), time.Second)
	if snap.ActiveTargetID != "local-mac" {
		t.Fatalf("expected active target local-mac, got %q", snap.ActiveTargetID)
	}
	if snap.TotalPendingGates != 3 {
		t.Fatalf("expected 3 total pending gates across federation, got %d", snap.TotalPendingGates)
	}
	if len(snap.Nodes) != 4 {
		t.Fatalf("expected 4 federated nodes, got %d", len(snap.Nodes))
	}
	// Deterministic role order: LocalWorkstation -> RemoteAgent(s) -> MobileCompanion.
	if snap.Nodes[0].Role != transport.RoleLocalWorkstation || snap.Nodes[0].Meta.ID != "local-mac" {
		t.Fatalf("expected local-mac first, got %+v", snap.Nodes[0])
	}
	if snap.Nodes[1].Role != transport.RoleRemoteAgent || snap.Nodes[2].Role != transport.RoleRemoteAgent {
		t.Fatalf("expected remote agents in positions 1..2, got %+v, %+v", snap.Nodes[1], snap.Nodes[2])
	}
	if snap.Nodes[3].Role != transport.RoleMobileCompanion || snap.Nodes[3].Meta.ID != "iphone-16" {
		t.Fatalf("expected iphone-16 last as mobile-companion, got %+v", snap.Nodes[3])
	}
	if snap.Nodes[2].Health.Phase != transport.PhaseError || snap.Nodes[2].Health.LastError == "" {
		t.Fatalf("expected offline node to transition to PhaseError, got %+v", snap.Nodes[2].Health)
	}

	// NextGateTarget prefers a non-active machine with pending gates.
	nextID, ok := sup.NextGateTarget()
	if !ok || nextID != "remote-gpu" {
		t.Fatalf("expected NextGateTarget=remote-gpu, got %q (%v)", nextID, ok)
	}
}
