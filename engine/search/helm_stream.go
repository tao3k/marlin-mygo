// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package search implements Marlin's 120Hz zero-allocation streaming Helm /
// Minibuffer candidate engine. The Rust backend owns authoritative indexing
// (ripgrep, Tantivy, org-roam, LSP symbols, sessions) and streams ranked
// batches into HelmEngine; the frontend enforces fail-closed capacity bounds,
// O(1) stale query-generation preemption, multi-source grouping, Follow-Mode
// preview coalescing, multi-mark batch actions, and 0-alloc viewport slicing
// over 100,000+ live candidates.
package search

import (
	"sort"
	"strings"
	"sync"
)

const (
	DefaultMaxCandidates = 100_000
	HardMaxCandidates    = 250_000
	DefaultViewportRows  = 14
)

// TargetAction selects how a confirmed Helm/Minibuffer candidate (or marked
// batch) is dispatched without destructively mutating the user's WindowTree.
type TargetAction string

const (
	ActionOpenInFocused     TargetAction = "open_focused"
	ActionOpenSplitRight    TargetAction = "split_right"
	ActionOpenInNewTab      TargetAction = "new_tab"
	ActionMentionInComposer TargetAction = "mention_composer"
	ActionExecuteCommand    TargetAction = "execute_command"
)

// DockMode selects whether the Minibuffer renders as a floating glass command
// deck or a non-destructive bottom-docked Emacs/Helm drawer.
type DockMode string

const (
	DockOverlay DockMode = "overlay"
	DockBottom  DockMode = "bottom"
)

// SourceMeta describes one logical Helm candidate source group (e.g. Buffers,
// Files, Symbols, Org Headings, Transcript, Commands).
type SourceMeta struct {
	ID       string
	Label    string
	Priority int // lower values appear first
}

// Candidate is one streamed search hit admitted from Rust or local host tables.
type Candidate struct {
	ID            string
	SourceID      string
	Title         string
	Subtitle      string
	Location      string
	Snippet       string
	Score         int32
	MatchMask     uint64 // bitmask of matched character positions in Title
	PayloadKind   string // "buffer" | "file" | "symbol" | "org" | "turn" | "command"
	PayloadTarget string
}

// DisplayRow maps a virtual flat row index in [0, len(rows)) to either a
// source group header or one concrete Candidate inside a source bucket.
type DisplayRow struct {
	IsHeader    bool
	SourceIdx   int
	CandIdx     int
	SourceCount int
}

// VisibleRow is the lightweight value copied into caller-owned scratch slices
// during 120Hz GPU rendering (zero heap allocation on steady frames).
type VisibleRow struct {
	FlatIndex   int
	IsHeader    bool
	SourceID    string
	SourceLabel string
	SourceCount int
	Candidate   Candidate
	Selected    bool
	Marked      bool
}

type sourceBucket struct {
	meta      SourceMeta
	items     []Candidate
	headerRow int // cached flat row index of this source's header
}

// HelmEngine coordinates concurrent streaming batches from Rust search engines
// with lock-protected, zero-allocation 120Hz UI navigation and viewport slicing.
type HelmEngine struct {
	mu sync.RWMutex

	maxCandidates int
	viewportRows  int
	dockMode      DockMode
	previewOpen   bool

	gen          uint64
	query        string
	activeFilter string // optional single-source filter ("" = all sources)

	sources    []sourceBucket
	sourceByID map[string]int
	totalHits  int

	rows        []DisplayRow
	rowsDirty   bool
	selectedRow int // flat index in rows (-1 when empty)
	scrollRow   int // top visible flat row index

	marked map[string]struct{}

	// Follow-mode live preview state (coalesced & generation-checked).
	previewCandID string
	previewTitle  string
	previewBody   string
	previewGen    uint64

	// Telemetry counters for observability & stress verification.
	staleDrops uint64
	cappedDrop uint64
}

// NewHelmEngine creates a bounded, zero-alloc-ready Helm streaming engine.
func NewHelmEngine(maxCandidates int) *HelmEngine {
	if maxCandidates <= 0 {
		maxCandidates = DefaultMaxCandidates
	}
	if maxCandidates > HardMaxCandidates {
		maxCandidates = HardMaxCandidates
	}
	return &HelmEngine{
		maxCandidates: maxCandidates,
		viewportRows:  DefaultViewportRows,
		dockMode:      DockOverlay,
		previewOpen:   true,
		sourceByID:    make(map[string]int, 8),
		marked:        make(map[string]struct{}, 16),
		selectedRow:   -1,
	}
}

