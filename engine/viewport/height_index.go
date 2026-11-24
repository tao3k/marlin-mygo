// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package viewport provides a Fenwick Tree (Binary Indexed Tree) virtual height
// index with O(log N) binary-lifting row lookup and O(log N) height updates for
// 100k+ row streaming transcripts and collapsible thinking/tool blocks (MAR-UI-R03).
package viewport

// VirtualHeightIndex tracks variable per-row pixel/cell heights with O(log N)
// append, fold/unfold height mutation, prefix sum, and scroll-to-row binary lifting.
type VirtualHeightIndex struct {
	heights []uint32
	bit     []uint64 // 1-indexed Fenwick tree
	total   uint64
}

func NewVirtualHeightIndex(initialCap int) *VirtualHeightIndex {
	if initialCap < 16 {
		initialCap = 16
	}
	return &VirtualHeightIndex{
		heights: make([]uint32, 0, initialCap),
		bit:     make([]uint64, 1, initialCap+1),
	}
}

func (v *VirtualHeightIndex) Len() int            { return len(v.heights) }
func (v *VirtualHeightIndex) TotalHeight() uint64 { return v.total }

func (v *VirtualHeightIndex) RowHeight(row int) uint32 {
	if row < 0 || row >= len(v.heights) {
		return 0
	}
	return v.heights[row]
}

// Append adds one row at the end in O(log N) amortized time.
func (v *VirtualHeightIndex) Append(height uint32) int {
	row := len(v.heights)
	v.heights = append(v.heights, height)
	idx := row + 1 // 1-indexed
	v.bit = append(v.bit, uint64(height))
	// Accumulate sub-ranges covered by lowbit(idx) in O(log N).
	low := idx & -idx
	for step := 1; step < low; step <<= 1 {
		v.bit[idx] += v.bit[idx-step]
	}
	v.total += uint64(height)
	return row
}

// UpdateHeight mutates a single row's height (e.g. toggling a collapsed thinking
// block or re-wrapping a streaming reply) in O(log N) without rebuilding the index.
func (v *VirtualHeightIndex) UpdateHeight(row int, newHeight uint32) {
	if row < 0 || row >= len(v.heights) {
		return
	}
	old := v.heights[row]
	if old == newHeight {
		return
	}
	v.heights[row] = newHeight
	n := len(v.heights)
	if newHeight > old {
		delta := uint64(newHeight - old)
		v.total += delta
		for i := row + 1; i <= n; i += i & -i {
			v.bit[i] += delta
		}
	} else {
		delta := uint64(old - newHeight)
		v.total -= delta
		for i := row + 1; i <= n; i += i & -i {
			v.bit[i] -= delta
		}
	}
}

// PrefixSum returns the cumulative height of rows [0, count) in O(log N).
func (v *VirtualHeightIndex) PrefixSum(count int) uint64 {
	if count <= 0 {
		return 0
	}
	if count > len(v.heights) {
		count = len(v.heights)
	}
	var sum uint64
	for i := count; i > 0; i -= i & -i {
		sum += v.bit[i]
	}
	return sum
}

// FindRowAtY locates the 0-indexed row covering vertical offset y and the
// residual offset within that row using binary lifting on the Fenwick tree
// in exact O(log N) time, skipping zero-height (collapsed/hidden) rows cleanly.
func (v *VirtualHeightIndex) FindRowAtY(y uint64) (row int, offsetInRow uint64) {
	n := len(v.heights)
	if n == 0 {
		return -1, 0
	}
	if y >= v.total {
		last := n - 1
		return last, uint64(v.heights[last])
	}

	// Largest power of two <= n.
	step := 1
	for (step << 1) <= n {
		step <<= 1
	}

	pos := 0
	var acc uint64
	for ; step > 0; step >>= 1 {
		next := pos + step
		if next <= n && acc+v.bit[next] <= y {
			pos = next
			acc += v.bit[next]
		}
	}
	if pos >= n {
		pos = n - 1
	}
	return pos, y - acc
}

// VisibleRange returns the inclusive [firstRow, lastRow] interval intersecting
// [scrollY, scrollY+viewportHeight) along with the top clip offset of firstRow.
func (v *VirtualHeightIndex) VisibleRange(scrollY, viewportHeight uint64) (firstRow, lastRow int, firstOffset uint64) {
	if len(v.heights) == 0 || v.total == 0 {
		return -1, -1, 0
	}
	firstRow, firstOffset = v.FindRowAtY(scrollY)
	endY := scrollY + viewportHeight
	if endY > 0 {
		endY--
	}
	lastRow, _ = v.FindRowAtY(endY)
	if lastRow < firstRow {
		lastRow = firstRow
	}
	return firstRow, lastRow, firstOffset
}
