// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package richtext

import (
	"errors"
	"strings"
	"testing"
)

func TestDecodeIRJSONAdmitsOrgRZ1AndMarkdownAndBlocksUnsafeLinks(t *testing.T) {
	// Representative JSON IR payload emitted by org-rz1 WASM or Rust backend.
	raw := []byte(`{
		"format": "org",
		"blocks": [
			{
				"kind": "heading",
				"level": 1,
				"is_org": true,
				"todo_word": "todo",
				"priority": "a",
				"tags": ["marlin", "org"],
				"raw_text": "Ship Org-RZ1 WASM Renderer"
			},
			{
				"kind": "org-planning",
				"is_org": true,
				"planning": [
					{"keyword": "SCHEDULED", "timestamp": "<2026-11-26 Thu 10:00>"},
					{"keyword": "DEADLINE", "timestamp": "<2026-11-27 Fri>"}
				]
			},
			{
				"kind": "drawer",
				"is_org": true,
				"drawer_name": "PROPERTIES",
				"drawer_rows": [{"key": "ID", "value": "org-rz1-42"}]
			},
			{
				"kind": "paragraph",
				"spans": [
					{"kind": "link", "text": "Org Mode", "url": "https://orgmode.org"},
					{"kind": "link", "text": "Evil XSS", "url": "javascript:alert(1)"}
				]
			},
			{
				"kind": "code",
				"is_org": true,
				"lang": "scheme",
				"code": "(display \"ok\")"
			}
		]
	}`)

	blocks, err := DecodeIRJSON("turn-1", raw)
	if err != nil {
		t.Fatalf("DecodeIRJSON failed: %v", err)
	}
	if len(blocks) != 5 {
		t.Fatalf("expected 5 admitted blocks, got %d", len(blocks))
	}
	if blocks[0].TodoWord != "TODO" || blocks[0].Priority != "A" || len(blocks[0].Tags) != 2 {
		t.Fatalf("expected normalized Org heading, got %+v", blocks[0])
	}
	// Verify safe link preserved & javascript: link stripped to SpanPlain fail-closed!
	para := blocks[3]
	if para.Spans[0].Kind != SpanLink || para.Spans[0].URL != "https://orgmode.org" {
		t.Fatalf("expected safe https link preserved, got %+v", para.Spans[0])
	}
	if para.Spans[1].Kind != SpanPlain || para.Spans[1].URL != "" {
		t.Fatalf("XSS VIOLATION: javascript: link not downgraded to plain: %+v", para.Spans[1])
	}
	if blocks[4].Lang != "SCHEME" || blocks[4].CopyKey != "copy-turn-1-1" {
		t.Fatalf("expected normalized code lang & CopyKey, got %+v", blocks[4])
	}
}

func TestDecodeIRJSONFailClosedOnUnknownBlockAndExcessivePayload(t *testing.T) {
	if _, err := DecodeIRJSON("doc", []byte(`{"blocks":[{"kind":"script_inject"}]}`)); !errors.Is(err, ErrIRUnknownBlock) {
		t.Fatalf("expected ErrIRUnknownBlock, got %v", err)
	}
	lim := AdmissionLimits{MaxPayloadBytes: 64}
	if _, err := DecodeIRJSONWithLimits("doc", []byte(strings.Repeat(" ", 128)), lim); !errors.Is(err, ErrIRTooLarge) {
		t.Fatalf("expected ErrIRTooLarge, got %v", err)
	}
}
