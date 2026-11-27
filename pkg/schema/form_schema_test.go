// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package schema

import (
	"errors"
	"math"
	"testing"
)

func TestBoundedSchemaValidationAndAutoUIProjection(t *testing.T) {
	spec := BoundedSchema{
		ID:    "tao3k.kanban.settings",
		Title: "Org Kanban Settings",
		Fields: []FieldSpec{
			{Key: "board_mode", Label: "Mode", Kind: FieldEnum, Required: true, EnumValues: []string{"columns", "swimlanes"}, Default: "columns"},
			{Key: "max_cards", Label: "Max Cards", Kind: FieldInt, MinInt: 10, MaxInt: 500, Default: 100},
			{Key: "auto_sync", Label: "Auto Sync", Kind: FieldBool, Default: true},
			{Key: "webhook_secret", Label: "Secret", Kind: FieldString, Sensitive: true, MaxLen: 64},
		},
	}

	valid := map[string]any{
		"board_mode":     "swimlanes",
		"max_cards":      250,
		"auto_sync":      true,
		"webhook_secret": "top-secret-token",
	}
	if err := spec.ValidateValues(valid); err != nil {
		t.Fatalf("expected valid values to pass, got %v", err)
	}

	// Reject unknown fields, invalid enum, and out-of-range int.
	if err := spec.ValidateValues(map[string]any{"board_mode": "columns", "rogue": 1}); !errors.Is(err, ErrSchemaValidation) {
		t.Fatalf("expected ErrSchemaValidation on unknown field, got %v", err)
	}
	if err := spec.ValidateValues(map[string]any{"board_mode": "invalid"}); !errors.Is(err, ErrSchemaValidation) {
		t.Fatalf("expected ErrSchemaValidation on bad enum, got %v", err)
	}
	if err := spec.ValidateValues(map[string]any{"board_mode": "columns", "max_cards": 9999}); !errors.Is(err, ErrSchemaValidation) {
		t.Fatalf("expected ErrSchemaValidation on int overflow, got %v", err)
	}

	// Auto-projected UI IR tree masks sensitive secrets and passes MAR-UI-R03 bounds.
	tree, err := spec.CompileToUIElement(valid)
	if err != nil {
		t.Fatalf("CompileToUIElement failed: %v", err)
	}
	if tree.Kind != ElemPanel || tree.Title != "Org Kanban Settings" {
		t.Fatalf("unexpected root element: %+v", tree)
	}
	rows := tree.Children[0].Children
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(rows))
	}
	if rows[3].Spans[1].Text != "••••••" {
		t.Fatalf("expected masked sensitive value, got %q", rows[3].Spans[1].Text)
	}
}

func TestDrawListRoundTripAndBoundsRejection(t *testing.T) {
	in := []DrawCmd{
		{Op: DrawOpFillRect, Radius: 6, X: 10, Y: 20, W: 120, H: 40, RGBA: 0x51afefFF, Extra0: 7},
		{Op: DrawOpLine, X: 0, Y: 0, W: 300, H: 150, RGBA: 0xc678ddFF, Extra0: 2},
	}
	raw, err := EncodeDrawList(in, nil)
	if err != nil {
		t.Fatalf("EncodeDrawList failed: %v", err)
	}
	var out [4]DrawCmd
	decoded, err := DecodeDrawList(raw, out[:0])
	if err != nil {
		t.Fatalf("DecodeDrawList failed: %v", err)
	}
	if len(decoded) != 2 || decoded[0] != in[0] || decoded[1] != in[1] {
		t.Fatalf("round-trip mismatch: got %+v want %+v", decoded, in)
	}

	// NaN coordinates must be rejected.
	if _, err := EncodeDrawList([]DrawCmd{{Op: DrawOpFillRect, X: float32(math.NaN())}}, nil); !errors.Is(err, ErrInvalidDrawList) {
		t.Fatalf("expected ErrInvalidDrawList for NaN, got %v", err)
	}
}

func FuzzDecodeDrawList(f *testing.F) {
	seed, _ := EncodeDrawList([]DrawCmd{{Op: DrawOpFillRect, X: 1, Y: 2, W: 3, H: 4, RGBA: 0xffffffff}}, nil)
	f.Add(seed)
	f.Add([]byte("MDLB\x01\x00\x01\x00"))
	f.Add([]byte("BAD!"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		cmds, err := DecodeDrawList(raw, nil)
		if err != nil {
			return
		}
		if len(cmds) > MaxDrawListCmds {
			t.Fatalf("decoded %d commands exceeding limit", len(cmds))
		}
		reencoded, err := EncodeDrawList(cmds, nil)
		if err != nil {
			t.Fatalf("re-encoding admitted drawlist failed: %v", err)
		}
		if len(reencoded) != len(raw) {
			t.Fatalf("re-encoded length %d != %d", len(reencoded), len(raw))
		}
	})
}
