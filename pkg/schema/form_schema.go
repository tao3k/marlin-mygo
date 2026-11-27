// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package schema

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const (
	MaxSchemaFields = 32
	MaxEnumOptions  = 32
)

var (
	ErrInvalidSchemaSpec = errors.New("schema: invalid bounded form schema specification")
	ErrSchemaValidation  = errors.New("schema: value violates bounded schema constraints")
)

// FieldKind enumerates the bounded algebraic scalar types supported across
// Rust admission, Scheme POO contracts, Go MyGo auto-forms, and TS SDKs.
type FieldKind string

const (
	FieldString FieldKind = "string"
	FieldInt    FieldKind = "int"
	FieldBool   FieldKind = "bool"
	FieldEnum   FieldKind = "enum"
)

// FieldSpec describes one typed, bounded field inside a BoundedSchema.
type FieldSpec struct {
	Key        string    `json:"key"`
	Label      string    `json:"label"`
	Kind       FieldKind `json:"kind"`
	Required   bool      `json:"required,omitempty"`
	Sensitive  bool      `json:"sensitive,omitempty"`
	MinLen     int       `json:"minLen,omitempty"`
	MaxLen     int       `json:"maxLen,omitempty"`
	MinInt     int64     `json:"minInt,omitempty"`
	MaxInt     int64     `json:"maxInt,omitempty"`
	EnumValues []string  `json:"enumValues,omitempty"`
	Default    any       `json:"default,omitempty"`
}

// BoundedSchema is Marlin's cross-layer SSOT record contract (referenced by
// ContributionDecl.SettingsSchema and DeviceCapabilityDescriptor.*SchemaID).
// One declaration derives strict validation AND native GPU UI IR presentation.
type BoundedSchema struct {
	ID     SchemaID    `json:"id"`
	Title  string      `json:"title"`
	Fields []FieldSpec `json:"fields"`
}

// Validate checks static structural soundness and bounded cardinality.
func (s *BoundedSchema) Validate() error {
	if err := ValidateDotIdentifier(string(s.ID)); err != nil {
		return fmt.Errorf("%w: id %q: %v", ErrInvalidSchemaSpec, s.ID, err)
	}
	if len(s.Fields) == 0 || len(s.Fields) > MaxSchemaFields {
		return fmt.Errorf("%w: field count %d out of [1,%d]", ErrInvalidSchemaSpec, len(s.Fields), MaxSchemaFields)
	}
	seen := make(map[string]struct{}, len(s.Fields))
	for i, f := range s.Fields {
		if err := ValidateDotIdentifier(f.Key); err != nil {
			return fmt.Errorf("%w: field[%d] key %q: %v", ErrInvalidSchemaSpec, i, f.Key, err)
		}
		if _, dup := seen[f.Key]; dup {
			return fmt.Errorf("%w: duplicate field key %q", ErrInvalidSchemaSpec, f.Key)
		}
		seen[f.Key] = struct{}{}
		switch f.Kind {
		case FieldString:
			if f.MinLen < 0 || (f.MaxLen > 0 && f.MinLen > f.MaxLen) {
				return fmt.Errorf("%w: string bounds [%d,%d] on %q", ErrInvalidSchemaSpec, f.MinLen, f.MaxLen, f.Key)
			}
		case FieldInt:
			if f.MaxInt != 0 && f.MinInt > f.MaxInt {
				return fmt.Errorf("%w: int bounds [%d,%d] on %q", ErrInvalidSchemaSpec, f.MinInt, f.MaxInt, f.Key)
			}
		case FieldBool:
		case FieldEnum:
			if len(f.EnumValues) == 0 || len(f.EnumValues) > MaxEnumOptions {
				return fmt.Errorf("%w: enum %q must declare 1..%d values", ErrInvalidSchemaSpec, f.Key, MaxEnumOptions)
			}
		default:
			return fmt.Errorf("%w: unknown field kind %q on %q", ErrInvalidSchemaSpec, f.Kind, f.Key)
		}
	}
	return nil
}

