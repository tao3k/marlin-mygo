// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package search

import "testing"

func TestConsultOrderlessParseAndScoring(t *testing.T) {
	cq := ParseConsultQuery("auth # !test session")
	if !cq.HasStageSep || cq.BackendQuery != "auth" || len(cq.Positive) != 1 || cq.Positive[0] != "session" || len(cq.Negative) != 1 || cq.Negative[0] != "test" {
		t.Fatalf("unexpected ConsultQuery: %+v", cq)
	}

	// Positive Orderless multi-token match ("sw th" -> "switch-themes") produces non-zero highlight mask.
	sc, mask, ok := ScoreOrderlessAndMask("sw th", "switch-themes")
	if !ok || sc <= 0 || mask == 0 {
		t.Fatalf("expected Orderless match with highlight mask, got sc=%d mask=%b ok=%v", sc, mask, ok)
	}

	// Negation excludes matching candidate cleanly.
	if _, _, ok := ScoreOrderlessAndMask("switch !themes", "switch-themes"); ok {
		t.Fatalf("expected '!themes' negation to reject 'switch-themes'")
	}

	// Two-stage Consult query refines backend results locally without rejecting valid matches.
	sc2, mask2, ok2 := ScoreOrderlessAndMask("auth # !test session", "auth_session_manager")
	if !ok2 || sc2 <= 0 || mask2 == 0 {
		t.Fatalf("expected Consult two-stage match, got sc=%d mask=%b ok=%v", sc2, mask2, ok2)
	}
	if _, _, ok3 := ScoreOrderlessAndMask("auth # !test session", "auth_session_test"); ok3 {
		t.Fatalf("expected '!test' to exclude 'auth_session_test'")
	}
}
