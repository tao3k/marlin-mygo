// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package arena implements a cache-friendly, flat BFS-ordered UI IR Arena with
// bottom-up FNV-1a 64-bit dual Merkle hashing (ContentHash + StyleHash) and
// O(1)-subtree-pruning incremental reconciliation.
package arena

import (
	"fmt"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

const (
	fnvOffset64 uint64 = 14695981039346656037
	fnvPrime64  uint64 = 1099511628211
)

// Node is a compact, pointer-free value stored contiguously inside Arena.Nodes.
// Because the arena uses BFS allocation, a node's children always occupy the
// contiguous slice Nodes[FirstChild : FirstChild+ChildCount], and every child
// index is strictly greater than its parent index.
type Node struct {
	Kind        schema.UIElementKind
	ID          schema.ComponentID
	SlotID      schema.SlotID
	Title       string
	Tone        schema.UITone
	Flex        uint16
	Cells       uint16
	SpanStart   uint32
	SpanCount   uint32
	FirstChild  uint32
	ChildCount  uint32
	ContentHash uint64 // structural + textual + slot identity digest
	StyleHash   uint64 // tone / presentation-only digest (MAR-UI-T06 restyle check)
}

// Arena is an immutable, bounded, cache-contiguous representation of a UI IR tree.
type Arena struct {
	Nodes []Node
	Spans []schema.UISpan
}

type queueItem struct {
	src     *schema.UIElement
	nodeIdx uint32
	depth   int
}

// Compile flattens root into a contiguous BFS Arena, enforcing MAR-UI-R03 node
// and depth bounds iteratively (zero recursion), and computes bottom-up dual
// Merkle hashes in a single reverse linear pass.
func Compile(root schema.UIElement, maxNodes, maxDepth int) (*Arena, error) {
	if maxNodes <= 0 {
		maxNodes = schema.MaxUITreeNodes
	}
	if maxDepth <= 0 {
		maxDepth = schema.MaxUITreeDepth
	}
	if root.Kind == "" {
		return nil, fmt.Errorf("%w: empty root kind", schema.ErrUITreeBounds)
	}

	a := &Arena{
		Nodes: make([]Node, 1, min(maxNodes, 64)),
		Spans: make([]schema.UISpan, 0, 32),
	}
	copyNodeFields(a, 0, &root)

	queue := make([]queueItem, 1, min(maxNodes, 64))
	queue[0] = queueItem{src: &root, nodeIdx: 0, depth: 1}

	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		children := cur.src.Children
		if len(children) == 0 {
			continue
		}
		nextDepth := cur.depth + 1
		if nextDepth > maxDepth {
			return nil, fmt.Errorf("%w: depth %d exceeds %d", schema.ErrUITreeBounds, nextDepth, maxDepth)
		}
		if len(a.Nodes)+len(children) > maxNodes {
			return nil, fmt.Errorf("%w: node count exceeds %d", schema.ErrUITreeBounds, maxNodes)
		}
		firstChild := uint32(len(a.Nodes))
		a.Nodes[cur.nodeIdx].FirstChild = firstChild
		a.Nodes[cur.nodeIdx].ChildCount = uint32(len(children))

		for i := range children {
			ch := &children[i]
			if ch.Kind == "" {
				return nil, fmt.Errorf("%w: empty element kind", schema.ErrUITreeBounds)
			}
			idx := uint32(len(a.Nodes))
			a.Nodes = append(a.Nodes, Node{})
			copyNodeFields(a, idx, ch)
			queue = append(queue, queueItem{src: ch, nodeIdx: idx, depth: nextDepth})
		}
	}

	// Single reverse linear pass: every child index > parent index by BFS layout.
	for i := len(a.Nodes) - 1; i >= 0; i-- {
		computeMerkleHashes(a, uint32(i))
	}
	return a, nil
}

func copyNodeFields(a *Arena, idx uint32, src *schema.UIElement) {
	spanStart := uint32(len(a.Spans))
	if len(src.Spans) > 0 {
		a.Spans = append(a.Spans, src.Spans...)
	}
	a.Nodes[idx] = Node{
		Kind:      src.Kind,
		ID:        src.ID,
		SlotID:    src.SlotID,
		Title:     src.Title,
		Tone:      src.Tone,
		Flex:      src.Flex,
		Cells:     src.Cells,
		SpanStart: spanStart,
		SpanCount: uint32(len(src.Spans)),
	}
}

func computeMerkleHashes(a *Arena, idx uint32) {
	n := &a.Nodes[idx]
	ch := fnvOffset64
	sh := fnvOffset64

	ch = hashString(ch, string(n.Kind))
	ch = hashString(ch, string(n.ID))
	ch = hashString(ch, string(n.SlotID))
	ch = hashString(ch, n.Title)
	ch = hashUint64(ch, uint64(n.Flex)<<16|uint64(n.Cells))

	sh = hashString(sh, string(n.Tone))

	spans := a.Spans[n.SpanStart : n.SpanStart+n.SpanCount]
	for i := range spans {
		ch = hashString(ch, spans[i].Text)
		sh = hashString(sh, string(spans[i].Tone))
	}

	children := a.Nodes[n.FirstChild : n.FirstChild+n.ChildCount]
	ch = hashUint64(ch, uint64(len(children))^0x9e3779b97f4a7c15)
	sh = hashUint64(sh, uint64(len(children))^0xbf58476d1ce4e5b9)
	for i := range children {
		ch = hashUint64(ch, children[i].ContentHash)
		sh = hashUint64(sh, children[i].StyleHash)
	}

	n.ContentHash = ch
	n.StyleHash = sh
}

