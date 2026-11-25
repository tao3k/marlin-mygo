// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package composer

import (
	"fmt"
	"strings"
	"sync"
)

// ReasoningEffort selects the model's reasoning compute budget.
type ReasoningEffort string

const (
	EffortFast     ReasoningEffort = "fast"
	EffortBalanced ReasoningEffort = "balanced"
	EffortDeep     ReasoningEffort = "deep"
)

// ModelOption describes one backend-advertised model selectable in the composer.
type ModelOption struct {
	ID             string
	Label          string
	Provider       string
	ContextLabel   string
	SupportsVision bool
	SupportsAudio  bool
	SupportsThink  bool
}

// SlashCommand describes one inline `/` command available in the composer popup.
type SlashCommand struct {
	Name        string // e.g. "/plan", "/review", "/auth"
	Description string
	ActionID    string // host command or prompt template identifier
}

// ContextMention represents one structured `@` context item attached to a turn
// (e.g. @file:control/grpcwire/client.go, @git:diff, @session:current).
type ContextMention struct {
	Kind   string // "file", "symbol", "git", "session", "docs"
	Target string
	Label  string
}

// Key returns the canonical deduplication key for a mention.
func (m ContextMention) Key() string {
	return m.Kind + ":" + m.Target
}

// TurnSubmission is the complete typed payload emitted when the user presses Send.
type TurnSubmission struct {
	Prompt          string
	Attachments     []StagedAttachment
	Mentions        []ContextMention
	ModelID         string
	ReasoningEffort ReasoningEffort
}

// ComposerSnapshot is an immutable view safe for 120Hz UI rendering.
type ComposerSnapshot struct {
	Attachments     []AttachmentRef
	TotalBytes      int
	Mentions        []ContextMention
	ActiveModel     ModelOption
	Models          []ModelOption
	ReasoningEffort ReasoningEffort
	LastError       string
}

// State owns the multimodal composer's staged attachments, structured mentions,
// slash command catalog, and model/reasoning selection.
type State struct {
	policy AttachmentPolicy

	mu            sync.RWMutex
	attachments   []StagedAttachment
	totalBytes    int
	mentions      []ContextMention
	models        []ModelOption
	activeModel   int
	effort        ReasoningEffort
	slashCommands []SlashCommand
	lastError     string
}

// NewState initializes a composer State with sensible defaults.
func NewState(policy AttachmentPolicy) *State {
	p := policy.withDefaults()
	return &State{
		policy: p,
		effort: EffortBalanced,
		models: []ModelOption{
			{ID: "marlin-core", Label: "Marlin Core", Provider: "Marlin", ContextLabel: "200K", SupportsVision: true, SupportsAudio: true, SupportsThink: true},
			{ID: "marlin-fast", Label: "Marlin Fast", Provider: "Marlin", ContextLabel: "128K", SupportsVision: true, SupportsThink: false},
			{ID: "marlin-pro", Label: "Marlin Pro", Provider: "Marlin", ContextLabel: "1M", SupportsVision: true, SupportsAudio: true, SupportsThink: true},
		},
		slashCommands: DefaultSlashCommands(),
	}
}

// DefaultSlashCommands returns the canonical built-in composer `/` commands.
func DefaultSlashCommands() []SlashCommand {
	return []SlashCommand{
		{Name: "/plan", Description: "Draft a step-by-step architecture plan", ActionID: "prompt:plan"},
		{Name: "/review", Description: "Review current workspace diff for bugs & security", ActionID: "prompt:review"},
		{Name: "/search", Description: "Open Cmd+F backend session search bar", ActionID: "studio.open-search"},
		{Name: "/auth", Description: "Open Tier-0 Account & OAuth Sign-In Center", ActionID: "studio.open-auth"},
		{Name: "/keys", Description: "Open searchable Keymap Inspector", ActionID: "studio.open-keymap-inspector"},
		{Name: "/activity", Description: "Open Tier-0 Notification & Activity Center", ActionID: "studio.open-notifications"},
		{Name: "/marketplace", Description: "Open Official Marketplace (Plugins, Themes, Layouts)", ActionID: "studio.open-marketplace"},
		{Name: "/federation", Description: "Open Tier-0 Multi-Target Federation Cockpit", ActionID: "studio.open-federation"},
		{Name: "/theme", Description: "Cycle active Studio theme preset", ActionID: "studio.cycle-theme"},
		{Name: "/export-layout", Description: "Export current WindowTree layout & theme profile JSON", ActionID: "studio.export-workspace-profile"},
		{Name: "/import-layout", Description: "Import saved WindowTree layout & theme profile JSON", ActionID: "studio.import-workspace-profile"},
	}
}

