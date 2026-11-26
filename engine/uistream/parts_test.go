// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package uistream

import "testing"

func TestVercelUIStreamAccumulatorAndParts(t *testing.T) {
	lines := []string{
		`data: {"type":"reasoning-start"}`,
		`data: {"type":"reasoning-delta","delta":"Checking gRPC "}`,
		`data: {"type":"reasoning-delta","delta":"route."}`,
		`data: {"type":"reasoning-end"}`,
		`data: {"type":"tool-input-available","toolCallId":"call-1","toolName":"workspace_search","input":{"query":"helm"}}`,
		`data: {"type":"tool-output-available","toolCallId":"call-1","toolName":"workspace_search","output":{"hits":4}}`,
		`data: {"type":"text-delta","delta":"## Result\nFound **4** hits."}`,
		`data: {"type":"data-workflow-graph","data":{"nodes":3}}`,
		`data: [DONE]`,
	}
	acc := NewAccumulator("msg-1", "assistant")
	for _, line := range lines {
		ch, done, err := ParseVercelStreamLine(line)
		if err != nil {
			t.Fatalf("ParseVercelStreamLine(%q): %v", line, err)
		}
		acc.Apply(ch)
		if done {
			break
		}
	}
	msg := acc.Message()
	if !msg.Finished || len(msg.Parts) != 4 {
		t.Fatalf("expected 4 finished parts, got finished=%v len=%d", msg.Finished, len(msg.Parts))
	}
	if msg.Parts[0].Kind != PartReasoning || msg.Parts[0].Text != "Checking gRPC route." || msg.Parts[0].Streaming {
		t.Fatalf("unexpected reasoning part: %+v", msg.Parts[0])
	}
	if msg.Parts[1].Kind != PartToolInvocation || msg.Parts[1].ToolState != ToolStateOutputAvailable {
		t.Fatalf("unexpected tool part: %+v", msg.Parts[1])
	}
	if msg.Parts[2].Kind != PartText || msg.Parts[2].Text != "## Result\nFound **4** hits." {
		t.Fatalf("unexpected text part: %+v", msg.Parts[2])
	}
	if msg.Parts[3].Kind != PartData || msg.Parts[3].DataType != "data-workflow-graph" {
		t.Fatalf("unexpected data part: %+v", msg.Parts[3])
	}
}

func TestSplitThinkTags(t *testing.T) {
	th, ans, open := SplitThinkTags("<think>Step 1: inspect\nStep 2: verify</think>\n\n# Answer\nReady.")
	if open || th != "Step 1: inspect\nStep 2: verify" || ans != "# Answer\nReady." {
		t.Fatalf("unexpected closed think split: open=%v th=%q ans=%q", open, th, ans)
	}
	th2, ans2, open2 := SplitThinkTags("<think>Streaming chain of thought...")
	if !open2 || th2 != "Streaming chain of thought..." || ans2 != "" {
		t.Fatalf("unexpected streaming think split: open=%v th=%q ans=%q", open2, th2, ans2)
	}
	th3, ans3, open3 := SplitThinkTags("Plain markdown answer")
	if open3 || th3 != "" || ans3 != "Plain markdown answer" {
		t.Fatalf("unexpected plain split: open=%v th=%q ans=%q", open3, th3, ans3)
	}
}
