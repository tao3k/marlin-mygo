// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/viewport"
)

// TranscriptRole identifies the semantic speaker or channel of one transcript row.
type TranscriptRole string

const (
	RoleUser      TranscriptRole = "USER"
	RoleAssistant TranscriptRole = "ASSISTANT"
	RoleThinking  TranscriptRole = "THINKING"
	RoleTool      TranscriptRole = "TOOL"
	RoleGate      TranscriptRole = "GATE"
	RoleSystem    TranscriptRole = "SYSTEM"
)

// ToolCallCardView projects a durable marlin.app.v1.ToolCallEvent into a
// transport-free structured card rendered inside the Fenwick transcript.
type ToolCallCardView struct {
	CallID        string
	ToolName      string
	Phase         string
	Kind          string
	Title         string
	Summary       string
	Detail        string
	GateID        string
	ReceiptID     string
	DenialMessage string
}

// HumanGateCardView projects a durable marlin.app.v1.HumanGateEvent into an
// inline transcript card synced with the authoritative Approval Gate modal.
type HumanGateCardView struct {
	GateID    string
	CallID    string
	Prompt    string
	Resolved  bool
	ReceiptID string
}

// CancellationReceiptView projects Rust's durable RuntimeShutdownReceipt into
// structured transcript evidence when a turn is cancelled mid-flight.
type CancellationReceiptView struct {
	TurnOrdinal           uint64
	Completed             bool
	CancellationRequested bool
	TrackerClosed         bool
	TrackedTasks          uint64
	RemainingTasks        uint64
	Status                string
	Outcome               string
}

// TranscriptEntry stores one row in the Fenwick-virtualized live transcript.
type TranscriptEntry struct {
	ID           string
	Role         TranscriptRole
	Title        string
	Text         string
	TurnOrdinal  uint64
	Collapsible  bool
	Folded       bool
	Streaming    bool
	OutcomeLabel string
	ToolCard     *ToolCallCardView
	HumanGate    *HumanGateCardView
	Cancellation *CancellationReceiptView
}

const (
	defaultTranscriptViewportH uint64 = 420
	defaultTranscriptWrapCols  int    = 72
	transcriptHeaderPx         uint32 = 34
	transcriptLinePx           uint32 = 20
	transcriptCollapsedPx      uint32 = 38
	transcriptEvidenceBadgePx  uint32 = 26
)

// TranscriptController maintains an O(log N) Fenwick-tree virtualized transcript
// suitable for 100k+ row streaming conversations, collapsible thinking/tool blocks,
// inline approval gate cards, durable cancellation receipts, idempotent event
// replay, and an interactive MyGo GPU chat composer.
type TranscriptController struct {
	mu           sync.RWMutex
	entries      []TranscriptEntry
	turnRow      map[uint64]int
	toolRows     map[string]int
	gateRows     map[string]int
	seenEvents   map[string]struct{}
	heights      *viewport.VirtualHeightIndex
	scrollY      uint64
	viewportH    uint64
	wrapCols     int
	autoFollow   bool
	composerText string
	activeTurn   uint64
	inFlight     bool
}

func NewTranscriptController() *TranscriptController {
	return &TranscriptController{
		turnRow:    make(map[uint64]int),
		toolRows:   make(map[string]int),
		gateRows:   make(map[string]int),
		seenEvents: make(map[string]struct{}),
		heights:    viewport.NewVirtualHeightIndex(64),
		viewportH:  defaultTranscriptViewportH,
		wrapCols:   defaultTranscriptWrapCols,
		autoFollow: true,
	}
}

// AppendUser appends a user message row in O(log N) and returns its row index.
func (tc *TranscriptController) AppendUser(text string) int {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	entry := TranscriptEntry{
		ID:   fmt.Sprintf("user-%d", len(tc.entries)+1),
		Role: RoleUser,
		Text: text,
	}
	return tc.appendLocked(entry)
}

// AppendThinking appends a collapsible reasoning/thinking block in O(log N).
func (tc *TranscriptController) AppendThinking(title, text string, folded bool) int {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if title == "" {
		title = "Reasoning trace"
	}
	entry := TranscriptEntry{
		ID:          fmt.Sprintf("thinking-%d", len(tc.entries)+1),
		Role:        RoleThinking,
		Title:       title,
		Text:        text,
		Collapsible: true,
		Folded:      folded,
	}
	return tc.appendLocked(entry)
}

