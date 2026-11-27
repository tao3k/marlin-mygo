// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
)

// TestAutoDiscoverAndHookRealSchemeHostAOT verifies zero-flag desktop governance boot:
// LaunchGovernanceSidecar automatically discovers the compiled Linked-AOT
// `marlin-scheme-host-aot` binary, launches it with Turso SQLite WAL durability,
// hydrates live Scheme POO x ASCENT x Bi-Temporal TM x Cedar proofs, executes live
// compiled-Cedar probes & dynamic delegation revocation, and shuts down cleanly.
func TestAutoDiscoverAndHookRealSchemeHostAOT(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	tursoDB := filepath.Join(t.TempDir(), "auto-gov.db")
	handle, err := grpcwire.LaunchGovernanceSidecar(ctx, grpcwire.GovernanceSidecarConfig{
		Mode:      "clean",
		TursoPath: tursoDB,
		SessionID: "auto-discover-scheme-host",
	})
	if errors.Is(err, grpcwire.ErrSchemeHostBinaryNotFound) {
		t.Skip("no local marlin-scheme-host-aot or marlin-scheme-host binary discovered")
	}
	if err != nil {
		t.Fatalf("LaunchGovernanceSidecar failed: %v", err)
	}

	snap := handle.Authority.Snapshot()
	if !snap.Admitted || snap.BreakerTripped {
		_ = handle.Close()
		t.Fatalf("expected clean armed governance snapshot on auto-discovered Scheme Host, got %+v", snap)
	}
	if !strings.HasPrefix(snap.AssessmentDigest, "sha256:") || len(snap.SynthesizedCedarRules) < 2 {
		_ = handle.Close()
		t.Fatalf("expected live sha256 proof digest and baseline Cedar policies, got %+v", snap)
	}

	// Live compiled-Cedar probe on auto-discovered Scheme Host:
	// agent attempting privileged rearm_breaker -> DENY; VerifiedHuman -> ALLOW.
	denySnap, err := handle.Authority.CheckCedar("agent-auto", "rearm_breaker", "workspace", true)
	if err != nil || denySnap.LastCedarProbe == nil || denySnap.LastCedarProbe.Decision != "deny" {
		_ = handle.Close()
		t.Fatalf("expected Cedar probe to deny agent rearm_breaker, err=%v probe=%+v", err, denySnap.LastCedarProbe)
	}
	allowSnap, err := handle.Authority.CheckCedar("ops-alice", "rearm_breaker", "workspace", true)
	if err != nil || allowSnap.LastCedarProbe == nil || allowSnap.LastCedarProbe.Decision != "allow" {
		_ = handle.Close()
		t.Fatalf("expected Cedar probe to allow VerifiedHuman rearm_breaker, err=%v probe=%+v", err, allowSnap.LastCedarProbe)
	}

	// Live dynamic ASCENT delegation + revocation closure on auto-discovered Scheme Host.
	if _, err := handle.Authority.DelegateActor("ops-lead", "agent-leaf"); err != nil {
		_ = handle.Close()
		t.Fatalf("DelegateActor: %v", err)
	}
	revSnap, err := handle.Authority.RevokeActor("ops-lead")
	if err != nil || !containsString(revSnap.FencedActors, "agent-leaf") {
		_ = handle.Close()
		t.Fatalf("expected ASCENT transitive fencing of agent-leaf, err=%v fenced=%v", err, revSnap.FencedActors)
	}

	sidecar := handle.Sidecar.Sidecar()
	if err := handle.Close(); err != nil {
		t.Fatalf("handle.Close failed: %v", err)
	}
	if exitErr := sidecar.ExitErr(); exitErr != nil {
		t.Fatalf("expected clean exit code 0 from auto-discovered Scheme Host, got %v", exitErr)
	}
}
