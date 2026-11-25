// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

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

// TranscriptSegment is a pre-parsed prose or fenced code block computed once at
// append/update time so 120Hz Render loops never re-scan markdown strings.
type TranscriptSegment struct {
	IsCode  bool
	Lang    string
	Content string
	CopyKey string
}

// TranscriptEntry stores one row in the Fenwick-virtualized live transcript.
// Precomputed display strings avoid per-frame fmt.Sprintf heap allocations at 120Hz/240Hz.
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

	headerLabel   string
	outcomeBadge  string
	foldKey       string
	collapseLabel string
	expandLabel   string
	foldedSummary string
	receiptBadge  string
	denialBadge   string
	cancelBadge   string
	segments      []TranscriptSegment
}

func (e *TranscriptEntry) refreshLabels() {
	if e.Title != "" {
		e.headerLabel = string(e.Role) + " · " + e.Title
	} else {
		e.headerLabel = string(e.Role)
	}
	if e.OutcomeLabel != "" {
		e.outcomeBadge = "[" + e.OutcomeLabel + "]"
	} else {
		e.outcomeBadge = ""
	}
	if e.Collapsible {
		e.foldKey = "fold-" + e.ID
		e.collapseLabel = "Collapse " + e.ID
		e.expandLabel = "Expand " + e.ID
		if e.Role == RoleThinking {
			e.foldedSummary = fmt.Sprintf("Thought · %d chars (click to expand)", utf8.RuneCountInString(e.Text))
		} else {
			e.foldedSummary = fmt.Sprintf("(folded · %d chars)", utf8.RuneCountInString(e.Text))
		}
	} else {
		e.foldKey = ""
		e.collapseLabel = ""
		e.expandLabel = ""
		e.foldedSummary = ""
	}
	if e.ToolCard != nil && e.ToolCard.ReceiptID != "" {
		e.receiptBadge = "RECEIPT: " + e.ToolCard.ReceiptID
	} else {
		e.receiptBadge = ""
	}
	if e.ToolCard != nil && e.ToolCard.DenialMessage != "" {
		e.denialBadge = "DENIED: " + e.ToolCard.DenialMessage
	} else {
		e.denialBadge = ""
	}
	if e.Cancellation != nil {
		rc := e.Cancellation
		e.cancelBadge = fmt.Sprintf("CANCELLATION RECEIPT · status=%s completed=%v tracker_closed=%v tasks=%d->%d",
			rc.Status, rc.Completed, rc.TrackerClosed, rc.TrackedTasks, rc.RemainingTasks)
	} else {
		e.cancelBadge = ""
	}
	if strings.Contains(e.Text, "```") {
		e.segments = parseFencedCodeSegments(e.ID, e.Text)
	} else {
		e.segments = nil
	}
}

func parseFencedCodeSegments(entryID, text string) []TranscriptSegment {
	var out []TranscriptSegment
	lines := strings.Split(text, "\n")
	var buf strings.Builder
	inCode := false
	lang := ""
	codeIdx := 0

	flushProse := func() {
		s := strings.TrimSpace(buf.String())
		buf.Reset()
		if s != "" {
			out = append(out, TranscriptSegment{IsCode: false, Content: s})
		}
	}
	flushCode := func() {
		s := strings.TrimRight(buf.String(), "\n")
		buf.Reset()
		l := strings.ToUpper(strings.TrimSpace(lang))
		if l == "" {
			l = "CODE"
		}
		codeIdx++
		out = append(out, TranscriptSegment{
			IsCode:  true,
			Lang:    l,
			Content: s,
			CopyKey: fmt.Sprintf("copy-%s-%d", entryID, codeIdx),
		})
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if !inCode {
				flushProse()
				inCode = true
				lang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			} else {
				flushCode()
				inCode = false
				lang = ""
			}
			continue
		}
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(line)
	}
	if inCode {
		flushCode()
	} else {
		flushProse()
	}
	return out
}

