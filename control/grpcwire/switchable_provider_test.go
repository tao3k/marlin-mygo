// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire_test

import (
	"context"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
)

func TestSwitchableTargetProviderHotSwapAndRegistryResolve(t *testing.T) {
	srvA := &composerMockAppServer{}
	srvB := &composerMockAppServer{}
	targetA := dialComposerMockTarget(t, srvA)
	targetB := dialComposerMockTarget(t, srvB)

	reg := transport.NewTargetRegistry()
	if err := reg.Register(targetA); err != nil {
		t.Fatalf("register targetA: %v", err)
	}

	sw := grpcwire.NewSwitchableTargetProvider("target-a", targetA)
	sw.Register("target-b", targetB)

	if sw.ActiveID() != "target-a" || sw.CurrentTarget() != targetA {
		t.Fatalf("expected initial target-a active, got id=%q target=%v", sw.ActiveID(), sw.CurrentTarget())
	}

	genBefore := sw.Generation()
	woken := make(chan uint64, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if g, ok := sw.WaitSwap(ctx, genBefore); ok {
			woken <- g
		}
	}()

	ok, newGen := sw.SwitchTo("target-b")
	if !ok || newGen <= genBefore || sw.CurrentTarget() != targetB {
		t.Fatalf("expected SwitchTo(target-b) to bump gen and swap target, got ok=%v gen=%d", ok, newGen)
	}
	select {
	case g := <-woken:
		if g != newGen {
			t.Fatalf("expected WaitSwap woken with gen=%d, got %d", newGen, g)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitSwap did not wake on SwitchTo")
	}

	// Verify ReconnectActive bumps generation while preserving active target.
	reconnGen := sw.ReconnectActive()
	if reconnGen <= newGen || sw.ActiveID() != "target-b" {
		t.Fatalf("expected ReconnectActive to bump gen while keeping target-b, got gen=%d id=%q", reconnGen, sw.ActiveID())
	}

	// Verify ResolveFromRegistry resolves and switches to a registered *grpcwire.Target.
	if resolved, rGen := sw.ResolveFromRegistry(reg, targetA.Meta().ID); !resolved || rGen <= reconnGen || sw.CurrentTarget() != targetA {
		t.Fatalf("expected ResolveFromRegistry to switch back to targetA, got ok=%v gen=%d", resolved, rGen)
	}
}
