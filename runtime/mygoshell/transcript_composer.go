// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/resilience"
	"github.com/tao3k/marlin-mygo/engine/richtext"
)

// AttachFile validates, SHA-256 hashes, decodes GPU image thumbnail once (if
// raster image), and stages one multimodal attachment with fail-closed checks.
func (tc *TranscriptController) AttachFile(in composer.AttachmentInput) (composer.AttachmentRef, error) {
	if tc.Composer == nil {
		tc.Composer = composer.NewState(composer.DefaultAttachmentPolicy())
	}
	ref, err := tc.Composer.AddAttachment(in)
	if err != nil {
		return composer.AttachmentRef{}, err
	}
	if ref.Kind == composer.AttachmentImage && tc.thumbs != nil {
		tc.thumbs.PutBytes(ref.SHA256Hex, in.Data)
	}
	return ref, nil
}

// StageLocalFiles reads local file paths with fail-closed byte caps, caches
// decoded GPU image thumbnails once, and stages each admitted attachment.
func (tc *TranscriptController) StageLocalFiles(paths []string) ([]composer.AttachmentRef, []error) {
	if tc.Composer == nil {
		tc.Composer = composer.NewState(composer.DefaultAttachmentPolicy())
	}
	refs, errs := composer.StageLocalFiles(tc.Composer, paths)
	if tc.thumbs != nil {
		for _, ref := range refs {
			if ref.Kind == composer.AttachmentImage {
				if staged, ok := tc.Composer.StagedByID(ref.ID); ok {
					tc.thumbs.PutBytes(ref.SHA256Hex, staged.Data)
				}
			}
		}
	}
	return refs, errs
}

// HandleDroppedTransfer ingests a native OS drag-and-drop transfer payload
// (FileList local files or raw PNG image bytes) through the fail-closed pipeline.
func (tc *TranscriptController) HandleDroppedTransfer(drop transfer.Drop) ([]composer.AttachmentRef, []error) {
	if files, err := drop.Data.Files(); err == nil && len(files) > 0 {
		return tc.StageLocalFiles(files)
	}
	if pngBytes, err := drop.Data.Read(transfer.PNG); err == nil && len(pngBytes) > 0 {
		ref, aErr := tc.AttachFile(composer.AttachmentInput{
			Filename: "dropped-image.png",
			MIMEType: "image/png",
			Data:     pngBytes,
		})
		if aErr != nil {
			return nil, []error{aErr}
		}
		return []composer.AttachmentRef{ref}, nil
	}
	return nil, nil
}

// CachedThumbnail returns the pre-decoded GPU ui.ImageSource for an attachment
// SHA-256 digest (or nil if non-image / not cached).
func (tc *TranscriptController) CachedThumbnail(sha256Hex string) ui.ImageSource {
	if tc == nil || tc.thumbs == nil {
		return nil
	}
	return tc.thumbs.Get(sha256Hex)
}

// AppendUserWithAttachments appends a user turn carrying multimodal attachment
// metadata badges into the virtualized transcript in O(log N).
func (tc *TranscriptController) AppendUserWithAttachments(text string, atts []composer.AttachmentRef) int {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	entry := TranscriptEntry{
		ID:          fmt.Sprintf("user-%d", len(tc.entries)+1),
		Role:        RoleUser,
		Text:        text,
		Attachments: append([]composer.AttachmentRef(nil), atts...),
	}
	return tc.appendLocked(entry)
}

