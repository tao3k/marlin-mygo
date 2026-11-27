// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package arena

import (
	"fmt"
	"testing"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func buildWideTree(panels, rowsPerPanel int, leafOverride string, tone schema.UITone) schema.UIElement {
	children := make([]schema.UIElement, panels)
	for i := 0; i < panels; i++ {
		rows := make([]schema.UIElement, rowsPerPanel)
		for j := 0; j < rowsPerPanel; j++ {
			txt := fmt.Sprintf("panel-%d-row-%d", i, j)
			if i == panels-1 && j == rowsPerPanel-1 && leafOverride != "" {
				txt = leafOverride
			}
			rows[j] = schema.UIElement{
				Kind:  schema.ElemText,
				Spans: []schema.UISpan{{Text: txt, Tone: tone}},
			}
		}
		children[i] = schema.UIElement{
			Kind:     schema.ElemPanel,
			ID:       schema.ComponentID(fmt.Sprintf("p-%d", i)),
			Title:    fmt.Sprintf("Panel %d", i),
			Tone:     tone,
			Children: rows,
		}
	}
	return schema.UIElement{Kind: schema.ElemRows, Children: children}
}

func TestArenaDiffPrunesUnchangedSubtreesAndDetectsRestyleOnly(t *testing.T) {
	base := buildWideTree(20, 20, "", schema.ToneNormal)
	a1, err := Compile(base, 2048, 32)
	if err != nil {
		t.Fatal(err)
	}
	// 1. Identical tree -> zero patches.
	if ops := Diff(a1, a1, nil); len(ops) != 0 {
		t.Fatalf("expected 0 ops for identical arenas, got %d", len(ops))
	}

	// 2. Single streaming leaf text update in 421-node tree -> exactly 1 OpUpdateLeaf.
	updated := buildWideTree(20, 20, "streamed-token-chunk", schema.ToneNormal)
	a2, err := Compile(updated, 2048, 32)
	if err != nil {
		t.Fatal(err)
	}
	ops := Diff(a1, a2, nil)
	if len(ops) != 1 || ops[0].Kind != OpUpdateLeaf {
		t.Fatalf("expected exactly 1 OpUpdateLeaf, got %+v", ops)
	}

	// 3. Tone-only restyle (MAR-UI-T06) -> single root OpRestyleSubtree, zero structural rebuild.
	restyled := buildWideTree(20, 20, "", schema.ToneAccent)
	a3, err := Compile(restyled, 2048, 32)
	if err != nil {
		t.Fatal(err)
	}
	ops = Diff(a1, a3, nil)
	if len(ops) != 1 || ops[0].Kind != OpRestyleSubtree {
		t.Fatalf("expected single OpRestyleSubtree on tone switch, got %+v", ops)
	}
}
