// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package richtext defines Marlin's razor-thin Rich Document Intermediate
// Representation (IR) and fail-closed admission boundary for rendering both
// Emacs Org-mode (parsed externally by org-rz1 WASM or the Rust backend) and
// Markdown (parsed externally by the Rust backend / WASM) on MyGo's 120Hz GPU
// pipeline.
//
// Architectural boundary: marlin-mygo does NOT duplicate Org-mode or Markdown
// parsers in Go. External parsers (org-rz1 WASM or Rust AppServer) produce
// typed Document IR JSON / structs; this package validates bounds and link
// safety once at admission time so the GPU renderer remains pure and zero-parse.
package richtext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Format identifies the source markup dialect handled by an external parser
// (such as org-rz1 WASM or the Rust AppServer).
type Format string

const (
	FormatOrg      Format = "org"
	FormatMarkdown Format = "markdown"
	FormatPlain    Format = "plain"
)

// Parser is the pluggable external parser boundary implemented by either an
// org-rz1 WASM module (via wazero) or the Rust backend over gRPC.
type Parser interface {
	ParseDocument(ctx context.Context, format Format, docID, source string) ([]Block, error)
}

// BlockKind identifies the structural category of one admitted document block.
type BlockKind string

const (
	BlockParagraph   BlockKind = "paragraph"
	BlockHeading     BlockKind = "heading"
	BlockCode        BlockKind = "code"
	BlockQuote       BlockKind = "quote"
	BlockTable       BlockKind = "table"
	BlockList        BlockKind = "list"
	BlockOrgPlanning BlockKind = "org-planning"
	BlockDrawer      BlockKind = "drawer"
	BlockRule        BlockKind = "rule"
)

// SpanKind identifies inline styling inside a paragraph, heading, or list item.
type SpanKind string

const (
	SpanPlain  SpanKind = "plain"
	SpanBold   SpanKind = "bold"
	SpanCode   SpanKind = "code"
	SpanItalic SpanKind = "italic"
	SpanLink   SpanKind = "link"
)

// InlineSpan represents one pre-parsed inline text run emitted by org-rz1 / Rust.
type InlineSpan struct {
	Kind SpanKind `json:"kind"`
	Text string   `json:"text"`
	URL  string   `json:"url,omitempty"`
}

// ListItem represents one bullet, numbered, or Org/Markdown checkbox item.
type ListItem struct {
	Marker  string       `json:"marker,omitempty"`
	Check   string       `json:"check,omitempty"` // "", " ", "X", "-" (Org partial)
	RawText string       `json:"raw_text,omitempty"`
	Spans   []InlineSpan `json:"spans,omitempty"`
}

// DrawerEntry is one `:KEY: value` property inside an Org drawer.
type DrawerEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// PlanningEntry is one Org planning timestamp (SCHEDULED, DEADLINE, CLOSED).
type PlanningEntry struct {
	Keyword   string `json:"keyword"`
	Timestamp string `json:"timestamp"`
}

// Block is one pre-parsed structural element ready for zero-parse 120Hz GPU rendering.
type Block struct {
	Kind BlockKind `json:"kind"`

	// Heading metadata (Org *..****** or Markdown #..######)
	Level    int      `json:"level,omitempty"`
	IsOrg    bool     `json:"is_org,omitempty"`
	TodoWord string   `json:"todo_word,omitempty"`
	Priority string   `json:"priority,omitempty"`
	Tags     []string `json:"tags,omitempty"`

	// Text & inline spans (Paragraph, Heading, Quote)
	RawText   string       `json:"raw_text,omitempty"`
	Spans     []InlineSpan `json:"spans,omitempty"`
	AlertKind string       `json:"alert_kind,omitempty"`

	// Code / #+BEGIN_SRC metadata
	Lang    string `json:"lang,omitempty"`
	Code    string `json:"code,omitempty"`
	CopyKey string `json:"copy_key,omitempty"`

	// Table metadata (Org |---+---| or GFM |---|---|)
	Headers []string   `json:"headers,omitempty"`
	Rows    [][]string `json:"rows,omitempty"`

	// List / checklist metadata
	Items []ListItem `json:"items,omitempty"`

	// Org planning & drawer metadata
	Planning   []PlanningEntry `json:"planning,omitempty"`
	DrawerName string          `json:"drawer_name,omitempty"`
	DrawerRows []DrawerEntry   `json:"drawer_rows,omitempty"`
}