// BeginQuery starts a new search generation, preempting any in-flight batches
// from earlier keystrokes in O(1) while reusing underlying slice capacity.
func (e *HelmEngine) BeginQuery(query string) uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.gen++
	e.query = query
	e.totalHits = 0
	for i := range e.sources {
		e.sources[i].items = e.sources[i].items[:0]
		e.sources[i].headerRow = -1
	}
	e.rows = e.rows[:0]
	e.rowsDirty = false
	e.selectedRow = -1
	e.scrollRow = 0
	clear(e.marked)
	e.previewCandID = ""
	e.previewTitle = ""
	e.previewBody = ""
	e.previewGen = e.gen
	return e.gen
}

// Generation returns the active query generation counter.
func (e *HelmEngine) Generation() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.gen
}

// Query returns the current search input string.
func (e *HelmEngine) Query() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.query
}

// SetViewportRows sets how many candidate rows fit in the GPU viewport.
func (e *HelmEngine) SetViewportRows(n int) {
	if n < 1 {
		n = 1
	}
	if n > 200 {
		n = 200
	}
	e.mu.Lock()
	e.viewportRows = n
	e.clampScrollLocked()
	e.mu.Unlock()
}

// SetSourceFilter restricts visible rows to one source ID ("" shows all).
func (e *HelmEngine) SetSourceFilter(sourceID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	sourceID = strings.TrimSpace(sourceID)
	if e.activeFilter == sourceID {
		return
	}
	e.activeFilter = sourceID
	e.rowsDirty = true
	e.selectedRow = -1
	e.scrollRow = 0
	e.ensureRowsLocked()
}

// ActiveSourceFilter returns the currently pinned source filter ID (or "").
func (e *HelmEngine) ActiveSourceFilter() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeFilter
}

// ToggleDockMode flips between floating Overlay and non-destructive BottomDock.
func (e *HelmEngine) ToggleDockMode() DockMode {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dockMode == DockOverlay {
		e.dockMode = DockBottom
	} else {
		e.dockMode = DockOverlay
	}
	return e.dockMode
}

// DockMode returns the current Minibuffer presentation mode.
func (e *HelmEngine) DockMode() DockMode {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.dockMode
}

// TogglePreview toggles the Helm Follow-Mode split preview pane.
func (e *HelmEngine) TogglePreview() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.previewOpen = !e.previewOpen
	return e.previewOpen
}

// PreviewOpen reports whether the live split preview pane is enabled.
func (e *HelmEngine) PreviewOpen() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.previewOpen
}

// IngestBatch admits one streamed batch of search hits for query generation gen.
// If gen does not match the active generation, the entire batch is discarded in
// O(1) with zero heap allocations (`stale=true`).
func (e *HelmEngine) IngestBatch(gen uint64, source SourceMeta, batch []Candidate) (admitted int, stale bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if gen != e.gen {
		e.staleDrops++
		return 0, true
	}
	if len(batch) == 0 {
		return 0, false
	}
	if strings.TrimSpace(source.ID) == "" {
		source.ID = "default"
	}
	if strings.TrimSpace(source.Label) == "" {
		source.Label = source.ID
	}

	idx, ok := e.sourceByID[source.ID]
	if !ok {
		idx = len(e.sources)
		e.sources = append(e.sources, sourceBucket{
			meta:      source,
			items:     make([]Candidate, 0, min(len(batch), 256)),
			headerRow: -1,
		})
		e.sourceByID[source.ID] = idx
	} else {
		e.sources[idx].meta = source
	}

	remaining := e.maxCandidates - e.totalHits
	if remaining <= 0 {
		e.cappedDrop += uint64(len(batch))
		return 0, false
	}
	take := len(batch)
	if take > remaining {
		e.cappedDrop += uint64(take - remaining)
		take = remaining
	}

	b := &e.sources[idx]
	start := len(b.items)
	b.items = append(b.items, batch[:take]...)
	for i := start; i < len(b.items); i++ {
		if b.items[i].SourceID == "" {
			b.items[i].SourceID = source.ID
		}
	}
	e.totalHits += take
	e.rowsDirty = true
	return take, false
}

// Materialize eagerly rebuilds the flat virtualized row index so subsequent
// 120Hz VisibleWindow / MoveSelection / ScrollBy calls run in O(1) with zero allocs.
func (e *HelmEngine) Materialize() {
	e.mu.Lock()
	e.ensureRowsLocked()
	e.mu.Unlock()
}

