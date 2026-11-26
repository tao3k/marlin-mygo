// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package compare

import (
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/input"
	"github.com/tao3k/marlin-mygo/engine/resilience"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

const (
	BufferIDCompare    = "feature:compare"
	CommandOpenCompare = "feature.open-compare"
	compareDraftScope  = "compare:prompt"
)

// ViewMode switches between Parallel Model Arena and Non-Destructive Branch Tree.
type ViewMode string

const (
	ModeArena    ViewMode = "arena"
	ModeBranches ViewMode = "branches"
)

// View renders Marlin's Multi-Model Compare Arena & Branch Tree on Desktop
// (side-by-side split columns) and Phone (compact lane switcher, zero overflow).
type View struct {
	Ctrl       *Controller
	Resilience *resilience.SessionResilience

	mu             sync.Mutex
	mode           ViewMode
	draft          string
	draftRev       uint64
	activeLane     int
	onDraftChanged func(scopeID, text string)
}

// NewView constructs a Marlin-native Compare GPU view.
func NewView(ctrl *Controller) *View {
	if ctrl == nil {
		ctrl = NewController(nil)
	}
	return &View{
		Ctrl: ctrl,
		mode: ModeArena,
	}
}

// SetPromptDraft programmatically sets the parallel compare prompt input and
// persists it into the shared SessionResilience store for power-loss recovery.
func (v *View) SetPromptDraft(text string) {
	v.mu.Lock()
	v.draft = text
	v.draftRev++
	res := v.Resilience
	cb := v.onDraftChanged
	v.mu.Unlock()
	if res != nil {
		res.SetDraft(compareDraftScope, text)
		if cb != nil {
			cb(compareDraftScope, text)
		}
	}
}

// PromptDraft returns the active parallel compare prompt draft buffer.
func (v *View) PromptDraft() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.draft
}

// RestoreDraftFromResilience restores any power-loss persisted compare prompt
// into the GPU input widget.
func (v *View) RestoreDraftFromResilience() string {
	v.mu.Lock()
	res := v.Resilience
	v.mu.Unlock()
	if res == nil {
		return ""
	}
	saved := res.Draft(compareDraftScope)
	if saved != "" {
		v.mu.Lock()
		v.draft = saved
		v.draftRev++
		v.mu.Unlock()
	}
	return saved
}

// Module implements features.FeatureModule for opt-in build-tag compilation.
type Module struct {
	View *View
}

// NewModule constructs an opt-in FeatureModule for Multi-Model Compare & Branching.
func NewModule(ledger *features.GovernanceLedger) *Module {
	return &Module{
		View: NewView(NewController(ledger)),
	}
}

// SetGovernanceLedger links compare winner promotions into Marlin's Tier-0 audit ledger.
func (m *Module) SetGovernanceLedger(ledger *features.GovernanceLedger) {
	if m != nil && m.View != nil && m.View.Ctrl != nil {
		m.View.Ctrl.SetGovernanceLedger(ledger)
	}
}

// SetResilience attaches the shared crash-durable SessionResilience store and
// immediately restores any unsent compare prompt draft after a restart.
func (m *Module) SetResilience(r *resilience.SessionResilience, onDraftChanged func(scopeID, text string)) {
	if m == nil || m.View == nil || r == nil {
		return
	}
	m.View.mu.Lock()
	m.View.Resilience = r
	m.View.onDraftChanged = onDraftChanged
	m.View.mu.Unlock()
	m.View.RestoreDraftFromResilience()
}

// SetPromoteHandler wires a transport-free callback invoked when a user promotes a winning lane.
func (m *Module) SetPromoteHandler(fn func(modelLabel, text string)) {
	if m == nil || m.View == nil || m.View.Ctrl == nil || fn == nil {
		return
	}
	m.View.Ctrl.OnPromoteWinner = func(lane ModelLane) {
		fn(lane.ModelLabel, lane.ResponseText)
	}
}

func (m *Module) Meta() features.FeatureMeta {
	return features.FeatureMeta{
		ID:                   features.FeatureCompare,
		Name:                 "Multi-Model Compare & Branches",
		Description:          "T3-inspired side-by-side parallel model streaming, winner promotion, and non-destructive branch tree",
		Category:             "reasoning",
		TrustTier:            features.Tier0HostNative,
		RequiredCapabilities: []string{"turn.branch", "model.compare"},
		DefaultEnabled:       false,
	}
}