// AppendTool appends a simple collapsible tool summary row in O(log N).
func (tc *TranscriptController) AppendTool(toolName, summary string, folded bool) int {
	return tc.UpsertToolCall(ToolCallCardView{
		CallID:   fmt.Sprintf("tool-%d", tc.Len()+1),
		ToolName: toolName,
		Title:    toolName,
		Summary:  summary,
	}, folded)
}

// UpsertToolCall creates or updates a structured tool-call card in-place keyed
// by card.CallID and updates its Fenwick height in O(log N).
func (tc *TranscriptController) UpsertToolCall(card ToolCallCardView, folded bool) int {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	cp := card
	if cp.CallID == "" {
		cp.CallID = fmt.Sprintf("tool-%d", len(tc.entries)+1)
	}
	title := cp.Title
	if title == "" {
		title = cp.ToolName
	}
	body := formatToolCardBody(cp)
	if row, ok := tc.toolRows[cp.CallID]; ok {
		tc.entries[row].Title = title
		tc.entries[row].Text = body
		tc.entries[row].OutcomeLabel = cp.Phase
		tc.entries[row].ToolCard = &cp
		tc.heights.UpdateHeight(row, tc.estimateHeightLocked(tc.entries[row]))
		if tc.autoFollow {
			tc.scrollToBottomLocked()
		}
		return row
	}
	entry := TranscriptEntry{
		ID:           "tool-" + cp.CallID,
		Role:         RoleTool,
		Title:        title,
		Text:         body,
		Collapsible:  true,
		Folded:       folded,
		OutcomeLabel: cp.Phase,
		ToolCard:     &cp,
	}
	row := tc.appendLocked(entry)
	tc.toolRows[cp.CallID] = row
	return row
}

// UpsertHumanGate creates or updates an inline Approval Gate card keyed by
// gate.GateID and updates its Fenwick height in O(log N).
func (tc *TranscriptController) UpsertHumanGate(gate HumanGateCardView) int {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	cp := gate
	if cp.GateID == "" {
		cp.GateID = fmt.Sprintf("gate-%d", len(tc.entries)+1)
	}
	badge := "PENDING GATE"
	if cp.Resolved {
		badge = "RESOLVED"
		if cp.ReceiptID != "" {
			badge = "RESOLVED · " + cp.ReceiptID
		}
	}
	if row, ok := tc.gateRows[cp.GateID]; ok {
		tc.entries[row].Text = cp.Prompt
		tc.entries[row].OutcomeLabel = badge
		tc.entries[row].HumanGate = &cp
		tc.heights.UpdateHeight(row, tc.estimateHeightLocked(tc.entries[row]))
		if tc.autoFollow {
			tc.scrollToBottomLocked()
		}
		return row
	}
	entry := TranscriptEntry{
		ID:           "gate-" + cp.GateID,
		Role:         RoleGate,
		Title:        cp.GateID,
		Text:         cp.Prompt,
		OutcomeLabel: badge,
		HumanGate:    &cp,
	}
	row := tc.appendLocked(entry)
	tc.gateRows[cp.GateID] = row
	return row
}

// ApplyCancellationReceipt attaches Rust's authoritative RuntimeShutdownReceipt
// evidence to the assistant turn row and updates its Fenwick height in O(log N).
func (tc *TranscriptController) ApplyCancellationReceipt(receipt CancellationReceiptView) int {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	cp := receipt
	row, ok := tc.turnRow[cp.TurnOrdinal]
	if !ok {
		row = tc.appendLocked(TranscriptEntry{
			ID:          fmt.Sprintf("turn-%d", cp.TurnOrdinal),
			Role:        RoleAssistant,
			Title:       fmt.Sprintf("Turn #%d", cp.TurnOrdinal),
			TurnOrdinal: cp.TurnOrdinal,
		})
		tc.turnRow[cp.TurnOrdinal] = row
	}
	tc.entries[row].Streaming = false
	if cp.Outcome != "" {
		tc.entries[row].OutcomeLabel = cp.Outcome
	} else {
		tc.entries[row].OutcomeLabel = "CANCELLED"
	}
	tc.entries[row].Cancellation = &cp
	if tc.activeTurn == cp.TurnOrdinal {
		tc.inFlight = false
	}
	tc.heights.UpdateHeight(row, tc.estimateHeightLocked(tc.entries[row]))
	if tc.autoFollow {
		tc.scrollToBottomLocked()
	}
	return row
}

