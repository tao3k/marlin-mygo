// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package richtext

import (
	"encoding/json"
	"fmt"
	"strings"
)

const maxOrgizeSectionDepth = 32

// OrgizePriorityDto mirrors tao3k/orgize OrgizePriorityDto (schemaVersion 1).
type OrgizePriorityDto struct {
	Raw       *string `json:"raw,omitempty"`
	Effective string  `json:"effective,omitempty"`
	IsDefault bool    `json:"isDefault,omitempty"`
}

// OrgizePropertyDto mirrors tao3k/orgize OrgizePropertyDto.
type OrgizePropertyDto struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// OrgizeTimestampDto mirrors tao3k/orgize OrgizeTimestampDto.
type OrgizeTimestampDto struct {
	Kind string `json:"kind"`
	Raw  string `json:"raw"`
}

// OrgizePlanningDto mirrors tao3k/orgize OrgizePlanningDto.
type OrgizePlanningDto struct {
	Deadline  *OrgizeTimestampDto `json:"deadline,omitempty"`
	Scheduled *OrgizeTimestampDto `json:"scheduled,omitempty"`
	Closed    *OrgizeTimestampDto `json:"closed,omitempty"`
}

// OrgizeTextSliceDto mirrors tao3k/orgize OrgizeTextSliceDto.
type OrgizeTextSliceDto struct {
	Text string `json:"text"`
}

// OrgizeLinkDto mirrors tao3k/orgize OrgizeLinkDto.
type OrgizeLinkDto struct {
	Path        string `json:"path"`
	Description string `json:"description"`
}

// OrgizeSectionIndexRecordDto mirrors tao3k/orgize OrgizeSectionIndexRecordDto.
type OrgizeSectionIndexRecordDto struct {
	Level      int                  `json:"level"`
	Title      string               `json:"title"`
	TitleText  string               `json:"titleText"`
	Body       []OrgizeTextSliceDto `json:"body"`
	Todo       *string              `json:"todo,omitempty"`
	TodoState  *string              `json:"todoState,omitempty"`
	Priority   OrgizePriorityDto    `json:"priority"`
	Tags       []string             `json:"tags"`
	Properties []OrgizePropertyDto  `json:"properties"`
	Planning   OrgizePlanningDto    `json:"planning"`
	Links      []OrgizeLinkDto      `json:"links"`
}

// OrgizeSectionIndexResponseDto mirrors tao3k/orgize Org.sectionIndexJson().
type OrgizeSectionIndexResponseDto struct {
	SchemaVersion int                           `json:"schemaVersion"`
	Records       []OrgizeSectionIndexRecordDto `json:"records"`
}

// OrgizeOrgElementsSectionDto mirrors tao3k/orgize OrgizeOrgElementsSectionDto.
type OrgizeOrgElementsSectionDto struct {
	Level      int                           `json:"level"`
	Title      string                        `json:"title"`
	Todo       *string                       `json:"todo,omitempty"`
	Tags       []string                      `json:"tags"`
	Properties []OrgizePropertyDto           `json:"properties"`
	Elements   []map[string]any              `json:"elements"`
	Children   []OrgizeOrgElementsSectionDto `json:"children"`
}

// OrgizeOrgElementsDto mirrors tao3k/orgize Org.orgElementsJson().
type OrgizeOrgElementsDto struct {
	SchemaVersion int                           `json:"schemaVersion"`
	Elements      []map[string]any              `json:"elements"`
	Sections      []OrgizeOrgElementsSectionDto `json:"sections"`
}

// DecodeDocumentAutoJSON auto-detects and admits any of Marlin's canonical
// external document IR JSON envelopes:
//  1. Native richtext.DocumentEnvelope (`{"blocks":[...]}`)
//  2. tao3k/orgize Org.sectionIndexJson() (`{"schemaVersion":1,"records":[...]}`)
//  3. tao3k/orgize Org.orgElementsJson() (`{"schemaVersion":1,"sections":[...]}`)
func DecodeDocumentAutoJSON(docID string, rawJSON []byte) ([]Block, error) {
	return DecodeDocumentAutoJSONWithLimits(docID, rawJSON, DefaultAdmissionLimits())
}