func (e *HelmEngine) ensureRowsLocked() {
	if !e.rowsDirty {
		return
	}
	e.rowsDirty = false

	// Order non-empty sources deterministically by Priority then ID.
	order := make([]int, 0, len(e.sources))
	for i := range e.sources {
		e.sources[i].headerRow = -1
		if len(e.sources[i].items) == 0 {
			continue
		}
		if e.activeFilter != "" && e.sources[i].meta.ID != e.activeFilter {
			continue
		}
		order = append(order, i)
	}
	sort.Slice(order, func(a, b int) bool {
		sa, sb := e.sources[order[a]].meta, e.sources[order[b]].meta
		if sa.Priority != sb.Priority {
			return sa.Priority < sb.Priority
		}
		return sa.ID < sb.ID
	})

	needed := 0
	for _, sIdx := range order {
		needed += 1 + len(e.sources[sIdx].items)
	}
	if cap(e.rows) < needed {
		e.rows = make([]DisplayRow, 0, needed)
	} else {
		e.rows = e.rows[:0]
	}

	for _, sIdx := range order {
		n := len(e.sources[sIdx].items)
		e.sources[sIdx].headerRow = len(e.rows)
		e.rows = append(e.rows, DisplayRow{
			IsHeader:    true,
			SourceIdx:   sIdx,
			CandIdx:     -1,
			SourceCount: n,
		})
		for cIdx := 0; cIdx < n; cIdx++ {
			e.rows = append(e.rows, DisplayRow{
				IsHeader:  false,
				SourceIdx: sIdx,
				CandIdx:   cIdx,
			})
		}
	}

	// Ensure selectedRow always points to a valid non-header candidate if any exist.
	if len(e.rows) == 0 {
		e.selectedRow = -1
		e.scrollRow = 0
		return
	}
	if e.selectedRow < 0 || e.selectedRow >= len(e.rows) || e.rows[e.selectedRow].IsHeader {
		e.selectedRow = e.nextCandidateRowLocked(0, +1)
	}
	e.clampScrollLocked()
}

func (e *HelmEngine) nextCandidateRowLocked(from int, dir int) int {
	n := len(e.rows)
	if n == 0 {
		return -1
	}
	if dir >= 0 {
		if from < 0 {
			from = 0
		}
		for i := from; i < n; i++ {
			if !e.rows[i].IsHeader {
				return i
			}
		}
		// Wrap around to first candidate.
		for i := 0; i < from && i < n; i++ {
			if !e.rows[i].IsHeader {
				return i
			}
		}
		return -1
	}
	if from >= n {
		from = n - 1
	}
	for i := from; i >= 0; i-- {
		if !e.rows[i].IsHeader {
			return i
		}
	}
	// Wrap around to last candidate.
	for i := n - 1; i > from && i >= 0; i-- {
		if !e.rows[i].IsHeader {
			return i
		}
	}
	return -1
}

func (e *HelmEngine) clampScrollLocked() {
	n := len(e.rows)
	if n == 0 || e.viewportRows <= 0 {
		e.scrollRow = 0
		return
	}
	maxScroll := n - e.viewportRows
	if maxScroll < 0 {
		maxScroll = 0
	}
	if e.scrollRow > maxScroll {
		e.scrollRow = maxScroll
	}
	if e.scrollRow < 0 {
		e.scrollRow = 0
	}
	if e.selectedRow >= 0 {
		if e.selectedRow < e.scrollRow {
			e.scrollRow = e.selectedRow
			if e.scrollRow > 0 && e.rows[e.scrollRow-1].IsHeader {
				e.scrollRow--
			}
		} else if e.selectedRow >= e.scrollRow+e.viewportRows {
			e.scrollRow = e.selectedRow - e.viewportRows + 1
		}
		if e.scrollRow > maxScroll {
			e.scrollRow = maxScroll
		}
		if e.scrollRow < 0 {
			e.scrollRow = 0
		}
	}
}

// MoveSelection moves the highlight by delta candidate rows (skipping group
// headers cleanly and wrapping at ends) in O(|delta|) with 0 allocations.
func (e *HelmEngine) MoveSelection(delta int) (Candidate, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ensureRowsLocked()
	if len(e.rows) == 0 {
		return Candidate{}, false
	}
	if delta == 0 {
		return e.selectedCandidateLocked()
	}
	dir := 1
	steps := delta
	if delta < 0 {
		dir = -1
		steps = -delta
	}
	cur := e.selectedRow
	if cur < 0 {
		cur = 0
	}
	for s := 0; s < steps; s++ {
		next := e.nextCandidateRowLocked(cur+dir, dir)
		if next < 0 {
			break
		}
		cur = next
	}
	e.selectedRow = cur
	e.clampScrollLocked()
	return e.selectedCandidateLocked()
}

