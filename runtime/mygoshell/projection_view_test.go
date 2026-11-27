// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

type projectionTestTarget struct {
	meta transport.TargetMeta
}

func (p *projectionTestTarget) Meta() transport.TargetMeta    { return p.meta }
func (p *projectionTestTarget) Ping(context.Context) error    { return nil }
func (p *projectionTestTarget) Close() error                  { return nil }

func TestProjectionInspectorTabsReceiptsDiffProcessesAndAddRemoteModal(t *testing.T) {
	reg := transport.NewTargetRegistry()
	if err := reg.Register(&projectionTestTarget{meta: transport.TargetMeta{
		ID:          "local-studio",
		DisplayName: "Local Studio",
		TargetKind:  schema.TargetDesktopMacOS,
		Mode:        transport.ModeLocalSidecar,
		Address:     "127.0.0.1:50051",
		Phase:       transport.PhaseReady,
	}}); err != nil {
		t.Fatal(err)
	}
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	if err := wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation:  1,
		ProfileName: "studio",
		Theme:       StudioDarkPalette(),
		FrameTree:   schema.UIElement{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Workspace Main"}}},
	}); err != nil {
		t.Fatal(err)
	}

	proj := NewProjectionController(ProjectionSnapshot{
		GraphNodes: []GraphNodeReceiptView{
			{
				NodeID:      "node-plan",
				Kind:        "planner",
				Phase:       "succeeded",
				DurationMS:  12,
				ReceiptID:   "rcpt-graph-001",
				GatewayNote: "no-live-llm gateway denial receipt preserved",
				Summary:     "Compiled daily org agenda graph",
			},
			{
				NodeID:     "node-verify",
				Kind:       "verifier",
				Phase:      "succeeded",
				DurationMS: 8,
				ReceiptID:  "rcpt-graph-002",
				Summary:    "Verified 249/249 SPDX headers",
			},
		},
		DiffFiles: []WorkspaceDiffFileView{
			{
				Path:      "runtime/pluginhost/artifact_protocol.go",
				Status:    "added",
				Additions: 180,
				Deletions: 0,
				HunkLines: []string{"+package pluginhost", "+const ArtifactScheme = \"marlin-artifact\""},
			},
		},
		Processes: []ManagedProcessView{
			{
				ProcessID: "proc-sidecar",
				Command:   "marlin-mygo-host --tls",
				State:     "running",
				ExitCode:  0,
				UptimeSec: 420,
				TailLines: []string{"listening on 127.0.0.1:50051"},
			},
		},
	})

	var addedTargetID string
	app := NewWorkbenchApp(reg, slots, wb)
	app.Projection = proj
	app.OnAddRemoteTarget = func(targetID, displayName, endpoint, bearerToken string) error {
		addedTargetID = targetID
		return reg.Register(&projectionTestTarget{meta: transport.TargetMeta{
			ID:          targetID,
			DisplayName: displayName,
			TargetKind:  schema.TargetRemoteHeadless,
			Mode:        transport.ModeRemoteGRPC,
			Address:     endpoint,
			Phase:       transport.PhaseReady,
		}})
	}

	tester := ui.NewTester(app.View, 1280, 820)

	// 1. Open Projection Inspector via top-bar Inspect button and switch across all 3 tabs.
	tester.Click("Inspect")
	tester.Frame()
	if !tester.HasText("rcpt-graph-001") {
		t.Fatalf("expected initial Graph node receipt rcpt-graph-001 to render, got %q", tester.Texts())
	}

	tester.Click("node-verify [verifier] · succeeded · 8ms")
	tester.Frame()
	if !tester.HasText("rcpt-graph-002") {
		t.Fatalf("expected selected node receipt rcpt-graph-002 to render, got %q", tester.Texts())
	}

	tester.Click("Workspace Diff (1)")
	tester.Frame()
	if proj.ActiveTab() != ProjectionTabDiff || !tester.HasText("Diff Preview: runtime/pluginhost/artifact_protocol.go") {
		t.Fatalf("expected Workspace Diff preview tab to render, got %q", tester.Texts())
	}

	tester.Click("Processes (1)")
	tester.Frame()
	if proj.ActiveTab() != ProjectionTabProcesses || !tester.HasText("listening on 127.0.0.1:50051") {
		t.Fatalf("expected Managed Processes tail to render, got %q", tester.Texts())
	}

	tester.Click("Close")
	tester.Frame()

	// 2. Open "+ Remote Target" modal and submit default bounded form.
	tester.Click("+ Remote Target")
	tester.Frame()
	if !tester.HasText("Connect Remote Marlin Host (TLS 1.3 gRPC)") {
		t.Fatalf("expected Add Remote Target bounded form modal, got %q", tester.Texts())
	}
	tester.Click("Save Settings")
	tester.Frame()
	if addedTargetID != "remote-gpu-1" {
		t.Fatalf("expected OnAddRemoteTarget to register remote-gpu-1, got %q", addedTargetID)
	}
	if len(reg.List()) != 2 {
		t.Fatalf("expected 2 targets in registry after remote add, got %d", len(reg.List()))
	}
}
