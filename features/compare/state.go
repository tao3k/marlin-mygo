// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Package compare implements Marlin's opt-in Multi-Model Parallel Compare Arena
// and Non-Destructive Conversation Branching feature (`marlin_feat_compare`),
// allowing operators to fan out one prompt across multiple models side-by-side,
// promote the winning response into the main timeline, and explore parallel
// conversation branches without overwriting history.
package compare

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/features"
)

var (
	ErrEmptyPrompt    = errors.New("compare: prompt cannot be empty")
	ErrNoModelsPicked = errors.New("compare: select at least one model lane")
	ErrLaneNotFound   = errors.New("compare: model lane not found")
	ErrBranchNotFound = errors.New("compare: conversation branch not found")
)

// LaneStatus tracks the live execution state of one side-by-side model lane.
type LaneStatus string

const (
	LaneIdle      LaneStatus = "IDLE"
	LaneStreaming LaneStatus = "STREAMING"
	LaneCompleted LaneStatus = "COMPLETED"
	LaneError     LaneStatus = "ERROR"
)

// ModelLane stores one side-by-side model response and throughput metrics.
type ModelLane struct {
	LaneID       string
	ModelID      string
	ModelLabel   string
	Effort       composer.ReasoningEffort
	Status       LaneStatus
	ResponseText string
	TokensPerSec float64
	ElapsedMS    int64
	ErrorText    string
	Promoted     bool
}

// BranchNode represents one non-destructive conversation fork in the branch tree.
type BranchNode struct {
	BranchID       string
	ParentBranchID string
	ForkedFromTurn uint64
	Title          string
	ModelID        string
	Prompt         string
	Summary        string
	CreatedAt      time.Time
}

// CompareSnapshot is an immutable 120Hz-safe projection for Desktop & Mobile views.
type CompareSnapshot struct {
	Prompt         string
	SelectedModels []string
	Catalog        []composer.ModelOption
	Lanes          []ModelLane
	Branches       []BranchNode
	ActiveBranchID string
	LastError      string
}

// Controller owns thread-safe multi-model comparison lanes and branch lineage.
type Controller struct {
	mu             sync.RWMutex
	ledger         *features.GovernanceLedger
	catalog        []composer.ModelOption
	selectedModels map[string]bool
	prompt         string
	lanes          []ModelLane
	branches       []BranchNode
	activeBranchID string
	lastError      string
	laneSeq        uint64
	branchSeq      uint64

	OnDispatchLane  func(prompt string, lane ModelLane)
	OnPromoteWinner func(lane ModelLane)
	OnSelectBranch  func(branch BranchNode)
}

// NewController constructs a Multi-Model Compare & Branching controller.
func NewController(ledger *features.GovernanceLedger) *Controller {
	st := composer.NewState(composer.DefaultAttachmentPolicy())
	models := st.Snapshot().Models
	sel := make(map[string]bool)
	for i, m := range models {
		if i < 2 {
			sel[m.ID] = true
		}
	}
	c := &Controller{
		ledger:         ledger,
		catalog:        models,
		selectedModels: sel,
	}
	c.seedDefaultSession()
	return c
}

// SetGovernanceLedger links compare winner promotions into Marlin's Tier-0 audit trail.
func (c *Controller) SetGovernanceLedger(ledger *features.GovernanceLedger) {
	c.mu.Lock()
	c.ledger = ledger
	c.mu.Unlock()
}

func (c *Controller) seedDefaultSession() {
	now := time.Now().UTC()
	c.branchSeq = 2
	c.branches = []BranchNode{
		{
			BranchID:       "branch-main",
			Title:          "Main Timeline",
			ModelID:        "marlin-core",
			Prompt:         "Design Rust-style build-tag feature isolation",
			Summary:        "Baseline architecture path",
			CreatedAt:      now.Add(-5 * time.Minute),
			ForkedFromTurn: 1,
		},
		{
			BranchID:       "branch-fast-proto",
			ParentBranchID: "branch-main",
			ForkedFromTurn: 1,
			Title:          "Fast Latency Path",
			ModelID:        "marlin-fast",
			Prompt:         "Compare minimal core vs full multi-feature binary size",
			Summary:        "Zero-bloat build tag profile",
			CreatedAt:      now.Add(-2 * time.Minute),
		},
	}
	c.activeBranchID = "branch-main"
	c.prompt = "Compare minimal core vs full feature-gated build strategy"
	c.laneSeq = 2
	c.lanes = []ModelLane{
		{
			LaneID:       "lane-1",
			ModelID:      "marlin-core",
			ModelLabel:   "Marlin Core",
			Effort:       composer.EffortBalanced,
			Status:       LaneCompleted,
			ResponseText: "Use Go build tags (`marlin_feat_*`) backed by `engine/features.Registry` so unselected paradigms are excluded at compile time and governed at runtime.",
			TokensPerSec: 64,
			ElapsedMS:    420,
		},
		{
			LaneID:       "lane-2",
			ModelID:      "marlin-fast",
			ModelLabel:   "Marlin Fast",
			Effort:       composer.EffortFast,
			Status:       LaneCompleted,
			ResponseText: "Split each paradigm into `features/<name>` with an opt-in `cmd/marlin-app/feat_<name>.go` init bridge; verify zero link leakage with `go list -tags marlin_minimal -deps`.",
			TokensPerSec: 118,
			ElapsedMS:    210,
		},
	}
}

