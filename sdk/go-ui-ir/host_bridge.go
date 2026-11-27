// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build wasip1

package gouir

import "unsafe"

//go:wasmimport marlin_host_v1 emit_action
func wasmHostEmitAction(actPtr, actLen, argsPtr, argsLen, idempPtr, idempLen uint32) int32

// EmitAction submits a manifest-declared scoped action from inside a Go WASM-IR
// plugin directly into the host's ScopedProxy (enforcing DeclaredActions,
// ActivationGeneration, and Revoke guarantees). Returns true iff admitted.
func EmitAction(action string, args []byte, idempotencyKey string) bool {
	actBytes := []byte(action)
	idempBytes := []byte(idempotencyKey)
	var actPtr, argsPtr, idempPtr uint32
	if len(actBytes) > 0 {
		actPtr = uint32(uintptr(unsafe.Pointer(&actBytes[0])))
	}
	if len(args) > 0 {
		argsPtr = uint32(uintptr(unsafe.Pointer(&args[0])))
	}
	if len(idempBytes) > 0 {
		idempPtr = uint32(uintptr(unsafe.Pointer(&idempBytes[0])))
	}
	return wasmHostEmitAction(
		actPtr, uint32(len(actBytes)),
		argsPtr, uint32(len(args)),
		idempPtr, uint32(len(idempBytes)),
	) == 0
}

// PackBytes encodes a guest linear-memory slice as (ptr<<32 | len) for
// //go:wasmexport return values. Caller must retain the slice until the next call.
func PackBytes(b []byte) uint64 {
	if len(b) == 0 {
		return 0
	}
	ptr := uint32(uintptr(unsafe.Pointer(&b[0])))
	return (uint64(ptr) << 32) | uint64(len(b))
}