// OpenAttachmentPreview opens the Tier-0 read-only Attachment Preview Lightbox.
// When OnParseRichDoc (backed by org-rz1 WASM or Rust) is wired and the file is
// `.org` or `.md`, its pre-parsed Rich Document IR is admitted once on open.
func (tc *TranscriptController) OpenAttachmentPreview(att composer.AttachmentRef) {
	cp := att
	tc.mu.RLock()
	parseFn := tc.OnParseRichDoc
	tc.mu.RUnlock()

	var blocks []richtext.Block
	if parseFn != nil && cp.PreviewText != "" && isRichMarkupFilename(cp.Name) {
		fmtKind := richtext.FormatMarkdown
		if strings.HasSuffix(strings.ToLower(cp.Name), ".org") {
			fmtKind = richtext.FormatOrg
		}
		if parsed, err := parseFn(fmtKind, "att-"+cp.ID, cp.PreviewText); err == nil {
			if admitted, aErr := richtext.AdmitBlocks("att-"+cp.ID, parsed); aErr == nil {
				blocks = admitted
			}
		}
	}

	tc.mu.Lock()
	tc.previewAtt = &cp
	tc.previewBlocks = blocks
	tc.mu.Unlock()
}

// CloseAttachmentPreview dismisses the Attachment Preview Lightbox.
func (tc *TranscriptController) CloseAttachmentPreview() {
	tc.mu.Lock()
	tc.previewAtt = nil
	tc.previewBlocks = nil
	tc.mu.Unlock()
}

// IsAttachmentPreviewOpen reports whether the Attachment Preview Lightbox is visible.
func (tc *TranscriptController) IsAttachmentPreviewOpen() bool {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.previewAtt != nil
}

// PreviewedAttachment returns the currently previewed attachment if open.
func (tc *TranscriptController) PreviewedAttachment() (composer.AttachmentRef, bool) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	if tc.previewAtt == nil {
		return composer.AttachmentRef{}, false
	}
	return *tc.previewAtt, true
}

// OpenModelPicker opens the interactive Model & Reasoning Effort picker popover.
func (tc *TranscriptController) OpenModelPicker() {
	tc.mu.Lock()
	tc.modelPickerOpen = true
	tc.mu.Unlock()
}

// CloseModelPicker closes the interactive Model & Reasoning Effort picker popover.
func (tc *TranscriptController) CloseModelPicker() {
	tc.mu.Lock()
	tc.modelPickerOpen = false
	tc.modelPickerQuery = ""
	tc.mu.Unlock()
}

// ToggleModelPicker toggles the Model & Reasoning Effort picker popover.
func (tc *TranscriptController) ToggleModelPicker() bool {
	tc.mu.Lock()
	tc.modelPickerOpen = !tc.modelPickerOpen
	if !tc.modelPickerOpen {
		tc.modelPickerQuery = ""
	}
	open := tc.modelPickerOpen
	tc.mu.Unlock()
	return open
}

// IsModelPickerOpen reports whether the Model & Reasoning Effort popover is open.
func (tc *TranscriptController) IsModelPickerOpen() bool {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.modelPickerOpen
}

// SetModelPickerQuery updates the model search filter string inside the picker.
func (tc *TranscriptController) SetModelPickerQuery(q string) {
	tc.mu.Lock()
	tc.modelPickerQuery = q
	tc.mu.Unlock()
}

// SelectModel activates a backend-advertised model by ID, syncs the composer
// badge label, closes the picker, and notifies OnModelChanged.
func (tc *TranscriptController) SelectModel(modelID string) (composer.ModelOption, bool) {
	if tc.Composer == nil {
		return composer.ModelOption{}, false
	}
	m, ok := tc.Composer.SetActiveModel(modelID)
	if !ok {
		return composer.ModelOption{}, false
	}
	tc.mu.Lock()
	tc.modelLabel = m.Label
	tc.modelPickerOpen = false
	tc.modelPickerQuery = ""
	cb := tc.OnModelChanged
	tc.mu.Unlock()
	if cb != nil {
		cb(m)
	}
	return m, true
}

// StartVoiceRecording transitions the VoiceController into live recording and
// invokes the platform microphone capture hook OnStartVoice if configured.
func (tc *TranscriptController) StartVoiceRecording() {
	if tc.Voice == nil {
		tc.Voice = composer.NewVoiceController()
	}
	tc.Voice.Start()
	tc.mu.RLock()
	cb := tc.OnStartVoice
	tc.mu.RUnlock()
	if cb != nil {
		cb()
	}
}

