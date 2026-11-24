// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package shell

import (
	"errors"
	"testing"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

type stubRenderer struct {
	id       string
	trusted  bool
	disposed bool
}

func (r *stubRenderer) ID() string              { return r.id }
func (r *stubRenderer) Trusted() bool           { return r.trusted }
func (r *stubRenderer) Render(RenderContext)    {}
func (r *stubRenderer) HandleEvent(HostEvent) error { return nil }
func (r *stubRenderer) Dispose()                { r.disposed = true }

func TestSlotHotPlugAndFallback(t *testing.T) {
	reg := NewSlotRegistry()
	fallback := &stubRenderer{id: "native.main", trusted: true}
	if err := reg.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle,
		FallbackRenderer: "native.main", AllowUntrustedPlugin: true,
	}, fallback); err != nil {
		t.Fatal(err)
	}

	if got := reg.Resolve("workbench.main"); len(got) != 1 || got[0].ID() != "native.main" {
		t.Fatalf("expected fallback renderer, got %+v", got)
	}

	pluginView := &stubRenderer{id: "tao3k.kanban", trusted: false}
	unmount, err := reg.Mount("workbench.main", "", pluginView)
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Resolve("workbench.main"); len(got) != 1 || got[0].ID() != "tao3k.kanban" {
		t.Fatalf("expected mounted plugin renderer, got %+v", got)
	}

	unmount()
	if !pluginView.disposed {
		t.Fatal("expected unmounted plugin renderer to be disposed")
	}
	if got := reg.Resolve("workbench.main"); len(got) != 1 || got[0].ID() != "native.main" {
		t.Fatalf("expected automatic fallback after unmount, got %+v", got)
	}
}

func TestUntrustedRejectedOnRestrictedSlot(t *testing.T) {
	reg := NewSlotRegistry()
	if err := reg.Declare(schema.SlotDeclaration{
		SlotID: "approval.gate", Cardinality: schema.SlotSingle,
		FallbackRenderer: "native.gate", AllowUntrustedPlugin: false,
	}, &stubRenderer{id: "native.gate", trusted: true}); err != nil {
		t.Fatal(err)
	}
	_, err := reg.Mount("approval.gate", "", &stubRenderer{id: "thirdparty", trusted: false})
	if !errors.Is(err, ErrUntrustedForbidden) {
		t.Fatalf("expected ErrUntrustedForbidden, got %v", err)
	}
}

func TestWorkbenchRejectsStaleGeneration(t *testing.T) {
	ctrl := NewWorkbenchController(NewSlotRegistry())
	snap := schema.UIWorkbenchSnapshot{
		Generation: 5,
		Theme:      schema.DefaultUITheme(),
		FrameTree:  schema.UIElement{Kind: schema.ElemRows},
	}
	if err := ctrl.AdmitSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	snap.Generation = 4
	if err := ctrl.AdmitSnapshot(snap); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("expected ErrStaleGeneration, got %v", err)
	}
}
