// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package resilience_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/tao3k/marlin-mygo/engine/resilience"
)

// TestResilienceAdversarialStateMachinePowerLossAndReplay runs 2,000 randomized
// adversarial steps combining draft edits, @ mention updates, flaky outbox
// deliveries, duplicate/out-of-order/gap stream replays, sudden power-loss
// restarts (atomic fsync SaveFile -> cold LoadFile), and corrupt-file attacks.
func TestResilienceAdversarialStateMachinePowerLossAndReplay(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261126, 42))
	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "recovery-mbt.json")
	corruptFile := filepath.Join(tmpDir, "corrupt-mbt.json")

	scopes := []string{"chat:sess-1", "mobile:chat", "room:ch-workspace", "compare:prompt", "canvas:art-spec"}

	// Reference oracle model.
	modelDrafts := make(map[string]string)
	modelMentions := make(map[string][]resilience.DraftMention)
	modelMaxSeq := make(map[string]uint64)
	modelSeen := make(map[string]map[string]bool)
	modelOutbox := make(map[string][]string) // scope -> ordered SubmissionIDs
	for _, sc := range scopes {
		modelSeen[sc] = make(map[string]bool)
	}

	res := resilience.NewSessionResilience()
	eventCounter := 0

	for step := 0; step < 2000; step++ {
		sc := scopes[rng.IntN(len(scopes))]
		op := rng.IntN(10)

		switch op {
		case 0, 1: // Draft edit or clear.
			txt := ""
			if rng.IntN(4) != 0 {
				txt = fmt.Sprintf("draft-step-%d-scope-%s", step, sc)
			}
			res.SetDraft(sc, txt)
			if txt == "" {
				delete(modelDrafts, sc)
			} else {
				modelDrafts[sc] = txt
			}
			if got := res.Draft(sc); got != modelDrafts[sc] {
				t.Fatalf("step %d: draft mismatch for %s: got %q want %q", step, sc, got, modelDrafts[sc])
			}

		case 2: // @ mention update or clear.
			if rng.IntN(3) == 0 {
				res.SetDraftMentions(sc, nil)
				delete(modelMentions, sc)
			} else {
				list := []resilience.DraftMention{
					{Kind: "file", Target: fmt.Sprintf("file-%d.go", step), Label: fmt.Sprintf("@file-%d.go", step)},
				}
				res.SetDraftMentions(sc, list)
				modelMentions[sc] = list
			}

		case 3, 4: // Outbox enqueue (bounded at 64 across all scopes).
			item, err := res.Outbox.Enqueue(sc, "human-owner", "", fmt.Sprintf("msg-%d", step))
			if errors.Is(err, resilience.ErrOutboxFull) {
				if res.Outbox.Len() < 64 {
					t.Fatalf("step %d: ErrOutboxFull when Len=%d < 64", step, res.Outbox.Len())
				}
			} else if err != nil {
				t.Fatalf("step %d: unexpected Enqueue error: %v", step, err)
			} else {
				modelOutbox[sc] = append(modelOutbox[sc], item.SubmissionID)
			}

		case 5: // Flaky outbox flush: fail ~40% of items, preserve FIFO and immutable SubmissionID!
			expectedOrder := append([]string(nil), modelOutbox[sc]...)
			seenInFlush := make([]string, 0, len(expectedOrder))
			var remaining []string
			res.Outbox.Flush(sc, func(it resilience.OutboxItem) error {
				seenInFlush = append(seenInFlush, it.SubmissionID)
				if rng.IntN(10) < 4 {
					remaining = append(remaining, it.SubmissionID)
					return errors.New("simulated transient gRPC unavailable")
				}
				return nil
			})
			if len(seenInFlush) != len(expectedOrder) {
				t.Fatalf("step %d: flush visited %d items, oracle expected %d", step, len(seenInFlush), len(expectedOrder))
			}
			for i := range expectedOrder {
				if seenInFlush[i] != expectedOrder[i] {
					t.Fatalf("step %d: FIFO/idempotency violation at %d: got %q want %q", step, i, seenInFlush[i], expectedOrder[i])
				}
			}
			modelOutbox[sc] = remaining

		case 6: // Stream event admit: fresh, duplicate replay, or sequence gap.
			if rng.IntN(3) == 0 && len(modelSeen[sc]) > 0 {
				// Pick an existing event ID -> MUST be detected as duplicate when within ring capacity.
				for evID := range modelSeen[sc] {
					if len(modelSeen[sc]) <= 4096 {
						dup, gap := res.AdmitEvent(sc, evID, 1, modelMaxSeq[sc])
						if !dup || gap {
							t.Fatalf("step %d: expected duplicate=true gap=false for %q, got dup=%v gap=%v", step, evID, dup, gap)
						}
					}
					break
				}
			} else {
				eventCounter++
				evID := fmt.Sprintf("ev-%d", eventCounter)
				nextSeq := modelMaxSeq[sc] + 1
				wantGap := false
				if modelMaxSeq[sc] > 0 && rng.IntN(4) == 0 {
					nextSeq = modelMaxSeq[sc] + 3 // inject sequence gap!
					wantGap = true
				}
				dup, gap := res.AdmitEvent(sc, evID, uint64(step+1), nextSeq)
				if dup {
					t.Fatalf("step %d: fresh event %q falsely flagged duplicate", step, evID)
				}
				if gap != wantGap {
					t.Fatalf("step %d: gap mismatch for seq=%d (prev=%d): got %v want %v", step, nextSeq, modelMaxSeq[sc], gap, wantGap)
				}
				modelSeen[sc][evID] = true
				modelMaxSeq[sc] = nextSeq
			}

		case 7: // Sudden power loss! Atomic fsync SaveFile -> cold boot brand-new instance -> verify 100% continuity.
			if err := res.SaveFile(stateFile); err != nil {
				t.Fatalf("step %d: SaveFile failed: %v", step, err)
			}
			cold := resilience.NewSessionResilience()
			if err := cold.LoadFile(stateFile); err != nil {
				t.Fatalf("step %d: cold LoadFile failed: %v", step, err)
			}
			for _, checkSc := range scopes {
				if got := cold.Draft(checkSc); got != modelDrafts[checkSc] {
					t.Fatalf("step %d: power-loss draft lost for %s: got %q want %q", step, checkSc, got, modelDrafts[checkSc])
				}
				gotM := cold.DraftMentions(checkSc)
				wantM := modelMentions[checkSc]
				if len(gotM) != len(wantM) {
					t.Fatalf("step %d: power-loss mentions lost for %s: got %d want %d", step, checkSc, len(gotM), len(wantM))
				}
				if gotSeq := cold.Cursor(checkSc).LastSequence; gotSeq != modelMaxSeq[checkSc] {
					t.Fatalf("step %d: power-loss cursor seq mismatch for %s: got %d want %d", step, checkSc, gotSeq, modelMaxSeq[checkSc])
				}
				gotPend := cold.Outbox.PendingForScope(checkSc)
				if len(gotPend) != len(modelOutbox[checkSc]) {
					t.Fatalf("step %d: power-loss outbox count mismatch for %s: got %d want %d", step, checkSc, len(gotPend), len(modelOutbox[checkSc]))
				}
				for i, it := range gotPend {
					if it.SubmissionID != modelOutbox[checkSc][i] {
						t.Fatalf("step %d: power-loss outbox SubmissionID mismatch at %d: got %q want %q", step, i, it.SubmissionID, modelOutbox[checkSc][i])
					}
					if it.State == resilience.DeliverySending {
						t.Fatalf("step %d: interrupted SENDING item %q was not normalized to QUEUED on reload", step, it.SubmissionID)
					}
				}
			}
			// Continue operating on the cold-booted instance (note dedup ring resets on cold boot by design).
			res = cold
			for _, checkSc := range scopes {
				modelSeen[checkSc] = make(map[string]bool)
			}

		case 8: // Adversarial corrupt/truncated recovery file MUST fail closed without mutating live state.
			junk := []byte(`{"version":1,"drafts":{"chat:sess-1":"truncated`)
			if err := os.WriteFile(corruptFile, junk, 0o600); err != nil {
				t.Fatalf("step %d: write corrupt file: %v", step, err)
			}
			if err := res.LoadFile(corruptFile); err == nil {
				t.Fatalf("step %d: expected fail-closed error loading truncated snapshot", step)
			}
			if got := res.Draft(sc); got != modelDrafts[sc] {
				t.Fatalf("step %d: corrupt LoadFile mutated live draft for %s", step, sc)
			}

		case 9: // Discard one pending item if any exist.
			if len(modelOutbox[sc]) > 0 {
				dropID := modelOutbox[sc][0]
				if !res.Outbox.Discard(dropID) {
					t.Fatalf("step %d: Discard(%q) returned false", step, dropID)
				}
				modelOutbox[sc] = modelOutbox[sc][1:]
			}
		}
	}
}