// JumpSource jumps immediately to the first candidate of the next (+1) or
// previous (-1) source group (Helm C-o / M-n / M-p behavior) with 0 allocs.
func (e *HelmEngine) JumpSource(delta int) (Candidate, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ensureRowsLocked()
	if len(e.rows) == 0 || e.selectedRow < 0 {
		return Candidate{}, false
	}
	curSrc := e.rows[e.selectedRow].SourceIdx
	if delta >= 0 {
		// Find next header after selectedRow belonging to a different source.
		for i := e.selectedRow + 1; i < len(e.rows); i++ {
			if e.rows[i].IsHeader && e.rows[i].SourceIdx != curSrc {
				e.selectedRow = e.nextCandidateRowLocked(i+1, +1)
				e.clampScrollLocked()
				return e.selectedCandidateLocked()
			}
		}
		// Wrap to first source.
		e.selectedRow = e.nextCandidateRowLocked(0, +1)
		e.clampScrollLocked()
		return e.selectedCandidateLocked()
	}
	// Find previous header before current source's header.
	curHdr := e.sources[curSrc].headerRow
	for i := curHdr - 1; i >= 0; i-- {
		if e.rows[i].IsHeader {
			e.selectedRow = e.nextCandidateRowLocked(i+1, +1)
			e.clampScrollLocked()
			return e.selectedCandidateLocked()
		}
	}
	// Wrap to last source's header.
	for i := len(e.rows) - 1; i >= 0; i-- {
		if e.rows[i].IsHeader {
			e.selectedRow = e.nextCandidateRowLocked(i+1, +1)
			e.clampScrollLocked()
			return e.selectedCandidateLocked()
		}
	}
	return e.selectedCandidateLocked()
}

// ScrollBy shifts the virtual viewport window by deltaRows in O(1), 0 allocs.
func (e *HelmEngine) ScrollBy(deltaRows int) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ensureRowsLocked()
	n := len(e.rows)
	if n == 0 {
		e.scrollRow = 0
		return 0
	}
	maxScroll := n - e.viewportRows
	if maxScroll < 0 {
		maxScroll = 0
	}
	e.scrollRow += deltaRows
	if e.scrollRow < 0 {
		e.scrollRow = 0
	}
	if e.scrollRow > maxScroll {
		e.scrollRow = maxScroll
	}
	return e.scrollRow
}

// SelectFlatRow selects a clicked flat row index if it is a candidate row.
func (e *HelmEngine) SelectFlatRow(flatIdx int) (Candidate, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ensureRowsLocked()
	if flatIdx < 0 || flatIdx >= len(e.rows) || e.rows[flatIdx].IsHeader {
		return Candidate{}, false
	}
	e.selectedRow = flatIdx
	e.clampScrollLocked()
	return e.selectedCandidateLocked()
}

// Selected returns the currently highlighted candidate (if any).
func (e *HelmEngine) Selected() (Candidate, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.rowsDirty {
		e.mu.RUnlock()
		e.mu.Lock()
		e.ensureRowsLocked()
		cand, ok := e.selectedCandidateLocked()
		e.mu.Unlock()
		e.mu.RLock()
		return cand, ok
	}
	return e.selectedCandidateLocked()
}

func (e *HelmEngine) selectedCandidateLocked() (Candidate, bool) {
	if e.selectedRow < 0 || e.selectedRow >= len(e.rows) {
		return Candidate{}, false
	}
	r := e.rows[e.selectedRow]
	if r.IsHeader || r.SourceIdx < 0 || r.SourceIdx >= len(e.sources) {
		return Candidate{}, false
	}
	items := e.sources[r.SourceIdx].items
	if r.CandIdx < 0 || r.CandIdx >= len(items) {
		return Candidate{}, false
	}
	return items[r.CandIdx], true
}

// ToggleMarkCurrent toggles Helm multi-selection mark on the selected item and
// advances selection by +1 row (classic Helm C-SPC / TAB workflow).
func (e *HelmEngine) ToggleMarkCurrent() (markedNow bool, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ensureRowsLocked()
	cand, found := e.selectedCandidateLocked()
	if !found || cand.ID == "" {
		return false, false
	}
	if _, exists := e.marked[cand.ID]; exists {
		delete(e.marked, cand.ID)
		markedNow = false
	} else {
		e.marked[cand.ID] = struct{}{}
		markedNow = true
	}
	next := e.nextCandidateRowLocked(e.selectedRow+1, +1)
	if next >= 0 {
		e.selectedRow = next
		e.clampScrollLocked()
	}
	return markedNow, true
}

