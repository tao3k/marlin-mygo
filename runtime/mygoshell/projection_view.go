// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
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
	var active *GraphNodeReceiptView
	ui.Column(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
		limit := min(len(nodes), 16)
		for i := 0; i < limit; i++ {
			n := nodes[i]
			if n.NodeID == selectedID {
				cp := n
				active = &cp
			}
			label := fmt.Sprintf("%s [%s] · %s · %dms", n.NodeID, n.Kind, n.Phase, n.DurationMS)
			variant := BtnGlass
			if n.NodeID == selectedID {
				variant = BtnPrimary
			}
			if ApplePillButton(c, "graph-node-"+n.NodeID, label, variant, dt, nil, true).Clicked() {
				p.mu.Lock()
				p.selectedNodeID = n.NodeID
				p.mu.Unlock()
			}
		}
		if active != nil {
			AppleGlassCard(c, "proj-graph-detail", dt, 14, 12, 5, func() {
				ui.Textf(c, "Node Receipt: %s", active.ReceiptID).FontWeight(700).TextColor(toColor(dt.Accent))
				if active.Summary != "" {
					ui.Text(c, active.Summary).FontSize(13).TextColor(toColor(dt.Ink))
				}
				if active.GatewayNote != "" {
					ui.Textf(c, "Gateway Evidence: %s", active.GatewayNote).FontSize(12).FontWeight(600).TextColor(toColor(dt.Warning))
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
	var active *WorkspaceDiffFileView
	ui.Column(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
		limit := min(len(files), 16)
		for i := 0; i < limit; i++ {
			f := files[i]
			if f.Path == selectedPath {
				cp := f
				active = &cp
			}
			label := fmt.Sprintf("%s (%s +%d/-%d)", f.Path, f.Status, f.Additions, f.Deletions)
			variant := BtnGlass
			if f.Path == selectedPath {
				variant = BtnPrimary
			}
			if ApplePillButton(c, "diff-file-"+f.Path, label, variant, dt, nil, true).Clicked() {
				p.mu.Lock()
				p.selectedDiffPath = f.Path
				p.mu.Unlock()
			}
		}
		if active != nil {
			AppleGlassCard(c, "proj-diff-detail", dt, 14, 12, 3, func() {
				ui.Textf(c, "Diff Preview: %s", active.Path).FontWeight(700).TextColor(toColor(dt.Accent))
				lineLimit := min(len(active.HunkLines), 24)
				for i := 0; i < lineLimit; i++ {
					ui.Text(c, active.HunkLines[i]).FontSize(12).TextColor(toColor(dt.InkSecondary))
				}
			})
		}
	})
}

func (p *ProjectionController) renderProcessSurface(c *ui.Context, t *ui.Theme, dt DerivedTheme, procs []ManagedProcessView, selectedID string) {
	if len(procs) == 0 {
		ui.Text(c, "No managed workspace processes active.").TextColor(t.TextMuted)
		return
	}
	var active *ManagedProcessView
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
			AppleGlassCard(c, "proj-proc-detail", dt, 14, 12, 3, func() {
				ui.Textf(c, "Process %s (exit=%d)", active.ProcessID, active.ExitCode).FontWeight(700).TextColor(toColor(dt.Accent))
				tailLimit := min(len(active.TailLines), 16)
				for i := 0; i < tailLimit; i++ {
					ui.Text(c, active.TailLines[i]).FontSize(12).TextColor(toColor(dt.InkMuted))
				}
			})
		}
	})
}
