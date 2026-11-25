// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build wasip1

package gouir

import "github.com/tao3k/marlin-mygo/pkg/schema"

// Retained guest linear-memory buffers keep pointers valid until the next call
// so plugin authors and LLMs never touch unsafe.Pointer or bit-packing.
var (
	retainedRenderBuf   []byte
	retainedDrawListBuf []byte
)

// RenderResult validates, encodes, retains, and packs a UIElement tree for
// direct return from `//go:wasmexport marlin_ui_render`.
func RenderResult(root schema.UIElement) uint64 {
	raw, err := Encode(root)
	if err != nil || len(raw) == 0 {
		return 0
	}
	retainedRenderBuf = raw
	return PackBytes(retainedRenderBuf)
}

// DrawListResult encodes a zero-JSON binary DrawList into retained guest memory
// for direct return from `//go:wasmexport marlin_draw_list`.
func DrawListResult(cmds []schema.DrawCmd) uint64 {
	raw, err := schema.EncodeDrawList(cmds, retainedDrawListBuf[:0])
	if err != nil || len(raw) == 0 {
		return 0
	}
	retainedDrawListBuf = raw
	return PackBytes(retainedDrawListBuf)
}
