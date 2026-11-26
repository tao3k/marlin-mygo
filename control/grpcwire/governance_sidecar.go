// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

// ErrSchemeHostBinaryNotFound indicates no local `marlin-scheme-host-aot` or
// `marlin-scheme-host` executable was discovered.
var ErrSchemeHostBinaryNotFound = errors.New("grpcwire: no local marlin-scheme-host-aot or marlin-scheme-host binary discovered")

// GovernanceSidecarConfig configures zero-config discovery, supervision, and live
// HTTP/2 gRPC wiring of the local Rust+Gerbil Scheme Governance Authority daemon.
type GovernanceSidecarConfig struct {
	ExplicitPath string
	Mode         string // "clean" (default) | "incident" | "armed"
	TursoPath    string // optional durable Turso SQLite WAL path
	ProjectID    string
	SessionID    string
	StartTimeout time.Duration
	OnSnapshot   func(mygoshell.BackendGovernanceSnapshot)
}

// GovernanceSidecarHandle owns both the supervised Linked-AOT Scheme Host sidecar
// and the live `RustGovernanceAuthority` thin-client connected to it.
type GovernanceSidecarHandle struct {
	Sidecar         *ResilientSidecarTarget
	Authority       *RustGovernanceAuthority
	BinaryPath      string
	DiscoverySource string
}

// Close stops the live gRPC governance trajectory watcher and terminates the
// supervised Rust+Scheme sidecar cleanly (SIGTERM -> Turso WAL flush).
func (h *GovernanceSidecarHandle) Close() error {
	if h == nil {
		return nil
	}
	if h.Authority != nil {
		h.Authority.Stop()
	}
	if h.Sidecar != nil {
		return h.Sidecar.Close()
	}
	return nil
}

// LaunchGovernanceSidecar auto-discovers `marlin-scheme-host-aot` (or fallback
// `marlin-scheme-host`), starts it with crash-resilient supervision and Turso WAL
// persistence, starts `RustGovernanceAuthority`, and performs an initial hydrate.
func LaunchGovernanceSidecar(
	ctx context.Context,
	cfg GovernanceSidecarConfig,
) (*GovernanceSidecarHandle, error) {
	binPath, source, found := transport.DiscoverSchemeHostBinary(transport.SidecarDiscoverOptions{
		ExplicitPath: cfg.ExplicitPath,
	})
	if !found {
		return nil, ErrSchemeHostBinaryNotFound
	}

	mode := strings.TrimSpace(cfg.Mode)
	if mode == "" {
		mode = "clean"
	}
	projectID := strings.TrimSpace(cfg.ProjectID)
	if projectID == "" {
		projectID = "default"
	}
	sessionID := strings.TrimSpace(cfg.SessionID)
	if sessionID == "" {
		sessionID = "marlin-governance"
	}
	startTimeout := cfg.StartTimeout
	if startTimeout <= 0 {
		startTimeout = 10 * time.Second
	}

	extraEnv := []string{
		"MARLIN_SCHEME_GOVERNANCE_MODE=" + mode,
	}
	if turso := strings.TrimSpace(cfg.TursoPath); turso != "" {
		if err := os.MkdirAll(filepath.Dir(turso), 0o755); err != nil {
			return nil, fmt.Errorf("grpcwire: prepare governance turso dir: %w", err)
		}
		extraEnv = append(extraEnv, "MARLIN_BRIDGE_TURSO_PATH="+turso)
	}

	sidecar, err := LaunchResilientSidecar(ctx, ResilientSidecarConfig{
		ID:          "local-scheme-governance-host",
		DisplayName: "Marlin Scheme Governance Authority",
		Sidecar: transport.SidecarConfig{
			BinaryPath:   binPath,
			ExtraEnv:     extraEnv,
			StartTimeout: startTimeout,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("grpcwire: launch scheme governance host (%s via %s): %w", binPath, source, err)
	}

	auth := NewRustGovernanceAuthority(RustGovernanceAuthorityConfig{
		Provider:   sidecar,
		ProjectID:  projectID,
		SessionID:  sessionID,
		Timeout:    startTimeout,
		OnSnapshot: cfg.OnSnapshot,
	})
	if err := auth.Start(ctx); err != nil {
		_ = sidecar.Close()
		return nil, fmt.Errorf("grpcwire: start RustGovernanceAuthority: %w", err)
	}
	if _, err := auth.Hydrate(ctx); err != nil {
		auth.Stop()
		_ = sidecar.Close()
		return nil, fmt.Errorf("grpcwire: initial governance hydrate failed: %w", err)
	}

	return &GovernanceSidecarHandle{
		Sidecar:         sidecar,
		Authority:       auth,
		BinaryPath:      binPath,
		DiscoverySource: source,
	}, nil
}
