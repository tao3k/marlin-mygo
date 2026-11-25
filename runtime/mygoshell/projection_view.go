// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

// ProjectionTab selects which secret-free marlin.projection.v1 surface is shown.
type ProjectionTab string

const (
	ProjectionTabGraph     ProjectionTab = "graph"
	ProjectionTabDiff      ProjectionTab = "diff"
	ProjectionTabProcesses ProjectionTab = "processes"
)

// GraphNodeReceiptView projects one typed per-node execution receipt from
// marlin.projection.v1 (GraphQueryVisibleSurface: Topology + Evidence).
type GraphNodeReceiptView struct {
	NodeID      string
	Kind        string
	Phase       string
	DurationMS  int64
	ReceiptID   string
	GatewayNote string
	Summary     string
}

// WorkspaceDiffFileView projects one bounded read-only file diff summary from
// marlin.projection.v1 (GraphQueryVisibleSurface: Workspace).
type WorkspaceDiffFileView struct {
	Path      string
	Status    string // "modified" | "added" | "deleted"
	Additions int
	Deletions int
	HunkLines []string
}

// ManagedProcessView projects one managed background process snapshot from
// marlin.projection.v1 (GraphQueryVisibleSurface: Processes).
type ManagedProcessView struct {
	ProcessID string
	Command   string
	State     string // "running" | "exited" | "failed"
	ExitCode  int
	UptimeSec int64
	TailLines []string
}

// ProjectionSnapshot is an immutable, secret-free point-in-time view of all
// three marlin.projection.v1 surfaces rendered at 120Hz on native GPU UI.
type ProjectionSnapshot struct {
	GraphNodes []GraphNodeReceiptView
	DiffFiles  []WorkspaceDiffFileView
	Processes  []ManagedProcessView
}

// ProjectionController manages thread-safe projection state and selection for
// the Tier-0 native GPU Projection Inspector drawer (0 MB WebView RAM).
type ProjectionController struct {
	mu               sync.RWMutex
	snap             ProjectionSnapshot
	activeTab        ProjectionTab
	selectedNodeID   string
	selectedDiffPath string
	selectedProcID   string
	diffSplitMode    bool
	diffFilter       string
	procFilter       string
	lastCopiedID     string

	// Thin interactive callbacks bridging read-only projections straight into the Composer.
	OnMentionFile       func(path string)
	OnAttachDiff        func(file WorkspaceDiffFileView)
	OnAttachProcessLogs func(proc ManagedProcessView)
	OnCopyText          func(text string)
}

func NewProjectionController(initial ProjectionSnapshot) *ProjectionController {
	pc := &ProjectionController{
		snap:      initial,
		activeTab: ProjectionTabGraph,
	}
	pc.normalizeSelectionsLocked()
	return pc
}

func (p *ProjectionController) SetSnapshot(snap ProjectionSnapshot) {
	p.mu.Lock()
	p.snap = snap
	p.normalizeSelectionsLocked()
	p.mu.Unlock()
}

func (p *ProjectionController) SetActiveTab(tab ProjectionTab) {
	p.mu.Lock()
	p.activeTab = tab
	p.mu.Unlock()
}

func (p *ProjectionController) ActiveTab() ProjectionTab {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.activeTab
}

// SetDiffSplitMode toggles between Unified (false) and side-by-side Split (true) diff view.
func (p *ProjectionController) SetDiffSplitMode(split bool) {
	p.mu.Lock()
	p.diffSplitMode = split
	p.mu.Unlock()
}

// SetDiffFilter sets the live path filter substring for Workspace Diff files.
func (p *ProjectionController) SetDiffFilter(q string) {
	p.mu.Lock()
	p.diffFilter = q
	p.mu.Unlock()
}

// SetProcessFilter sets the live substring filter for Managed Process tail logs.
func (p *ProjectionController) SetProcessFilter(q string) {
	p.mu.Lock()
	p.procFilter = q
	p.mu.Unlock()
}