func (m *Module) Register(host *features.Host) error {
	host.AddBuffer(shell.PaneBuffer{
		BufferID: BufferIDCompare,
		Title:    "Model Compare",
		Kind:     "native-gpu",
	}, func(surface any, theme any) {
		c, ok1 := surface.(*ui.Context)
		t, ok2 := theme.(*ui.Theme)
		if ok1 && ok2 {
			m.View.Render(c, t, false)
		}
	})
	host.AddCommand(input.CommandEntry{
		Name:        CommandOpenCompare,
		Description: "Open Multi-Model Compare Arena & Branch Tree buffer",
	})
	return nil
}

// Render paints the Multi-Model Compare Arena & Branch Tree on Desktop or Mobile.
func (v *View) Render(c *ui.Context, t *ui.Theme, compact bool) {
	dt := mygoshell.DeriveTheme("compare", "Compare", themeFromUI(t))
	snap := v.Ctrl.Snapshot()

	v.mu.Lock()
	mode := v.mode
	draft := v.draft
	prevDraft := draft
	draftRev := v.draftRev
	activeLane := v.activeLane
	res := v.Resilience
	draftCB := v.onDraftChanged
	v.mu.Unlock()

	if activeLane < 0 || activeLane >= len(snap.Lanes) {
		activeLane = 0
	}

	selectedSet := make(map[string]bool, len(snap.SelectedModels))
	for _, id := range snap.SelectedModels {
		selectedSet[id] = true
	}

	var (
		nextMode       = mode
		nextLaneIdx    = activeLane
		toggleModelID  string
		runCompareNow  bool
		promoteLaneID  string
		forkFromLaneID string
		selectBranchID string
	)

	root := ui.Column(c.Key("compare-root")).Grow(1).MinWidth(0).Padding(10).Gap(8).Radius(12).
		Background(rgbColor(dt.Surface)).Border(1, rgbaColor(dt.Border, 0.85))
	if !compact && root.Bounds().W > 0 && root.Bounds().W < 720 {
		compact = true
	}

	root.Children(func() {
		segs := []mygoshell.SegmentedItem{
			{ID: string(ModeArena), Label: fmt.Sprintf("Arena (%d)", len(snap.Lanes))},
			{ID: string(ModeBranches), Label: fmt.Sprintf("Branches (%d)", len(snap.Branches))},
		}
		if compact {
			ui.Column(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					mygoshell.AppleBadgePill(c, "MODEL COMPARE", dt.Accent, dt)
					ui.Text(c, snap.ActiveBranchID).FontSize(12).FontWeight(600).TextColor(rgbColor(dt.InkSecondary))
				})
				if chosen := mygoshell.AppleSegmentedControl(c, "cmp-mode", segs, string(mode), dt, true); chosen != "" {
					nextMode = ViewMode(chosen)
				}
			})
		} else {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				mygoshell.AppleBadgePill(c, "MODEL COMPARE", dt.Accent, dt)
				ui.Text(c, "Parallel Multi-Model Arena").FontSize(14).FontWeight(700).TextColor(rgbColor(dt.Ink))
				mygoshell.AppleBadgePill(c, "Branch: "+snap.ActiveBranchID, dt.Info, dt)
				ui.Spacer(c)
				if chosen := mygoshell.AppleSegmentedControl(c, "cmp-mode", segs, string(mode), dt, true); chosen != "" {
					nextMode = ViewMode(chosen)
				}
			})
		}

		if snap.LastError != "" {
			ui.Text(c, snap.LastError).FontSize(11.5).FontWeight(600).TextColor(rgbColor(dt.Danger))
		}
		if res != nil {
			resSnap := res.Snapshot(compareDraftScope)
			if banner := resSnap.BannerText(); banner != "" {
				bTone := dt.Warning
				if resSnap.State == resilience.ConnOffline {
					bTone = dt.Danger
				}
				ui.Row(c.Key("cmp-continuity-banner")).Padding(5, 9).Gap(6).Radius(10).AlignItems(ui.Center).
					Background(rgbaColor(bTone, 0.10)).Border(1, rgbaColor(bTone, 0.35)).Children(func() {
					mygoshell.AppleBadgePill(c, string(resSnap.State), bTone, dt)
					ui.Text(c, banner).FontSize(11).FontWeight(600).TextColor(rgbColor(dt.Ink))
				})
			}
		}

		if mode == ModeBranches {
			v.renderBranchesView(c, dt, snap, &selectBranchID)
			return
		}

		// Prompt banner + side-by-side (Desktop) or tabbed (Mobile) model lanes.
		if snap.Prompt != "" {
			ui.Row(c).Padding(7, 10).Gap(6).Radius(10).AlignItems(ui.Center).
				Background(rgbColor(dt.SurfaceRecessed)).Border(1, rgbaColor(dt.Border, 0.7)).Children(func() {
				mygoshell.AppleBadgePill(c, "PROMPT", dt.Info, dt)
				ui.Text(c, snap.Prompt).FontSize(12.5).FontWeight(600).TextColor(rgbColor(dt.Ink))
			})
		}

		if len(snap.Lanes) > 0 {
			if compact {
				laneSegs := make([]mygoshell.SegmentedItem, 0, len(snap.Lanes))
				for i, ln := range snap.Lanes {
					laneSegs = append(laneSegs, mygoshell.SegmentedItem{
						ID:    fmt.Sprintf("%d", i),
						Label: ln.ModelLabel,
					})
				}
				if chosen := mygoshell.AppleSegmentedControl(c, "cmp-mob-lane", laneSegs, fmt.Sprintf("%d", activeLane), dt, true); chosen != "" {
					fmt.Sscanf(chosen, "%d", &nextLaneIdx)
				}
				renderLaneCard(c, dt, snap.Lanes[activeLane], &promoteLaneID, &forkFromLaneID)
			} else {
				ui.Row(c).Grow(1).MinWidth(0).Gap(10).AlignItems(ui.Stretch).Children(func() {
					for _, ln := range snap.Lanes {
						renderLaneCard(c, dt, ln, &promoteLaneID, &forkFromLaneID)
					}
				})
			}
		}

		ui.Spacer(c)

		// Shared compare prompt bar with model toggles.
		ui.Column(c).Padding(8, 10).Gap(6).Radius(16).
			Background(rgbColor(dt.SurfaceElevated)).Border(1, rgbaColor(dt.Border, 0.85)).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				for _, m := range snap.Catalog {
					model := m
					variant := mygoshell.BtnGlass
					if selectedSet[model.ID] {
						variant = mygoshell.BtnTinted
					}
					if mygoshell.ApplePillButton(c, "cmp-m-"+model.ID, model.Label, variant, dt, nil, true).Clicked() {
						toggleModelID = model.ID
					}
				}
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInputBase(c.Key(fmt.Sprintf("cmp-in-%d", draftRev)), &draft).
					Placeholder("Run one prompt across selected models in parallel...").
					FontSize(13).TextColor(rgbColor(dt.Ink)).Grow(1).Padding(4, 2)
				if mygoshell.ApplePillButton(c, "cmp-run", "Compare", mygoshell.BtnPrimary, dt, nil, true).Clicked() {
					runCompareNow = true
				}
			})
		})
	})

	v.mu.Lock()
	v.mode = nextMode
	v.draft = draft
	v.activeLane = nextLaneIdx
	v.mu.Unlock()
	if draft != prevDraft && res != nil {
		res.SetDraft(compareDraftScope, draft)
		if draftCB != nil {
			draftCB(compareDraftScope, draft)
		}
	}

	if toggleModelID != "" {
		v.Ctrl.ToggleModelSelection(toggleModelID)
	}
	if runCompareNow {
		if _, err := v.Ctrl.DispatchPrompt(draft, composer.EffortBalanced); err == nil {
			v.SetPromptDraft("")
		}
	}
	if promoteLaneID != "" {
		_, _ = v.Ctrl.PromoteLane(promoteLaneID)
	}
	if forkFromLaneID != "" {
		for _, ln := range snap.Lanes {
			if ln.LaneID == forkFromLaneID {
				_, _ = v.Ctrl.ForkBranch(snap.ActiveBranchID, ln.ModelLabel+" Fork", ln.ModelID, snap.Prompt)
				break
			}
		}
	}
	if selectBranchID != "" {
		_, _ = v.Ctrl.SelectBranch(selectBranchID)
		v.mu.Lock()
		v.mode = ModeArena
		v.mu.Unlock()
	}
}

