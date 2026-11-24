// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strconv"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// SchemaFormState owns mutable UI state for a schema-generated native GPU form.
// One schema.BoundedSchema declaration drives validation and interactive GPU UI
// with zero WebView overhead and full iOS/Android App Store compliance.
type SchemaFormState struct {
	Strings   map[string]string
	Bools     map[string]bool
	LastError string
}

func NewSchemaFormState(spec schema.BoundedSchema, initial map[string]any) *SchemaFormState {
	st := &SchemaFormState{
		Strings: make(map[string]string, len(spec.Fields)),
		Bools:   make(map[string]bool, len(spec.Fields)),
	}
	for _, f := range spec.Fields {
		val := f.Default
		if v, ok := initial[f.Key]; ok && v != nil {
			val = v
		}
		switch f.Kind {
		case schema.FieldBool:
			if bv, ok := val.(bool); ok {
				st.Bools[f.Key] = bv
			}
		default:
			if val != nil {
				st.Strings[f.Key] = fmt.Sprintf("%v", val)
			}
		}
	}
	return st
}

// Values converts current form widget inputs into typed candidate values ready
// for BoundedSchema.ValidateValues.
func (s *SchemaFormState) Values(spec schema.BoundedSchema) (map[string]any, error) {
	out := make(map[string]any, len(spec.Fields))
	for _, f := range spec.Fields {
		switch f.Kind {
		case schema.FieldBool:
			out[f.Key] = s.Bools[f.Key]
		case schema.FieldInt:
			raw := s.Strings[f.Key]
			if raw == "" {
				continue
			}
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: field %q must be an integer", schema.ErrSchemaValidation, f.Key)
			}
			out[f.Key] = n
		default:
			raw := s.Strings[f.Key]
			if raw != "" {
				out[f.Key] = raw
			}
		}
	}
	return out, nil
}

// RenderBoundedForm renders an interactive native GPU settings/tool form
// directly from spec, enforcing BoundedSchema.ValidateValues on submit.
func RenderBoundedForm(
	c *ui.Context,
	spec schema.BoundedSchema,
	state *SchemaFormState,
	onSubmit func(map[string]any),
) {
	t := c.Theme()
	ui.Column(c).Padding(12).Gap(10).Radius(8).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		title := spec.Title
		if title == "" {
			title = string(spec.ID)
		}
		ui.Text(c, title).Bold().FontSize(15)

		for _, f := range spec.Fields {
			label := f.Label
			if label == "" {
				label = f.Key
			}
			switch f.Kind {
			case schema.FieldBool:
				val := state.Bools[f.Key]
				ui.Checkbox(c.Key("field-"+f.Key), &val, label)
				state.Bools[f.Key] = val

			case schema.FieldEnum:
				ui.Column(c).Gap(4).Children(func() {
					ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
					ui.Row(c).Gap(6).Children(func() {
						cur := state.Strings[f.Key]
						for _, opt := range f.EnumValues {
							btnKey := c.Key("enum-" + f.Key + "-" + opt)
							if cur == opt {
								if ui.PrimaryButton(btnKey, opt).Clicked() {
									state.Strings[f.Key] = opt
								}
							} else {
								if ui.Button(btnKey, opt).Clicked() {
									state.Strings[f.Key] = opt
								}
							}
						}
					})
				})

			default: // FieldString or FieldInt
				ui.Column(c).Gap(4).Children(func() {
					ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
					val := state.Strings[f.Key]
					ui.TextInput(c.Key("input-"+f.Key), &val).Placeholder(label)
					state.Strings[f.Key] = val
				})
			}
		}

		if state.LastError != "" {
			ui.Text(c, state.LastError).FontSize(12).TextColor(t.Warning)
		}

		ui.Row(c).Justify(ui.End).Children(func() {
			if ui.PrimaryButton(c.Key("schema-form-submit"), "Save Settings").Clicked() {
				vals, err := state.Values(spec)
				if err == nil {
					err = spec.ValidateValues(vals)
				}
				if err != nil {
					state.LastError = err.Error()
					return
				}
				state.LastError = ""
				if onSubmit != nil {
					onSubmit(vals)
				}
			}
		})
	})
}