// DocumentEnvelope is the wire JSON envelope returned by org-rz1 WASM or Rust
// backend document-parsing endpoints.
type DocumentEnvelope struct {
	Format Format  `json:"format,omitempty"`
	Blocks []Block `json:"blocks"`
}

// AdmissionLimits defines fail-closed resource ceilings for untrusted IR JSON.
type AdmissionLimits struct {
	MaxPayloadBytes  int // default 2 MiB
	MaxBlocks        int // default 2048
	MaxTableRows     int // default 512
	MaxTableCols     int // default 32
	MaxListItems     int // default 512
	MaxSpansPerBlock int // default 256
}

// DefaultAdmissionLimits returns safe production bounds for WASM/gRPC document IR.
func DefaultAdmissionLimits() AdmissionLimits {
	return AdmissionLimits{
		MaxPayloadBytes:  2 << 20,
		MaxBlocks:        2048,
		MaxTableRows:     512,
		MaxTableCols:     32,
		MaxListItems:     512,
		MaxSpansPerBlock: 256,
	}
}

var (
	ErrIRTooLarge     = errors.New("richtext: IR payload exceeds safety ceiling")
	ErrIRInvalidJSON  = errors.New("richtext: invalid document IR JSON")
	ErrIRUnknownBlock = errors.New("richtext: unknown or unsupported block kind")
)

// DecodeIRJSON decodes and validates a DocumentEnvelope JSON payload emitted by
// org-rz1 WASM or the Rust backend using fail-closed bounds and link sanitization.
func DecodeIRJSON(docID string, rawJSON []byte) ([]Block, error) {
	return DecodeIRJSONWithLimits(docID, rawJSON, DefaultAdmissionLimits())
}

// DecodeIRJSONWithLimits decodes and admits a DocumentEnvelope under custom limits.
func DecodeIRJSONWithLimits(docID string, rawJSON []byte, lim AdmissionLimits) ([]Block, error) {
	def := DefaultAdmissionLimits()
	if lim.MaxPayloadBytes <= 0 {
		lim.MaxPayloadBytes = def.MaxPayloadBytes
	}
	if lim.MaxBlocks <= 0 {
		lim.MaxBlocks = def.MaxBlocks
	}
	if lim.MaxTableRows <= 0 {
		lim.MaxTableRows = def.MaxTableRows
	}
	if lim.MaxTableCols <= 0 {
		lim.MaxTableCols = def.MaxTableCols
	}
	if lim.MaxListItems <= 0 {
		lim.MaxListItems = def.MaxListItems
	}
	if lim.MaxSpansPerBlock <= 0 {
		lim.MaxSpansPerBlock = def.MaxSpansPerBlock
	}
	if len(rawJSON) > lim.MaxPayloadBytes {
		return nil, fmt.Errorf("%w (%d > %d)", ErrIRTooLarge, len(rawJSON), lim.MaxPayloadBytes)
	}
	var env DocumentEnvelope
	if err := json.Unmarshal(rawJSON, &env); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIRInvalidJSON, err)
	}
	return AdmitBlocksWithLimits(docID, env.Blocks, lim)
}

// AdmitBlocks validates and sanitizes an in-memory block slice (e.g. mapped
// from gRPC protobuf or org-rz1 WASM) before handing it to the GPU renderer.
func AdmitBlocks(docID string, in []Block) ([]Block, error) {
	return AdmitBlocksWithLimits(docID, in, DefaultAdmissionLimits())
}