// StopVoiceRecording transitions the VoiceController into VoiceProcessing and
// invokes OnStopVoice so the platform audio adapter finalizes the audio buffer.
func (tc *TranscriptController) StopVoiceRecording() {
	if tc.Voice != nil {
		tc.Voice.BeginProcessing()
	}
	tc.mu.RLock()
	cb := tc.OnStopVoice
	tc.mu.RUnlock()
	if cb != nil {
		cb()
	}
}

// CancelVoiceRecording aborts live voice recording and notifies OnCancelVoice.
func (tc *TranscriptController) CancelVoiceRecording() {
	if tc.Voice != nil {
		tc.Voice.Cancel()
	}
	tc.mu.RLock()
	cb := tc.OnCancelVoice
	tc.mu.RUnlock()
	if cb != nil {
		cb()
	}
}

// CompleteVoiceAudio validates captured audio bytes (WAV/OGG/MP3/MP4/WebM) and
// stages them as an AttachmentAudio in the composer.
func (tc *TranscriptController) CompleteVoiceAudio(filename string, audioBytes []byte) (composer.AttachmentRef, error) {
	if tc.Voice == nil {
		tc.Voice = composer.NewVoiceController()
	}
	if tc.Composer == nil {
		tc.Composer = composer.NewState(composer.DefaultAttachmentPolicy())
	}
	return tc.Voice.CompleteWithAudio(filename, audioBytes, tc.Composer)
}

// OpenSearch opens the thin Cmd+F backend-delegated session search bar.
func (tc *TranscriptController) OpenSearch() {
	tc.mu.Lock()
	tc.searchOpen = true
	tc.mu.Unlock()
}

// CloseSearch dismisses the session search bar.
func (tc *TranscriptController) CloseSearch() {
	tc.mu.Lock()
	tc.searchOpen = false
	tc.mu.Unlock()
}

// IsSearchOpen reports whether the Cmd+F session search bar is visible.
func (tc *TranscriptController) IsSearchOpen() bool {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.searchOpen
}

// SetSearchQuery programmatically updates the search bar query and refreshes
// hits from OnSearchSession (jumping to the first hit via O(log N) JumpToRow).
func (tc *TranscriptController) SetSearchQuery(query string) {
	tc.mu.Lock()
	tc.searchOpen = true
	tc.searchQuery = query
	tc.searchLastQuery = ""
	searchFn := tc.OnSearchSession
	tc.mu.Unlock()
	if searchFn != nil {
		tc.runSearchQuery(query, searchFn)
	}
}

func (tc *TranscriptController) runSearchQuery(query string, searchFn func(string) []SessionSearchHit) {
	q := strings.TrimSpace(query)
	var hits []SessionSearchHit
	if q != "" && searchFn != nil {
		hits = searchFn(q)
	}
	tc.mu.Lock()
	tc.searchLastQuery = query
	tc.searchHits = hits
	tc.searchHitIdx = 0
	tc.mu.Unlock()
	if len(hits) > 0 {
		tc.JumpToRow(hits[0].Row)
	}
}

// StepSearchHit advances (+1) or rewinds (-1) through backend search hits and
// jumps directly to the targeted row via JumpToRow.
func (tc *TranscriptController) StepSearchHit(delta int) {
	tc.mu.Lock()
	n := len(tc.searchHits)
	if n == 0 {
		tc.mu.Unlock()
		return
	}
	tc.searchHitIdx = (tc.searchHitIdx + delta%n + n) % n
	row := tc.searchHits[tc.searchHitIdx].Row
	tc.mu.Unlock()
	tc.JumpToRow(row)
}

// SetContextUsage updates the backend-reported prompt/session token budget pill.
func (tc *TranscriptController) SetContextUsage(usedTokens, maxTokens int) {
	if usedTokens < 0 {
		usedTokens = 0
	}
	if maxTokens < 0 {
		maxTokens = 0
	}
	tc.mu.Lock()
	tc.contextUsage = composer.ContextUsage{UsedTokens: usedTokens, MaxTokens: maxTokens}
	tc.mu.Unlock()
}

