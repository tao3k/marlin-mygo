// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !wasip1

package gouir

import "github.com/tao3k/marlin-mygo/pkg/schema"

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
