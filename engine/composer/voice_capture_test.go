// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package composer

import (
	"strings"
	"testing"
	"time"
)

func TestContextUsageAndStreamMetricsFormatting(t *testing.T) {
	u := ContextUsage{UsedTokens: 34_000, MaxTokens: 200_000}
	if got := u.Label(); got != "34K / 200K · 17%" {
		t.Fatalf("unexpected ContextUsage label: %q", got)
	}
	over := ContextUsage{UsedTokens: 250_000, MaxTokens: 200_000}
	if over.Fraction() != 1.0 {
		t.Fatalf("expected clamped fraction 1.0, got %f", over.Fraction())
	}

	var sm StreamMetrics
	sm.Begin()
	sm.RecordDelta(strings.Repeat("abcd", 40)) // ~40 tokens
	time.Sleep(60 * time.Millisecond)
	sm.Finish()
	lbl := sm.Label()
	if !strings.Contains(lbl, "tok/s") {
		t.Fatalf("expected throughput label with tok/s, got %q", lbl)
	}
	if got := ParseContextWindowTokens("200K"); got != 200_000 {
		t.Fatalf("expected 200K -> 200000, got %d", got)
	}
	if got := ParseContextWindowTokens("1M"); got != 1_000_000 {
		t.Fatalf("expected 1M -> 1000000, got %d", got)
	}
	if got := ParseContextWindowTokens("1.5M"); got != 1_500_000 {
		t.Fatalf("expected 1.5M -> 1500000, got %d", got)
	}
	if got := EstimateTokenCount("hello world"); got < 1 {
		t.Fatalf("expected positive token estimate, got %d", got)
	}
}

func TestNativeAudioRecorderIdleStopAndCancelSafety(t *testing.T) {
	rec := NewNativeAudioRecorder()
	rec.Cancel()
	if _, _, err := rec.Stop(); err == nil {
		t.Fatal("expected Stop on idle recorder to return ErrAttachmentEmpty")
	}
}
