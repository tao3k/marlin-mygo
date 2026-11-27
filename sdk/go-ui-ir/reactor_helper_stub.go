// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !wasip1

package gouir

import (
	"encoding/json"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

var hostInputBuf []byte

// AllocBuffer sizes the local scratch buffer on non-wasip1 hosts for native tests.
func AllocBuffer(size uint32) uint32 {
	if size == 0 || size > 64*1024 {
		return 0
	}
	hostInputBuf = make([]byte, size)
	return size
}

// DispatchEvent decodes raw event JSON on non-wasip1 hosts for fast native unit tests.
func DispatchEvent(_ uint32, size uint32, handler func(Event) error) int32 {
	if size == 0 || uint32(len(hostInputBuf)) < size || handler == nil {
		return -1
	}
	var ev Event
	if err := json.Unmarshal(hostInputBuf[:size], &ev); err != nil {
		return -2
	}
	if err := handler(ev); err != nil {
		return -3
	}
	return 0
}

// DispatchEventBytes decodes raw JSON bytes directly in native Go unit tests.
func DispatchEventBytes(raw []byte, handler func(Event) error) int32 {
	if len(raw) == 0 || handler == nil {
		return -1
	}
	hostInputBuf = append(hostInputBuf[:0], raw...)
	return DispatchEvent(0, uint32(len(hostInputBuf)), handler)
}

// RenderResult encodes root and returns its byte length on non-wasip1 hosts so
// plugin logic compiles and unit-tests natively under `go test`.
func RenderResult(root schema.UIElement) uint64 {
	raw, err := Encode(root)
	if err != nil {
		return 0
	}
	return uint64(len(raw))
}

// DrawListResult encodes cmds and returns its byte length on non-wasip1 hosts.
func DrawListResult(cmds []schema.DrawCmd) uint64 {
	raw, err := schema.EncodeDrawList(cmds, nil)
	if err != nil {
		return 0
	}
	return uint64(len(raw))
}
