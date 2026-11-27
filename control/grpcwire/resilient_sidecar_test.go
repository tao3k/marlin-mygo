// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
	appv1 "github.com/tao3k/marlin-mygo/gen/marlin/app/v1"
)

type helperAppServer struct {
	appv1.UnimplementedAppServerServer
	expectedAuth string
}

func (s *helperAppServer) GetCapabilities(ctx context.Context, _ *appv1.GetCapabilitiesRequest) (*appv1.GetCapabilitiesResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	vals := md.Get("authorization")
	if len(vals) == 0 || vals[0] != s.expectedAuth {
		return nil, status.Error(codes.Unauthenticated, "bad bearer token")
	}
	return &appv1.GetCapabilitiesResponse{
		MaxRequestBytes: 256 * 1024,
		MaxMessages:     128,
	}, nil
}

// TestHelperCrashableSidecar acts as a real child gRPC process for testing
// ResilientSidecarTarget SIGKILL recovery without needing external binaries.
func TestHelperCrashableSidecar(t *testing.T) {
	if os.Getenv("MARLIN_GO_HELPER_SIDECAR") != "1" {
		return
	}
	addr := os.Getenv("MARLIN_BRIDGE_ADDR")
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	token := os.Getenv("MARLIN_BRIDGE_TOKEN")
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		os.Exit(2)
	}
	srv := grpc.NewServer()
	appv1.RegisterAppServerServer(srv, &helperAppServer{expectedAuth: "Bearer " + token})

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		srv.GracefulStop()
	}()

	fmt.Printf("READY %s\n", lis.Addr().String())
	_ = os.Stdout.Sync()
	_ = srv.Serve(lis)
	os.Exit(0)
}

func TestResilientSidecarAutoRecoversFromSIGKILLAndRotatesToken(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var crashCount atomic.Int32
	recoveredCh := make(chan int, 2)

	resilient, err := grpcwire.LaunchResilientSidecar(ctx, grpcwire.ResilientSidecarConfig{
		ID:             "helper-sidecar",
		DisplayName:    "Crashable Helper Sidecar",
		InitialBackoff: 30 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		MaxRestarts:    3,
		Sidecar: transport.SidecarConfig{
			BinaryPath:   os.Args[0],
			Args:         []string{"-test.run=^TestHelperCrashableSidecar$"},
			ExtraEnv:     []string{"MARLIN_GO_HELPER_SIDECAR=1"},
			StartTimeout: 5 * time.Second,
		},
		OnCrash: func(_ int, _ error) {
			crashCount.Add(1)
		},
		OnRecovered: func(rc context.Context, target *grpcwire.Target, restarts int) {
			if err := target.Ping(rc); err == nil {
				recoveredCh <- restarts
			}
		},
	})
	if err != nil {
		t.Fatalf("LaunchResilientSidecar failed: %v", err)
	}

	if err := resilient.Ping(ctx); err != nil {
		_ = resilient.Close()
		t.Fatalf("initial Ping failed: %v", err)
	}
	pid1 := resilient.PID()
	token1 := resilient.Sidecar().Token()

	// Send real SIGKILL to child #1 and assert automatic token-rotating respawn!
	proc, err := os.FindProcess(pid1)
	if err != nil {
		_ = resilient.Close()
		t.Fatalf("FindProcess(%d): %v", pid1, err)
	}
	if err := proc.Kill(); err != nil {
		_ = resilient.Close()
		t.Fatalf("SIGKILL child %d: %v", pid1, err)
	}

	select {
	case restarts := <-recoveredCh:
		if restarts != 1 {
			_ = resilient.Close()
			t.Fatalf("expected restart count 1, got %d", restarts)
		}
	case <-ctx.Done():
		_ = resilient.Close()
		t.Fatalf("timed out waiting for ResilientSidecarTarget recovery")
	}

	pid2 := resilient.PID()
	token2 := resilient.Sidecar().Token()
	if pid2 == 0 || pid2 == pid1 {
		_ = resilient.Close()
		t.Fatalf("expected new PID after SIGKILL recovery, pid1=%d pid2=%d", pid1, pid2)
	}
	if token2 == "" || token2 == token1 {
		_ = resilient.Close()
		t.Fatalf("expected rotated 64-hex ephemeral Bearer token across respawn")
	}
	if crashCount.Load() != 1 || resilient.Restarts() != 1 {
		_ = resilient.Close()
		t.Fatalf("expected 1 crash & 1 restart, got crashes=%d restarts=%d", crashCount.Load(), resilient.Restarts())
	}
	if err := resilient.Ping(ctx); err != nil {
		_ = resilient.Close()
		t.Fatalf("post-recovery Ping failed: %v", err)
	}

	// Intentional Close must NOT trigger another crash/restart.
	if err := resilient.Close(); err != nil {
		t.Fatalf("resilient.Close() failed: %v", err)
	}
	if crashCount.Load() != 1 {
		t.Fatalf("intentional Close triggered spurious crash callback: count=%d", crashCount.Load())
	}
}
