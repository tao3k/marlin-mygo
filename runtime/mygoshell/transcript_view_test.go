// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

func TestTranscriptControllerFenwickVirtualizationAndFolding(t *testing.T) {
	tc := NewTranscriptController()

	// Populate 2,000 rows to verify O(log N) Fenwick virtualization bounds.
	for i := 0; i < 2000; i++ {
		tc.AppendUser(fmt.Sprintf("historical prompt #%d", i))
	}
	thinkRow := tc.AppendThinking("Chain of Thought", strings.Repeat("reasoning step\n", 8), false)
	if tc.Len() != 2001 {
		t.Fatalf("expected 2001 rows, got %d", tc.Len())
	}

	unfoldedTotal := tc.TotalHeight()
	unfoldedRowH := tc.RowHeight(thinkRow)
	if !tc.ToggleFold(thinkRow) {
		t.Fatalf("expected thinking row to become folded")
	}
	foldedTotal := tc.TotalHeight()
	if foldedTotal >= unfoldedTotal {
		t.Fatalf("expected Fenwick TotalHeight to decrease on fold: folded=%d unfolded=%d", foldedTotal, unfoldedTotal)
	}
	if unfoldedTotal-foldedTotal != uint64(unfoldedRowH-tc.RowHeight(thinkRow)) {
		t.Fatalf("Fenwick delta mismatch: total diff=%d row diff=%d",
			unfoldedTotal-foldedTotal, unfoldedRowH-tc.RowHeight(thinkRow))
	}

	// VisibleRange at bottom should return only a small viewport slice, never all 2,001 rows.
	first, last, _ := tc.VisibleRange()
	visibleCount := last - first + 1
	if first < 1980 || visibleCount <= 0 || visibleCount > 25 {
		t.Fatalf("expected virtualized tail slice, got [%d..%d] (%d rows)", first, last, visibleCount)
	}

	// Verify streaming checkpoints grow Fenwick height and deduplicate event IDs idempotently.
	row := tc.BeginAssistantTurn(1)
	h0 := tc.RowHeight(row)
	tc.ApplyCheckpoint(1, "ev-1", "first chunk\n")
	tc.ApplyCheckpoint(1, "ev-1", "first chunk\n") // duplicate replay ignored
	tc.ApplyCheckpoint(1, "ev-2", "second chunk\nthird line")
	tc.CompleteTurn(1, "COMPLETED")

	entry, ok := tc.Entry(row)
	if !ok {
		t.Fatalf("missing assistant row %d", row)
	}
	wantText := "first chunk\nsecond chunk\nthird line"
	if entry.Text != wantText {
		t.Fatalf("expected idempotent checkpoint text %q, got %q", wantText, entry.Text)
	}
	if tc.RowHeight(row) <= h0 {
		t.Fatalf("expected streaming checkpoints to grow row height: before=%d after=%d", h0, tc.RowHeight(row))
	}
	if tc.InFlight() {
		t.Fatalf("expected InFlight=false after CompleteTurn")
	}
}

