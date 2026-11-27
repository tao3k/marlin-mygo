// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !wasip1

package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	gouir "github.com/tao3k/marlin-mygo/sdk/go-ui-ir"
)

func TestFocusClockStateAppliesOrgizeDTOsAndCompletesAlarm(t *testing.T) {
	st := NewFocusClockState()
	st.RemainingSec = 10

	// 1. Ingest canonical orgize OrgizeAgendaViewResponseDto.
	agendaEv := gouir.Event{
		EventID: "ev-ag-1",
		Topic:   "org.agenda",
		Payload: json.RawMessage(`{
			"schemaVersion": 1,
			"cards": [
				{"displayDate":"2026-11-25","time":"10:00","title":"Done standup","todoState":"done","urgency":{"total":90}},
				{"displayDate":"2026-11-25","time":"15:00","title":"Ship focus clock plugin","todoState":"todo","urgency":{"total":95}}
			]
		}`),
	}
	completed, err := st.ApplyEvent(agendaEv)
	if err != nil || completed {
		t.Fatalf("unexpected agenda event result: completed=%t err=%v", completed, err)
	}
	if st.NextAgendaTitle != "Ship focus clock plugin" || st.NextAgendaTime != "15:00" || st.NextUrgency != 95 {
		t.Fatalf("unexpected next agenda: %+v", st)
	}

	// 2. Ingest canonical orgize OrgizeClockRollupResponseDto + tick completion + quick log.
	clockEv := gouir.Event{
		EventID: "ev-clk-1",
		Topic:   "org.clock",
		Payload: json.RawMessage(`{
			"schemaVersion": 1,
			"records": [{
				"title": "Deep Work RFC",
				"localClock": {"runningEntries": 1, "totalSeconds": 1800},
				"effort": {"status": "overEffort"}
			}],
			"tickSeconds": 10,
			"quickNote": "Finished security invariant check",
			"hourBucket": 7
		}`),
	}
	completed, err = st.ApplyEvent(clockEv)
	if err != nil {
		t.Fatalf("ApplyEvent(org.clock) failed: %v", err)
	}
	if !completed || st.CompletedBlocks != 1 || st.RemainingSec != 0 {
		t.Fatalf("expected alarm completion: completed=%t blocks=%d rem=%d", completed, st.CompletedBlocks, st.RemainingSec)
	}
	if st.ActiveTitle != "Deep Work RFC" || st.EffortStatus != "overEffort" {
		t.Fatalf("expected active clock metadata from orgize DTO, got %+v", st)
	}
	if len(st.QuickLogs) != 1 || st.QuickLogs[0] != "Finished security invariant check" {
		t.Fatalf("expected quick log recorded, got %v", st.QuickLogs)
	}

	// 3. Verify bounded quick-log FIFO ring cap.
	for i := 0; i < 6; i++ {
		st.AppendQuickLog("note")
	}
	if len(st.QuickLogs) != maxQuickLogs {
		t.Fatalf("quick log ring exceeded cap: got %d, want %d", len(st.QuickLogs), maxQuickLogs)
	}

	// 4. Verify bounded UIElement tree & interactive GPU ActionButtons.
	tree := st.BuildUITree()
	if err := tree.ValidateBounds(schema.MaxUITreeNodes, schema.MaxUITreeDepth); err != nil {
		t.Fatalf("BuildUITree bounds failed: %v", err)
	}
	if _, err := gouir.Encode(tree); err != nil {
		t.Fatalf("gouir.Encode failed: %v", err)
	}
	var buttons []string
	var walk func(schema.UIElement)
	walk = func(el schema.UIElement) {
		if el.Kind == schema.ElemButton {
			buttons = append(buttons, el.Action)
		}
		for _, ch := range el.Children {
			walk(ch)
		}
	}
	walk(tree)
	if len(buttons) != 3 || buttons[0] != "org.clock.in" || buttons[1] != "org.clock.out" || buttons[2] != "org.capture.plan" {
		t.Fatalf("unexpected GPU action buttons: %v", buttons)
	}

	// 5. Verify zero-JSON binary DrawList round-trip.
	var buf [64]schema.DrawCmd
	cmds := st.BuildDrawList(&buf)
	if len(cmds) != 2+dailyBuckets {
		t.Fatalf("unexpected draw cmd count: got %d, want %d", len(cmds), 2+dailyBuckets)
	}
	raw, err := schema.EncodeDrawList(cmds, nil)
	if err != nil {
		t.Fatalf("EncodeDrawList failed: %v", err)
	}
	decoded, err := schema.DecodeDrawList(raw, nil)
	if err != nil || len(decoded) != len(cmds) {
		t.Fatalf("DecodeDrawList failed: len=%d err=%v", len(decoded), err)
	}
}

func TestFocusClockPerfBudgets(t *testing.T) {
	st := NewFocusClockState()
	var buf [64]schema.DrawCmd

	// Enforce strict zero-allocation GPU DrawList generation so 120Hz frames stay silky smooth.
	gouir.AssertPerfCase(t, gouir.PerfCase{
		Name:        "BuildDrawList_zero_alloc",
		MaxP50:      25 * time.Microsecond,
		MaxAllocs:   0,
		Iterations:  200,
		Remediation: "Populate the caller-provided *[64]schema.DrawCmd array directly with zero heap allocations.",
		Run: func() {
			_ = st.BuildDrawList(&buf)
		},
	})

	// Enforce bounded UIElement tree construction latency + compactness.
	gouir.AssertPerfCase(t, gouir.PerfCase{
		Name:        "BuildUITree_and_encode",
		MaxP50:      250 * time.Microsecond,
		MaxAllocs:   -1,
		Iterations:  100,
		Remediation: "Pre-format static labels and avoid O(N^2) loops inside BuildUITree.",
		Run: func() {
			tree := st.BuildUITree()
			_, _ = gouir.Encode(tree)
		},
	})
	gouir.AssertUITreeBudget(t, st.BuildUITree(), 32, 6)
}
