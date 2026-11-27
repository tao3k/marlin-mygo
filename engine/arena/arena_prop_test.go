// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package arena

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

var propKinds = []schema.UIElementKind{
	schema.ElemPanel, schema.ElemRows, schema.ElemColumns,
	schema.ElemText, schema.ElemBar, schema.ElemList, schema.ElemSlot,
}

var propTones = []schema.UITone{
	schema.ToneNormal, schema.ToneCanvas, schema.ToneAccent,
	schema.ToneInfo, schema.ToneSuccess, schema.ToneWarning, schema.ToneMuted,
}

func randomUIElement(rng *rand.Rand, depth, maxDepth int, budget *int) schema.UIElement {
	*budget--
	kind := propKinds[rng.IntN(len(propKinds))]
	tone := propTones[rng.IntN(len(propTones))]
	el := schema.UIElement{
		Kind:  kind,
		ID:    schema.ComponentID(fmt.Sprintf("n%d", *budget)),
		Title: fmt.Sprintf("T%d", rng.IntN(16)),
		Tone:  tone,
		Flex:  uint16(rng.IntN(4)),
		Cells: uint16(rng.IntN(32)),
		Spans: []schema.UISpan{{Text: fmt.Sprintf("S%d", rng.IntN(16)), Tone: tone}},
	}
	if depth < maxDepth && *budget > 0 {
		fanout := rng.IntN(4)
		for i := 0; i < fanout && *budget > 0; i++ {
			el.Children = append(el.Children, randomUIElement(rng, depth+1, maxDepth, budget))
		}
	}
	return el
}

func cloneWithRecoloredTones(el schema.UIElement) schema.UIElement {
	nextTone := schema.ToneWarning
	if el.Tone == schema.ToneWarning {
		nextTone = schema.ToneSuccess
	}
	cp := el
	cp.Tone = nextTone
	if len(el.Spans) > 0 {
		cp.Spans = make([]schema.UISpan, len(el.Spans))
		for i, s := range el.Spans {
			s.Tone = nextTone
			cp.Spans[i] = s
		}
	}
	if len(el.Children) > 0 {
		cp.Children = make([]schema.UIElement, len(el.Children))
		for i := range el.Children {
			cp.Children[i] = cloneWithRecoloredTones(el.Children[i])
		}
	}
	return cp
}

func TestArenaPropertyCompileAndDiffInvariants(t *testing.T) {
	rng := rand.New(rand.NewPCG(2026, 42))
	var patchBuf []PatchOp

	for trial := 0; trial < 200; trial++ {
		budget := 1 + rng.IntN(120)
		root := randomUIElement(rng, 1, 8, &budget)

		a, err := Compile(root, schema.MaxUITreeNodes, schema.MaxUITreeDepth)
		if err != nil {
			t.Fatalf("trial %d: unexpected compile error: %v", trial, err)
		}
		// Property 1: BFS parent-before-child ordering & contiguous child slices.
		for i, n := range a.Nodes {
			if n.ChildCount > 0 {
				if int(n.FirstChild) <= i {
					t.Fatalf("trial %d: child index %d <= parent %d", trial, n.FirstChild, i)
				}
				if int(n.FirstChild+n.ChildCount) > len(a.Nodes) {
					t.Fatalf("trial %d: child slice [%d:%d] out of range %d", trial, n.FirstChild, n.FirstChild+n.ChildCount, len(a.Nodes))
				}
			}
		}

		// Property 2: Diff reflexivity (identical tree -> 0 ops).
		if ops := Diff(a, a, patchBuf[:0]); len(ops) != 0 {
			t.Fatalf("trial %d: identical arena produced %d ops", trial, len(ops))
		}

		// Property 3: Pure theme/tone restyle preserves ContentHash and emits only OpRestyleSubtree.
		recolored := cloneWithRecoloredTones(root)
		b, err := Compile(recolored, schema.MaxUITreeNodes, schema.MaxUITreeDepth)
		if err != nil {
			t.Fatalf("trial %d: recolored compile error: %v", trial, err)
		}
		if a.Nodes[0].ContentHash != b.Nodes[0].ContentHash {
			t.Fatalf("trial %d: pure restyle changed ContentHash (%x != %x)", trial, a.Nodes[0].ContentHash, b.Nodes[0].ContentHash)
		}
		ops := Diff(a, b, patchBuf[:0])
		if len(ops) != 1 || ops[0].Kind != OpRestyleSubtree {
			t.Fatalf("trial %d: expected single OpRestyleSubtree, got %+v (nodes=%d a0.Tone=%q b0.Tone=%q a0.SH=%x b0.SH=%x a0.CH=%x b0.CH=%x)",
				trial, ops, len(a.Nodes), a.Nodes[0].Tone, b.Nodes[0].Tone, a.Nodes[0].StyleHash, b.Nodes[0].StyleHash, a.Nodes[0].ContentHash, b.Nodes[0].ContentHash)
		}

		// Property 4: Leaf text mutation changes ContentHash and emits non-empty patch.
		mutated := root
		mutated.Title = root.Title + "-mut"
		c, err := Compile(mutated, schema.MaxUITreeNodes, schema.MaxUITreeDepth)
		if err != nil {
			t.Fatalf("trial %d: mutated compile error: %v", trial, err)
		}
		if a.Nodes[0].ContentHash == c.Nodes[0].ContentHash {
			t.Fatalf("trial %d: title mutation failed to change ContentHash", trial)
		}
		if ops := Diff(a, c, patchBuf[:0]); len(ops) == 0 {
			t.Fatalf("trial %d: title mutation produced empty diff", trial)
		}
	}

	// Property 5: Exceeding maxDepth or maxNodes ALWAYS fails with ErrUITreeBounds.
	deep := schema.UIElement{Kind: schema.ElemRows}
	cur := &deep
	for i := 0; i < 40; i++ {
		cur.Children = []schema.UIElement{{Kind: schema.ElemRows}}
		cur = &cur.Children[0]
	}
	if _, err := Compile(deep, schema.MaxUITreeNodes, schema.MaxUITreeDepth); !errors.Is(err, schema.ErrUITreeBounds) {
		t.Fatalf("expected ErrUITreeBounds for depth 41, got %v", err)
	}
}