func (p *ProjectionController) normalizeSelectionsLocked() {
	if p.selectedNodeID == "" && len(p.snap.GraphNodes) > 0 {
		p.selectedNodeID = p.snap.GraphNodes[0].NodeID
	}
	if p.selectedDiffPath == "" && len(p.snap.DiffFiles) > 0 {
		p.selectedDiffPath = p.snap.DiffFiles[0].Path
	}
	if p.selectedProcID == "" && len(p.snap.Processes) > 0 {
		p.selectedProcID = p.snap.Processes[0].ProcessID
	}
}

func derivedThemeFromUITheme(t *ui.Theme) DerivedTheme {
	if t == nil {
		return DeriveTheme("midnight", "Midnight", StudioDarkPalette())
	}
	return DeriveTheme("runtime", "Runtime", schema.UITheme{
		Canvas:  schema.RGB{R: t.Background.R, G: t.Background.G, B: t.Background.B},
		Bar:     schema.RGB{R: t.Surface.R, G: t.Surface.G, B: t.Surface.B},
		Ink:     schema.RGB{R: t.Text.R, G: t.Text.G, B: t.Text.B},
		Accent:  schema.RGB{R: t.Accent.R, G: t.Accent.G, B: t.Accent.B},
		Info:    schema.RGB{R: t.Accent.R, G: t.Accent.G, B: t.Accent.B},
		Success: schema.RGB{R: t.Success.R, G: t.Success.G, B: t.Success.B},
		Warning: schema.RGB{R: t.Warning.R, G: t.Warning.G, B: t.Warning.B},
		Muted:   schema.RGB{R: t.TextMuted.R, G: t.TextMuted.G, B: t.TextMuted.B},
	})
}

// Render paints the native GPU Projection Inspector modal with tab switching
// across Graph Receipts, Workspace Diff, and Managed Processes.
func (p *ProjectionController) Render(c *ui.Context, t *ui.Theme, onClose func()) {
	p.mu.RLock()
	snap := p.snap
	tab := p.activeTab
	selNode := p.selectedNodeID
	selDiff := p.selectedDiffPath
	selProc := p.selectedProcID
	p.mu.RUnlock()

	dt := derivedThemeFromUITheme(t)

	ui.Column(c).Width(720).Gap(12).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "Projection Inspector (marlin.projection.v1 · secrets hidden)").FontSize(15).FontWeight(700).TextColor(toColor(dt.Ink))
			ui.Spacer(c)
			if onClose != nil {
				if ApplePillButton(c, "close-projection", "Close", BtnGlass, dt, svgIconXMark, true).Clicked() {
					onClose()
				}
			}
		})

		if chosen := AppleSegmentedControl(c, "proj-tab", []SegmentedItem{
			{ID: string(ProjectionTabGraph), Label: fmt.Sprintf("Graph Receipts (%d)", len(snap.GraphNodes))},
			{ID: string(ProjectionTabDiff), Label: fmt.Sprintf("Workspace Diff (%d)", len(snap.DiffFiles))},
			{ID: string(ProjectionTabProcesses), Label: fmt.Sprintf("Processes (%d)", len(snap.Processes))},
		}, string(tab), dt, true); chosen != "" {
			p.SetActiveTab(ProjectionTab(chosen))
		}

		switch tab {
		case ProjectionTabDiff:
			p.renderDiffSurface(c, t, dt, snap.DiffFiles, selDiff)
		case ProjectionTabProcesses:
			p.renderProcessSurface(c, t, dt, snap.Processes, selProc)
		default:
			p.renderGraphSurface(c, t, dt, snap.GraphNodes, selNode)
		}
	})
}