func TestTranscriptViewInteractiveComposerAndStreaming(t *testing.T) {
	targets := transport.NewTargetRegistry()
	_ = targets.Register(&testTarget{meta: transport.TargetMeta{
		ID: "local-mac", DisplayName: "Local Sidecar", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)

	tc := NewTranscriptController()
	tc.AppendThinking("Planner", "Step 1: inspect journal\nStep 2: stream checkpoints", false)
	tc.BeginAssistantTurn(1)
	tc.ApplyCheckpoint(1, "evt-1", "Streaming reply from Rust authority")
	tc.SetComposerText("Deploy Phase H")

	var sentPrompt string
	var cancelled bool

	app := NewWorkbenchApp(targets, slots, wb)
	app.Transcript = tc
	app.OnSendPrompt = func(prompt string) {
		sentPrompt = prompt
		tc.AppendUser(prompt)
	}
	app.OnCancelTurn = func() {
		cancelled = true
		tc.CompleteTurn(1, "CANCELLED")
	}

	tt := ui.NewTester(app.View, 1100, 720)
	if !tt.HasText("LIVE TRANSCRIPT") || !tt.HasText("Streaming reply from Rust authority") {
		t.Fatalf("expected live transcript and streamed reply in GPU frame, got %q", tt.Texts())
	}
	if !tt.HasText("Step 1: inspect journal") {
		t.Fatalf("expected unfolded thinking block text, got %q", tt.Texts())
	}

	// Fold the thinking block via its button and verify the GPU frame collapses it.
	if err := tt.Click("Collapse thinking-1"); err != nil {
		t.Fatalf("clicking Collapse button failed: %v", err)
	}
	tt.Frame()
	if tt.HasText("Step 1: inspect journal") || !tt.HasText("Expand thinking-1") {
		t.Fatalf("expected thinking block collapsed after click, got %q", tt.Texts())
	}

	// Click Send to submit composer draft and Cancel Turn to cancel in-flight turn.
	if err := tt.Click("Send"); err != nil {
		t.Fatalf("clicking Send button failed: %v", err)
	}
	if err := tt.Click("Cancel Turn"); err != nil {
		t.Fatalf("clicking Cancel Turn button failed: %v", err)
	}
	tt.Frame()

	if sentPrompt != "Deploy Phase H" {
		t.Fatalf("expected OnSendPrompt='Deploy Phase H', got %q", sentPrompt)
	}
	if !cancelled {
		t.Fatalf("expected OnCancelTurn callback to fire")
	}
	if !tt.HasText("Deploy Phase H") || !tt.HasText("[CANCELLED]") {
		t.Fatalf("expected submitted user message and [CANCELLED] outcome in frame, got %q", tt.Texts())
	}
}

func TestTranscriptToolCallHumanGateAndCancellationReceiptCards(t *testing.T) {
	targets := transport.NewTargetRegistry()
	_ = targets.Register(&testTarget{meta: transport.TargetMeta{
		ID: "local-mac", DisplayName: "Local Sidecar", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Phase: transport.PhaseReady,
	}})
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)

	tc := NewTranscriptController()
	toolRow := tc.UpsertToolCall(ToolCallCardView{
		CallID:   "call-42",
		ToolName: "workspace.diff",
		Phase:    "RUNNING",
		Kind:     "DIFF",
		Title:    "Update session.rs",
		Summary:  "+18 -2 lines",
		Detail:   "@@ -10,2 +10,18 @@",
	}, false)
	gateRow := tc.UpsertHumanGate(HumanGateCardView{
		GateID:   "gate-99",
		CallID:   "call-42",
		Prompt:   "Approve device notification?",
		Resolved: false,
	})
	tc.BeginAssistantTurn(3)
	tc.ApplyCheckpoint(3, "ev-3-1", "Partial output before cancellation")

	app := NewWorkbenchApp(targets, slots, wb)
	app.Transcript = tc

	tt := ui.NewTester(app.View, 1180, 760)
	if !tt.HasText("Update session.rs") || !tt.HasText("[RUNNING]") || !tt.HasText("[PENDING GATE]") {
		t.Fatalf("expected running tool card and pending gate card, got %q", tt.Texts())
	}

	// Update tool card in-place with authoritative receipt and resolve gate card.
	if gotRow := tc.UpsertToolCall(ToolCallCardView{
		CallID:    "call-42",
		ToolName:  "workspace.diff",
		Phase:     "SUCCEEDED",
		Kind:      "DIFF",
		Title:     "Update session.rs",
		Summary:   "+18 -2 lines",
		Detail:    "@@ -10,2 +10,18 @@",
		ReceiptID: "rcpt-diff-42",
	}, false); gotRow != toolRow {
		t.Fatalf("expected in-place tool card update at row %d, got %d", toolRow, gotRow)
	}
	if gotGateRow := tc.UpsertHumanGate(HumanGateCardView{
		GateID:    "gate-99",
		CallID:    "call-42",
		Prompt:    "Approve device notification?",
		Resolved:  true,
		ReceiptID: "rcpt-gate-99",
	}); gotGateRow != gateRow {
		t.Fatalf("expected in-place gate card update at row %d, got %d", gateRow, gotGateRow)
	}
	hBeforeCancel := tc.TotalHeight()
	tc.ApplyCancellationReceipt(CancellationReceiptView{
		TurnOrdinal:           3,
		Completed:             true,
		CancellationRequested: true,
		TrackerClosed:         true,
		TrackedTasks:          1,
		RemainingTasks:        0,
		Status:                "RUNTIME_RETIREMENT_STATUS_COMPLETED",
		Outcome:               "SESSION_TURN_OUTCOME_REMOTE_UNCERTAIN",
	})
	if tc.TotalHeight() <= hBeforeCancel {
		t.Fatalf("expected Fenwick TotalHeight to grow for cancellation receipt badge: before=%d after=%d",
			hBeforeCancel, tc.TotalHeight())
	}
	if tc.InFlight() {
		t.Fatalf("expected InFlight=false after ApplyCancellationReceipt")
	}

	tt.Frame()
	if !tt.HasText("RECEIPT: rcpt-diff-42") || !tt.HasText("[RESOLVED · rcpt-gate-99]") {
		t.Fatalf("expected resolved tool/gate receipts in GPU frame, got %q", tt.Texts())
	}
	if !tt.HasText("CANCELLATION RECEIPT · status=RUNTIME_RETIREMENT_STATUS_COMPLETED completed=true tracker_closed=true tasks=1->0") {
		t.Fatalf("expected authoritative cancellation receipt badge in GPU frame, got %q", tt.Texts())
	}
}

func TestTranscriptSeenEventsWindowStaysBounded(t *testing.T) {
	tc := NewTranscriptController()
	tc.BeginAssistantTurn(1)

	// Feed far more distinct eventIDs than the dedup window holds.
	const total = maxTrackedEventIDs + 512
	for i := 0; i < total; i++ {
		tc.ApplyCheckpoint(1, fmt.Sprintf("evt-%d", i), "x")
	}
	if got := len(tc.seenEvents); got != maxTrackedEventIDs {
		t.Fatalf("expected dedup map bounded at %d, got %d", maxTrackedEventIDs, got)
	}
	if got := len(tc.seenEventOrder); got != maxTrackedEventIDs {
		t.Fatalf("expected FIFO window bounded at %d, got %d", maxTrackedEventIDs, got)
	}

	// The oldest long-evicted event may be re-applied without dedup (documented
	// window semantics), but events inside the window must still dedupe.
	row := tc.turnRow[1]
	before := tc.entries[row].Text
	tc.ApplyCheckpoint(1, fmt.Sprintf("evt-%d", total-1), "DUPLICATE")
	if tc.entries[row].Text != before {
		t.Fatal("event inside dedup window was re-applied")
	}
}

func TestTranscriptCJKHeightEstimateUsesDisplayWidth(t *testing.T) {
	tc := NewTranscriptController()

	// 72 ASCII chars fill exactly 1 wrap line at the default 72-column budget.
	asciiRow := tc.AppendUser(strings.Repeat("a", 72))
	// 36 CJK ideographs render 72 columns wide but are only 108 bytes; a
	// byte-based estimator would claim 2 lines instead of 1.
	cjkRow := tc.AppendUser(strings.Repeat("字", 36))
	// Same display width via fullwidth punctuation: 36×2 = 72 columns.
	mixedRow := tc.AppendUser(strings.Repeat("！", 36))

	ha, hb, hc := tc.RowHeight(asciiRow), tc.RowHeight(cjkRow), tc.RowHeight(mixedRow)
	if ha != hb || ha != hc {
		t.Fatalf("expected equal display-width rows to have equal heights: ascii=%d cjk=%d mixed=%d", ha, hb, hc)
	}
	// And the CJK row must be exactly one header+line unit, not byte-inflated.
	want := transcriptHeaderPx + transcriptLinePx
	if hb != want {
		t.Fatalf("expected CJK row height %d (single wrapped line), got %d", want, hb)
	}

	// Byte-based height would double-count CJK: 108 bytes / 72 cols = 2 lines.
	longCJK := tc.AppendUser(strings.Repeat("字", 80)) // 160 display cols → 3 lines
	wantLong := transcriptHeaderPx + 3*transcriptLinePx
	if got := tc.RowHeight(longCJK); got != wantLong {
		t.Fatalf("expected 80-CJK row height %d (3 wrapped lines), got %d", wantLong, got)
	}
}