// ContextUsage returns the current backend-reported context window utilization.
func (tc *TranscriptController) ContextUsage() composer.ContextUsage {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.contextUsage
}

// StreamMetricsLabel returns the formatted live or frozen turn throughput badge
// (e.g. "48 tok/s · 2.4s") or empty string before any streamed checkpoint arrives.
func (tc *TranscriptController) StreamMetricsLabel() string {
	tc.mu.RLock()
	sm := tc.streamMetrics
	tc.mu.RUnlock()
	return sm.Label()
}

// ToggleRichHeadingFold toggles outline folding for one pre-parsed Org/Markdown
// heading key inside the transcript or Lightbox preview.
func (tc *TranscriptController) ToggleRichHeadingFold(headingKey string) bool {
	if headingKey == "" {
		return false
	}
	tc.mu.Lock()
	if tc.richFolded == nil {
		tc.richFolded = make(map[string]bool)
	}
	next := !tc.richFolded[headingKey]
	if next {
		tc.richFolded[headingKey] = true
	} else {
		delete(tc.richFolded, headingKey)
	}
	tc.mu.Unlock()
	return next
}

// ToggleRichChecklistItem updates one admitted checklist item state in-place for
// immediate 120Hz feedback and invokes OnToggleRichCheck so the backend owns persistence.
func (tc *TranscriptController) ToggleRichChecklistItem(row, blockIdx, itemIdx int, nextCheck string) bool {
	tc.mu.Lock()
	if row < 0 || row >= len(tc.entries) {
		tc.mu.Unlock()
		return false
	}
	blocks := tc.entries[row].blocks
	if blockIdx < 0 || blockIdx >= len(blocks) || itemIdx < 0 || itemIdx >= len(blocks[blockIdx].Items) {
		tc.mu.Unlock()
		return false
	}
	blocks[blockIdx].Items[itemIdx].Check = nextCheck
	entryID := tc.entries[row].ID
	cb := tc.OnToggleRichCheck
	tc.mu.Unlock()
	if cb != nil {
		cb(entryID, blockIdx, itemIdx, nextCheck)
	}
	return true
}

// RestoreDraftFromResilience binds tc.ResilienceScope and restores any
// crash-persisted unsent composer text straight back into the chat input box.
func (tc *TranscriptController) RestoreDraftFromResilience(scopeID string) string {
	tc.mu.Lock()
	if scopeID != "" {
		tc.ResilienceScope = scopeID
	}
	scope := tc.ResilienceScope
	if scope == "" {
		scope = "chat:default"
	}
	res := tc.Resilience
	tc.mu.Unlock()
	if res == nil {
		return ""
	}
	saved := res.Draft(scope)
	if saved != "" {
		tc.mu.Lock()
		tc.composerText = saved
		tc.composerRev++
		tc.mu.Unlock()
	}
	if tc.Composer != nil {
		for _, dm := range res.DraftMentions(scope) {
			tc.Composer.AddMention(composer.ContextMention{
				Kind:   dm.Kind,
				Target: dm.Target,
				Label:  dm.Label,
			})
		}
	}
	return saved
}

// SyncDraftMentionsToResilience persists current composer @ context mentions
// alongside the active draft scope and triggers OnDraftChanged so crash
// recovery restores both unsent text and attached context mentions.
func (tc *TranscriptController) SyncDraftMentionsToResilience() {
	tc.mu.RLock()
	res := tc.Resilience
	scope := tc.ResilienceScope
	if scope == "" {
		scope = "chat:default"
	}
	draft := tc.composerText
	cb := tc.OnDraftChanged
	comp := tc.Composer
	tc.mu.RUnlock()
	if res == nil || comp == nil {
		return
	}
	cur := comp.Snapshot().Mentions
	out := make([]resilience.DraftMention, 0, len(cur))
	for _, m := range cur {
		out = append(out, resilience.DraftMention{
			Kind:   m.Kind,
			Target: m.Target,
			Label:  m.Label,
		})
	}
	res.SetDraftMentions(scope, out)
	if cb != nil {
		cb(scope, draft)
	}
}