func (p *ProjectionController) renderGraphSurface(c *ui.Context, t *ui.Theme, dt DerivedTheme, nodes []GraphNodeReceiptView, selectedID string) {
	if len(nodes) == 0 {
		ui.Text(c, "No graph-loop node receipts recorded yet.").TextColor(t.TextMuted)
		return
	}
	var maxDur int64 = 1
	limit := min(len(nodes), 16)
	for i := 0; i < limit; i++ {
		if nodes[i].DurationMS > maxDur {
			maxDur = nodes[i].DurationMS
		}
	}
	var active *GraphNodeReceiptView
	ui.Column(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
		// Harness-style Pipeline Stage Stepper with semantic phase dot + proportional latency bar.
		for i := 0; i < limit; i++ {
			n := nodes[i]
			if n.NodeID == selectedID {
				cp := n
				active = &cp
			}
			label := fmt.Sprintf("%s [%s] · %s · %dms", n.NodeID, n.Kind, n.Phase, n.DurationMS)
			phaseTone := dt.Success
			phaseGlyph := "✓"
			switch strings.ToLower(n.Phase) {
			case "running", "active":
				phaseTone = dt.Accent
				phaseGlyph = "●"
			case "failed", "error":
				phaseTone = dt.Danger
				phaseGlyph = "✕"
			case "denied", "gated", "waiting_gate":
				phaseTone = dt.Warning
				phaseGlyph = "⚠"
			}
			selected := n.NodeID == selectedID
			rowBg := toColor(dt.SurfaceElevated)
			borderCol := glassBorderColor(dt)
			if selected {
				rowBg = rgbaFromRGB(dt.Accent, 0.14)
				borderCol = rgbaFromRGB(dt.Accent, 0.42)
			}
			barW := float32(64) * float32(n.DurationMS) / float32(maxDur)
			if barW < 6 {
				barW = 6
			}
			rowBtn := ui.ButtonBase(c.Key("graph-node-"+n.NodeID)).Label(label).
				Padding(7, 10).Radius(10).AlignItems(ui.Center).Gap(8).
				Background(rowBg).Border(1, borderCol)
			rowBtn.Children(func() {
				ui.Text(c, phaseGlyph).FontSize(12).FontWeight(700).TextColor(toColor(phaseTone))
				ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(toColor(dt.Ink))
				ui.Spacer(c)
				// Proportional stage duration bar.
				ui.Row(c).Width(64).Height(6).Radius(3).Background(toColor(dt.SurfaceRecessed)).Children(func() {
					ui.Box(c).Width(barW).Height(6).Radius(3).Background(toColor(phaseTone))
				})
			})
			if rowBtn.Clicked() {
				p.mu.Lock()
				p.selectedNodeID = n.NodeID
				p.mu.Unlock()
			}
		}
		if active != nil {
			AppleGlassCard(c, "proj-graph-detail", dt, 14, 12, 6, func() {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Textf(c, "Node Receipt: %s", active.ReceiptID).FontWeight(700).TextColor(toColor(dt.Accent))
					ui.Spacer(c)
					AppleBadgePill(c, strings.ToUpper(active.Kind), dt.Info, dt)
				})
				if active.Summary != "" {
					ui.Text(c, active.Summary).FontSize(13).LineHeight(1.4).TextColor(toColor(dt.Ink))
				}
				if active.GatewayNote != "" {
					ui.Row(c).Padding(6, 10).Radius(8).
						Background(rgbaFromRGB(dt.Warning, 0.12)).
						BorderWidth(0, 0, 0, 2).BorderColor(toColor(dt.Warning)).Children(func() {
						ui.Textf(c, "Gateway Evidence: %s", active.GatewayNote).FontSize(12).FontWeight(600).TextColor(toColor(dt.Warning))
					})
				}
			})
		}
	})
}