// ValidateValues strictly admits a map of candidate field values, rejecting
// unknown keys, missing required fields, type mismatches, and range violations.
func (s *BoundedSchema) ValidateValues(values map[string]any) error {
	if err := s.Validate(); err != nil {
		return err
	}
	allowed := make(map[string]FieldSpec, len(s.Fields))
	for _, f := range s.Fields {
		allowed[f.Key] = f
	}
	for k := range values {
		if _, ok := allowed[k]; !ok {
			return fmt.Errorf("%w: unknown field %q", ErrSchemaValidation, k)
		}
	}
	for _, f := range s.Fields {
		raw, present := values[f.Key]
		if !present || raw == nil {
			if f.Required {
				return fmt.Errorf("%w: missing required field %q", ErrSchemaValidation, f.Key)
			}
			continue
		}
		switch f.Kind {
		case FieldString:
			sv, ok := raw.(string)
			if !ok {
				return fmt.Errorf("%w: field %q expects string", ErrSchemaValidation, f.Key)
			}
			if len(sv) < f.MinLen {
				return fmt.Errorf("%w: field %q shorter than %d", ErrSchemaValidation, f.Key, f.MinLen)
			}
			maxLen := f.MaxLen
			if maxLen == 0 {
				maxLen = 1024
			}
			if len(sv) > maxLen {
				return fmt.Errorf("%w: field %q exceeds max length %d", ErrSchemaValidation, f.Key, maxLen)
			}
		case FieldInt:
			iv, ok := asInt64(raw)
			if !ok {
				return fmt.Errorf("%w: field %q expects integer", ErrSchemaValidation, f.Key)
			}
			if iv < f.MinInt || (f.MaxInt != 0 && iv > f.MaxInt) {
				return fmt.Errorf("%w: field %q value %d out of [%d,%d]", ErrSchemaValidation, f.Key, iv, f.MinInt, f.MaxInt)
			}
		case FieldBool:
			if _, ok := raw.(bool); !ok {
				return fmt.Errorf("%w: field %q expects bool", ErrSchemaValidation, f.Key)
			}
		case FieldEnum:
			sv, ok := raw.(string)
			if !ok || !slices.Contains(f.EnumValues, sv) {
				return fmt.Errorf("%w: field %q value %v not in %v", ErrSchemaValidation, f.Key, raw, f.EnumValues)
			}
		}
	}
	return nil
}

// CompileToUIElement projects a BoundedSchema + values into a bounded,
// host-neutral UIElement card/summary rendered natively by MyGo GPU or TUI
// without requiring plugin authors to write custom settings or tool-card UI.
func (s *BoundedSchema) CompileToUIElement(values map[string]any) (UIElement, error) {
	if err := s.Validate(); err != nil {
		return UIElement{}, err
	}
	title := s.Title
	if strings.TrimSpace(title) == "" {
		title = string(s.ID)
	}
	rows := make([]UIElement, 0, len(s.Fields))
	for _, f := range s.Fields {
		label := f.Label
		if label == "" {
			label = f.Key
		}
		val, ok := values[f.Key]
		if !ok || val == nil {
			val = f.Default
		}
		rendered := formatFieldValue(f, val)
		tone := ToneNormal
		if f.Sensitive {
			tone = ToneWarning
		} else if f.Kind == FieldBool {
			if bv, isBool := val.(bool); isBool && bv {
				tone = ToneSuccess
			} else {
				tone = ToneMuted
			}
		} else if f.Kind == FieldEnum {
			tone = ToneAccent
		}
		rows = append(rows, UIElement{
			Kind: ElemText,
			ID:   ComponentID("field." + f.Key),
			Spans: []UISpan{
				{Text: label + ": ", Tone: ToneInfo},
				{Text: rendered, Tone: tone},
			},
		})
	}
	tree := UIElement{
		Kind:  ElemPanel,
		ID:    ComponentID(s.ID),
		Title: title,
		Tone:  ToneInfo,
		Children: []UIElement{
			{Kind: ElemRows, Children: rows},
		},
	}
	if err := tree.ValidateBounds(MaxUITreeNodes, MaxUITreeDepth); err != nil {
		return UIElement{}, err
	}
	return tree, nil
}

func asInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case float64:
		if float64(int64(x)) == x {
			return int64(x), true
		}
		return 0, false
	default:
		return 0, false
	}
}

func formatFieldValue(f FieldSpec, v any) string {
	if v == nil {
		return "<unset>"
	}
	if f.Sensitive {
		return "••••••"
	}
	switch x := v.(type) {
	case string:
		if x == "" {
			return "<empty>"
		}
		return x
	case bool:
		if x {
			return "enabled"
		}
		return "disabled"
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	default:
		return fmt.Sprintf("%v", v)
	}
}
