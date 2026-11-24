// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package schema

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

const (
	// DrawListMagic identifies the zero-JSON little-endian binary DisplayList v1
	// transferred directly out of wazero guest linear memory into MyGo's GPU pipeline.
	DrawListMagic        = "MDLB"
	DrawListVersion      = 1
	DrawListHeaderBytes  = 8
	DrawCmdByteSize      = 32
	MaxDrawListCmds      = 4096
	MaxDrawListByteBytes = DrawListHeaderBytes + MaxDrawListCmds*DrawCmdByteSize
)

var ErrInvalidDrawList = errors.New("schema: invalid or out-of-bounds binary drawlist")

// DrawOp enumerates compact 2D GPU primitives emitted by high-frequency WASM plugins.
type DrawOp uint8

const (
	DrawOpFillRect   DrawOp = 1
	DrawOpStrokeRect DrawOp = 2
	DrawOpLine       DrawOp = 3
	DrawOpBadge      DrawOp = 4
)

// DrawCmd is a 32-byte pointer-free binary GPU command (zero heap allocations).
type DrawCmd struct {
	Op     DrawOp
	Flags  uint8
	Radius uint16
	X      float32
	Y      float32
	W      float32
	H      float32
	RGBA   uint32
	Extra0 uint32
	Extra1 uint32
}

// EncodeDrawList serializes cmds into dst (reusing its backing array) in deterministic
// little-endian binary form with zero JSON overhead.
func EncodeDrawList(cmds []DrawCmd, dst []byte) ([]byte, error) {
	if len(cmds) > MaxDrawListCmds {
		return nil, fmt.Errorf("%w: %d commands exceed limit %d", ErrInvalidDrawList, len(cmds), MaxDrawListCmds)
	}
	total := DrawListHeaderBytes + len(cmds)*DrawCmdByteSize
	if cap(dst) < total {
		dst = make([]byte, total)
	} else {
		dst = dst[:total]
	}
	copy(dst[0:4], DrawListMagic)
	binary.LittleEndian.PutUint16(dst[4:6], DrawListVersion)
	binary.LittleEndian.PutUint16(dst[6:8], uint16(len(cmds)))

	off := DrawListHeaderBytes
	for i := range cmds {
		c := cmds[i]
		if !validDrawCmd(c) {
			return nil, fmt.Errorf("%w: invalid command at index %d", ErrInvalidDrawList, i)
		}
		dst[off] = byte(c.Op)
		dst[off+1] = c.Flags
		binary.LittleEndian.PutUint16(dst[off+2:off+4], c.Radius)
		binary.LittleEndian.PutUint32(dst[off+4:off+8], math.Float32bits(c.X))
		binary.LittleEndian.PutUint32(dst[off+8:off+12], math.Float32bits(c.Y))
		binary.LittleEndian.PutUint32(dst[off+12:off+16], math.Float32bits(c.W))
		binary.LittleEndian.PutUint32(dst[off+16:off+20], math.Float32bits(c.H))
		binary.LittleEndian.PutUint32(dst[off+20:off+24], c.RGBA)
		binary.LittleEndian.PutUint32(dst[off+24:off+28], c.Extra0)
		binary.LittleEndian.PutUint32(dst[off+28:off+32], c.Extra1)
		off += DrawCmdByteSize
	}
	return dst, nil
}

// DecodeDrawList parses and validates raw binary commands directly into dst[:0]
// with zero heap allocations when dst has sufficient capacity.
func DecodeDrawList(raw []byte, dst []DrawCmd) ([]DrawCmd, error) {
	if len(raw) < DrawListHeaderBytes || len(raw) > MaxDrawListByteBytes {
		return nil, fmt.Errorf("%w: length %d out of bounds", ErrInvalidDrawList, len(raw))
	}
	if string(raw[0:4]) != DrawListMagic {
		return nil, fmt.Errorf("%w: bad magic", ErrInvalidDrawList)
	}
	ver := binary.LittleEndian.Uint16(raw[4:6])
	if ver != DrawListVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrInvalidDrawList, ver)
	}
	count := int(binary.LittleEndian.Uint16(raw[6:8]))
	if count > MaxDrawListCmds {
		return nil, fmt.Errorf("%w: command count %d exceeds %d", ErrInvalidDrawList, count, MaxDrawListCmds)
	}
	wantLen := DrawListHeaderBytes + count*DrawCmdByteSize
	if len(raw) != wantLen {
		return nil, fmt.Errorf("%w: length %d != expected %d", ErrInvalidDrawList, len(raw), wantLen)
	}

	out := dst[:0]
	if cap(out) < count {
		out = make([]DrawCmd, 0, count)
	}
	off := DrawListHeaderBytes
	for i := 0; i < count; i++ {
		c := DrawCmd{
			Op:     DrawOp(raw[off]),
			Flags:  raw[off+1],
			Radius: binary.LittleEndian.Uint16(raw[off+2 : off+4]),
			X:      math.Float32frombits(binary.LittleEndian.Uint32(raw[off+4 : off+8])),
			Y:      math.Float32frombits(binary.LittleEndian.Uint32(raw[off+8 : off+12])),
			W:      math.Float32frombits(binary.LittleEndian.Uint32(raw[off+12 : off+16])),
			H:      math.Float32frombits(binary.LittleEndian.Uint32(raw[off+16 : off+20])),
			RGBA:   binary.LittleEndian.Uint32(raw[off+20 : off+24]),
			Extra0: binary.LittleEndian.Uint32(raw[off+24 : off+28]),
			Extra1: binary.LittleEndian.Uint32(raw[off+28 : off+32]),
		}
		if !validDrawCmd(c) {
			return nil, fmt.Errorf("%w: invalid command at index %d", ErrInvalidDrawList, i)
		}
		out = append(out, c)
		off += DrawCmdByteSize
	}
	return out, nil
}

func validDrawCmd(c DrawCmd) bool {
	if c.Op < DrawOpFillRect || c.Op > DrawOpBadge {
		return false
	}
	for _, f := range [4]float32{c.X, c.Y, c.W, c.H} {
		f64 := float64(f)
		if math.IsNaN(f64) || math.IsInf(f64, 0) {
			return false
		}
	}
	return true
}