func (p *ProjectionController) renderDiffSurface(c *ui.Context, t *ui.Theme, dt DerivedTheme, files []WorkspaceDiffFileView, selectedPath string) {
	if len(files) == 0 {
		ui.Text(c, "Workspace clean — zero pending file diffs.").TextColor(t.TextMuted)
		return
	}
	p.mu.RLock()
	diffFilter := p.diffFilter
	splitMode := p.diffSplitMode
	lastCopied := p.lastCopiedID
	onMention := p.OnMentionFile
	onAttach := p.OnAttachDiff
	onCopy := p.OnCopyText
	p.mu.RUnlock()

	var (
		active        *WorkspaceDiffFileView
		mentionPath   string
		attachFile    *WorkspaceDiffFileView
		copyPatchText string
		copyKey       string
		nextSplit     = splitMode
	)

	ui.Column(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
		// Filter + Unified/Split diff mode bar.
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Padding(4, 10).Radius(10).Grow(1).
				Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
				ui.TextInputBase(c.Key("proj-diff-filter"), &diffFilter).
					Placeholder("Filter changed files...").
					FontSize(12).TextColor(toColor(dt.Ink)).Grow(1)
			})
			curMode := "unified"
			if splitMode {
				curMode = "split"
			}
			if chosen := AppleSegmentedControl(c, "diff-mode", []SegmentedItem{
				{ID: "unified", Label: "Unified"},
				{ID: "split", Label: "Split"},
			}, curMode, dt, true); chosen != "" {
				nextSplit = chosen == "split"
			}
		})

		q := strings.ToLower(strings.TrimSpace(diffFilter))
		filtered := make([]WorkspaceDiffFileView, 0, len(files))
		for _, f := range files {
			if q == "" || strings.Contains(strings.ToLower(f.Path), q) {
				filtered = append(filtered, f)
			}
		}
		if len(filtered) == 0 {
			ui.Textf(c, "No changed files match %q.", diffFilter).FontSize(12).TextColor(toColor(dt.InkMuted))
			return
		}

		limit := min(len(filtered), 16)
		for i := 0; i < limit; i++ {
			f := filtered[i]
			if f.Path == selectedPath || (active == nil && i == 0) {
				cp := f
				active = &cp
			}
			label := fmt.Sprintf("%s (%s +%d/-%d)", f.Path, f.Status, f.Additions, f.Deletions)
			selected := active != nil && f.Path == active.Path
			rowBg := toColor(dt.SurfaceElevated)
			borderCol := glassBorderColor(dt)
			if selected {
				rowBg = rgbaFromRGB(dt.Accent, 0.14)
				borderCol = rgbaFromRGB(dt.Accent, 0.42)
			}
			totalDelta := f.Additions + f.Deletions
			addW := float32(28)
			delW := float32(0)
			if totalDelta > 0 {
				addW = float32(56) * float32(f.Additions) / float32(totalDelta)
				delW = 56 - addW
			}
			rowBtn := ui.ButtonBase(c.Key("diff-file-"+f.Path)).Label(label).
				Padding(7, 10).Radius(10).AlignItems(ui.Center).Gap(8).
				Background(rowBg).Border(1, borderCol)
			rowBtn.Children(func() {
				ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(toColor(dt.Ink))
				ui.Spacer(c)
				ui.Row(c).Width(56).Height(6).Radius(3).Background(toColor(dt.SurfaceRecessed)).Clip().Children(func() {
					if addW > 0 {
						ui.Box(c).Width(addW).Height(6).Background(toColor(dt.Success))
					}
					if delW > 0 {
						ui.Box(c).Width(delW).Height(6).Background(toColor(dt.Danger))
					}
				})
			})
			if rowBtn.Clicked() {
				p.mu.Lock()
				p.selectedDiffPath = f.Path
				p.mu.Unlock()
			}
		}
		if active != nil {
			AppleGlassCard(c, "proj-diff-detail", dt, 14, 12, 6, func() {
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Textf(c, "Diff Preview: %s", active.Path).FontWeight(700).TextColor(toColor(dt.Accent))
					ui.Spacer(c)
					AppleBadgePill(c, fmt.Sprintf("+%d", active.Additions), dt.Success, dt)
					AppleBadgePill(c, fmt.Sprintf("-%d", active.Deletions), dt.Danger, dt)
					if onMention != nil {
						if ApplePillButton(c, "diff-mention-"+active.Path, "@ Mention File", BtnGlass, dt, nil, true).Clicked() {
							mentionPath = active.Path
						}
					}
					if onAttach != nil {
						if ApplePillButton(c, "diff-attach-"+active.Path, "Attach Diff", BtnTinted, dt, svgIconPaperclip, true).Clicked() {
							cp := *active
							attachFile = &cp
						}
					}
					if onCopy != nil {
						cKey := "diff-copy-" + active.Path
						cLbl := "Copy Patch"
						cVar := BtnGlass
						if lastCopied == cKey {
							cLbl = "Copied"
							cVar = BtnSuccess
						}
						if ApplePillButton(c, cKey, cLbl, cVar, dt, svgIconClipboard, true).Clicked() {
							copyKey = cKey
							copyPatchText = strings.Join(active.HunkLines, "\n")
						}
					}
				})
				if nextSplit {
					renderSplitDiffLines(c, dt, active.HunkLines)
				} else {
					renderUnifiedDiffLines(c, dt, active.HunkLines)
				}
			})
		}
	})

	p.mu.Lock()
	p.diffFilter = diffFilter
	p.diffSplitMode = nextSplit
	if copyKey != "" {
		p.lastCopiedID = copyKey
	}
	p.mu.Unlock()

	if mentionPath != "" && onMention != nil {
		onMention(mentionPath)
	}
	if attachFile != nil && onAttach != nil {
		onAttach(*attachFile)
	}
	if copyKey != "" && onCopy != nil {
		onCopy(copyPatchText)
	}
}

