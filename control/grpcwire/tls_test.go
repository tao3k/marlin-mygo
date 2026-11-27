// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire_test

import (
	"errors"
	"testing"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
)

func TestRemotePlaintextRefusedByDefault(t *testing.T) {
	// Non-loopback remote address MUST refuse plaintext unless TLS or explicit opt-in is configured.
	if _, err := grpcwire.Dial(grpcwire.DialConfig{
		ID:          "remote-prod",
		DisplayName: "Prod GPU Box",
		Mode:        transport.ModeRemoteGRPC,
		Address:     "10.42.0.9:50051",
		AuthToken:   "Bearer secret",
	}); !errors.Is(err, grpcwire.ErrInsecureRemoteForbidden) {
		t.Fatalf("expected ErrInsecureRemoteForbidden for plaintext remote dial, got %v", err)
	}

	// Loopback addresses remain allowed for local sidecar development.
	target, err := grpcwire.Dial(grpcwire.DialConfig{
		ID:          "local-sidecar",
		DisplayName: "Local Sidecar",
		Mode:        transport.ModeLocalSidecar,
		Address:     "127.0.0.1:50051",
		AuthToken:   "Bearer dev",
	})
	if err != nil {
		t.Fatalf("expected loopback dial to succeed, got %v", err)
	}
	_ = target.Close()

	// Explicit AllowInsecureRemote allows controlled LAN/CI tunnels.
	lanTarget, err := grpcwire.Dial(grpcwire.DialConfig{
		ID:          "lan-dev",
		DisplayName: "LAN Dev",
		Mode:        transport.ModeRemoteGRPC,
		Address:     "10.42.0.9:50051",
		TLS:         &grpcwire.TLSConfig{AllowInsecureRemote: true},
	})
	if err != nil {
		t.Fatalf("expected explicit AllowInsecureRemote dial to succeed, got %v", err)
	}
	_ = lanTarget.Close()
}
