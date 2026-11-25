<!--
SPDX-FileCopyrightText: 2026 tao3k team and Contributors

SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later
-->

# `tao3k/orgize` WASI Reactor ABI Specification for `marlin-mygo` (`wazero`)

## Overview

`marlin-mygo` intentionally does **not** duplicate Org-mode or Markdown parsing in Go.
Instead, it defines a razor-thin fail-closed Document IR boundary (`engine/richtext`) and
renders admitted `[]richtext.Block` natively on MyGo's 120Hz GPU pipeline (`runtime/mygoshell/richtext_view.go`).

- **Browser / WebView plugins** can consume `tao3k/orgize/wasm/dist/orgize_bg.wasm` (`wasm-bindgen`) directly and pass the JSON string from `Org.sectionIndexJson()` or `Org.orgElementsJson()` into `richtext.DecodeDocumentAutoJSON`.
- **Desktop / Mobile Go hosts (`CGO_ENABLED=0` via `wazero`)** execute a JS-free **WASI Reactor** (`wasm32-wasip1`) binary via `runtime/wazeroir.WasmDocumentParser`.

## Required WASI Exports

A `wasm32-wasip1` Reactor build of `orgize-wasm` exports linear memory (`memory`) plus 3 C-ABI functions:

| Export | Signature | Semantics |
| --- | --- | --- |
| `alloc` | `(size: u32) -> ptr: u32` | Allocates `size` bytes in guest linear memory for host writes. |
| `dealloc` | `(ptr: u32, size: u32)` (optional) | Releases host-allocated input buffers after `parse_document`. |
| `parse_document` | `(fmt_ptr: u32, fmt_len: u32, src_ptr: u32, src_len: u32) -> u64` | Parses UTF-8 source (`fmt` = `"org"` or `"markdown"`), stores JSON output (`OrgizeSectionIndexResponseDto`, `OrgizeOrgElementsDto`, or `richtext.DocumentEnvelope`), and returns `(out_ptr << 32) \| out_len`. |

## Minimal Rust Reference Snippet (`orgize-wasm/src/wasi_abi.rs`, Rust 2024 Edition)

Add a `#[cfg(target_os = "wasi")] mod wasi_abi;` in `tao3k/orgize/wasm/src/lib.rs`:

```rust
use std::cell::RefCell;
use std::slice;

thread_local! {
    static LAST_OUTPUT: RefCell<Vec<u8>> = const { RefCell::new(Vec::new()) };
}

#[unsafe(no_mangle)]
pub extern "C" fn alloc(size: u32) -> u32 {
    let mut buf = Vec::<u8>::with_capacity(size as usize);
    let ptr = buf.as_mut_ptr();
    std::mem::forget(buf);
    ptr as u32
}

#[unsafe(no_mangle)]
pub extern "C" fn dealloc(ptr: u32, size: u32) {
    if ptr == 0 || size == 0 {
        return;
    }
    unsafe {
        let _ = Vec::from_raw_parts(ptr as *mut u8, 0, size as usize);
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn parse_document(
    _fmt_ptr: u32,
    _fmt_len: u32,
    src_ptr: u32,
    src_len: u32,
) -> u64 {
    let src_bytes = if src_ptr == 0 || src_len == 0 {
        &[]
    } else {
        unsafe { slice::from_raw_parts(src_ptr as *const u8, src_len as usize) }
    };
    let Ok(input) = std::str::from_utf8(src_bytes) else {
        return 0;
    };
    let org = crate::Org::new(input);
    let json = org.section_index_json(None);
    LAST_OUTPUT.with(|cell| {
        let mut out = cell.borrow_mut();
        out.clear();
        out.extend_from_slice(json.as_bytes());
        ((out.as_ptr() as u64) << 32) | (out.len() as u64)
    })
}
```

## Building & Auto-Discovery in Marlin Studio

```bash
cd /path/to/orgize/wasm
rustup target add wasm32-wasip1
cargo build --target wasm32-wasip1 --release
```

`wazeroir.DiscoverOrgWasmBinary` automatically hooks the compiled WASI binary in priority order:

1. Explicit path argument
2. `MARLIN_ORG_WASM` environment variable
3. `~/.config/marlin/orgize_wasm.wasm`
4. Sibling workspace build (`../orgize/wasm/target/wasm32-wasip1/release/orgize_wasm.wasm`)

## Fail-Closed Security Invariants

`wazeroir.WasmDocumentParser` enforces:
- **Context timeout cancellation** (`WithCloseOnContextDone(true)`, default 500ms) killing runaway or infinite-loop WASM parsers without stalling 120Hz UI frames.
- **2 MiB payload ceiling** and strict block/table/span count bounds in `richtext.DecodeDocumentAutoJSONWithLimits`.
- **Fail-closed URL sanitization**: only `https://`, `http://`, `mailto:`, and `file://` links are admitted; `javascript:` / `data:` URLs are downgraded to plain text.