// BeginAssistantTurn creates or locates the streaming assistant row for turnOrdinal.
func (tc *TranscriptController) BeginAssistantTurn(turnOrdinal uint64) int {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.activeTurn = turnOrdinal
	tc.inFlight = true
	if row, ok := tc.turnRow[turnOrdinal]; ok {
		tc.entries[row].Streaming = true
		return row
	}
	row := tc.appendLocked(TranscriptEntry{
		ID:          fmt.Sprintf("turn-%d", turnOrdinal),
		Role:        RoleAssistant,
		Title:       fmt.Sprintf("Turn #%d", turnOrdinal),
		TurnOrdinal: turnOrdinal,
		Streaming:   true,
	})
	tc.turnRow[turnOrdinal] = row
	return row
}

// ApplyCheckpoint applies one durable incremental text delta from Rust's
// ConversationJournal. Duplicate non-empty eventIDs are ignored idempotently so
// reconnect replay never duplicates streamed text, and row height is updated
// in O(log N) in the Fenwick VirtualHeightIndex.
func (tc *TranscriptController) ApplyCheckpoint(turnOrdinal uint64, eventID, textDelta string) int {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if eventID != "" {
		if _, dup := tc.seenEvents[eventID]; dup {
			if row, ok := tc.turnRow[turnOrdinal]; ok {
				return row
			}
			return -1
		}
		tc.seenEvents[eventID] = struct{}{}
	}
	row, ok := tc.turnRow[turnOrdinal]
	if !ok {
		row = tc.appendLocked(TranscriptEntry{
			ID:          fmt.Sprintf("turn-%d", turnOrdinal),
			Role:        RoleAssistant,
			Title:       fmt.Sprintf("Turn #%d", turnOrdinal),
			TurnOrdinal: turnOrdinal,
			Streaming:   true,
		})
		tc.turnRow[turnOrdinal] = row
	}
	tc.activeTurn = turnOrdinal
	tc.inFlight = true
	tc.entries[row].Text += textDelta
	tc.entries[row].Streaming = true
	tc.heights.UpdateHeight(row, tc.estimateHeightLocked(tc.entries[row]))
	if tc.autoFollow {
		tc.scrollToBottomLocked()
	}
	return row
}

// CompleteTurn finalizes a streaming assistant turn and updates its badge height.
func (tc *TranscriptController) CompleteTurn(turnOrdinal uint64, outcomeLabel string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if row, ok := tc.turnRow[turnOrdinal]; ok {
		tc.entries[row].Streaming = false
		tc.entries[row].OutcomeLabel = outcomeLabel
		tc.heights.UpdateHeight(row, tc.estimateHeightLocked(tc.entries[row]))
	}
	if tc.activeTurn == turnOrdinal {
		tc.inFlight = false
	}
	if tc.autoFollow {
		tc.scrollToBottomLocked()
	}
}

// ToggleFold flips a collapsible entry's folded state and updates the Fenwick
// height tree in O(log N) without rebuilding any prefix sums.
func (tc *TranscriptController) ToggleFold(row int) bool {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if row < 0 || row >= len(tc.entries) || !tc.entries[row].Collapsible {
		return false
	}
	tc.entries[row].Folded = !tc.entries[row].Folded
	tc.heights.UpdateHeight(row, tc.estimateHeightLocked(tc.entries[row]))
	if tc.autoFollow {
		tc.scrollToBottomLocked()
	}
	return tc.entries[row].Folded
}

// SetViewport configures the virtualized scroll window height and offset.
func (tc *TranscriptController) SetViewport(scrollY, viewportH uint64) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if viewportH > 0 {
		tc.viewportH = viewportH
	}
	tc.scrollY = scrollY
	tc.autoFollow = false
}

// ScrollToBottom re-enables auto-follow and positions the virtual viewport at the tail.
func (tc *TranscriptController) ScrollToBottom() {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.autoFollow = true
	tc.scrollToBottomLocked()
}