// DecodeDocumentAutoJSONWithLimits auto-detects and admits under custom limits.
func DecodeDocumentAutoJSONWithLimits(docID string, rawJSON []byte, lim AdmissionLimits) ([]Block, error) {
	def := DefaultAdmissionLimits()
	if lim.MaxPayloadBytes <= 0 {
		lim.MaxPayloadBytes = def.MaxPayloadBytes
	}
	if len(rawJSON) > lim.MaxPayloadBytes {
		return nil, fmt.Errorf("%w (%d > %d)", ErrIRTooLarge, len(rawJSON), lim.MaxPayloadBytes)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON, &probe); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIRInvalidJSON, err)
	}
	if _, ok := probe["blocks"]; ok {
		return DecodeIRJSONWithLimits(docID, rawJSON, lim)
	}
	if _, ok := probe["records"]; ok {
		return DecodeOrgizeSectionIndexJSON(docID, rawJSON, lim)
	}
	if _, hasSec := probe["sections"]; hasSec {
		return DecodeOrgizeOrgElementsJSON(docID, rawJSON, lim)
	}
	if _, hasElem := probe["elements"]; hasElem {
		return DecodeOrgizeOrgElementsJSON(docID, rawJSON, lim)
	}
	return nil, fmt.Errorf("%w: unrecognized document IR envelope", ErrIRInvalidJSON)
}

// DecodeOrgizeSectionIndexJSON projects tao3k/orgize Org.sectionIndexJson()
// directly into admitted richtext.Block slices for 120Hz GPU rendering.
func DecodeOrgizeSectionIndexJSON(docID string, rawJSON []byte, lim AdmissionLimits) ([]Block, error) {
	var dto OrgizeSectionIndexResponseDto
	if err := json.Unmarshal(rawJSON, &dto); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIRInvalidJSON, err)
	}
	var blocks []Block
	for _, rec := range dto.Records {
		title := strings.TrimSpace(rec.TitleText)
		if title == "" {
			title = strings.TrimSpace(rec.Title)
		}
		todo := ""
		if rec.Todo != nil {
			todo = strings.TrimSpace(*rec.Todo)
		}
		prio := ""
		if rec.Priority.Raw != nil && strings.TrimSpace(*rec.Priority.Raw) != "" {
			prio = strings.Trim(strings.TrimSpace(*rec.Priority.Raw), "[]#")
		} else if !rec.Priority.IsDefault && strings.TrimSpace(rec.Priority.Effective) != "" {
			prio = strings.Trim(strings.TrimSpace(rec.Priority.Effective), "[]#")
		}
		blocks = append(blocks, Block{
			Kind:     BlockHeading,
			Level:    rec.Level,
			IsOrg:    true,
			TodoWord: todo,
			Priority: prio,
			Tags:     append([]string(nil), rec.Tags...),
			RawText:  title,
		})

		var planning []PlanningEntry
		if rec.Planning.Scheduled != nil && rec.Planning.Scheduled.Raw != "" {
			planning = append(planning, PlanningEntry{Keyword: "SCHEDULED", Timestamp: rec.Planning.Scheduled.Raw})
		}
		if rec.Planning.Deadline != nil && rec.Planning.Deadline.Raw != "" {
			planning = append(planning, PlanningEntry{Keyword: "DEADLINE", Timestamp: rec.Planning.Deadline.Raw})
		}
		if rec.Planning.Closed != nil && rec.Planning.Closed.Raw != "" {
			planning = append(planning, PlanningEntry{Keyword: "CLOSED", Timestamp: rec.Planning.Closed.Raw})
		}
		if len(planning) > 0 {
			blocks = append(blocks, Block{Kind: BlockOrgPlanning, IsOrg: true, Planning: planning})
		}

		if len(rec.Properties) > 0 {
			rows := make([]DrawerEntry, 0, len(rec.Properties))
			for _, prop := range rec.Properties {
				rows = append(rows, DrawerEntry{Key: prop.Key, Value: prop.Value})
			}
			blocks = append(blocks, Block{
				Kind:       BlockDrawer,
				IsOrg:      true,
				DrawerName: "PROPERTIES",
				DrawerRows: rows,
			})
		}

		for _, bSlice := range rec.Body {
			if txt := strings.TrimSpace(bSlice.Text); txt != "" {
				blocks = append(blocks, Block{Kind: BlockParagraph, IsOrg: true, RawText: txt})
			}
		}

		if len(rec.Links) > 0 {
			spans := make([]InlineSpan, 0, len(rec.Links))
			for _, lnk := range rec.Links {
				lbl := strings.TrimSpace(lnk.Description)
				if lbl == "" {
					lbl = lnk.Path
				}
				spans = append(spans, InlineSpan{Kind: SpanLink, Text: lbl, URL: lnk.Path})
			}
			blocks = append(blocks, Block{Kind: BlockParagraph, IsOrg: true, Spans: spans})
		}
	}
	return AdmitBlocksWithLimits(docID, blocks, lim)
}

