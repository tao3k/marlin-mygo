// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package uistream

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// ChatStatus mirrors Vercel AI SDK v5/v6 useChat status states.
type ChatStatus string

const (
	ChatStatusReady     ChatStatus = "ready"
	ChatStatusSubmitted ChatStatus = "submitted"
	ChatStatusStreaming ChatStatus = "streaming"
	ChatStatusError     ChatStatus = "error"
)

// ChatAdapter bridges Layer 1 (Rust gRPC AppServer turns or Vercel SSE streams)
// into Layer 2 pure-Go typed Vercel AI SDK UIMessage slices and Layer 3
// sandboxed WebView JSON payloads with thread-safe RCU-friendly snapshots.
type ChatAdapter struct {
	mu       sync.RWMutex
	messages []UIMessage
	status   ChatStatus
	lastErr  string
	turnIdx  map[uint64]int
	acc      *Accumulator
}

// NewChatAdapter constructs an idle Vercel AI SDK UIMessage chat state adapter.
func NewChatAdapter() *ChatAdapter {
	return &ChatAdapter{
		status:  ChatStatusReady,
		turnIdx: make(map[uint64]int),
	}
}

// Status returns the current Vercel AI SDK chat lifecycle status.
func (a *ChatAdapter) Status() ChatStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}

// Error returns the last surfaced error string, if any.
func (a *ChatAdapter) Error() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastErr
}

// Messages returns a deep copy snapshot of all accumulated UIMessages.
func (a *ChatAdapter) Messages() []UIMessage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]UIMessage, len(a.messages))
	for i, m := range a.messages {
		cp := m
		cp.Parts = append([]Part(nil), m.Parts...)
		out[i] = cp
	}
	return out
}

// AppendUser records a user prompt and transitions status to submitted.
func (a *ChatAdapter) AppendUser(id, text string) UIMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id == "" {
		id = fmt.Sprintf("user-%d", len(a.messages)+1)
	}
	msg := UIMessage{
		ID:       id,
		Role:     "user",
		Finished: true,
		Parts: []Part{
			{Kind: PartText, Text: text},
		},
	}
	a.messages = append(a.messages, msg)
	a.status = ChatStatusSubmitted
	a.lastErr = ""
	return msg
}

// ApplyRustTextDelta ingests an incremental text delta from Rust's
// ConversationJournal, automatically splitting <think>...</think> reasoning
// into typed PartReasoning and fenced marlin-data/mermaid blocks into PartData.
func (a *ChatAdapter) ApplyRustTextDelta(turnOrdinal uint64, rawFullText string, streaming bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	idx, ok := a.turnIdx[turnOrdinal]
	if !ok {
		idx = len(a.messages)
		a.turnIdx[turnOrdinal] = idx
		a.messages = append(a.messages, UIMessage{
			ID:   fmt.Sprintf("turn-%d", turnOrdinal),
			Role: "assistant",
		})
	}
	thinking, body, thinkingStreaming := SplitThinkTags(rawFullText)
	dataParts, cleanBody := ExtractDataParts(body)

	// Preserve existing tool/source/file parts on this turn while rebuilding reasoning/text/data parts.
	var preserved []Part
	for _, p := range a.messages[idx].Parts {
		if p.Kind == PartToolInvocation || p.Kind == PartSourceURL || p.Kind == PartFile {
			preserved = append(preserved, p)
		}
	}

	parts := make([]Part, 0, len(preserved)+len(dataParts)+2)
	if strings.TrimSpace(thinking) != "" {
		parts = append(parts, Part{
			Kind:      PartReasoning,
			Text:      thinking,
			Streaming: thinkingStreaming && streaming,
		})
	}
	parts = append(parts, preserved...)
	if strings.TrimSpace(cleanBody) != "" {
		parts = append(parts, Part{
			Kind:      PartText,
			Text:      cleanBody,
			Streaming: streaming,
		})
	}
	parts = append(parts, dataParts...)

	a.messages[idx].Parts = parts
	a.messages[idx].Finished = !streaming
	if streaming {
		a.status = ChatStatusStreaming
	} else {
		a.status = ChatStatusReady
	}
}