// SetModels replaces the declarative model list advertised by the Rust backend.
func (s *State) SetModels(models []ModelOption) {
	if len(models) == 0 {
		return
	}
	s.mu.Lock()
	s.models = append([]ModelOption(nil), models...)
	if s.activeModel >= len(s.models) {
		s.activeModel = 0
	}
	s.mu.Unlock()
}

// SetSlashCommands overrides the inline `/` command suggestions.
func (s *State) SetSlashCommands(cmds []SlashCommand) {
	s.mu.Lock()
	s.slashCommands = append([]SlashCommand(nil), cmds...)
	s.mu.Unlock()
}

// AddAttachment validates, SHA-256 hashes, deduplicates, and stages a multimodal attachment.
func (s *State) AddAttachment(in AttachmentInput) (AttachmentRef, error) {
	staged, err := ValidateAndStageAttachment(in, s.policy)
	if err != nil {
		s.mu.Lock()
		s.lastError = err.Error()
		s.mu.Unlock()
		return AttachmentRef{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Content-addressed SHA-256 deduplication: attaching the exact same file twice reuses it!
	for _, existing := range s.attachments {
		if existing.Ref.SHA256Hex == staged.Ref.SHA256Hex {
			s.lastError = ""
			return existing.Ref, nil
		}
	}
	if len(s.attachments) >= s.policy.MaxCount {
		s.lastError = ErrAttachmentLimitReached.Error()
		return AttachmentRef{}, ErrAttachmentLimitReached
	}
	if s.totalBytes+staged.Ref.ByteSize > s.policy.MaxTotalTurnBytes {
		err = fmt.Errorf("%w (%s > %s)", ErrAttachmentTotalBudget,
			FormatByteSize(s.totalBytes+staged.Ref.ByteSize), FormatByteSize(s.policy.MaxTotalTurnBytes))
		s.lastError = err.Error()
		return AttachmentRef{}, err
	}

	s.attachments = append(s.attachments, staged)
	s.totalBytes += staged.Ref.ByteSize
	s.lastError = ""
	return staged.Ref, nil
}

// RemoveAttachment removes a staged attachment by ID and releases its byte budget.
func (s *State) RemoveAttachment(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, att := range s.attachments {
		if att.Ref.ID == id {
			s.totalBytes -= att.Ref.ByteSize
			if s.totalBytes < 0 {
				s.totalBytes = 0
			}
			s.attachments = append(s.attachments[:i], s.attachments[i+1:]...)
			s.lastError = ""
			return true
		}
	}
	return false
}

// UpdateAttachmentStatus updates upload progress or CAS receipt state for one attachment.
func (s *State) UpdateAttachmentStatus(id string, status AttachmentStatus, progress float32, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.attachments {
		if s.attachments[i].Ref.ID == id {
			s.attachments[i].Ref.Status = status
			s.attachments[i].Ref.Progress = progress
			s.attachments[i].Ref.ErrorMessage = errMsg
			return
		}
	}
}

// StagedByID returns a copy of the staged attachment matching id if present.
func (s *State) StagedByID(id string) (StagedAttachment, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, att := range s.attachments {
		if att.Ref.ID == id {
			return att, true
		}
	}
	return StagedAttachment{}, false
}

// AddMention appends a deduplicated `@` context mention chip.
func (s *State) AddMention(m ContextMention) {
	if strings.TrimSpace(m.Target) == "" {
		return
	}
	if m.Kind == "" {
		m.Kind = "file"
	}
	if m.Label == "" {
		m.Label = m.Target
	}
	key := m.Key()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.mentions {
		if existing.Key() == key {
			return
		}
	}
	s.mentions = append(s.mentions, m)
}

// RemoveMention removes a context mention by canonical Key().
func (s *State) RemoveMention(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, m := range s.mentions {
		if m.Key() == key {
			s.mentions = append(s.mentions[:i], s.mentions[i+1:]...)
			return
		}
	}
}

// CycleModel advances to the next advertised backend model.
func (s *State) CycleModel() ModelOption {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.models) == 0 {
		return ModelOption{}
	}
	s.activeModel = (s.activeModel + 1) % len(s.models)
	return s.models[s.activeModel]
}

// SetActiveModel directly activates a backend-advertised model by ID.
func (s *State) SetActiveModel(modelID string) (ModelOption, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, m := range s.models {
		if m.ID == modelID {
			s.activeModel = i
			return m, true
		}
	}
	return ModelOption{}, false
}

// MatchModels filters backend-advertised models by ID, Label, or Provider substring.
func (s *State) MatchModels(query string) []ModelOption {
	q := strings.ToLower(strings.TrimSpace(query))
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ModelOption, 0, len(s.models))
	for _, m := range s.models {
		if q == "" ||
			strings.Contains(strings.ToLower(m.ID), q) ||
			strings.Contains(strings.ToLower(m.Label), q) ||
			strings.Contains(strings.ToLower(m.Provider), q) {
			out = append(out, m)
		}
	}
	return out
}