const (
	defaultTranscriptViewportH uint64 = 420
	defaultTranscriptWrapCols  int    = 72
	defaultMaxSeenEvents       int    = 8192
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
	mu            sync.RWMutex
	entries       []TranscriptEntry
	turnRow       map[uint64]int
	toolRows      map[string]int
	gateRows      map[string]int
	seenEvents    map[string]struct{}
	seenRing      []string
	seenHead      int
	maxSeenEvents int
	heights       *viewport.VirtualHeightIndex
	scrollY       uint64
	viewportH     uint64
	wrapCols      int
	autoFollow    bool
	composerText  string
	activeTurn    uint64
	inFlight      bool
	deepThink     bool
	modelLabel    string
	lastCopiedKey string
	OnCopyCode    func(code string)
	OnDeepThink   func(enabled bool)

	// Single-frame scratch buffers reused across 120Hz/240Hz Render calls (0 slice allocs/frame).
	renderMu       sync.Mutex
	renderEntries  []TranscriptEntry
	renderIndices  []int
	statsRows      int
	statsVisible   int
	statsHeight    uint64
	cachedStatsStr string
}

func NewTranscriptController() *TranscriptController {
	return &TranscriptController{
		turnRow:       make(map[uint64]int),
		toolRows:      make(map[string]int),
		gateRows:      make(map[string]int),
		seenEvents:    make(map[string]struct{}),
		maxSeenEvents: defaultMaxSeenEvents,
		heights:       viewport.NewVirtualHeightIndex(64),
		viewportH:     defaultTranscriptViewportH,
		wrapCols:      defaultTranscriptWrapCols,
		autoFollow:    true,
	}
}

// SetMaxSeenEvents configures the bounded FIFO deduplication ring capacity for
// streaming checkpoint eventIDs (preventing unbounded map growth over 100k+ turns).
func (tc *TranscriptController) SetMaxSeenEvents(limit int) {
	if limit < 1 {
		limit = 1
	}
	tc.mu.Lock()
	tc.maxSeenEvents = limit
	for len(tc.seenRing) > limit {
		oldest := tc.seenRing[tc.seenHead]
		delete(tc.seenEvents, oldest)
		tc.seenRing = append(tc.seenRing[:tc.seenHead], tc.seenRing[tc.seenHead+1:]...)
		if tc.seenHead >= len(tc.seenRing) {
			tc.seenHead = 0
		}
	}
	tc.mu.Unlock()
}

// SeenEventCount reports the current number of cached checkpoint eventIDs.
func (tc *TranscriptController) SeenEventCount() int {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return len(tc.seenEvents)
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
		tc.entries[row].refreshLabels()
		tc.updateRowHeightLocked(row)
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
		tc.entries[row].refreshLabels()
		tc.updateRowHeightLocked(row)
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
	tc.entries[row].refreshLabels()
	if tc.activeTurn == cp.TurnOrdinal {
		tc.inFlight = false
	}
	tc.updateRowHeightLocked(row)
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
		tc.recordSeenEventLocked(eventID)
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
	tc.entries[row].refreshLabels()
	tc.updateRowHeightLocked(row)
	return row
}

// CompleteTurn finalizes a streaming assistant turn and updates its badge height
// using anchor-preserving Fenwick mutation so readers scrolled up never jump.
func (tc *TranscriptController) CompleteTurn(turnOrdinal uint64, outcomeLabel string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if row, ok := tc.turnRow[turnOrdinal]; ok {
		tc.entries[row].Streaming = false
		tc.entries[row].OutcomeLabel = outcomeLabel
		tc.entries[row].refreshLabels()
		tc.updateRowHeightLocked(row)
	}
	if tc.activeTurn == turnOrdinal {
		tc.inFlight = false
	}
}