func renderLaneCard(
	c *ui.Context,
	dt mygoshell.DerivedTheme,
	lane ModelLane,
	promoteID *string,
	forkID *string,
) {
	ui.Column(c.Key("cmp-lane-"+lane.LaneID)).Grow(1).MinWidth(0).Padding(12).Gap(8).Radius(14).
		Background(rgbColor(dt.SurfaceElevated)).
		Border(1, laneBorder(lane.Promoted, dt)).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, lane.ModelLabel).FontSize(13.5).FontWeight(700).TextColor(rgbColor(dt.Ink))
			stTone := dt.Info
			if lane.Status == LaneStreaming {
				stTone = dt.Warning
			} else if lane.Status == LaneCompleted {
				stTone = dt.Success
			}
			mygoshell.AppleBadgePill(c, string(lane.Status), stTone, dt)
			ui.Spacer(c)
			if lane.TokensPerSec > 0 {
				mygoshell.AppleBadgePill(c, fmt.Sprintf("%.0f tok/s · %dms", lane.TokensPerSec, lane.ElapsedMS), dt.Info, dt)
			}
		})
		body := strings.TrimSpace(lane.ResponseText)
		if body == "" {
			body = "Streaming response..."
		}
		ui.Text(c, body).FontSize(12.5).LineHeight(1.42).TextColor(rgbColor(dt.InkSecondary))
		ui.Spacer(c)
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			pLbl := "Promote " + lane.ModelLabel
			pVar := mygoshell.BtnTinted
			if lane.Promoted {
				pLbl = "Promoted"
				pVar = mygoshell.BtnSuccess
			}
			if mygoshell.ApplePillButton(c, "cmp-prom-"+lane.LaneID, pLbl, pVar, dt, nil, true).Clicked() {
				*promoteID = lane.LaneID
			}
			if mygoshell.ApplePillButton(c, "cmp-fork-"+lane.LaneID, "Branch "+lane.ModelLabel, mygoshell.BtnGlass, dt, nil, true).Clicked() {
				*forkID = lane.LaneID
			}
		})
	})
}