func TestArenaStyleHashNonCommutativeParentChildSwap(t *testing.T) {
	aTree := schema.UIElement{
		Kind:     schema.ElemPanel,
		Tone:     schema.ToneSuccess,
		Spans:    []schema.UISpan{{Text: "p", Tone: schema.ToneSuccess}},
		Children: []schema.UIElement{{Kind: schema.ElemText, Tone: schema.ToneWarning, Spans: []schema.UISpan{{Text: "c", Tone: schema.ToneWarning}}}},
	}
	bTree := schema.UIElement{
		Kind:     schema.ElemPanel,
		Tone:     schema.ToneWarning,
		Spans:    []schema.UISpan{{Text: "p", Tone: schema.ToneWarning}},
		Children: []schema.UIElement{{Kind: schema.ElemText, Tone: schema.ToneSuccess, Spans: []schema.UISpan{{Text: "c", Tone: schema.ToneSuccess}}}},
	}
	a, err := Compile(aTree, 64, 8)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Compile(bTree, 64, 8)
	if err != nil {
		t.Fatal(err)
	}
	if a.Nodes[0].StyleHash == b.Nodes[0].StyleHash {
		t.Fatalf("parent/child tone swap produced colliding StyleHash %x", a.Nodes[0].StyleHash)
	}
	ops := Diff(a, b, nil)
	if len(ops) != 1 || ops[0].Kind != OpRestyleSubtree {
		t.Fatalf("expected OpRestyleSubtree on parent/child tone swap, got %+v", ops)
	}
}

func decodeSyntheticElement(data []byte, cursor *int, depth int) schema.UIElement {
	if *cursor >= len(data) || depth > 8 {
		return schema.UIElement{Kind: schema.ElemText, Title: "leaf", Tone: schema.ToneNormal}
	}
	b := data[*cursor]
	*cursor++
	el := schema.UIElement{
		Kind:  propKinds[int(b)%len(propKinds)],
		Tone:  propTones[int(b>>3)%len(propTones)],
		Title: fmt.Sprintf("k%d", b),
		Flex:  uint16(b & 3),
		Spans: []schema.UISpan{{Text: fmt.Sprintf("s%d", b), Tone: propTones[int(b)%len(propTones)]}},
	}
	fanout := int(b % 3)
	for i := 0; i < fanout && *cursor < len(data); i++ {
		el.Children = append(el.Children, decodeSyntheticElement(data, cursor, depth+1))
	}
	return el
}

func FuzzArenaCompileAndDiff(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4}, []byte{1, 2, 9, 4})
	f.Add([]byte{0}, []byte{255, 128, 64})
	f.Fuzz(func(t *testing.T, rawA, rawB []byte) {
		cA, cB := 0, 0
		elA := decodeSyntheticElement(rawA, &cA, 1)
		elB := decodeSyntheticElement(rawB, &cB, 1)

		a, err := Compile(elA, 256, 16)
		if err != nil {
			return
		}
		b, err := Compile(elB, 256, 16)
		if err != nil {
			return
		}
		if ops := Diff(a, a, nil); len(ops) != 0 {
			t.Fatalf("reflexive diff non-empty: %d ops", len(ops))
		}
		ops := Diff(a, b, nil)
		for _, op := range ops {
			if op.Kind != OpInsertChild && int(op.PrevIndex) >= len(a.Nodes) {
				t.Fatalf("invalid PrevIndex %d >= %d", op.PrevIndex, len(a.Nodes))
			}
			if op.Kind != OpRemoveChild && int(op.NextIndex) >= len(b.Nodes) {
				t.Fatalf("invalid NextIndex %d >= %d", op.NextIndex, len(b.Nodes))
			}
		}
	})
}
