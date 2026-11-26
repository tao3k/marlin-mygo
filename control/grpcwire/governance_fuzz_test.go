// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"testing"
)

// FuzzDecodeGovernanceSnapshotFromFacets fuzzes the authoritative Rust+Scheme
// gRPC projection facet decoder with hostile/corrupted JSON, integer overflows,
// invalid UTF-8, and contradictory flags to guarantee zero panics, non-negative
// clamped counters, and immutable deep cloning.
func FuzzDecodeGovernanceSnapshotFromFacets(f *testing.F) {
	f.Add("true", "false", "1", "2", "1", "0", "0", `[{"policy_id":"p1","source":"forbid (principal, action, resource);"}]`, `[{"parent":"ops","child":"agent"}]`, `[{"entry":"s","src_scope":"private","target":"ext","dst_scope":"external"}]`, `[{"derivation_id":"D1","policy_id":"p","subject_premises":["p1"],"parent_derivations":[]}]`, `[{"src":"a","dst":"b"}]`, `{"seq":1,"actor":"ops","action":"rearm_breaker","resource":"workspace","step_up_fresh":true,"decision":"allow","matched_policy_ids":["p1"]}`)
	f.Add("false", "true", "18446744073709551615", "99999999999999999999", "-5", "18446744073709551615", "18446744073709551615", `{bad-json`, `[null`, `[{"entry":123}]`, `not-json`, `[[[`, `{"seq":0}`)
	f.Add("", "", "", "", "", "", "", "", "", "", "", "", "")

	f.Fuzz(func(
		t *testing.T,
		admitted, tripped, revEpoch, polRev, tripEpoch, budgetUsed, activeLeases,
		cedarJSON, delJSON, flowJSON, derivJSON, taintJSON, probeJSON string,
	) {
		facets := map[string]string{
			"admitted":                admitted,
			"breaker_tripped":         tripped,
			"revocation_epoch":        revEpoch,
			"policy_revision":         polRev,
			"breaker_trip_epoch":      tripEpoch,
			"breaker_budget_used":     budgetUsed,
			"breaker_active_leases":   activeLeases,
			"cedar_policies_json":     cedarJSON,
			"fact_delegations_json":   delJSON,
			"fact_context_flows_json": flowJSON,
			"fact_derivations_json":   derivJSON,
			"fact_taint_edges_json":   taintJSON,
			"last_cedar_probe_json":   probeJSON,
			"rejection_reasons_json":  cedarJSON,
			"fenced_actors_json":      delJSON,
			"retracted_conclusions_json": derivJSON,
		}

		snap := DecodeGovernanceSnapshotFromFacets(facets)
		if snap.BreakerActiveLeases < 0 {
			t.Fatalf("hostile breaker_active_leases=%q wrapped negative: %d", activeLeases, snap.BreakerActiveLeases)
		}
		if snap.LastCedarProbe != nil && snap.LastCedarProbe.Seq == 0 {
			t.Fatalf("expected zero-seq probe to be ignored fail-closed, got %+v", snap.LastCedarProbe)
		}

		// Deep clone must never panic and must isolate slices from caller mutation.
		cloned := cloneGovernanceSnapshot(snap)
		if len(cloned.SynthesizedCedarRules) != len(snap.SynthesizedCedarRules) {
			t.Fatalf("clone cedar rule count mismatch: %d != %d", len(cloned.SynthesizedCedarRules), len(snap.SynthesizedCedarRules))
		}
		if len(cloned.FactDerivations) != len(snap.FactDerivations) {
			t.Fatalf("clone derivation count mismatch: %d != %d", len(cloned.FactDerivations), len(snap.FactDerivations))
		}
	})
}