func (v *View) renderBranchesView(
	c *ui.Context,
	dt mygoshell.DerivedTheme,
	snap CompareSnapshot,
	selectID *string,
) {
	ui.Column(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Stretch).Children(func() {
		ui.Text(c, "NON-DESTRUCTIVE CONVERSATION BRANCH TREE").
			FontSize(10.5).FontWeight(700).LetterSpacing(0.5).TextColor(rgbColor(dt.InkMuted))
		for _, b := range snap.Branches {
			branch := b
			mygoshell.AppleGlassCard(c, "cmp-br-"+branch.BranchID, dt, 12, 10, 5, func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, branch.Title).FontSize(13).FontWeight(700).TextColor(rgbColor(dt.Ink))
					mygoshell.AppleBadgePill(c, branch.ModelID, dt.Info, dt)
					if branch.BranchID == snap.ActiveBranchID {
						mygoshell.AppleBadgePill(c, "ACTIVE", dt.Success, dt)
					}
					ui.Spacer(c)
					if branch.BranchID != snap.ActiveBranchID {
						if mygoshell.ApplePillButton(c, "cmp-sw-"+branch.BranchID, "Switch "+branch.BranchID, mygoshell.BtnTinted, dt, nil, true).Clicked() {
							*selectID = branch.BranchID
						}
					}
				})
				if branch.ParentBranchID != "" {
					ui.Textf(c, "Parent: %s · %s", branch.ParentBranchID, branch.Summary).
						FontSize(11).TextColor(rgbColor(dt.InkMuted))
				}
			})
		}
	})
}

func laneBorder(promoted bool, dt mygoshell.DerivedTheme) ui.Color {
	if promoted {
		return rgbColor(dt.Success)
	}
	return rgbaColor(dt.Border, 0.85)
}

func themeFromUI(t *ui.Theme) schema.UITheme {
	if t == nil {
		return schema.DefaultUITheme()
	}
	if int(t.Background.R)+int(t.Background.G)+int(t.Background.B) > 380 {
		return mygoshell.ThemeByID("paper").Base
	}
	return mygoshell.ThemeByID("midnight").Base
}

func rgbColor(c schema.RGB) ui.Color {
	return ui.RGB(c.R, c.G, c.B)
}

func rgbaColor(c schema.RGB, a float32) ui.Color {
	return ui.RGBA(c.R, c.G, c.B, a)
}
