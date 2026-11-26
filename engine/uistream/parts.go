// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package uistream provides a pure-Go, zero-CGO typed UIMessage & Part stream
// projector compatible with the Vercel AI SDK UI Message Stream Protocol
// (text, reasoning, tool-invocation, source-url, file, and custom data-*
// Generative UI parts) as well as Marlin's authoritative Rust gRPC
// ConversationJournal TurnStreamUpdate events.
package uistream

import (
	"encoding/json"
	"strings"
)

// PartKind enumerates Vercel AI SDK UIMessagePart types mapped into Marlin's
// 3-layer architecture (Layer 1 Rust authority -> Layer 2 GPU renderer ->
// Layer 3 Sandboxed WebView Generative UI islands).
type PartKind string

const (
	PartText           PartKind = "text"
	PartReasoning      PartKind = "reasoning"
	PartToolInvocation PartKind = "tool-invocation"
	PartSourceURL      PartKind = "source-url"
	PartFile           PartKind = "file"
	PartData           PartKind = "data"
)

// ToolState matches Vercel AI SDK tool part lifecycle states.
type ToolState string

const (
	ToolStateInputStreaming    ToolState = "input-streaming"
	ToolStateInputAvailable    ToolState = "input-available"
	ToolStateApprovalRequested ToolState = "approval-requested"
	ToolStateOutputAvailable   ToolState = "output-available"
	ToolStateOutputDenied      ToolState = "output-denied"
)

// Part is one typed renderable segment inside a UIMessage.
type Part struct {
	Kind       PartKind        `json:"type"`
	Text       string          `json:"text,omitempty"`
	Streaming  bool            `json:"streaming,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	ToolState  ToolState       `json:"state,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	Output     json.RawMessage `json:"output,omitempty"`
	ErrorText  string          `json:"errorText,omitempty"`
	SourceID   string          `json:"sourceId,omitempty"`
	URL        string          `json:"url,omitempty"`
	Title      string          `json:"title,omitempty"`
	MediaType  string          `json:"mediaType,omitempty"`
	DataType   string          `json:"dataType,omitempty"` // e.g. "data-workflow-graph", "data-chart"
	Data       json.RawMessage `json:"data,omitempty"`
}

// UIMessage represents one structured Agent Platform turn (user or assistant)
// composed of ordered typed Parts.
type UIMessage struct {
	ID       string `json:"id"`
	Role     string `json:"role"` // "user" | "assistant" | "system"
	Parts    []Part `json:"parts"`
	Finished bool   `json:"finished,omitempty"`
}

// StreamChunk represents one decoded Vercel AI SDK UI Message Stream SSE event
// (`data: {"type": ...}`).
type StreamChunk struct {
	Type       string          `json:"type"`
	ID         string          `json:"id,omitempty"`
	Delta      string          `json:"delta,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	Output     json.RawMessage `json:"output,omitempty"`
	ErrorText  string          `json:"errorText,omitempty"`
	SourceID   string          `json:"sourceId,omitempty"`
	URL        string          `json:"url,omitempty"`
	Title      string          `json:"title,omitempty"`
	MediaType  string          `json:"mediaType,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
}

// ParseVercelStreamLine decodes one SSE `data: ...` line from a Vercel AI SDK
// UI Message Stream (v5/v6). Returns (chunk, done, err).
func ParseVercelStreamLine(line string) (StreamChunk, bool, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, ":") {
		return StreamChunk{}, false, nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if payload == "[DONE]" {
		return StreamChunk{Type: "finish"}, true, nil
	}
	var ch StreamChunk
	if err := json.Unmarshal([]byte(payload), &ch); err != nil {
		return StreamChunk{}, false, err
	}
	return ch, ch.Type == "finish", nil
}

// Accumulator folds Vercel AI SDK UI Message Stream chunks incrementally into a
// deterministic UIMessage with zero lost deltas.
type Accumulator struct {
	msg UIMessage
}

// NewAccumulator initializes an assistant UIMessage accumulator.
func NewAccumulator(id, role string) *Accumulator {
	if role == "" {
		role = "assistant"
	}
	return &Accumulator{msg: UIMessage{ID: id, Role: role}}
}