// MarkedCount returns how many candidates are currently multi-marked.
func (e *HelmEngine) MarkedCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.marked)
}

// ResolveActionTargets returns all marked candidates (or the single selected
// candidate when nothing is marked) into dst without extra allocations.
func (e *HelmEngine) ResolveActionTargets(dst []Candidate) []Candidate {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ensureRowsLocked()
	dst = dst[:0]
	if len(e.marked) == 0 {
		if cand, ok := e.selectedCandidateLocked(); ok {
			dst = append(dst, cand)
		}
		return dst
	}
	for _, r := range e.rows {
		if r.IsHeader {
			continue
		}
		cand := e.sources[r.SourceIdx].items[r.CandIdx]
		if _, ok := e.marked[cand.ID]; ok {
			dst = append(dst, cand)
		}
	}
	return dst
}

// UpdatePreview stores a Follow-Mode preview payload if gen matches the active
// query generation, preventing stale async preview flashes.
func (e *HelmEngine) UpdatePreview(gen uint64, candID, title, body string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if gen != e.gen {
		return false
	}
	e.previewCandID = candID
	e.previewTitle = title
	e.previewBody = body
	e.previewGen = gen
	return true
}

// Preview returns the active Follow-Mode preview title and body.
func (e *HelmEngine) Preview() (candID, title, body string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.previewCandID, e.previewTitle, e.previewBody
}

// SourceSummary summarizes one active source tab/pill in the Helm header.
type SourceSummary struct {
	ID    string
	Label string
	Count int
}

// SourceSummaries writes active source counts into dst for zero-alloc header pills.
func (e *HelmEngine) SourceSummaries(dst []SourceSummary) []SourceSummary {
	e.mu.RLock()
	defer e.mu.RUnlock()
	dst = dst[:0]
	for i := range e.sources {
		if len(e.sources[i].items) == 0 {
			continue
		}
		dst = append(dst, SourceSummary{
			ID:    e.sources[i].meta.ID,
			Label: e.sources[i].meta.Label,
			Count: len(e.sources[i].items),
		})
	}
	return dst
}

// Stats returns live engine telemetry (totalHits, staleDrops, cappedDrops, gen).
func (e *HelmEngine) Stats() (totalHits int, staleDrops uint64, cappedDrops uint64, gen uint64) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.totalHits, e.staleDrops, e.cappedDrop, e.gen
}

// VisibleWindow fills dst with ONLY the viewportRows currently visible rows
// starting at scrollRow. When rows are materialized and dst has capacity >=
// viewportRows, this executes in O(viewportRows) with ZERO heap allocations
// even when holding 100,000+ streamed candidates.
func (e *HelmEngine) VisibleWindow(dst []VisibleRow) (out []VisibleRow, totalRows int, totalHits int, scrollRow int, selectedRow int, gen uint64) {
	e.mu.RLock()
	if e.rowsDirty {
		e.mu.RUnlock()
		e.mu.Lock()
		e.ensureRowsLocked()
		e.mu.Unlock()
		e.mu.RLock()
	}
	defer e.mu.RUnlock()

	dst = dst[:0]
	n := len(e.rows)
	if n == 0 {
		return dst, 0, e.totalHits, 0, -1, e.gen
	}
	start := e.scrollRow
	if start < 0 {
		start = 0
	}
	end := start + e.viewportRows
	if end > n {
		end = n
	}
	hasMarks := len(e.marked) > 0
	for i := start; i < end; i++ {
		dr := e.rows[i]
		src := &e.sources[dr.SourceIdx]
		vr := VisibleRow{
			FlatIndex:   i,
			IsHeader:    dr.IsHeader,
			SourceID:    src.meta.ID,
			SourceLabel: src.meta.Label,
			SourceCount: dr.SourceCount,
			Selected:    i == e.selectedRow,
		}
		if !dr.IsHeader {
			vr.Candidate = src.items[dr.CandIdx]
			if hasMarks {
				_, vr.Marked = e.marked[vr.Candidate.ID]
			}
		}
		dst = append(dst, vr)
	}
	return dst, n, e.totalHits, start, e.selectedRow, e.gen
}
