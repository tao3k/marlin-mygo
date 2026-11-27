// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire_test

import (
	"context"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	appv1 "github.com/tao3k/marlin-mygo/gen/marlin/app/v1"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/device"
)

// TestManagedSidecarLaunchesRealRustBridge spawns a real compiled Rust
// mygo_bridge_server binary via SidecarSupervisor with a random ephemeral token,
// proves unauthenticated/wrong-token callers are rejected, runs live 6-service
// FederationSupervisor + SessionDriver + Gate -> OpenDeviceChannel roundtrip,
// and confirms clean child process teardown on Close().
// Skipped unless MARLIN_RUST_SIDECAR_BIN is set.
func TestManagedSidecarLaunchesRealRustBridge(t *testing.T) {
	binPath := os.Getenv("MARLIN_RUST_SIDECAR_BIN")
	if binPath == "" {
		t.Skip("set MARLIN_RUST_SIDECAR_BIN to run managed Rust sidecar E2E")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	managed, err := grpcwire.LaunchSidecarTarget(ctx, "managed-rust-core", "Managed Rust Core", transport.SidecarConfig{
		BinaryPath: binPath,
	})
	if err != nil {
		t.Fatalf("LaunchSidecarTarget failed: %v", err)
	}

	// 1. Wrong ephemeral token MUST fail with Unauthenticated!
	wrongTarget, err := grpcwire.Dial(grpcwire.DialConfig{
		ID:          "intruder",
		DisplayName: "Intruder",
		Mode:        transport.ModeLocalSidecar,
		Address:     managed.Sidecar.Address(),
		AuthToken:   "Bearer wrong-token",
	})
	if err != nil {
		_ = managed.Close()
		t.Fatal(err)
	}
	defer wrongTarget.Close()
	if _, err := wrongTarget.App().GetCapabilities(ctx, &appv1.GetCapabilitiesRequest{}); status.Code(err) != codes.Unauthenticated {
		_ = managed.Close()
		t.Fatalf("expected Unauthenticated for wrong ephemeral token, got %v", err)
	}

	// 2. Managed target with crypto/rand ephemeral token probes all 6 Rust services cleanly!
	reg := transport.NewTargetRegistry()
	if err := reg.Register(managed); err != nil {
		_ = managed.Close()
		t.Fatal(err)
	}
	fed := transport.NewFederationSupervisor(reg)
	fedSnap := fed.ProbeAll(ctx, 2*time.Second)
	if len(fedSnap.Nodes) != 1 || fedSnap.Nodes[0].Health.Phase != transport.PhaseReady || len(fedSnap.Nodes[0].Meta.SupportedSvcs) != 6 {
		_ = managed.Close()
		t.Fatalf("unexpected managed sidecar federation snapshot: %+v", fedSnap)
	}

	// 3. Full SessionDriver + human approval -> Rust ReceiptRef -> OpenDeviceChannel -> device.Bridge!
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	notify := &e2eNotify{}
	devBridge := device.NewBridge(schema.TargetMobileIOS)
	_ = devBridge.RegisterProvider(ctx, &device.IOSPersonalProvider{Notify: notify})

	invoked := make(chan device.InvocationResult, 1)
	driver := grpcwire.NewSessionDriver(grpcwire.DriverConfig{
		Target:       managed.Target,
		TargetKind:   schema.TargetDesktopMacOS,
		SessionID:    "sidecar-session",
		Workbench:    wb,
		Federation:   fed,
		DeviceBridge: devBridge,
		DeviceID:     "desktop-sidecar-client",
		DisplayName:  "Desktop Client",
		OnDeviceInvoked: func(_ device.InvocationRequest, res device.InvocationResult) {
			select {
			case invoked <- res:
			default:
			}
		},
	})
	if _, err := driver.Start(ctx); err != nil {
		_ = managed.Close()
		t.Fatalf("SessionDriver.Start against managed sidecar failed: %v", err)
	}
	if _, err := driver.ApproveGateOnce(ctx, "gate-e2e-1", "call-e2e-1", "sidecar-approve-1"); err != nil {
		driver.Stop()
		_ = managed.Close()
		t.Fatalf("ApproveGateOnce failed: %v", err)
	}
	select {
	case res := <-invoked:
		if !res.OK {
			driver.Stop()
			_ = managed.Close()
			t.Fatalf("expected OK device invocation, got %+v", res)
		}
	case <-time.After(3 * time.Second):
		driver.Stop()
		_ = managed.Close()
		t.Fatal("timed out waiting for device invocation over managed sidecar")
	}

	driver.Stop()

	// 4. Closing ManagedSidecarTarget MUST terminate and reap the Rust sidecar process cleanly!
	if err := managed.Close(); err != nil {
		t.Fatalf("managed.Close failed: %v", err)
	}
	select {
	case <-managed.Sidecar.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("managed Rust sidecar did not terminate after Close()")
	}
}
