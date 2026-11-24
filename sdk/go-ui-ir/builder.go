// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package gouir provides the pure-Go builder SDK for hot-pluggable WASM-IR
// plugins (GOOS=wasip1 GOARCH=wasm) hosted inside marlin-mygo's wazero runtime.
// Guest plugins emit bounded schema.UIElement trees rendered directly on the
// host's Metal / Direct3D 11 / OpenGL GPU pipeline with zero WebView overhead.
package gouir

import (
	"encoding/json"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func Text(spans ...schema.UISpan) schema.UIElement {
	return schema.UIElement{Kind: schema.ElemText, Spans: spans}
}

func Span(text string, tone schema.UITone) schema.UISpan {
	return schema.UISpan{Text: text, Tone: tone}
}

func Rows(children ...schema.UIElement) schema.UIElement {
	return schema.UIElement{Kind: schema.ElemRows, Children: children}
}

func Columns(children ...schema.UIElement) schema.UIElement {
	return schema.UIElement{Kind: schema.ElemColumns, Children: children}
}

func Panel(title string, tone schema.UITone, child schema.UIElement) schema.UIElement {
	return schema.UIElement{
		Kind:     schema.ElemPanel,
		Title:    title,
		Tone:     tone,
		Children: []schema.UIElement{child},
	}
}

func Slot(slotID schema.SlotID) schema.UIElement {
	return schema.UIElement{Kind: schema.ElemSlot, SlotID: slotID}
}

// Encode validates MAR-UI-R03 bounds before serializing the tree across the
// wazero guest-to-host memory boundary.
func Encode(root schema.UIElement) ([]byte, error) {
	if err := root.ValidateBounds(schema.MaxUITreeNodes, schema.MaxUITreeDepth); err != nil {
		return nil, err
	}
	return json.Marshal(root)
}
