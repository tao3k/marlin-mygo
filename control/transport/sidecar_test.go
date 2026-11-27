// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package transport_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/control/transport"
)

// TestSidecarHelperProcess acts as a cross-platform fake sidecar binary when
// invoked by the SidecarSupervisor tests via os.Executable().
func TestSidecarHelperProcess(t *testing.T) {
	mode := os.Getenv("MARLIN_TEST_SIDECAR_MODE")
	if mode == "" {
		return
	}
	// Verify token was NEVER leaked into process argv.
	tok := os.Getenv("MARLIN_BRIDGE_TOKEN")
	for _, arg := range os.Args {
		if tok != "" && strings.Contains(arg, tok) {
			fmt.Fprintln(os.Stderr, "token leaked into argv")
			os.Exit(99)
		}
	}
	switch mode {
	case "ready":
		if len(tok) != 64 {
			fmt.Fprintf(os.Stderr, "expected 64-hex random token, got len=%d\n", len(tok))
			os.Exit(98)
		}
		fmt.Println("READY 127.0.0.1:54321")
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt)
		select {
		case <-sigCh:
			os.Exit(0)
		case <-time.After(10 * time.Second):
			os.Exit(0)
		}
	case "non-loopback":
		fmt.Println("READY 10.20.30.40:50051")
		time.Sleep(5 * time.Second)
		os.Exit(0)
	case "hang":
		time.Sleep(5 * time.Second)
		os.Exit(0)
	case "crash":
		fmt.Println("READY 127.0.0.1:54322")
		time.Sleep(40 * time.Millisecond)
		os.Exit(7)
	}
}

func TestSidecarSupervisorHandshakeSecurityAndLifecycle(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	baseArgs := []string{"-test.run=^TestSidecarHelperProcess$"}

	// 1. Happy path: random 64-hex token in env (never argv), loopback READY, clean Stop.
	sup, err := transport.StartSidecar(context.Background(), transport.SidecarConfig{
		BinaryPath: exe,
		Args:       baseArgs,
		ExtraEnv:   []string{"MARLIN_TEST_SIDECAR_MODE=ready"},
	})
	if err != nil {
		t.Fatalf("StartSidecar(ready) failed: %v", err)
	}
	if sup.Address() != "127.0.0.1:54321" {
		t.Fatalf("unexpected sidecar address %q", sup.Address())
	}
	if len(sup.Token()) != 64 || !strings.HasPrefix(sup.AuthHeader(), "Bearer ") {
		t.Fatalf("unexpected ephemeral token/header: len=%d", len(sup.Token()))
	}
	if err := sup.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	select {
	case <-sup.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("sidecar Done channel did not close after Stop")
	}

	// 2. Security negative control: non-loopback READY address MUST be rejected & process killed.
	if _, err := transport.StartSidecar(context.Background(), transport.SidecarConfig{
		BinaryPath: exe,
		Args:       baseArgs,
		ExtraEnv:   []string{"MARLIN_TEST_SIDECAR_MODE=non-loopback"},
	}); !errors.Is(err, transport.ErrSidecarNonLoopback) {
		t.Fatalf("expected ErrSidecarNonLoopback, got %v", err)
	}

	// 3. Startup timeout MUST kill hung child and return ErrSidecarStartTimeout.
	if _, err := transport.StartSidecar(context.Background(), transport.SidecarConfig{
		BinaryPath:   exe,
		Args:         baseArgs,
		StartTimeout: 120 * time.Millisecond,
		ExtraEnv:     []string{"MARLIN_TEST_SIDECAR_MODE=hang"},
	}); !errors.Is(err, transport.ErrSidecarStartTimeout) {
		t.Fatalf("expected ErrSidecarStartTimeout, got %v", err)
	}

	// 4. Unexpected crash invokes OnExit callback and closes Done().
	crashed := make(chan error, 1)
	crashSup, err := transport.StartSidecar(context.Background(), transport.SidecarConfig{
		BinaryPath: exe,
		Args:       baseArgs,
		ExtraEnv:   []string{"MARLIN_TEST_SIDECAR_MODE=crash"},
		OnExit:     func(e error) { crashed <- e },
	})
	if err != nil {
		t.Fatalf("StartSidecar(crash) failed: %v", err)
	}
	select {
	case exitErr := <-crashed:
		if exitErr == nil {
			t.Fatal("expected non-nil exit error on crash")
		}
	case <-time.After(2 * time.Second):
		_ = crashSup.Stop(context.Background())
		t.Fatal("timed out waiting for OnExit crash notification")
	}
}