// DecodeOrgizeOrgElementsJSON projects tao3k/orgize Org.orgElementsJson()
// directly into admitted richtext.Block slices with recursion depth protection.
func DecodeOrgizeOrgElementsJSON(docID string, rawJSON []byte, lim AdmissionLimits) ([]Block, error) {
	var dto OrgizeOrgElementsDto
	if err := json.Unmarshal(rawJSON, &dto); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIRInvalidJSON, err)
	}
	var blocks []Block
	for _, elem := range dto.Elements {
		if b, ok := projectOrgizeElement(elem); ok {
			blocks = append(blocks, b)
		}
	}
	for _, sec := range dto.Sections {
		appendOrgizeSection(&blocks, sec, 0)
	}
	return AdmitBlocksWithLimits(docID, blocks, lim)
}

func appendOrgizeSection(out *[]Block, sec OrgizeOrgElementsSectionDto, depth int) {
	if depth > maxOrgizeSectionDepth {
		return
	}
	todo := ""
	if sec.Todo != nil {
		todo = strings.TrimSpace(*sec.Todo)
	}
	*out = append(*out, Block{
		Kind:     BlockHeading,
		Level:    sec.Level,
		IsOrg:    true,
		TodoWord: todo,
		Tags:     append([]string(nil), sec.Tags...),
		RawText:  strings.TrimSpace(sec.Title),
	})
	if len(sec.Properties) > 0 {
		rows := make([]DrawerEntry, 0, len(sec.Properties))
		for _, prop := range sec.Properties {
			rows = append(rows, DrawerEntry{Key: prop.Key, Value: prop.Value})
		}
		*out = append(*out, Block{
			Kind:       BlockDrawer,
			IsOrg:      true,
			DrawerName: "PROPERTIES",
			DrawerRows: rows,
		})
	}
	for _, elem := range sec.Elements {
		if b, ok := projectOrgizeElement(elem); ok {
			*out = append(*out, b)
		}
	}
	for _, child := range sec.Children {
		appendOrgizeSection(out, child, depth+1)
	}
}

func projectOrgizeElement(elem map[string]any) (Block, bool) {
	kind, _ := elem["kind"].(string)
	lower := strings.ToLower(strings.TrimSpace(kind))
	switch lower {
	case "sourceblock", "srcblock", "source_block", "code":
		lang, _ := elem["language"].(string)
		if lang == "" {
			lang, _ = elem["lang"].(string)
		}
		code, _ := elem["body"].(string)
		if code == "" {
			code, _ = elem["value"].(string)
		}
		if code == "" {
			code, _ = elem["code"].(string)
		}
		return Block{Kind: BlockCode, IsOrg: true, Lang: lang, Code: code}, true
	case "quoteblock", "quote_block", "quote":
		raw := extractElementText(elem)
		return Block{Kind: BlockQuote, IsOrg: true, RawText: raw}, raw != ""
	case "rule", "horizontalrule", "horizontal_rule":
		return Block{Kind: BlockRule, IsOrg: true}, true
	case "paragraph":
		raw := extractElementText(elem)
		return Block{Kind: BlockParagraph, IsOrg: true, RawText: raw}, raw != ""
	default:
		if raw := extractElementText(elem); raw != "" {
			return Block{Kind: BlockParagraph, IsOrg: true, RawText: raw}, true
		}
		return Block{}, false
	}
}

func extractElementText(elem map[string]any) string {
	for _, k := range []string{"text", "value", "body"} {
		if s, ok := elem[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	if src, ok := elem["source"].(map[string]any); ok {
		if raw, ok := src["raw"].(string); ok {
			return strings.TrimSpace(raw)
		}
	}
	return ""
}