// AdmitBlocksWithLimits enforces block/row/column/span bounds, fail-closed link
// URL filtering, and deterministic code CopyKey assignment.
func AdmitBlocksWithLimits(docID string, in []Block, lim AdmissionLimits) ([]Block, error) {
	if len(in) > lim.MaxBlocks {
		return nil, fmt.Errorf("%w: %d blocks > max %d", ErrIRTooLarge, len(in), lim.MaxBlocks)
	}
	out := make([]Block, 0, len(in))
	codeIdx := 0
	for _, b := range in {
		if !isKnownBlockKind(b.Kind) {
			return nil, fmt.Errorf("%w: %q", ErrIRUnknownBlock, b.Kind)
		}
		if len(b.Spans) > lim.MaxSpansPerBlock {
			b.Spans = b.Spans[:lim.MaxSpansPerBlock]
		}
		b.Spans = sanitizeSpans(b.Spans)

		if b.Kind == BlockHeading {
			if b.Level < 1 {
				b.Level = 1
			} else if b.Level > 6 {
				b.Level = 6
			}
			b.TodoWord = strings.ToUpper(strings.TrimSpace(b.TodoWord))
			b.Priority = strings.ToUpper(strings.TrimSpace(b.Priority))
		}
		if b.Kind == BlockCode {
			codeIdx++
			if strings.TrimSpace(b.Lang) == "" {
				b.Lang = "CODE"
			} else {
				b.Lang = strings.ToUpper(strings.TrimSpace(b.Lang))
			}
			if b.CopyKey == "" {
				b.CopyKey = fmt.Sprintf("copy-%s-%d", docID, codeIdx)
			}
		}
		if b.Kind == BlockTable {
			if len(b.Headers) > lim.MaxTableCols {
				b.Headers = b.Headers[:lim.MaxTableCols]
			}
			if len(b.Rows) > lim.MaxTableRows {
				b.Rows = b.Rows[:lim.MaxTableRows]
			}
			normRows := make([][]string, len(b.Rows))
			for ri, row := range b.Rows {
				if len(row) > lim.MaxTableCols {
					row = row[:lim.MaxTableCols]
				}
				normRows[ri] = append([]string(nil), row...)
			}
			b.Rows = normRows
		}
		if b.Kind == BlockList {
			if len(b.Items) > lim.MaxListItems {
				b.Items = b.Items[:lim.MaxListItems]
			}
			normItems := make([]ListItem, len(b.Items))
			for li, item := range b.Items {
				if len(item.Spans) > lim.MaxSpansPerBlock {
					item.Spans = item.Spans[:lim.MaxSpansPerBlock]
				}
				item.Spans = sanitizeSpans(item.Spans)
				item.Check = normalizeCheckState(item.Check)
				normItems[li] = item
			}
			b.Items = normItems
		}
		out = append(out, b)
	}
	return out, nil
}

func isKnownBlockKind(k BlockKind) bool {
	switch k {
	case BlockParagraph, BlockHeading, BlockCode, BlockQuote, BlockTable,
		BlockList, BlockOrgPlanning, BlockDrawer, BlockRule:
		return true
	default:
		return false
	}
}

func normalizeCheckState(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "X":
		return "X"
	case "-":
		return "-"
	case " ":
		return " "
	default:
		if raw == " " {
			return " "
		}
		return ""
	}
}

func sanitizeSpans(in []InlineSpan) []InlineSpan {
	if len(in) == 0 {
		return nil
	}
	out := make([]InlineSpan, len(in))
	for i, sp := range in {
		if sp.Kind == "" {
			sp.Kind = SpanPlain
		}
		if sp.Kind == SpanLink {
			if safeURL, ok := SanitizeLinkURL(sp.URL); ok {
				sp.URL = safeURL
			} else {
				sp.Kind = SpanPlain
				sp.URL = ""
			}
		} else {
			sp.URL = ""
		}
		out[i] = sp
	}
	return out
}

// SanitizeLinkURL allows only safe external/document schemes (fail-closed
// against javascript:, data:, vbscript:, or arbitrary unknown schemes).
func SanitizeLinkURL(raw string) (string, bool) {
	u := strings.TrimSpace(raw)
	lower := strings.ToLower(u)
	for _, prefix := range []string{"https://", "http://", "mailto:", "file://"} {
		if strings.HasPrefix(lower, prefix) {
			return u, true
		}
	}
	return "", false
}
