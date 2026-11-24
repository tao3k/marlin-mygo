// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package viewport

import (
	"math/rand/v2"
	"testing"
)

func linearPrefix(heights []uint32, count int) uint64 {
	var s uint64
	for i := 0; i < count && i < len(heights); i++ {
		s += uint64(heights[i])
	}
	return s
}

func linearFindRow(heights []uint32, y uint64) (int, uint64) {
	if len(heights) == 0 {
		return -1, 0
	}
	var acc uint64
	for i, h := range heights {
		if y < acc+uint64(h) {
			return i, y - acc
		}
		acc += uint64(h)
	}
	last := len(heights) - 1
	return last, uint64(heights[last])
}

func TestVirtualHeightIndexPropertyMatchesLinearOracle(t *testing.T) {
	rng := rand.New(rand.NewPCG(9001, 1337))
	idx := NewVirtualHeightIndex(16)
	var ref []uint32

	for step := 0; step < 600; step++ {
		switch rng.IntN(3) {
		case 0: // Append (including 0-height collapsed rows)
			var h uint32
			if rng.IntN(5) != 0 {
				h = uint32(1 + rng.IntN(200))
			}
			idx.Append(h)
			ref = append(ref, h)
		case 1: // UpdateHeight (fold/unfold/resize)
			if len(ref) > 0 {
				row := rng.IntN(len(ref))
				var h uint32
				if rng.IntN(4) != 0 {
					h = uint32(1 + rng.IntN(200))
				}
				idx.UpdateHeight(row, h)
				ref[row] = h
			}
		case 2: // Query checks
			if idx.Len() != len(ref) {
				t.Fatalf("step %d: Len %d != %d", step, idx.Len(), len(ref))
			}
			wantTotal := linearPrefix(ref, len(ref))
			if idx.TotalHeight() != wantTotal {
				t.Fatalf("step %d: TotalHeight %d != %d", step, idx.TotalHeight(), wantTotal)
			}
			if len(ref) > 0 {
				c := rng.IntN(len(ref) + 1)
				if got, want := idx.PrefixSum(c), linearPrefix(ref, c); got != want {
					t.Fatalf("step %d: PrefixSum(%d) = %d != %d", step, c, got, want)
				}
				var y uint64
				if wantTotal > 0 {
					y = uint64(rng.IntN(int(wantTotal) + 10))
				}
				gotRow, gotOff := idx.FindRowAtY(y)
				wantRow, wantOff := linearFindRow(ref, y)
				if gotRow != wantRow || gotOff != wantOff {
					t.Fatalf("step %d: FindRowAtY(%d) = (%d,%d), want (%d,%d)", step, y, gotRow, gotOff, wantRow, wantOff)
				}
			}
		}
	}
}