// VisibleRange returns the inclusive [firstRow, lastRow] interval intersecting
// the current virtual viewport via O(log N) Fenwick binary lifting.
func (tc *TranscriptController) VisibleRange() (firstRow, lastRow int, firstOffset uint64) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.heights.VisibleRange(tc.scrollY, tc.viewportH)
}

func (tc *TranscriptController) Len() int {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return len(tc.entries)
}

func (tc *TranscriptController) TotalHeight() uint64 {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.heights.TotalHeight()
}

func (tc *TranscriptController) RowHeight(row int) uint32 {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.heights.RowHeight(row)
}

func (tc *TranscriptController) Entry(row int) (TranscriptEntry, bool) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	if row < 0 || row >= len(tc.entries) {
		return TranscriptEntry{}, false
	}
	return tc.entries[row], true
}

func (tc *TranscriptController) InFlight() bool {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.inFlight
}

func (tc *TranscriptController) ActiveTurn() uint64 {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.activeTurn
}

func (tc *TranscriptController) ComposerText() string {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.composerText
}

func (tc *TranscriptController) SetComposerText(text string) {
	tc.mu.Lock()
	tc.composerText = text
	tc.mu.Unlock()
}

// Render paints ONLY the Fenwick-virtualized visible slice of transcript rows
// plus the interactive chat composer onto MyGo's GPU scene graph.
func (tc *TranscriptController) Render(c *ui.Context, t *ui.Theme, onSend func(string), onCancel func()) {
	tc.mu.RLock()
	first, last, _ := tc.heights.VisibleRange(tc.scrollY, tc.viewportH)
	totalRows := len(tc.entries)
	totalH := tc.heights.TotalHeight()
	inFlight := tc.inFlight
	draft := tc.composerText
	var visible []TranscriptEntry
	var visibleIndices []int
	if first >= 0 && last >= first && last < totalRows {
		visible = make([]TranscriptEntry, 0, last-first+1)
		visibleIndices = make([]int, 0, last-first+1)
		for i := first; i <= last; i++ {
			visible = append(visible, tc.entries[i])
			visibleIndices = append(visibleIndices, i)
		}
	}
	tc.mu.RUnlock()

	var toggleRow = -1
	var submitPrompt string
	var cancelClicked bool

	ui.Column(c).Grow(1).MinWidth(0).Padding(10).Gap(8).Radius(8).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "LIVE TRANSCRIPT").FontSize(11).Bold().TextColor(t.TextMuted)
			ui.Textf(c, "rows:%d visible:%d height:%dpx", totalRows, len(visible), totalH).
				FontSize(11).TextColor(t.TextMuted)
			ui.Spacer(c)
			if inFlight {
				ui.Text(c, "STREAMING").FontSize(11).Bold().TextColor(t.Warning)
			} else {
				ui.Text(c, "IDLE").FontSize(11).TextColor(t.Accent)
			}
		})

		if len(visible) == 0 {
			ui.Text(c, "No turns yet. Send a message below to stream from Rust AppServer.").
				FontSize(12).TextColor(t.TextMuted)
		} else {
			ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
				for idx, entry := range visible {
					rowIdx := visibleIndices[idx]
					border := t.Border
					roleColor := t.Accent
					switch entry.Role {
					case RoleUser:
						roleColor = t.Accent
					case RoleAssistant:
						border = t.Accent
					case RoleThinking, RoleTool:
						roleColor = t.Warning
					case RoleGate:
						border = t.Warning
						roleColor = t.Warning
					}
					ui.Column(c).MinWidth(0).Padding(8).Gap(4).Radius(6).
						Border(1, border).Children(func() {
						ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
							header := string(entry.Role)
							if entry.Title != "" {
								header = fmt.Sprintf("%s · %s", entry.Role, entry.Title)
							}
							ui.Text(c, header).FontSize(11).Bold().TextColor(roleColor)
							if entry.Streaming {
								ui.Text(c, "[streaming...]").FontSize(11).TextColor(t.Warning)
							} else if entry.OutcomeLabel != "" {
								ui.Textf(c, "[%s]", entry.OutcomeLabel).FontSize(11).TextColor(t.TextMuted)
							}
							ui.Spacer(c)
							if entry.Collapsible {
								btnLabel := fmt.Sprintf("Collapse %s", entry.ID)
								if entry.Folded {
									btnLabel = fmt.Sprintf("Expand %s", entry.ID)
								}
								if ui.Button(c.Key("fold-"+entry.ID), btnLabel).Clicked() {
									toggleRow = rowIdx
								}
							}
						})
						if entry.Collapsible && entry.Folded {
							ui.Textf(c, "(folded · %d chars)", len(entry.Text)).
								FontSize(11).TextColor(t.TextMuted)
						} else if entry.Text != "" {
							ui.Text(c, entry.Text).FontSize(13)
						}
						if entry.ToolCard != nil && !entry.Folded {
							if entry.ToolCard.ReceiptID != "" {
								ui.Textf(c, "RECEIPT: %s", entry.ToolCard.ReceiptID).FontSize(11).TextColor(t.Accent)
							}
							if entry.ToolCard.DenialMessage != "" {
								ui.Textf(c, "DENIED: %s", entry.ToolCard.DenialMessage).FontSize(11).TextColor(t.Warning)
							}
						}
						if entry.Cancellation != nil {
							rc := entry.Cancellation
							ui.Textf(c, "CANCELLATION RECEIPT · status=%s completed=%v tracker_closed=%v tasks=%d->%d",
								rc.Status, rc.Completed, rc.TrackerClosed, rc.TrackedTasks, rc.RemainingTasks).
								FontSize(11).Bold().TextColor(t.Warning)
						}
					})
				}
			})
		}

		// Interactive chat composer bound to live durable sessions.
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("transcript-composer"), &draft).Placeholder("Message Marlin agent...")
			if ui.PrimaryButton(c.Key("transcript-send"), "Send").Clicked() {
				trimmed := strings.TrimSpace(draft)
				if trimmed != "" {
					submitPrompt = trimmed
					draft = ""
				}
			}
			if inFlight && onCancel != nil {
				if ui.Button(c.Key("transcript-cancel"), "Cancel Turn").Clicked() {
					cancelClicked = true
				}
			}
		})
	})

	tc.mu.Lock()
	tc.composerText = draft
	tc.mu.Unlock()

	if toggleRow >= 0 {
		tc.ToggleFold(toggleRow)
	}
	if submitPrompt != "" && onSend != nil {
		onSend(submitPrompt)
	}
	if cancelClicked && onCancel != nil {
		onCancel()
	}
}