// CycleEffort cycles Fast -> Balanced -> Deep reasoning effort.
func (s *State) CycleEffort() ReasoningEffort {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.effort {
	case EffortFast:
		s.effort = EffortBalanced
	case EffortBalanced:
		s.effort = EffortDeep
	default:
		s.effort = EffortFast
	}
	return s.effort
}

// SetEffort explicitly sets the reasoning effort level.
func (s *State) SetEffort(e ReasoningEffort) {
	s.mu.Lock()
	s.effort = e
	s.mu.Unlock()
}

// MatchSlashCommands returns slash command suggestions when draft starts with `/`.
func (s *State) MatchSlashCommands(draft string) []SlashCommand {
	trimmed := strings.TrimSpace(draft)
	if !strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, " ") {
		return nil
	}
	q := strings.ToLower(trimmed)
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []SlashCommand
	for _, cmd := range s.slashCommands {
		if strings.HasPrefix(strings.ToLower(cmd.Name), q) || strings.Contains(strings.ToLower(cmd.Description), strings.TrimPrefix(q, "/")) {
			out = append(out, cmd)
		}
	}
	return out
}

// ActiveMentionQuery extracts the trailing `@query` token if the user is typing
// a context mention at the end of draft (e.g. "fix @grpc" -> ("grpc", true)).
func ActiveMentionQuery(draft string) (string, bool) {
	if draft == "" {
		return "", false
	}
	lastAt := strings.LastIndexByte(draft, '@')
	if lastAt < 0 {
		return "", false
	}
	if lastAt > 0 && draft[lastAt-1] != ' ' && draft[lastAt-1] != '\n' && draft[lastAt-1] != '\t' {
		return "", false
	}
	tail := draft[lastAt+1:]
	if strings.ContainsAny(tail, " \t\n") {
		return "", false
	}
	return tail, true
}

// StripTrailingMentionToken removes the active `@query` token after a mention is selected.
func StripTrailingMentionToken(draft string) string {
	lastAt := strings.LastIndexByte(draft, '@')
	if lastAt < 0 {
		return draft
	}
	return strings.TrimRight(draft[:lastAt], " ")
}

// BuildAndResetSubmission atomically packages the prompt, staged attachments,
// context mentions, model, and reasoning effort, clearing staged turn inputs.
func (s *State) BuildAndResetSubmission(prompt string) (TurnSubmission, bool) {
	trimmed := strings.TrimSpace(prompt)
	s.mu.Lock()
	defer s.mu.Unlock()
	if trimmed == "" && len(s.attachments) == 0 {
		return TurnSubmission{}, false
	}
	var activeModelID string
	if len(s.models) > 0 && s.activeModel < len(s.models) {
		activeModelID = s.models[s.activeModel].ID
	}
	sub := TurnSubmission{
		Prompt:          trimmed,
		Attachments:     append([]StagedAttachment(nil), s.attachments...),
		Mentions:        append([]ContextMention(nil), s.mentions...),
		ModelID:         activeModelID,
		ReasoningEffort: s.effort,
	}
	s.attachments = nil
	s.totalBytes = 0
	s.mentions = nil
	s.lastError = ""
	return sub, true
}

// Snapshot returns an immutable, zero-payload-byte view safe for 120Hz GPU rendering.
func (s *State) Snapshot() ComposerSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	refs := make([]AttachmentRef, len(s.attachments))
	for i, a := range s.attachments {
		refs[i] = a.Ref
	}
	var active ModelOption
	if len(s.models) > 0 && s.activeModel < len(s.models) {
		active = s.models[s.activeModel]
	}
	return ComposerSnapshot{
		Attachments:     refs,
		TotalBytes:      s.totalBytes,
		Mentions:        append([]ContextMention(nil), s.mentions...),
		ActiveModel:     active,
		Models:          append([]ModelOption(nil), s.models...),
		ReasoningEffort: s.effort,
		LastError:       s.lastError,
	}
}
