// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package canvas implements Marlin's opt-in Live Artifact & Document Version
// Canvas (`marlin_feat_canvas`), pinning code, diffs, and pre-parsed Org-mode
// or Markdown Document IR (zero Go-side parsing) beside the active session
// with v1/v2/v3 version snapshots and inline revision requests.
package canvas

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/richtext"
)

var (
	ErrDocNotFound     = errors.New("canvas: artifact document not found")
	ErrVersionNotFound = errors.New("canvas: artifact version not found")
	ErrEmptyRevision   = errors.New("canvas: revision instruction cannot be empty")
)

// ArtifactKind identifies how a pinned Canvas artifact is rendered.
type ArtifactKind string

const (
	ArtifactCode    ArtifactKind = "code"
	ArtifactRichDoc ArtifactKind = "richdoc" // pre-parsed Org-RZ1 / Markdown IR blocks
	ArtifactDiff    ArtifactKind = "diff"
)

// ArtifactVersion stores one immutable snapshot in an artifact's revision history.
type ArtifactVersion struct {
	Version    int
	Lang       string
	RawText    string
	RichBlocks []richtext.Block
	AuthorRole string
	Summary    string
	CreatedAt  time.Time
}

// ArtifactDoc tracks a pinned artifact and its ordered immutable version history.
type ArtifactDoc struct {
	ID            string
	Title         string
	Kind          ArtifactKind
	Versions      []ArtifactVersion
	ActiveVersion int
}

// CanvasSnapshot is an immutable 120Hz-safe projection for Desktop & Mobile views.
type CanvasSnapshot struct {
	Docs        []ArtifactDoc
	ActiveDocID string
	DiffMode    bool
	LastError   string
}

// Controller manages pinned live Canvas artifacts, immutable versions, and
// revision callbacks without local disk persistence or Go-side markup parsing.
type Controller struct {
	mu          sync.RWMutex
	ledger      *features.GovernanceLedger
	docs        []ArtifactDoc
	activeDocID string
	diffMode    bool
	lastError   string

	OnCopyArtifact    func(text string)
	OnRequestRevision func(docID string, version int, instruction string)
}

// NewController constructs a Live Artifact Canvas controller.
func NewController(ledger *features.GovernanceLedger) *Controller {
	c := &Controller{ledger: ledger}
	c.seedDefaultArtifacts()
	return c
}

// SetGovernanceLedger links revision requests into Marlin's Tier-0 audit trail.
func (c *Controller) SetGovernanceLedger(ledger *features.GovernanceLedger) {
	c.mu.Lock()
	c.ledger = ledger
	c.mu.Unlock()
}

func (c *Controller) seedDefaultArtifacts() {
	now := time.Now().UTC()
	v1Blocks, _ := richtext.AdmitBlocks("canvas-org-v1", []richtext.Block{
		{Kind: richtext.BlockHeading, Level: 1, IsOrg: true, TodoWord: "TODO", Priority: "A", Tags: []string{"marlin", "features"}, RawText: "Feature-Gated Architecture Plan"},
		{Kind: richtext.BlockList, Items: []richtext.ListItem{
			{Check: "X", RawText: "Build engine/features registry and UI Governance console"},
			{Check: "X", RawText: "Ship Marlin-native Multi-Role Agent Room"},
			{Check: " ", RawText: "Verify compile-time zero-bloat pruning across all 3 features"},
		}},
	})
	v2Blocks, _ := richtext.AdmitBlocks("canvas-org-v2", []richtext.Block{
		{Kind: richtext.BlockHeading, Level: 1, IsOrg: true, TodoWord: "DONE", Priority: "A", Tags: []string{"marlin", "features"}, RawText: "Feature-Gated Architecture Plan"},
		{Kind: richtext.BlockList, Items: []richtext.ListItem{
			{Check: "X", RawText: "Build engine/features registry and UI Governance console"},
			{Check: "X", RawText: "Ship Marlin-native Multi-Role Agent Room"},
			{Check: "X", RawText: "Verify compile-time zero-bloat pruning across all 3 features"},
		}},
	})
	c.docs = []ArtifactDoc{
		{
			ID: "art-plan", Title: "architecture.org", Kind: ArtifactRichDoc, ActiveVersion: 2,
			Versions: []ArtifactVersion{
				{Version: 1, Lang: "org", RawText: "* TODO Feature-Gated Architecture Plan\n- [X] Registry\n- [ ] Pruning\n", RichBlocks: v1Blocks, AuthorRole: "Architect", Summary: "Initial draft", CreatedAt: now.Add(-4 * time.Minute)},
				{Version: 2, Lang: "org", RawText: "* DONE Feature-Gated Architecture Plan\n- [X] Registry\n- [X] Pruning\n", RichBlocks: v2Blocks, AuthorRole: "Reviewer", Summary: "Marked pruning verified", CreatedAt: now.Add(-1 * time.Minute)},
			},
		},
		{
			ID: "art-code", Title: "registry.go", Kind: ArtifactCode, ActiveVersion: 2,
			Versions: []ArtifactVersion{
				{Version: 1, Lang: "go", RawText: "package features\n\ntype Registry struct{}\n", AuthorRole: "Architect", Summary: "Skeleton", CreatedAt: now.Add(-3 * time.Minute)},
				{Version: 2, Lang: "go", RawText: "package features\n\ntype Registry struct {\n\tmodules map[FeatureID]FeatureModule\n}\n", AuthorRole: "Coder", Summary: "Added compiled module map", CreatedAt: now},
			},
		},
	}
	c.activeDocID = "art-plan"
}

