// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package transport

import (
	"context"
	"testing"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

type fakeTarget struct {
	meta   TargetMeta
	closed bool
}

func (f *fakeTarget) Meta() TargetMeta           { return f.meta }
func (f *fakeTarget) Ping(context.Context) error { return nil }
func (f *fakeTarget) Close() error               { f.closed = true; return nil }

func TestTargetRegistryMultiMachineSwitch(t *testing.T) {
	reg := NewTargetRegistry()
	var seenActive string
	unsub := reg.Subscribe(func(_ []TargetMeta, activeID string) {
		seenActive = activeID
	})
	defer unsub()

	local := &fakeTarget{meta: TargetMeta{
		ID: "local", DisplayName: "This Mac", Mode: ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: PhaseReady,
	}}
	remote := &fakeTarget{meta: TargetMeta{
		ID: "linux-box", DisplayName: "Build Box", Mode: ModeRemoteGRPC,
		TargetKind: schema.TargetRemoteHeadless, Address: "10.0.0.12:50051", Phase: PhaseReady,
	}}

	if err := reg.Register(local); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(remote); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Switch("linux-box"); err != nil {
		t.Fatal(err)
	}
	if seenActive != "linux-box" {
		t.Fatalf("expected active linux-box, got %q", seenActive)
	}
	if err := reg.Unregister("linux-box"); err != nil {
		t.Fatal(err)
	}
	if !remote.closed {
		t.Fatal("expected unregistered target to be closed")
	}
	if active, ok := reg.Active(); !ok || active.Meta().ID != "local" {
		t.Fatalf("expected fallback to local target, got %+v", active)
	}
}
