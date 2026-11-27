// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package richtext

import "testing"

func TestDecodeOrgizeSectionIndexAndOrgElementsAutoJSON(t *testing.T) {
	// 1. Exact tao3k/orgize Org.sectionIndexJson() schemaVersion 1 payload.
	secIdxJSON := []byte(`{
		"schemaVersion": 1,
		"records": [
			{
				"level": 1,
				"title": "Align Orgize WASM & Rust",
				"titleText": "Align Orgize WASM & Rust",
				"todo": "NEXT",
				"todoState": "todo",
				"priority": {"raw": "[#A]", "effective": "A", "isDefault": false},
				"tags": ["orgize", "wasm"],
				"properties": [{"key": "ID", "value": "orgize-101"}],
				"planning": {
					"scheduled": {"kind": "active", "raw": "<2026-11-26 Thu 09:00>"},
					"deadline": {"kind": "active", "raw": "<2026-11-28 Sat>"}
				},
				"body": [{"text": "Rendered natively at 120Hz via orgize DTO."}],
				"links": [
					{"path": "https://github.com/tao3k/orgize", "description": "tao3k/orgize"},
					{"path": "javascript:alert(1)", "description": "Blocked Link"}
				]
			}
		]
	}`)

	blocks, err := DecodeDocumentAutoJSON("orgize-sec", secIdxJSON)
	if err != nil {
		t.Fatalf("DecodeDocumentAutoJSON(sectionIndex) failed: %v", err)
	}
	if len(blocks) != 5 {
		t.Fatalf("expected 5 projected blocks (heading, planning, drawer, body, links), got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].TodoWord != "NEXT" || blocks[0].Priority != "A" || len(blocks[0].Tags) != 2 {
		t.Fatalf("unexpected heading projection: %+v", blocks[0])
	}
	if blocks[1].Kind != BlockOrgPlanning || len(blocks[1].Planning) != 2 {
		t.Fatalf("unexpected planning projection: %+v", blocks[1])
	}
	if blocks[2].Kind != BlockDrawer || blocks[2].DrawerRows[0].Value != "orgize-101" {
		t.Fatalf("unexpected properties drawer projection: %+v", blocks[2])
	}
	// Verify safe link preserved and javascript: link stripped fail-closed!
	linkBlock := blocks[4]
	if linkBlock.Spans[0].Kind != SpanLink || linkBlock.Spans[0].URL != "https://github.com/tao3k/orgize" {
		t.Fatalf("expected safe orgize link preserved, got %+v", linkBlock.Spans[0])
	}
	if linkBlock.Spans[1].Kind != SpanPlain || linkBlock.Spans[1].URL != "" {
		t.Fatalf("XSS VIOLATION: javascript: link admitted from orgize DTO: %+v", linkBlock.Spans[1])
	}

	// 2. Exact tao3k/orgize Org.orgElementsJson() nested sections + sourceBlock payload.
	elemsJSON := []byte(`{
		"schemaVersion": 1,
		"elements": [],
		"sections": [
			{
				"level": 1,
				"title": "WASM Source Blocks",
				"todo": "DONE",
				"tags": ["scheme"],
				"properties": [{"key": "OWNER", "value": "tao3k"}],
				"elements": [
					{"kind": "sourceBlock", "language": "scheme", "body": "(orgize-ready? #t)"},
					{"kind": "quoteBlock", "text": "Zero Go-side Org parser duplication."}
				],
				"children": [
					{"level": 2, "title": "Nested Child", "tags": [], "properties": [], "elements": [], "children": []}
				]
			}
		]
	}`)
	elemBlocks, err := DecodeDocumentAutoJSON("orgize-el", elemsJSON)
	if err != nil {
		t.Fatalf("DecodeDocumentAutoJSON(orgElements) failed: %v", err)
	}
	if len(elemBlocks) != 5 {
		t.Fatalf("expected 5 projected blocks, got %d: %+v", len(elemBlocks), elemBlocks)
	}
	if elemBlocks[2].Kind != BlockCode || elemBlocks[2].Lang != "SCHEME" || elemBlocks[2].CopyKey != "copy-orgize-el-1" {
		t.Fatalf("unexpected orgize sourceBlock projection: %+v", elemBlocks[2])
	}
	if elemBlocks[4].Kind != BlockHeading || elemBlocks[4].Level != 2 || elemBlocks[4].RawText != "Nested Child" {
		t.Fatalf("unexpected nested orgize section projection: %+v", elemBlocks[4])
	}
}