// PatchKind categorizes minimal incremental updates emitted by Diff.
type PatchKind uint8

const (
	OpReplaceSubtree PatchKind = iota + 1
	OpRestyleSubtree           // ContentHash identical; only StyleHash changed (zero layout rebuild)
	OpUpdateLeaf               // Same structural node identity, updated local spans/title
	OpInsertChild
	OpRemoveChild
)

// PatchOp is a value-type reconciliation instruction (zero heap pointers).
type PatchOp struct {
	Kind       PatchKind
	PrevIndex  uint32
	NextIndex  uint32
	ChildIndex uint32
}

type diffPair struct {
	prevIdx uint32
	nextIdx uint32
}

// Diff computes the minimal patch sequence transforming prev into next.
// Unchanged subtrees are pruned in O(1) via dual Merkle hash equality.
// Callers may pass a reusable dst slice (e.g. buf[:0]) for zero-allocation diffing.
func Diff(prev, next *Arena, dst []PatchOp) []PatchOp {
	ops := dst[:0]
	if prev == nil || len(prev.Nodes) == 0 {
		if next != nil && len(next.Nodes) > 0 {
			ops = append(ops, PatchOp{Kind: OpReplaceSubtree, NextIndex: 0})
		}
		return ops
	}
	if next == nil || len(next.Nodes) == 0 {
		ops = append(ops, PatchOp{Kind: OpRemoveChild, PrevIndex: 0})
		return ops
	}

	// Stack-allocated fixed buffer avoids heap allocs for normal trees (depth <= 32).
	var stackBuf [64]diffPair
	stack := append(stackBuf[:0], diffPair{prevIdx: 0, nextIdx: 0})

	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		p := &prev.Nodes[top.prevIdx]
		n := &next.Nodes[top.nextIdx]

		// O(1) full subtree prune: neither content nor style changed.
		if p.ContentHash == n.ContentHash && p.StyleHash == n.StyleHash {
			continue
		}
		// O(1) restyle-only shortcut: structure & text unchanged, only tones/theme changed.
		if p.ContentHash == n.ContentHash && p.StyleHash != n.StyleHash {
			ops = append(ops, PatchOp{
				Kind:      OpRestyleSubtree,
				PrevIndex: top.prevIdx,
				NextIndex: top.nextIdx,
			})
			continue
		}
		// Structural mismatch at this node -> replace subtree.
		if p.Kind != n.Kind || p.ID != n.ID || p.SlotID != n.SlotID || p.Flex != n.Flex || p.Cells != n.Cells {
			ops = append(ops, PatchOp{
				Kind:      OpReplaceSubtree,
				PrevIndex: top.prevIdx,
				NextIndex: top.nextIdx,
			})
			continue
		}
		// Local text/title changed on same structural node.
		if p.Title != n.Title || !spansEqual(prev, p, next, n) {
			ops = append(ops, PatchOp{
				Kind:      OpUpdateLeaf,
				PrevIndex: top.prevIdx,
				NextIndex: top.nextIdx,
			})
		}

		common := min(p.ChildCount, n.ChildCount)
		for i := uint32(0); i < common; i++ {
			pc := p.FirstChild + i
			nc := n.FirstChild + i
			if prev.Nodes[pc].ContentHash == next.Nodes[nc].ContentHash &&
				prev.Nodes[pc].StyleHash == next.Nodes[nc].StyleHash {
				continue
			}
			stack = append(stack, diffPair{prevIdx: pc, nextIdx: nc})
		}
		for i := common; i < n.ChildCount; i++ {
			ops = append(ops, PatchOp{
				Kind:       OpInsertChild,
				PrevIndex:  top.prevIdx,
				NextIndex:  n.FirstChild + i,
				ChildIndex: i,
			})
		}
		for i := common; i < p.ChildCount; i++ {
			ops = append(ops, PatchOp{
				Kind:       OpRemoveChild,
				PrevIndex:  p.FirstChild + i,
				NextIndex:  top.nextIdx,
				ChildIndex: i,
			})
		}
	}
	return ops
}

func spansEqual(pa *Arena, p *Node, na *Arena, n *Node) bool {
	if p.SpanCount != n.SpanCount {
		return false
	}
	ps := pa.Spans[p.SpanStart : p.SpanStart+p.SpanCount]
	ns := na.Spans[n.SpanStart : n.SpanStart+n.SpanCount]
	for i := range ps {
		if ps[i] != ns[i] {
			return false
		}
	}
	return true
}

func hashString(h uint64, s string) uint64 {
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= fnvPrime64
	}
	h ^= 0xff
	h *= fnvPrime64
	return h
}

func hashUint64(h, v uint64) uint64 {
	// Non-commutative avalanche mixing so swapping parent/child digests
	// (e.g. parent=A, child=B vs parent=B, child=A) never collides.
	h = (h * fnvPrime64) ^ (v + 0x9e3779b97f4a7c15 + (h << 6) + (h >> 2))
	h ^= h >> 30
	h *= 0xbf58476d1ce4e5b9
	h ^= h >> 27
	return h
}