// ToggleFold flips a collapsible entry's folded state and updates the Fenwick
// height tree in O(log N) with 0px ScrollAnchor compensation when scrolled up.
func (tc *TranscriptController) ToggleFold(row int) bool {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if row < 0 || row >= len(tc.entries) || !tc.entries[row].Collapsible {
		return false
	}
	tc.entries[row].Folded = !tc.entries[row].Folded
	tc.updateRowHeightLocked(row)
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

// DeepThinkEnabled reports whether the composer's DeepSeek-style Deep Think toggle is active.
func (tc *TranscriptController) DeepThinkEnabled() bool {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.deepThink
}

// SetDeepThink toggles the composer's Deep Think pill (pure UI state).
func (tc *TranscriptController) SetDeepThink(enabled bool) {
	tc.mu.Lock()
	tc.deepThink = enabled
	cb := tc.OnDeepThink
	tc.mu.Unlock()
	if cb != nil {
		cb(enabled)
	}
}

// SetModelLabel sets the ambient model/profile badge displayed in the composer bar.
func (tc *TranscriptController) SetModelLabel(label string) {
	tc.mu.Lock()
	tc.modelLabel = strings.TrimSpace(label)
	tc.mu.Unlock()
}

// WrapCols returns the current estimated wrapping column width for Fenwick heights.
func (tc *TranscriptController) WrapCols() int {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.wrapCols
}

// SyncWrapWidth adapts wrapCols dynamically to the real GPU pane width (in DIPs)
// and rebuilds Fenwick heights in O(N) ONLY when wrapCols changes, preserving
// the reader's ScrollAnchor with 0px drift (fixes narrow-pane wrapping drift).
func (tc *TranscriptController) SyncWrapWidth(paneWidthPx float32) bool {
	if paneWidthPx <= 80 {
		return false
	}
	cols := int((paneWidthPx - 56) / 7.5)
	if cols < 24 {
		cols = 24
	} else if cols > 120 {
		cols = 120
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if cols == tc.wrapCols {
		return false
	}
	tc.wrapCols = cols
	if len(tc.entries) == 0 {
		return true
	}
	newHeights := make([]uint32, len(tc.entries))
	for i := range tc.entries {
		newHeights[i] = tc.estimateHeightLocked(tc.entries[i])
	}
	if tc.autoFollow {
		_ = tc.heights.RebuildHeightsAnchored(newHeights, tc.scrollY)
		tc.scrollToBottomLocked()
	} else {
		tc.scrollY = tc.heights.RebuildHeightsAnchored(newHeights, tc.scrollY)
		tc.clampScrollLocked()
	}
	return true
}

// JumpToRow positions the virtualized viewport directly at row in O(log N)
// (e.g. when the Rust backend returns a search hit or receipt target),
// automatically unfolding a collapsed Thinking/Tool block if targeted.
func (tc *TranscriptController) JumpToRow(row int) bool {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if row < 0 || row >= len(tc.entries) {
		return false
	}
	if tc.entries[row].Collapsible && tc.entries[row].Folded {
		tc.entries[row].Folded = false
		tc.entries[row].refreshLabels()
		tc.heights.UpdateHeight(row, tc.estimateHeightLocked(tc.entries[row]))
	}
	tc.autoFollow = false
	tc.scrollY = tc.heights.PrefixSum(row)
	tc.clampScrollLocked()
	return true
}

// ScrollY returns the current virtual scroll offset in pixels.
func (tc *TranscriptController) ScrollY() uint64 {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.scrollY
}

// AutoFollow reports whether the transcript is locked to the streaming tail.
func (tc *TranscriptController) AutoFollow() bool {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.autoFollow
}

func (tc *TranscriptController) updateRowHeightLocked(row int) {
	newH := tc.estimateHeightLocked(tc.entries[row])
	if tc.autoFollow {
		tc.heights.UpdateHeight(row, newH)
		tc.scrollToBottomLocked()
	} else {
		tc.scrollY = tc.heights.UpdateHeightAnchored(row, newH, tc.scrollY)
		tc.clampScrollLocked()
	}
}

func (tc *TranscriptController) clampScrollLocked() {
	total := tc.heights.TotalHeight()
	if total <= tc.viewportH {
		tc.scrollY = 0
		return
	}
	maxY := total - tc.viewportH
	if tc.scrollY > maxY {
		tc.scrollY = maxY
	}
}

// Render paints ONLY the Fenwick-virtualized visible slice of transcript rows
// plus the interactive chat composer onto MyGo's GPU scene graph.
func (tc *TranscriptController) Render(c *ui.Context, t *ui.Theme, onSend func(string), onCancel func()) {
	tc.renderMu.Lock()
	defer tc.renderMu.Unlock()

	tc.mu.RLock()
	first, last, _ := tc.heights.VisibleRange(tc.scrollY, tc.viewportH)
	totalRows := len(tc.entries)
	totalH := tc.heights.TotalHeight()
	inFlight := tc.inFlight
	autoFollow := tc.autoFollow
	deepThink := tc.deepThink
	modelLbl := tc.modelLabel
	lastCopied := tc.lastCopiedKey
	draft := tc.composerText
	tc.renderEntries = tc.renderEntries[:0]
	tc.renderIndices = tc.renderIndices[:0]
	if first >= 0 && last >= first && last < totalRows {
		for i := first; i <= last; i++ {
			tc.renderEntries = append(tc.renderEntries, tc.entries[i])
			tc.renderIndices = append(tc.renderIndices, i)
		}
	}
	tc.mu.RUnlock()
	if modelLbl == "" {
		modelLbl = "Marlin Core"
	}

	visible := tc.renderEntries
	visibleIndices := tc.renderIndices
	if tc.cachedStatsStr == "" || tc.statsRows != totalRows || tc.statsVisible != len(visible) || tc.statsHeight != totalH {
		tc.statsRows = totalRows
		tc.statsVisible = len(visible)
		tc.statsHeight = totalH
		tc.cachedStatsStr = fmt.Sprintf("rows:%d visible:%d height:%dpx", totalRows, len(visible), totalH)
	}
	statsLine := tc.cachedStatsStr

	var (
		toggleRow     = -1
		submitPrompt  string
		cancelClicked bool
		jumpLatest    bool
		toggleThink   bool
		copiedKey     string
		copiedCode    string
	)

	dt := derivedThemeFromUITheme(t)

	root := ui.Column(c.Key("transcript-root")).Grow(1).MinWidth(0).Padding(10).Gap(8).Radius(12).
		Background(toColor(dt.Surface)).Border(1, glassBorderColor(dt))
	if w := root.Bounds().W; w > 80 {
		tc.SyncWrapWidth(w)
	}
	root.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "LIVE TRANSCRIPT").FontSize(11).FontWeight(700).LetterSpacing(0.5).TextColor(toColor(dt.InkMuted))
			ui.Text(c, statsLine).FontSize(11).TextColor(toColor(dt.InkMuted))
			ui.Spacer(c)
			if inFlight {
				AppleBadgePill(c, "STREAMING", dt.Warning, dt)
			} else {
				AppleBadgePill(c, "IDLE", dt.Accent, dt)
			}
		})

		if len(visible) == 0 {
			ui.Text(c, "No turns yet. Send a message below to stream from Rust AppServer.").
				FontSize(12.5).TextColor(toColor(dt.InkMuted))
		} else {
			ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
				for idx := range visible {
					entry := &visible[idx]
					rowIdx := visibleIndices[idx]

					switch entry.Role {
					case RoleUser:
						// T3 Chat asymmetric user bubble: soft accent tint with crisp pill radius.
						ui.Row(c).Justify(ui.End).Children(func() {
							ui.Column(c).MinWidth(0).Padding(9, 13).Gap(4).Radius(16, 16, 4, 16).
								Background(rgbaFromRGB(dt.Accent, 0.14)).
								Border(1, rgbaFromRGB(dt.Accent, 0.24)).Children(func() {
								ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
									ui.Text(c, entry.headerLabel).FontSize(10.5).FontWeight(700).TextColor(toColor(dt.Accent))
									if entry.outcomeBadge != "" {
										ui.Text(c, entry.outcomeBadge).FontSize(10.5).TextColor(toColor(dt.InkMuted))
									}
								})
								if entry.Text != "" {
									ui.Text(c, entry.Text).FontSize(13).LineHeight(1.4).TextColor(toColor(dt.Ink))
								}
							})
						})

					case RoleThinking:
						// DeepSeek R1 calm collapsible thinking timeline (recessed + 2px left rail).
						ui.Column(c).MinWidth(0).Padding(8, 12).Gap(5).Radius(10).
							Background(toColor(dt.SurfaceRecessed)).
							BorderWidth(0, 0, 0, 2).BorderColor(toColor(dt.Accent)).Children(func() {
							ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
								ui.Text(c, entry.headerLabel).FontSize(11).FontWeight(600).TextColor(toColor(dt.InkSecondary))
								if entry.Streaming {
									AppleBadgePill(c, "THINKING...", dt.Accent, dt)
								} else if entry.outcomeBadge != "" {
									ui.Text(c, entry.outcomeBadge).FontSize(11).TextColor(toColor(dt.InkMuted))
								}
								ui.Spacer(c)
								if entry.Collapsible {
									btnLabel := entry.collapseLabel
									if entry.Folded {
										btnLabel = entry.expandLabel
									}
									if ApplePillButton(c, entry.foldKey, btnLabel, BtnGlass, dt, nil, true).Clicked() {
										toggleRow = rowIdx
									}
								}
							})
							if entry.Collapsible && entry.Folded {
								ui.Text(c, entry.foldedSummary).FontSize(11.5).TextColor(toColor(dt.InkMuted))
							} else if entry.Text != "" {
								ui.Text(c, entry.Text).FontSize(12.5).LineHeight(1.4).TextColor(toColor(dt.InkSecondary))
							}
						})

					default:
						// Assistant editorial card (with T3 code-fence blocks) / Tool / Gate cards.
						cardBg := toColor(dt.SurfaceElevated)
						borderCol := glassBorderColor(dt)
						roleColor := toColor(dt.Accent)
						if entry.Role == RoleTool {
							cardBg = toColor(dt.SurfaceRecessed)
							roleColor = toColor(dt.Info)
						} else if entry.Role == RoleGate {
							cardBg = rgbaFromRGB(dt.Warning, 0.08)
							borderCol = rgbaFromRGB(dt.Warning, 0.34)
							roleColor = toColor(dt.Warning)
						}
						ui.Column(c).MinWidth(0).Padding(10, 12).Gap(6).Radius(12).
							Background(cardBg).Border(1, borderCol).Children(func() {
							ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
								ui.Text(c, entry.headerLabel).FontSize(11).FontWeight(700).TextColor(roleColor)
								if entry.Streaming {
									AppleBadgePill(c, "[streaming...]", dt.Warning, dt)
								} else if entry.outcomeBadge != "" {
									ui.Text(c, entry.outcomeBadge).FontSize(11).FontWeight(600).TextColor(toColor(dt.InkMuted))
								}
								ui.Spacer(c)
								if entry.Collapsible {
									btnLabel := entry.collapseLabel
									if entry.Folded {
										btnLabel = entry.expandLabel
									}
									if ApplePillButton(c, entry.foldKey, btnLabel, BtnGlass, dt, nil, true).Clicked() {
										toggleRow = rowIdx
									}
								}
							})
							if entry.Collapsible && entry.Folded {
								ui.Text(c, entry.foldedSummary).FontSize(11.5).TextColor(toColor(dt.InkMuted))
							} else if len(entry.segments) > 0 {
								for sIdx := range entry.segments {
									seg := entry.segments[sIdx]
									if !seg.IsCode {
										ui.Text(c, seg.Content).FontSize(13).LineHeight(1.45).TextColor(toColor(dt.Ink))
										continue
									}
									// T3 Chat / DeepSeek GPU code-fence card with language badge + 1-click Copy.
									ui.Column(c).MinWidth(0).Padding(8, 10).Gap(5).Radius(10).
										Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
										ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
											AppleBadgePill(c, seg.Lang, dt.Info, dt)
											ui.Spacer(c)
											copyLbl := "Copy Code"
											copyVar := BtnGlass
											if lastCopied == seg.CopyKey {
												copyLbl = "Copied"
												copyVar = BtnSuccess
											}
											if ApplePillButton(c, seg.CopyKey, copyLbl, copyVar, dt, nil, true).Clicked() {
												copiedKey = seg.CopyKey
												copiedCode = seg.Content
											}
										})
										ui.Text(c, seg.Content).FontSize(12).LineHeight(1.4).TextColor(toColor(dt.Ink))
									})
								}
							} else if entry.Text != "" {
								ui.Text(c, entry.Text).FontSize(13).LineHeight(1.45).TextColor(toColor(dt.Ink))
							}
							if entry.ToolCard != nil && !entry.Folded {
								if entry.receiptBadge != "" {
									AppleBadgePill(c, entry.receiptBadge, dt.Accent, dt)
								}
								if entry.denialBadge != "" {
									AppleBadgePill(c, entry.denialBadge, dt.Warning, dt)
								}
							}
							if entry.cancelBadge != "" {
								ui.Text(c, entry.cancelBadge).FontSize(11).FontWeight(700).TextColor(toColor(dt.Warning))
							}
						})
					}
				}
			})
		}

		// Floating T3/DeepSeek "Jump to Latest" pill whenever user scrolled up from live stream.
		if !autoFollow && totalRows > 0 {
			ui.Row(c).Justify(ui.Center).Children(func() {
				if ApplePillButton(c, "transcript-jump-latest", "Jump to Latest", BtnTinted, dt, nil, true).Clicked() {
					jumpLatest = true
				}
			})
		}

		// 2-row T3 Chat / DeepSeek floating glass capsule composer.
		ui.Column(c).Gap(6).Padding(8, 10, 6, 12).Radius(20).
			Background(toColor(dt.SurfaceElevated)).
			Border(1, glassBorderColor(dt)).
			InsetShadow(0, 1, 0, 0, glassTopSpecular(dt)).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.TextInputBase(c.Key("transcript-composer"), &draft).
					Placeholder("Message Marlin agent...").
					FontSize(13.5).TextColor(toColor(dt.Ink)).Grow(1).Padding(4, 2)
			})
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				AppleBadgePill(c, modelLbl, dt.InkMuted, dt)
				thinkVar := BtnGlass
				if deepThink {
					thinkVar = BtnTinted
				}
				if ApplePillButton(c, "transcript-deep-think", "Deep Think", thinkVar, dt, svgIconSparkles, true).Clicked() {
					toggleThink = true
				}
				ui.Spacer(c)
				if inFlight && onCancel != nil {
					if ApplePillButton(c, "transcript-cancel", "Cancel Turn", BtnDestructive, dt, svgIconXMark, true).Clicked() {
						cancelClicked = true
					}
				}
				if AppleCircleIconButton(c, "transcript-send", "Send", svgIconArrowUp, 30, dt).Clicked() {
					trimmed := strings.TrimSpace(draft)
					if trimmed != "" {
						submitPrompt = trimmed
						draft = ""
					}
				}
			})
		})
	})

	tc.mu.Lock()
	tc.composerText = draft
	if jumpLatest {
		tc.autoFollow = true
		tc.scrollToBottomLocked()
	}
	if copiedKey != "" {
		tc.lastCopiedKey = copiedKey
	}
	copyCB := tc.OnCopyCode
	tc.mu.Unlock()

	if toggleThink {
		tc.SetDeepThink(!deepThink)
	}
	if copiedKey != "" && copyCB != nil {
		copyCB(copiedCode)
	}
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
	entry.refreshLabels()
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

