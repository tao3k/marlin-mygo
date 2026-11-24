// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package viewport

import (
	"math/rand/v2"
	"testing"
)

func oracleFind(heights []uint32, y uint64) (int, uint64) {
	var acc uint64
	for i, h := range heights {
		if h > 0 && y < acc+uint64(h) {
			return i, y - acc
		}
		acc += uint64(h)
	}
	if len(heights) == 0 {
		return -1, 0
	}
	return len(heights) - 1, uint64(heights[len(heights)-1])
}

func TestVirtualHeightIndexMatchesOracleAcrossFoldsAndZeros(t *testing.T) {
	idx := NewVirtualHeightIndex(64)
	rng := rand.New(rand.NewPCG(42, 99))
	oracle := make([]uint32, 0, 500)

	for i := 0; i < 500; i++ {
		h := uint32(rng.IntN(60))
		if i%7 == 0 {
			h = 0 // collapsed/hidden thinking row
		}
		idx.Append(h)
		oracle = append(oracle, h)
	}
	// Mutate heights dynamically (expand/collapse folds).
	for i := 0; i < 150; i++ {
		row := rng.IntN(len(oracle))
		nh := uint32(rng.IntN(120))
		if i%5 == 0 {
			nh = 0
		}
		idx.UpdateHeight(row, nh)
		oracle[row] = nh
	}

	var oracleTotal uint64
	for _, h := range oracle {
		oracleTotal += uint64(h)
	}
	if idx.TotalHeight() != oracleTotal {
		t.Fatalf("total mismatch: got %d want %d", idx.TotalHeight(), oracleTotal)
	}

	for probe := 0; probe < 300; probe++ {
		y := uint64(rng.Int64N(int64(oracleTotal) + 5))
		gotRow, gotOff := idx.FindRowAtY(y)
		wantRow, wantOff := oracleFind(oracle, y)
		if gotRow != wantRow || gotOff != wantOff {
			t.Fatalf("y=%d: got (%d,%d), want (%d,%d)", y, gotRow, gotOff, wantRow, wantOff)
		}
	}
}
