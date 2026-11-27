// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell_test

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

func TestProjectionDiffSplitModeLogActionsAndTranscriptTurnControls(t *testing.T) {
	// 1. Test ProjectionController interactive Split/Unified Diff, @Mention, Attach Diff, Copy Patch, and Process Attach Logs.
	proj := mygoshell.NewProjectionController(mygoshell.ProjectionSnapshot{
		DiffFiles: []mygoshell.WorkspaceDiffFileView{
			{
				Path:      "runtime/mygoshell/composer_view.go",
				Status:    "modified",
				Additions: 12,
				Deletions: 3,
				HunkLines: []string{
					"@@ -10,3 +10,5 @@",
					"-oldLine()",
					"+newLine()",
				},
			},
			{
				Path:      "engine/richtext/ir.go",
				Status:    "added",
				Additions: 40,
				Deletions: 0,
				HunkLines: []string{"+type Block struct{}"},
			},
		},
		Processes: []mygoshell.ManagedProcessView{
			{
				ProcessID: "proc-host-1",
				Command:   "marlin-app-server",
				State:     "failed",
				ExitCode:  1,
				UptimeSec: 12,
				TailLines: []string{
					"INFO boot ready",
					"WARN slow rpc",
					"ERROR socket timeout",
				},
			},
		},
	})
	proj.SetActiveTab(mygoshell.ProjectionTabDiff)

	var mentionedPath, copiedText string
	var attachedDiffPath, attachedProcID string
	proj.OnMentionFile = func(p string) { mentionedPath = p }
	proj.OnAttachDiff = func(f mygoshell.WorkspaceDiffFileView) { attachedDiffPath = f.Path }
	proj.OnAttachProcessLogs = func(pr mygoshell.ManagedProcessView) { attachedProcID = pr.ProcessID }
	proj.OnCopyText = func(txt string) { copiedText = txt }

	theme := ui.DarkTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		proj.Render(c, theme, nil)
	}, 960, 760)
	tt.Frame()

	// Switch to Split diff mode and trigger @ Mention File, Attach Diff, and Copy Patch.
	if err := tt.Click("Split"); err != nil {
		t.Fatalf("click Split diff mode failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("BEFORE (-)") || !tt.HasText("AFTER (+)") {
		t.Fatalf("expected Split diff columns, got %q", strings.Join(tt.Texts(), " | "))
	}
	for _, btn := range []string{"@ Mention File", "Attach Diff", "Copy Patch"} {
		if err := tt.Click(btn); err != nil {
			t.Fatalf("click %q failed: %v", btn, err)
		}
	}
	if mentionedPath != "runtime/mygoshell/composer_view.go" || attachedDiffPath != "runtime/mygoshell/composer_view.go" || !strings.Contains(copiedText, "+newLine()") {
		t.Fatalf("unexpected diff actions: mention=%q attach=%q copy=%q", mentionedPath, attachedDiffPath, copiedText)
	}

	// Switch to Processes tab, filter by ERROR, and click Attach Logs.
	proj.SetActiveTab(mygoshell.ProjectionTabProcesses)
	proj.SetProcessFilter("ERROR")
	tt.Frame()
	if !tt.HasText("ERROR socket timeout") || tt.HasText("INFO boot ready") {
		t.Fatalf("expected process log filter to show only ERROR line, got %q", strings.Join(tt.Texts(), " | "))
	}
	if err := tt.Click("Attach Logs"); err != nil {
		t.Fatalf("click Attach Logs failed: %v", err)
	}
	if attachedProcID != "proc-host-1" {
		t.Fatalf("expected Attach Logs for proc-host-1, got %q", attachedProcID)
	}

	// 2. Test TranscriptController Turn Actions (Copy, Retry, Fork) and Fold/Unfold All Thinking toggle!
	tc := mygoshell.NewTranscriptController()
	var copiedTurn string
	var retriedOrd, forkedOrd uint64
	tc.OnCopyCode = func(txt string) { copiedTurn = txt }
	tc.OnRetryTurn = func(ord uint64) { retriedOrd = ord }
	tc.OnForkTurn = func(ord uint64) { forkedOrd = ord }

	tc.AppendThinking("Step 1", "Analyzing workspace diff", false)
	tc.BeginAssistantTurn(1)
	tc.ApplyCheckpoint(1, "ev-1", "All checks passed cleanly.")
	tc.CompleteTurn(1, "DONE")

	tt2 := ui.NewTester(func(c *ui.Context) {
		tc.Render(c, theme, nil, nil)
	}, 960, 700)
	tt2.Frame()

	// Fold all thinking blocks via header toggle pill!
	if err := tt2.Click("Fold Thinking"); err != nil {
		t.Fatalf("click Fold Thinking failed: %v", err)
	}
	tt2.Frame()
	if !tt2.HasText("Unfold Thinking") {
		t.Fatalf("expected header pill to switch to 'Unfold Thinking', got %q", strings.Join(tt2.Texts(), " | "))
	}

	// Trigger Copy, Retry, and Fork on Assistant Turn #1!
	for _, btn := range []string{"Copy", "Retry", "Fork"} {
		if err := tt2.Click(btn); err != nil {
			t.Fatalf("click turn action %q failed: %v", btn, err)
		}
	}
	if copiedTurn != "All checks passed cleanly." || retriedOrd != 1 || forkedOrd != 1 {
		t.Fatalf("unexpected turn action results: copy=%q retry=%d fork=%d", copiedTurn, retriedOrd, forkedOrd)
	}
}
