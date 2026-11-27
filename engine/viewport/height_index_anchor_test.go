// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package viewport

import (
	"math/rand"
	"testing"
)

func TestVirtualHeightIndexScrollAnchorZeroDriftProperty(t *testing.T) {
	const numRows = 2000
	rng := rand.New(rand.NewSource(42))

	idx := NewVirtualHeightIndex(numRows)
	for i := 0; i < numRows; i++ {
		h := uint32(32 + rng.Intn(400))
		idx.Append(h)
	}

	// Pick an interior reading scrollY and verify 5,000 randomized fold/unfold/stream
	// mutations strictly above OR below the viewport produce 0px visual drift!
	scrollY := idx.PrefixSum(1000) + 17
	for step := 0; step < 5000; step++ {
		beforeAnchor := idx.CaptureAnchor(scrollY)
		targetRow := rng.Intn(numRows)
		for targetRow == beforeAnchor.Row {
			targetRow = (targetRow + 1) % numRows
		}
		newH := uint32(28 + rng.Intn(1800))
		scrollY = idx.UpdateHeightAnchored(targetRow, newH, scrollY)
		afterAnchor := idx.CaptureAnchor(scrollY)
		if afterAnchor != beforeAnchor {
			t.Fatalf("step %d (mutated row %d -> %dpx): anchor drifted from %+v to %+v",
				step, targetRow, newH, beforeAnchor, afterAnchor)
		}
	}

	// Verify full O(N) RebuildHeightsAnchored (pane resize re-wrap) preserves the
	// exact logical row & intra-row offset with 0px drift!
	beforeResize := idx.CaptureAnchor(scrollY)
	resized := make([]uint32, numRows)
	for i := 0; i < numRows; i++ {
		h := uint32(40 + rng.Intn(600))
		if i == beforeResize.Row && uint64(h) <= beforeResize.OffsetInRow {
			h = uint32(beforeResize.OffsetInRow) + 20
		}
		resized[i] = h
	}
	scrollY = idx.RebuildHeightsAnchored(resized, scrollY)
	afterResize := idx.CaptureAnchor(scrollY)
	if afterResize != beforeResize {
		t.Fatalf("RebuildHeightsAnchored drifted anchor from %+v to %+v", beforeResize, afterResize)
	}
}

func TestVirtualHeightIndexUpdateHeightAnchoredZeroAlloc(t *testing.T) {
	idx := NewVirtualHeightIndex(10000)
	for i := 0; i < 10000; i++ {
		idx.Append(48)
	}
	scrollY := idx.PrefixSum(5000) + 12
	allocs := testing.AllocsPerRun(500, func() {
		scrollY = idx.UpdateHeightAnchored(200, 640, scrollY)
		scrollY = idx.UpdateHeightAnchored(200, 38, scrollY)
	})
	if allocs > 0 {
		t.Fatalf("expected 0 allocs/op for O(log N) UpdateHeightAnchored on 10k rows, got %.2f", allocs)
	}
}
