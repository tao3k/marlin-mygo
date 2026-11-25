// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !wasip1

package gouir

import (
	"strings"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestMeasurePerfCasePassesFastPathAndRejectsAllocsOrSlowBurn(t *testing.T) {
	var sink int
	passRes := AssertPerfCase(t, PerfCase{
		Name:        "zero_alloc_hot_loop",
		MaxP50:      500 * time.Microsecond,
		MaxAllocs:   0,
		Iterations:  50,
		Remediation: "keep zero allocations",
		Run: func() {
			for i := 0; i < 32; i++ {
				sink += i
			}
		},
	})
	if !passRes.Passed || passRes.AllocsPerOp != 0 {
		t.Fatalf("expected fast zero-alloc pass, got %+v", passRes)
	}

	// Negative control 1: heap allocation ceiling violation must fail with remediation.
	var escaped []byte
	allocFail := MeasurePerfCase(PerfCase{
		Name:        "leaky_alloc_builder",
		MaxP50:      5 * time.Millisecond,
		MaxAllocs:   0,
		Iterations:  20,
		Remediation: "Reuse fixed [64]schema.DrawCmd backing array instead of make([]byte) per frame",
		Run: func() {
			escaped = make([]byte, 256)
		},
	})
	_ = escaped
	if allocFail.Passed || !strings.Contains(allocFail.Remediation, "DrawCmd") {
		t.Fatalf("expected alloc negative control to fail with remediation, got %+v", allocFail)
	}

	// Negative control 2: latency ceiling violation must fail with remediation.
	slowFail := MeasurePerfCase(PerfCase{
		Name:        "slow_busy_render",
		MaxP50:      1 * time.Nanosecond,
		MaxP95:      2 * time.Nanosecond,
		MaxAllocs:   -1,
		Iterations:  10,
		Remediation: "Avoid O(N^2) recomputation inside marlin_ui_render",
		Run: func() {
			time.Sleep(50 * time.Microsecond)
		},
	})
	if slowFail.Passed || !strings.Contains(slowFail.Remediation, "marlin_ui_render") {
		t.Fatalf("expected latency negative control to fail with remediation, got %+v", slowFail)
	}

	// UI tree compactness check.
	tree := Panel("Demo", schema.ToneAccent, Rows(Text(Span("ok", schema.ToneInfo))))
	nodes, depth := AssertUITreeBudget(t, tree, 16, 6)
	if nodes != 3 || depth != 3 {
		t.Fatalf("expected 3 nodes / depth 3, got nodes=%d depth=%d", nodes, depth)
	}
}
