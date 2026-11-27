// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package wazeroir

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

type spyDispatcher struct {
	mu       sync.Mutex
	requests []pluginhost.ScopedActionRequest
}

func (s *spyDispatcher) SubmitScopedPanelAction(_ context.Context, req pluginhost.ScopedActionRequest) (pluginhost.ScopedActionReceipt, error) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	return pluginhost.ScopedActionReceipt{Admitted: true, ReceiptID: "rcpt-wasm-1"}, nil
}

func compileReactorWasm(t *testing.T) []byte {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go binary not in PATH")
	}
	wasmOut := filepath.Join(t.TempDir(), "reactor.wasm")
	build := exec.Command(goBin, "build", "-buildmode=c-shared", "-o", wasmOut, "../../examples/plugins/go-graph-inspector-reactor")
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("failed to compile wasip1 reactor plugin: %v\n%s", err, string(out))
	}
	wasmBytes, err := os.ReadFile(wasmOut)
	if err != nil {
		t.Fatal(err)
	}
	return wasmBytes
}

func TestResidentReactorBidirectionalEventsActionsAndDrawList(t *testing.T) {
	wasmBytes := compileReactorWasm(t)
	ctx := context.Background()
	eng, err := NewModuleEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close(ctx)

	disp := &spyDispatcher{}
	scope := pluginhost.ScopedScope{
		PluginID:             "tao3k.graph-inspector",
		ContributionID:       "inspector-panel",
		ActivationGeneration: 7,
		Runtime:              schema.RuntimeWasmUIIR,
		ReadScopes:           []string{"graph.focus"},
		DeclaredActions:      []string{"graph.node.inspect"},
	}
	proxy := pluginhost.NewScopedProxy(scope, disp)

	inst, err := eng.InstantiateResidentWithProxy(ctx, wasmBytes, proxy)
	if err != nil {
		t.Fatalf("InstantiateResidentWithProxy failed: %v", err)
	}
	defer inst.Close(ctx)

	// 1. Multi-frame warm render check.
	for wantSeq := 1; wantSeq <= 3; wantSeq++ {
		tree, err := inst.Render(ctx)
		if err != nil {
			t.Fatalf("frame %d Render failed: %v", wantSeq, err)
		}
		spans := tree.Children[0].Children[0].Spans
		if len(spans) < 2 || !strings.Contains(spans[1].Text, "#"+string(rune('0'+wantSeq))) {
			t.Fatalf("frame %d expected monotonic counter in spans, got %+v", wantSeq, spans)
		}
	}

	// 2. Host -> Guest OnEvent mutates guest state AND triggers declared emit_action.
	if err := inst.OnEvent(ctx, pluginhost.ScopedEvent{
		EventID:              "ev-100",
		PluginID:             scope.PluginID,
		ActivationGeneration: 7,
		Topic:                "graph.focus",
		Payload:              json.RawMessage(`{"nodeId":"verify_gate_receipt"}`),
	}); err != nil {
		t.Fatalf("OnEvent failed: %v", err)
	}
	tree, err := inst.Render(ctx)
	if err != nil {
		t.Fatalf("Render after OnEvent failed: %v", err)
	}
	nodeLine := tree.Children[0].Children[1].Spans[0].Text
	if !strings.Contains(nodeLine, "verify_gate_receipt") {
		t.Fatalf("expected updated focusedNode in rendered tree, got %q", nodeLine)
	}
	if len(disp.requests) != 1 || disp.requests[0].ActionName != "graph.node.inspect" || disp.requests[0].ActivationGeneration != 7 {
		t.Fatalf("expected 1 admitted scoped action with gen 7, got %+v", disp.requests)
	}

	// 3. Adversarial probe: guest tries undeclared action "shell.exec" -> MUST be blocked by ScopedProxy.
	if err := inst.OnEvent(ctx, pluginhost.ScopedEvent{
		EventID:              "ev-101",
		PluginID:             scope.PluginID,
		ActivationGeneration: 7,
		Topic:                "graph.focus",
		Payload:              json.RawMessage(`{"triggerProbe":true}`),
	}); err != nil {
		t.Fatalf("OnEvent probe failed: %v", err)
	}
	if len(disp.requests) != 1 {
		t.Fatalf("undeclared WASM action leaked to dispatcher: %+v", disp.requests)
	}

	// 4. Revoke proxy -> subsequent declared emit_action from WASM MUST be rejected locally.
	proxy.Revoke()
	if err := inst.OnEvent(ctx, pluginhost.ScopedEvent{
		EventID:              "ev-102",
		PluginID:             scope.PluginID,
		ActivationGeneration: 7,
		Topic:                "graph.focus",
		Payload:              json.RawMessage(`{"nodeId":"after_revoke"}`),
	}); err != nil {
		t.Fatalf("OnEvent after revoke failed: %v", err)
	}
	if len(disp.requests) != 1 {
		t.Fatalf("revoked WASM proxy still dispatched action: %+v", disp.requests)
	}

	// 5. Zero-JSON binary DrawList high-frequency GPU fast path.
	var cmdBuf [64]schema.DrawCmd
	cmds, err := inst.RenderDrawList(ctx, cmdBuf[:0])
	if err != nil {
		t.Fatalf("RenderDrawList failed: %v", err)
	}
	if len(cmds) != 64 || cmds[0].Op != schema.DrawOpFillRect || cmds[0].RGBA != 0x51afefFF {
		t.Fatalf("unexpected binary DrawList output: len=%d first=%+v", len(cmds), cmds[0])
	}
}