func renderUnifiedDiffLines(c *ui.Context, dt DerivedTheme, lines []string) {
	ui.Column(c).Padding(6).Gap(2).Radius(10).Background(toColor(dt.SurfaceRecessed)).Children(func() {
		lineLimit := min(len(lines), 24)
		for i := 0; i < lineLimit; i++ {
			line := lines[i]
			bg := ui.Transparent
			ink := toColor(dt.InkSecondary)
			weight := 400
			if strings.HasPrefix(line, "@@") {
				bg = rgbaFromRGB(dt.Accent, 0.12)
				ink = toColor(dt.Accent)
				weight = 600
			} else if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				bg = rgbaFromRGB(dt.Success, 0.13)
				ink = toColor(dt.Success)
			} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
				bg = rgbaFromRGB(dt.Danger, 0.13)
				ink = toColor(dt.Danger)
			}
			ui.Row(c).Padding(2, 6).Gap(6).Radius(4).Background(bg).Children(func() {
				ui.Textf(c, "%2d", i+1).FontSize(10.5).TextColor(toColor(dt.InkMuted))
				ui.Text(c, line).FontSize(12).FontWeight(weight).TextColor(ink)
			})
		}
	})
}

func renderSplitDiffLines(c *ui.Context, dt DerivedTheme, lines []string) {
	var leftLines, rightLines []string
	lineLimit := min(len(lines), 24)
	for i := 0; i < lineLimit; i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			leftLines = append(leftLines, line)
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			rightLines = append(rightLines, line)
		default:
			leftLines = append(leftLines, line)
			rightLines = append(rightLines, line)
		}
	}
	ui.Row(c).Gap(8).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Grow(1).Padding(6).Gap(2).Radius(10).Background(toColor(dt.SurfaceRecessed)).Children(func() {
			ui.Text(c, "BEFORE (-)").FontSize(10).FontWeight(700).TextColor(toColor(dt.Danger))
			for i, line := range leftLines {
				bg := ui.Transparent
				ink := toColor(dt.InkSecondary)
				if strings.HasPrefix(line, "-") {
					bg = rgbaFromRGB(dt.Danger, 0.14)
					ink = toColor(dt.Danger)
				}
				ui.Row(c).Padding(2, 6).Gap(6).Radius(4).Background(bg).Children(func() {
					ui.Textf(c, "%2d", i+1).FontSize(10).TextColor(toColor(dt.InkMuted))
					ui.Text(c, line).FontSize(11.5).TextColor(ink)
				})
			}
		})
		ui.Column(c).Grow(1).Padding(6).Gap(2).Radius(10).Background(toColor(dt.SurfaceRecessed)).Children(func() {
			ui.Text(c, "AFTER (+)").FontSize(10).FontWeight(700).TextColor(toColor(dt.Success))
			for i, line := range rightLines {
				bg := ui.Transparent
				ink := toColor(dt.InkSecondary)
				if strings.HasPrefix(line, "+") {
					bg = rgbaFromRGB(dt.Success, 0.14)
					ink = toColor(dt.Success)
				}
				ui.Row(c).Padding(2, 6).Gap(6).Radius(4).Background(bg).Children(func() {
					ui.Textf(c, "%2d", i+1).FontSize(10).TextColor(toColor(dt.InkMuted))
					ui.Text(c, line).FontSize(11.5).TextColor(ink)
				})
			}
		})
	})
}