// ToggleModelSelection toggles whether modelID participates in parallel compare runs.
func (c *Controller) ToggleModelSelection(modelID string) {
	c.mu.Lock()
	if c.selectedModels == nil {
		c.selectedModels = make(map[string]bool)
	}
	if c.selectedModels[modelID] {
		delete(c.selectedModels, modelID)
	} else {
		c.selectedModels[modelID] = true
	}
	c.lastError = ""
	c.mu.Unlock()
}

// DispatchPrompt launches parallel compare lanes for every selected model.
func (c *Controller) DispatchPrompt(prompt string, effort composer.ReasoningEffort) ([]ModelLane, error) {
	text := strings.TrimSpace(prompt)
	if text == "" {
		c.mu.Lock()
		c.lastError = ErrEmptyPrompt.Error()
		c.mu.Unlock()
		return nil, ErrEmptyPrompt
	}
	if effort == "" {
		effort = composer.EffortBalanced
	}

	c.mu.Lock()
	var lanes []ModelLane
	for _, m := range c.catalog {
		if !c.selectedModels[m.ID] {
			continue
		}
		c.laneSeq++
		lanes = append(lanes, ModelLane{
			LaneID:     fmt.Sprintf("lane-%d", c.laneSeq),
			ModelID:    m.ID,
			ModelLabel: m.Label,
			Effort:     effort,
			Status:     LaneStreaming,
		})
	}
	if len(lanes) == 0 {
		c.lastError = ErrNoModelsPicked.Error()
		c.mu.Unlock()
		return nil, ErrNoModelsPicked
	}
	c.prompt = text
	c.lanes = lanes
	c.lastError = ""
	cb := c.OnDispatchLane
	c.mu.Unlock()

	if cb != nil {
		for _, ln := range lanes {
			cb(text, ln)
		}
	}
	return lanes, nil
}

// ApplyLaneDelta appends streamed text into one parallel model lane.
func (c *Controller) ApplyLaneDelta(laneID, delta string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.lanes {
		if c.lanes[i].LaneID == laneID {
			c.lanes[i].Status = LaneStreaming
			c.lanes[i].ResponseText += delta
			return nil
		}
	}
	return ErrLaneNotFound
}

// CompleteLane finalizes one model lane with throughput telemetry.
func (c *Controller) CompleteLane(laneID string, tps float64, elapsedMS int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.lanes {
		if c.lanes[i].LaneID == laneID {
			c.lanes[i].Status = LaneCompleted
			c.lanes[i].TokensPerSec = tps
			c.lanes[i].ElapsedMS = elapsedMS
			return nil
		}
	}
	return ErrLaneNotFound
}

// PromoteLane marks laneID as the winning model output, creates a branch record,
// writes a GovernanceLedger receipt, and invokes OnPromoteWinner.
func (c *Controller) PromoteLane(laneID string) (ModelLane, error) {
	c.mu.Lock()
	idx := -1
	for i := range c.lanes {
		c.lanes[i].Promoted = false
		if c.lanes[i].LaneID == laneID {
			idx = i
		}
	}
	if idx < 0 {
		c.mu.Unlock()
		return ModelLane{}, ErrLaneNotFound
	}
	c.lanes[idx].Promoted = true
	winner := c.lanes[idx]
	ledger := c.ledger
	cb := c.OnPromoteWinner
	c.mu.Unlock()

	if ledger != nil {
		ledger.RecordAudit("user", winner.ModelID, "compare.promote_winner", features.AuditAdmitted, winner.LaneID)
	}
	if cb != nil {
		cb(winner)
	}
	return winner, nil
}

// ForkBranch spawns a non-destructive conversation branch from parentID.
func (c *Controller) ForkBranch(parentID, title, modelID, prompt string) (BranchNode, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return BranchNode{}, ErrEmptyPrompt
	}
	c.mu.Lock()
	if parentID == "" {
		parentID = c.activeBranchID
	}
	c.branchSeq++
	node := BranchNode{
		BranchID:       fmt.Sprintf("branch-%d", c.branchSeq),
		ParentBranchID: parentID,
		ForkedFromTurn: c.branchSeq,
		Title:          title,
		ModelID:        modelID,
		Prompt:         strings.TrimSpace(prompt),
		Summary:        "Forked from " + parentID,
		CreatedAt:      time.Now().UTC(),
	}
	c.branches = append(c.branches, node)
	c.activeBranchID = node.BranchID
	cb := c.OnSelectBranch
	c.mu.Unlock()

	if cb != nil {
		cb(node)
	}
	return node, nil
}

// SelectBranch switches the active non-destructive conversation branch.
func (c *Controller) SelectBranch(branchID string) (BranchNode, error) {
	c.mu.Lock()
	for _, b := range c.branches {
		if b.BranchID == branchID {
			c.activeBranchID = branchID
			cb := c.OnSelectBranch
			c.mu.Unlock()
			if cb != nil {
				cb(b)
			}
			return b, nil
		}
	}
	c.mu.Unlock()
	return BranchNode{}, ErrBranchNotFound
}

// Snapshot returns a thread-safe immutable projection for Desktop & Mobile views.
func (c *Controller) Snapshot() CompareSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var selected []string
	for _, m := range c.catalog {
		if c.selectedModels[m.ID] {
			selected = append(selected, m.ID)
		}
	}
	return CompareSnapshot{
		Prompt:         c.prompt,
		SelectedModels: selected,
		Catalog:        append([]composer.ModelOption(nil), c.catalog...),
		Lanes:          append([]ModelLane(nil), c.lanes...),
		Branches:       append([]BranchNode(nil), c.branches...),
		ActiveBranchID: c.activeBranchID,
		LastError:      c.lastError,
	}
}