// UpsertVersion appends a new immutable version snapshot to docID (creating
// docID if new), admitting any RichBlocks fail-closed via richtext.AdmitBlocks.
func (c *Controller) UpsertVersion(docID, title string, kind ArtifactKind, ver ArtifactVersion) (int, error) {
	if strings.TrimSpace(docID) == "" {
		return 0, ErrDocNotFound
	}
	if len(ver.RichBlocks) > 0 {
		admitted, err := richtext.AdmitBlocks(docID, ver.RichBlocks)
		if err != nil {
			return 0, err
		}
		ver.RichBlocks = admitted
	}
	if ver.CreatedAt.IsZero() {
		ver.CreatedAt = time.Now().UTC()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.docs {
		if c.docs[i].ID == docID {
			ver.Version = len(c.docs[i].Versions) + 1
			c.docs[i].Versions = append(c.docs[i].Versions, ver)
			c.docs[i].ActiveVersion = ver.Version
			c.activeDocID = docID
			c.lastError = ""
			return ver.Version, nil
		}
	}
	ver.Version = 1
	if title == "" {
		title = docID
	}
	c.docs = append(c.docs, ArtifactDoc{
		ID:            docID,
		Title:         title,
		Kind:          kind,
		Versions:      []ArtifactVersion{ver},
		ActiveVersion: 1,
	})
	c.activeDocID = docID
	c.lastError = ""
	return 1, nil
}

// SelectDoc switches the active artifact displayed in Canvas.
func (c *Controller) SelectDoc(docID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.docs {
		if c.docs[i].ID == docID {
			c.activeDocID = docID
			c.lastError = ""
			return nil
		}
	}
	return ErrDocNotFound
}

// SelectVersion switches the active snapshot version for docID.
func (c *Controller) SelectVersion(docID string, version int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.docs {
		if c.docs[i].ID == docID {
			if version < 1 || version > len(c.docs[i].Versions) {
				return ErrVersionNotFound
			}
			c.docs[i].ActiveVersion = version
			return nil
		}
	}
	return ErrDocNotFound
}

// ToggleDiffMode toggles between rendered artifact view and v(N-1)->vN line diff.
func (c *Controller) ToggleDiffMode() bool {
	c.mu.Lock()
	c.diffMode = !c.diffMode
	d := c.diffMode
	c.mu.Unlock()
	return d
}

// RequestRevision dispatches an inline revision request against the active artifact version.
func (c *Controller) RequestRevision(instruction string) error {
	inst := strings.TrimSpace(instruction)
	if inst == "" {
		c.mu.Lock()
		c.lastError = ErrEmptyRevision.Error()
		c.mu.Unlock()
		return ErrEmptyRevision
	}
	c.mu.Lock()
	docID := c.activeDocID
	ver := 1
	for _, d := range c.docs {
		if d.ID == docID {
			ver = d.ActiveVersion
			break
		}
	}
	c.lastError = ""
	ledger := c.ledger
	cb := c.OnRequestRevision
	c.mu.Unlock()

	if ledger != nil {
		ledger.RecordAudit("user", fmt.Sprintf("%s@v%d", docID, ver), "canvas.request_revision", features.AuditAdmitted, inst)
	}
	if cb != nil {
		cb(docID, ver, inst)
	}
	return nil
}

// Snapshot returns an immutable view safe for 120Hz GPU rendering.
func (c *Controller) Snapshot() CanvasSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	docs := make([]ArtifactDoc, len(c.docs))
	for i, d := range c.docs {
		cp := d
		cp.Versions = append([]ArtifactVersion(nil), d.Versions...)
		docs[i] = cp
	}
	return CanvasSnapshot{
		Docs:        docs,
		ActiveDocID: c.activeDocID,
		DiffMode:    c.diffMode,
		LastError:   c.lastError,
	}
}

// ComputeLineDiff returns simple unified `-`/`+`/` ` diff lines comparing prev vs cur.
func ComputeLineDiff(prevText, curText string) []string {
	prevLines := strings.Split(strings.TrimRight(prevText, "\n"), "\n")
	curLines := strings.Split(strings.TrimRight(curText, "\n"), "\n")
	prevSet := make(map[string]bool, len(prevLines))
	for _, l := range prevLines {
		prevSet[l] = true
	}
	curSet := make(map[string]bool, len(curLines))
	for _, l := range curLines {
		curSet[l] = true
	}
	var out []string
	for _, l := range prevLines {
		if !curSet[l] && l != "" {
			out = append(out, "- "+l)
		}
	}
	for _, l := range curLines {
		if !prevSet[l] && l != "" {
			out = append(out, "+ "+l)
		} else if l != "" {
			out = append(out, "  "+l)
		}
	}
	return out
}