func (p *ProjectionController) renderProcessSurface(c *ui.Context, t *ui.Theme, dt DerivedTheme, procs []ManagedProcessView, selectedID string) {
	if len(procs) == 0 {
		ui.Text(c, "No managed workspace processes active.").TextColor(t.TextMuted)
		return
	}
	p.mu.RLock()
	procFilter := p.procFilter
	lastCopied := p.lastCopiedID
	onAttachLogs := p.OnAttachProcessLogs
	onCopy := p.OnCopyText
	p.mu.RUnlock()

	var (
		active       *ManagedProcessView
		attachProc   *ManagedProcessView
		copyLogsText string
		copyKey      string
	)
	ui.Column(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
		limit := min(len(procs), 12)
		for i := 0; i < limit; i++ {
			pr := procs[i]
			if pr.ProcessID == selectedID {
				cp := pr
				active = &cp
			}
			label := fmt.Sprintf("%s · %s · %s (%ds)", pr.ProcessID, pr.Command, pr.State, pr.UptimeSec)
			variant := BtnGlass
			if pr.ProcessID == selectedID {
				variant = BtnPrimary
			}
			if ApplePillButton(c, "proc-"+pr.ProcessID, label, variant, dt, nil, true).Clicked() {
				p.mu.Lock()
				p.selectedProcID = pr.ProcessID
				p.mu.Unlock()
			}
		}
		if active != nil {
			stateTone := dt.InkMuted
			switch strings.ToLower(active.State) {
			case "running":
				stateTone = dt.Success
			case "failed":
				stateTone = dt.Danger
			}
			AppleGlassCard(c, "proj-proc-detail", dt, 14, 12, 6, func() {
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Textf(c, "Process %s (exit=%d)", active.ProcessID, active.ExitCode).FontWeight(700).TextColor(toColor(dt.Accent))
					ui.Spacer(c)
					AppleBadgePill(c, strings.ToUpper(active.State), stateTone, dt)
					if onAttachLogs != nil {
						if ApplePillButton(c, "proc-attach-"+active.ProcessID, "Attach Logs", BtnTinted, dt, svgIconPaperclip, true).Clicked() {
							cp := *active
							attachProc = &cp
						}
					}
					if onCopy != nil {
						cKey := "proc-copy-" + active.ProcessID
						cLbl := "Copy Logs"
						cVar := BtnGlass
						if lastCopied == cKey {
							cLbl = "Copied"
							cVar = BtnSuccess
						}
						if ApplePillButton(c, cKey, cLbl, cVar, dt, svgIconClipboard, true).Clicked() {
							copyKey = cKey
							copyLogsText = strings.Join(active.TailLines, "\n")
						}
					}
				})
				ui.Row(c).Padding(4, 10).Radius(10).
					Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
					ui.TextInputBase(c.Key("proj-proc-filter"), &procFilter).
						Placeholder("Filter process tail (e.g. ERROR, WARN)...").
						FontSize(12).TextColor(toColor(dt.Ink)).Grow(1)
				})
				q := strings.ToLower(strings.TrimSpace(procFilter))
				ui.Column(c).Padding(8, 10).Gap(3).Radius(10).Background(toColor(dt.SurfaceRecessed)).Border(1, glassBorderColor(dt)).Children(func() {
					shown := 0
					for i, line := range active.TailLines {
						if shown >= 16 {
							break
						}
						if q != "" && !strings.Contains(strings.ToLower(line), q) {
							continue
						}
						ink := toColor(dt.InkSecondary)
						upper := strings.ToUpper(line)
						if strings.Contains(upper, "ERROR") || strings.Contains(upper, "PANIC") || strings.Contains(upper, "FAIL") {
							ink = toColor(dt.Danger)
						} else if strings.Contains(upper, "WARN") {
							ink = toColor(dt.Warning)
						}
						ui.Textf(c, "%02d | %s", i+1, line).FontSize(12).TextColor(ink)
						shown++
					}
					if shown == 0 {
						ui.Textf(c, "No log lines match %q.", procFilter).FontSize(11.5).TextColor(toColor(dt.InkMuted))
					}
				})
			})
		}
	})

	p.mu.Lock()
	p.procFilter = procFilter
	if copyKey != "" {
		p.lastCopiedID = copyKey
	}
	p.mu.Unlock()

	if attachProc != nil && onAttachLogs != nil {
		onAttachLogs(*attachProc)
	}
	if copyKey != "" && onCopy != nil {
		onCopy(copyLogsText)
	}
}