func (tc *TranscriptController) recordSeenEventLocked(eventID string) {
	limit := tc.maxSeenEvents
	if limit <= 0 {
		limit = defaultMaxSeenEvents
	}
	if len(tc.seenRing) >= limit {
		evicted := tc.seenRing[tc.seenHead]
		delete(tc.seenEvents, evicted)
		tc.seenRing[tc.seenHead] = eventID
		tc.seenHead++
		if tc.seenHead >= len(tc.seenRing) {
			tc.seenHead = 0
		}
	} else {
		tc.seenRing = append(tc.seenRing, eventID)
	}
	tc.seenEvents[eventID] = struct{}{}
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
	lines := estimateWrappedVisualLines(entry.Text, cols)
	return transcriptHeaderPx + uint32(lines)*transcriptLinePx + extra
}

// estimateWrappedVisualLines counts wrapped lines in O(len(text)) with zero heap
// allocations using Unicode display column width (2 columns for CJK/East Asian
// wide runes, 0 columns for combining marks/format runes, 1 column otherwise)
// so long CJK responses do not clip.
func estimateWrappedVisualLines(text string, cols int) int {
	if cols <= 0 {
		cols = defaultTranscriptWrapCols
	}
	lines := 0
	colWidth := 0
	for _, r := range text {
		if r == '\n' {
			if colWidth == 0 {
				lines++
			} else {
				lines += (colWidth + cols - 1) / cols
				colWidth = 0
			}
			continue
		}
		colWidth += displayRuneWidth(r)
	}
	if colWidth == 0 {
		lines++
	} else {
		lines += (colWidth + cols - 1) / cols
	}
	if lines < 1 {
		return 1
	}
	return lines
}

// displayRuneWidth estimates terminal/monospace display columns (0 for combining
// marks & format runes; 2 for CJK, Hangul, Kana, and Fullwidth forms/punctuation;
// 1 otherwise) with zero allocations.
func displayRuneWidth(r rune) int {
	if r < 0x0300 {
		return 1
	}
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
		return 0
	}
	if r < 0x1100 {
		return 1
	}
	if (r >= 0x1100 && r <= 0x115F) || // Hangul Jamo
		(r >= 0x2E80 && r <= 0xA4CF) || // CJK Radicals, Punctuation (0x3000..0x303F), Kana, Bopomofo, Han
		(r >= 0xAC00 && r <= 0xD7A3) || // Hangul Syllables
		(r >= 0xF900 && r <= 0xFAFF) || // CJK Compatibility Ideographs
		(r >= 0xFE10 && r <= 0xFE6F) || // CJK Compatibility Forms
		(r >= 0xFF00 && r <= 0xFF60) || // Fullwidth Forms
		(r >= 0xFFE0 && r <= 0xFFE6) ||
		(r >= 0x20000 && r <= 0x3FFFD) { // CJK Extension B..I
		return 2
	}
	return 1
}
