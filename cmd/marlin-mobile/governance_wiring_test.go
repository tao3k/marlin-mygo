// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

func TestWireMobileGovernanceStandalonePreviewAndLiveAutoDiscovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// 1. Explicit `-scheme-host off` falls back cleanly to standalone preview bridge with Gov · LIVE.
	govPreview, stopPreview := wireMobileGovernance(ctx, nil, "off", "clean", "", "mob-preview", nil)
	if stopPreview != nil {
		stopPreview()
	}
	if lbl, variant, failClosed := govPreview.StatusPillAppearance(); lbl != "Gov · LIVE" || variant != mygoshell.BtnSuccess || failClosed {
		t.Fatalf("expected Gov · LIVE preview pill, got lbl=%q variant=%v failClosed=%v", lbl, variant, failClosed)
	}
	snap := govPreview.BackendAuthority.Snapshot()
	if len(snap.SynthesizedCedarRules) < 2 {
		t.Fatalf("expected baseline Cedar rules in mobile standalone preview, got %+v", snap.SynthesizedCedarRules)
	}

	// 2. Zero-config auto-discovery hooks real Linked-AOT `marlin-scheme-host-aot` when present!
	dbPath := filepath.Join(t.TempDir(), "mob-auto-gov.db")
	govAuto, stopAuto := wireMobileGovernance(ctx, nil, "", "clean", dbPath, "mob-auto", nil)
	if stopAuto != nil {
		defer stopAuto()
	}
	autoSnap := govAuto.BackendAuthority.Snapshot()
	if !autoSnap.Admitted || autoSnap.BreakerTripped {
		t.Fatalf("expected armed mobile governance snapshot, got %+v", autoSnap)
	}
	if stopAuto != nil && !strings.HasPrefix(autoSnap.AssessmentDigest, "sha256:") {
		t.Fatalf("expected live sha256 proof digest from auto-discovered Scheme Host, got %+v", autoSnap)
	}
}
