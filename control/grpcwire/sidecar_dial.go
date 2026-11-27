// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"context"
	"errors"

	"github.com/tao3k/marlin-mygo/control/transport"
)

// ManagedSidecarTarget bundles a supervised local Rust Authority sidecar process
// with its authenticated loopback HTTP/2 gRPC Target. Closing it closes the gRPC
// channel and gracefully stops the child process.
type ManagedSidecarTarget struct {
	*Target
	Sidecar *transport.SidecarSupervisor
}

// LaunchSidecarTarget starts a local Marlin Rust host binary with a fresh
// crypto/rand ephemeral Bearer token, verifies loopback binding, and dials it.
func LaunchSidecarTarget(
	ctx context.Context,
	id string,
	displayName string,
	cfg transport.SidecarConfig,
) (*ManagedSidecarTarget, error) {
	sup, err := transport.StartSidecar(ctx, cfg)
	if err != nil {
		return nil, err
	}
	target, err := Dial(DialConfig{
		ID:          id,
		DisplayName: displayName,
		Mode:        transport.ModeLocalSidecar,
		Address:     sup.Address(),
		AuthToken:   sup.AuthHeader(),
	})
	if err != nil {
		_ = sup.Stop(context.Background())
		return nil, err
	}
	return &ManagedSidecarTarget{
		Target:  target,
		Sidecar: sup,
	}, nil
}

// Close closes the HTTP/2 gRPC client connection and terminates the supervised sidecar.
func (m *ManagedSidecarTarget) Close() error {
	var err1, err2 error
	if m.Target != nil {
		err1 = m.Target.Close()
	}
	if m.Sidecar != nil {
		err2 = m.Sidecar.Stop(context.Background())
	}
	return errors.Join(err1, err2)
}