// Apply folds one StreamChunk into the accumulated UIMessage.
func (a *Accumulator) Apply(ch StreamChunk) {
	switch {
	case ch.Type == "reasoning-start":
		a.msg.Parts = append(a.msg.Parts, Part{Kind: PartReasoning, Streaming: true})
	case ch.Type == "reasoning-delta":
		if idx := a.lastKind(PartReasoning); idx >= 0 {
			a.msg.Parts[idx].Text += ch.Delta
			a.msg.Parts[idx].Streaming = true
		} else {
			a.msg.Parts = append(a.msg.Parts, Part{Kind: PartReasoning, Text: ch.Delta, Streaming: true})
		}
	case ch.Type == "reasoning-end":
		if idx := a.lastKind(PartReasoning); idx >= 0 {
			a.msg.Parts[idx].Streaming = false
		}
	case ch.Type == "text-start":
		a.msg.Parts = append(a.msg.Parts, Part{Kind: PartText, Streaming: true})
	case ch.Type == "text-delta":
		if idx := a.lastKind(PartText); idx >= 0 {
			a.msg.Parts[idx].Text += ch.Delta
			a.msg.Parts[idx].Streaming = true
		} else {
			a.msg.Parts = append(a.msg.Parts, Part{Kind: PartText, Text: ch.Delta, Streaming: true})
		}
	case ch.Type == "text-end":
		if idx := a.lastKind(PartText); idx >= 0 {
			a.msg.Parts[idx].Streaming = false
		}
	case ch.Type == "tool-input-start":
		a.upsertTool(ch.ToolCallID, ch.ToolName, ToolStateInputStreaming, ch.Input, nil, "")
	case ch.Type == "tool-input-available":
		a.upsertTool(ch.ToolCallID, ch.ToolName, ToolStateInputAvailable, ch.Input, nil, "")
	case ch.Type == "tool-approval-request":
		a.upsertTool(ch.ToolCallID, ch.ToolName, ToolStateApprovalRequested, ch.Input, nil, "")
	case ch.Type == "tool-output-available":
		a.upsertTool(ch.ToolCallID, ch.ToolName, ToolStateOutputAvailable, nil, ch.Output, "")
	case ch.Type == "tool-output-denied" || ch.Type == "tool-output-error":
		a.upsertTool(ch.ToolCallID, ch.ToolName, ToolStateOutputDenied, nil, nil, ch.ErrorText)
	case ch.Type == "source-url":
		a.msg.Parts = append(a.msg.Parts, Part{Kind: PartSourceURL, SourceID: ch.SourceID, URL: ch.URL, Title: ch.Title})
	case ch.Type == "file":
		a.msg.Parts = append(a.msg.Parts, Part{Kind: PartFile, URL: ch.URL, MediaType: ch.MediaType, Title: ch.Title})
	case strings.HasPrefix(ch.Type, "data-"):
		a.msg.Parts = append(a.msg.Parts, Part{Kind: PartData, DataType: ch.Type, Data: ch.Data})
	case ch.Type == "finish":
		for i := range a.msg.Parts {
			a.msg.Parts[i].Streaming = false
		}
		a.msg.Finished = true
	}
}

// Message returns a defensive snapshot of the accumulated UIMessage.
func (a *Accumulator) Message() UIMessage {
	cp := a.msg
	if len(a.msg.Parts) > 0 {
		cp.Parts = append([]Part(nil), a.msg.Parts...)
	}
	return cp
}

func (a *Accumulator) lastKind(kind PartKind) int {
	if n := len(a.msg.Parts); n > 0 && a.msg.Parts[n-1].Kind == kind {
		return n - 1
	}
	return -1
}

func (a *Accumulator) upsertTool(callID, name string, st ToolState, in, out json.RawMessage, errText string) {
	for i := range a.msg.Parts {
		if a.msg.Parts[i].Kind == PartToolInvocation && a.msg.Parts[i].ToolCallID == callID && callID != "" {
			if name != "" {
				a.msg.Parts[i].ToolName = name
			}
			a.msg.Parts[i].ToolState = st
			if len(in) > 0 {
				a.msg.Parts[i].Input = in
			}
			if len(out) > 0 {
				a.msg.Parts[i].Output = out
			}
			if errText != "" {
				a.msg.Parts[i].ErrorText = errText
			}
			return
		}
	}
	a.msg.Parts = append(a.msg.Parts, Part{
		Kind:       PartToolInvocation,
		ToolCallID: callID,
		ToolName:   name,
		ToolState:  st,
		Input:      in,
		Output:     out,
		ErrorText:  errText,
	})
}

// SplitThinkTags separates streaming or completed `<think>...</think>` or
// `<thinking>...</thinking>` reasoning envelopes (used by DeepSeek R1/V4 and
// Anthropic-compatible streams) from the user-facing Markdown answer so UI
// layers always fold reasoning cleanly by default.
func SplitThinkTags(raw string) (thinking string, answer string, thinkingOpen bool) {
	if raw == "" {
		return "", "", false
	}
	openTag := "<think>"
	closeTag := "</think>"
	start := strings.Index(raw, openTag)
	if start < 0 {
		openTag = "<thinking>"
		closeTag = "</thinking>"
		start = strings.Index(raw, openTag)
		if start < 0 {
			return "", raw, false
		}
	}
	prefix := strings.TrimSpace(raw[:start])
	rest := raw[start+len(openTag):]
	end := strings.Index(rest, closeTag)
	if end < 0 {
		return strings.TrimSpace(rest), prefix, true
	}
	thought := strings.TrimSpace(rest[:end])
	suffix := strings.TrimSpace(rest[end+len(closeTag):])
	if prefix != "" && suffix != "" {
		return thought, prefix + "\n\n" + suffix, false
	}
	if prefix != "" {
		return thought, prefix, false
	}
	return thought, suffix, false
}
