// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"sync"

	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/richtext"
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
	Attachments  []composer.AttachmentRef

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
	blocks        []richtext.Block
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
	Composer      *composer.State
	Voice         *composer.VoiceController
	contextUsage  composer.ContextUsage
	streamMetrics *composer.StreamMetrics

	// Thin Backend-Delegated Callbacks (Rust owns search index, context mentions, and CAS upload).
	OnCopyCode        func(code string)
	OnDeepThink       func(enabled bool)
	OnSubmitTurn      func(sub composer.TurnSubmission)
	OnSlashCommand    func(cmd composer.SlashCommand)
	OnQueryMentions   func(query string) []composer.ContextMention
	OnPickAttachment  func()
	OnPasteAttachment func()
	OnStartVoice      func()
	OnStopVoice       func()
	OnCancelVoice     func()
	OnModelChanged    func(model composer.ModelOption)
	OnSearchSession   func(query string) []SessionSearchHit
	OnRetryTurn       func(turnOrdinal uint64)
	OnForkTurn        func(turnOrdinal uint64)
	// Optional external parser hook backed by org-rz1 WASM or the Rust backend
	// for rendering rich Org-mode / Markdown previews without Go-side parsing.
	OnParseRichDoc func(format richtext.Format, docID, source string) ([]richtext.Block, error)

	// Thin Cmd+F Find-in-Session, Model Picker, and Attachment Preview Lightbox state.
	previewAtt       *composer.AttachmentRef
	previewBlocks    []richtext.Block
	thumbs           *AttachmentThumbCache
	modelPickerOpen  bool
	modelPickerQuery string

	// Thin Cmd+F Session Search UI state (hits supplied exclusively by OnSearchSession).
	searchOpen      bool
	searchQuery     string
	searchLastQuery string
	searchHits      []SessionSearchHit
	searchHitIdx    int

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
		Composer:      composer.NewState(composer.DefaultAttachmentPolicy()),
		Voice:         composer.NewVoiceController(),
		streamMetrics: &composer.StreamMetrics{},
		thumbs:        NewAttachmentThumbCache(32),
	}
}

// SetAllThinkingFolded folds or unfolds every collapsible RoleThinking block
// in the transcript while preserving ScrollAnchor zero-drift invariants.
func (tc *TranscriptController) SetAllThinkingFolded(folded bool) int {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	changed := 0
	for i := range tc.entries {
		if tc.entries[i].Role == RoleThinking && tc.entries[i].Collapsible && tc.entries[i].Folded != folded {
			tc.entries[i].Folded = folded
			tc.updateRowHeightLocked(i)
			changed++
		}
	}
	return changed
}

// AppendAssistantRichBlocks admits pre-parsed Org-mode or Markdown Document IR
// (emitted by org-rz1 WASM or the Rust backend) directly into the virtualized
// transcript with zero Go-side parsing and O(log N) Fenwick height tracking.
func (tc *TranscriptController) AppendAssistantRichBlocks(
	turnOrdinal uint64,
	title string,
	blocks []richtext.Block,
	outcomeLabel string,
) (int, error) {
	id := fmt.Sprintf("turn-%d", turnOrdinal)
	admitted, err := richtext.AdmitBlocks(id, blocks)
	if err != nil {
		return -1, err
	}
	if title == "" {
		title = fmt.Sprintf("Turn #%d", turnOrdinal)
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	row := tc.appendLocked(TranscriptEntry{
		ID:           id,
		Role:         RoleAssistant,
		Title:        title,
		TurnOrdinal:  turnOrdinal,
		OutcomeLabel: outcomeLabel,
		blocks:       admitted,
	})
	tc.turnRow[turnOrdinal] = row
	return row, nil
}

// ApplyRichBlocks attaches pre-parsed Document IR (from org-rz1 WASM or Rust)
// to an existing transcript row and updates its Fenwick height in O(log N).
func (tc *TranscriptController) ApplyRichBlocks(row int, blocks []richtext.Block) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if row < 0 || row >= len(tc.entries) {
		return fmt.Errorf("transcript: row %d out of range", row)
	}
	admitted, err := richtext.AdmitBlocks(tc.entries[row].ID, blocks)
	if err != nil {
		return err
	}
	tc.entries[row].blocks = admitted
	tc.updateRowHeightLocked(row)
	return nil
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
	if tc.streamMetrics != nil {
		tc.streamMetrics.Finish()
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
	if tc.streamMetrics != nil {
		tc.streamMetrics.Begin()
	}
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
	if tc.streamMetrics != nil {
		tc.streamMetrics.RecordDelta(textDelta)
	}
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
	if tc.streamMetrics != nil {
		tc.streamMetrics.Finish()
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
	if len(entry.blocks) > 0 {
		extra += estimateRichBlockExtraPx(entry.blocks)
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