// UpsertRustToolPart records or updates a typed PartToolInvocation on a turn.
func (a *ChatAdapter) UpsertRustToolPart(turnOrdinal uint64, callID, toolName string, state ToolState, input, output json.RawMessage, errText string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	idx, ok := a.turnIdx[turnOrdinal]
	if !ok {
		idx = len(a.messages)
		a.turnIdx[turnOrdinal] = idx
		a.messages = append(a.messages, UIMessage{
			ID:   fmt.Sprintf("turn-%d", turnOrdinal),
			Role: "assistant",
		})
	}
	tp := Part{
		Kind:       PartToolInvocation,
		ToolCallID: callID,
		ToolName:   toolName,
		ToolState:  state,
		Input:      input,
		Output:     output,
		ErrorText:  errText,
	}
	for i := range a.messages[idx].Parts {
		if a.messages[idx].Parts[i].Kind == PartToolInvocation && a.messages[idx].Parts[i].ToolCallID == callID {
			a.messages[idx].Parts[i] = tp
			return
		}
	}
	a.messages[idx].Parts = append(a.messages[idx].Parts, tp)
}

// ApplyVercelSSELine applies one raw Vercel AI SDK UI Message Stream SSE line
// ("data: {...}") onto the active streaming assistant message.
func (a *ChatAdapter) ApplyVercelSSELine(line string) (bool, error) {
	ch, done, err := ParseVercelStreamLine(line)
	if err != nil {
		a.mu.Lock()
		a.status = ChatStatusError
		a.lastErr = err.Error()
		a.mu.Unlock()
		return false, err
	}
	if ch.Type == "" {
		return false, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if ch.Type == "start" || a.acc == nil {
		id := ch.ID
		if id == "" {
			id = fmt.Sprintf("msg-%d", len(a.messages)+1)
		}
		a.acc = NewAccumulator(id, "assistant")
		a.messages = append(a.messages, a.acc.Message())
	}
	a.acc.Apply(ch)
	if len(a.messages) > 0 {
		a.messages[len(a.messages)-1] = a.acc.Message()
	}
	switch {
	case done || ch.Type == "finish":
		a.status = ChatStatusReady
		a.acc = nil
	case ch.Type == "error":
		a.status = ChatStatusError
		a.lastErr = ch.ErrorText
	default:
		a.status = ChatStatusStreaming
	}
	return true, nil
}

// ToVercelJSON serializes all messages into standard Vercel AI SDK v5/v6 JSON
// ready for Layer 3 `@tao3k/marlin-panel-sdk/ai-sdk` consumption.
func (a *ChatAdapter) ToVercelJSON() ([]byte, error) {
	return json.Marshal(a.Messages())
}

// ExtractDataParts scans assistant Markdown for fenced generative UI blocks:
//   - ```marlin-data:<name> ... ```
//   - ```vercel-ui:<name> ... ```
//   - ```mermaid ... ```
// and emits typed Vercel AI SDK `data-*` PartData values while keeping normal
// prose intact for Markdown rendering.
func ExtractDataParts(markdown string) (parts []Part, prose string) {
	if !strings.Contains(markdown, "```") {
		return nil, markdown
	}
	lines := strings.Split(markdown, "\n")
	var outLines []string
	var inFence bool
	var dataType string
	var buf []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inFence {
			if strings.HasPrefix(trimmed, "```") {
				lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				switch {
				case strings.HasPrefix(lang, "marlin-data:"):
					inFence = true
					dataType = "data-" + strings.TrimSpace(strings.TrimPrefix(lang, "marlin-data:"))
					buf = buf[:0]
					continue
				case strings.HasPrefix(lang, "vercel-ui:"):
					inFence = true
					dataType = "data-" + strings.TrimSpace(strings.TrimPrefix(lang, "vercel-ui:"))
					buf = buf[:0]
					continue
				case lang == "mermaid":
					inFence = true
					dataType = "data-mermaid"
					buf = buf[:0]
					continue
				}
			}
			outLines = append(outLines, line)
			continue
		}
		if trimmed == "```" {
			payloadStr := strings.TrimSpace(strings.Join(buf, "\n"))
			var rawJSON json.RawMessage
			if json.Valid([]byte(payloadStr)) {
				rawJSON = json.RawMessage(payloadStr)
			} else if encoded, err := json.Marshal(payloadStr); err == nil {
				rawJSON = json.RawMessage(encoded)
			}
			parts = append(parts, Part{
				Kind:     PartData,
				DataType: dataType,
				Data:     rawJSON,
			})
			inFence = false
			dataType = ""
			buf = buf[:0]
			continue
		}
		buf = append(buf, line)
	}
	if inFence {
		outLines = append(outLines, "```"+strings.TrimPrefix(dataType, "data-"))
		outLines = append(outLines, buf...)
	}
	return parts, strings.Join(outLines, "\n")
}
