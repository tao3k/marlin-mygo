// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package uistream

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChatAdapterBridgesRustAndVercelStreams(t *testing.T) {
	ad := NewChatAdapter()
	ad.AppendUser("u1", "Render a workflow diagram")
	if ad.Status() != ChatStatusSubmitted {
		t.Fatalf("expected submitted status, got %s", ad.Status())
	}

	ad.UpsertRustToolPart(
		1,
		"call-1",
		"workspace_search",
		ToolStateOutputAvailable,
		json.RawMessage(`{"q":"helm"}`),
		json.RawMessage(`{"hits":3}`),
		"",
	)
	ad.ApplyRustTextDelta(1, "<think>Plan graph nodes.</think>\nHere is the graph:\n```mermaid\ngraph TD\n  A-->B\n```\nDone.", false)

	if ad.Status() != ChatStatusReady {
		t.Fatalf("expected ready status, got %s", ad.Status())
	}
	msgs := ad.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	asst := msgs[1]
	var hasReasoning, hasTool, hasText, hasData bool
	for _, p := range asst.Parts {
		switch p.Kind {
		case PartReasoning:
			hasReasoning = strings.Contains(p.Text, "Plan graph nodes.")
		case PartToolInvocation:
			hasTool = p.ToolName == "workspace_search"
		case PartText:
			hasText = strings.Contains(p.Text, "Here is the graph:")
		case PartData:
			hasData = p.DataType == "data-mermaid"
		}
	}
	if !hasReasoning || !hasTool || !hasText || !hasData {
		t.Fatalf("expected reasoning+tool+text+data parts, got %+v", asst.Parts)
	}

	raw, err := ad.ToVercelJSON()
	if err != nil {
		t.Fatalf("ToVercelJSON failed: %v", err)
	}
	var decoded []UIMessage
	if err := json.Unmarshal(raw, &decoded); err != nil || len(decoded) != 2 {
		t.Fatalf("roundtrip Vercel JSON failed: err=%v len=%d", err, len(decoded))
	}
}

func TestChatAdapterVercelSSEFlow(t *testing.T) {
	ad := NewChatAdapter()
	lines := []string{
		`data: {"type":"start","id":"m-42"}`,
		`data: {"type":"reasoning-delta","id":"r1","delta":"Checking..."}`,
		`data: {"type":"text-delta","id":"t1","delta":"Answer!"}`,
		`data: {"type":"data-workflow","id":"d1","data":{"nodes":3}}`,
		`data: {"type":"finish"}`,
	}
	for _, ln := range lines {
		if _, err := ad.ApplyVercelSSELine(ln); err != nil {
			t.Fatalf("ApplyVercelSSELine(%q): %v", ln, err)
		}
	}
	if ad.Status() != ChatStatusReady {
		t.Fatalf("expected ready after finish, got %s", ad.Status())
	}
	msgs := ad.Messages()
	if len(msgs) != 1 || len(msgs[0].Parts) != 3 {
		t.Fatalf("unexpected SSE messages: %+v", msgs)
	}
}