func formatToolCardBody(card ToolCallCardView) string {
	var parts []string
	if card.Kind != "" {
		parts = append(parts, fmt.Sprintf("[%s] %s", card.Kind, card.ToolName))
	} else if card.ToolName != "" {
		parts = append(parts, card.ToolName)
	}
	if card.Summary != "" {
		parts = append(parts, card.Summary)
	}
	if card.Detail != "" {
		parts = append(parts, card.Detail)
	}
	return strings.Join(parts, "\n")
}

func (tc *TranscriptController) appendLocked(entry TranscriptEntry) int {
	h := tc.estimateHeightLocked(entry)
	row := tc.heights.Append(h)
	tc.entries = append(tc.entries, entry)
	if tc.autoFollow {
		tc.scrollToBottomLocked()
	}
	return row
}

func (tc *TranscriptController) scrollToBottomLocked() {
	total := tc.heights.TotalHeight()
	if total > tc.viewportH {
		tc.scrollY = total - tc.viewportH
	} else {
		tc.scrollY = 0
	}
}

func (tc *TranscriptController) estimateHeightLocked(entry TranscriptEntry) uint32 {
	if entry.Collapsible && entry.Folded {
		return transcriptCollapsedPx
	}
	extra := uint32(0)
	if entry.Cancellation != nil {
		extra += transcriptEvidenceBadgePx
	}
	if entry.ToolCard != nil && (entry.ToolCard.ReceiptID != "" || entry.ToolCard.DenialMessage != "") {
		extra += transcriptEvidenceBadgePx
	}
	if entry.Text == "" {
		return transcriptHeaderPx + transcriptLinePx + extra
	}
	cols := tc.wrapCols
	if cols <= 0 {
		cols = defaultTranscriptWrapCols
	}
	lines := 0
	for _, para := range strings.Split(entry.Text, "\n") {
		n := len(para)
		if n == 0 {
			lines++
			continue
		}
		lines += (n + cols - 1) / cols
	}
	if lines < 1 {
		lines = 1
	}
	return transcriptHeaderPx + uint32(lines)*transcriptLinePx + extra
}
