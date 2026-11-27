// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build wasip1

package gouir

import (
	"encoding/json"
	"unsafe"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// Retained guest linear-memory buffers keep pointers valid across ABI calls so
// plugin authors and coding LLMs never import `unsafe` or manage raw pointers.
var (
	retainedInputBuf    []byte
	retainedRenderBuf   []byte
	retainedDrawListBuf []byte
)

// AllocBuffer implements `//go:wasmexport marlin_alloc` with bounded size checks
// so guest plugins never touch `unsafe.Pointer`.
func AllocBuffer(size uint32) uint32 {
	if size == 0 || size > 64*1024 {
		return 0
	}
	if uint32(cap(retainedInputBuf)) < size {
		retainedInputBuf = make([]byte, size)
	} else {
		retainedInputBuf = retainedInputBuf[:size]
	}
	return uint32(uintptr(unsafe.Pointer(&retainedInputBuf[0])))
}

// DispatchEvent decodes a host-scoped event written into AllocBuffer and invokes
// handler with a typed Event struct, returning 0 on success or a negative error code.
func DispatchEvent(ptr, size uint32, handler func(Event) error) int32 {
	if size == 0 || uint32(len(retainedInputBuf)) < size || handler == nil {
		return -1
	}
	if ptr != uint32(uintptr(unsafe.Pointer(&retainedInputBuf[0]))) {
		return -1
	}
	var ev Event
	if err := json.Unmarshal(retainedInputBuf[:size], &ev); err != nil {
		return -2
	}
	if err := handler(ev); err != nil {
		return -3
	}
	return 0
}

// DispatchEventBytes decodes raw JSON event bytes directly for unit tests.
func DispatchEventBytes(raw []byte, handler func(Event) error) int32 {
	if len(raw) == 0 || handler == nil {
		return -1
	}
	retainedInputBuf = append(retainedInputBuf[:0], raw...)
	return DispatchEvent(uint32(uintptr(unsafe.Pointer(&retainedInputBuf[0]))), uint32(len(retainedInputBuf)), handler)
}

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
