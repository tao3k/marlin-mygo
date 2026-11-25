// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package mygoshell

import (
	"fmt"
	"sync"

	"github.com/egoist/mygo/ui"
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

	ui.Column(c).Width(720).Gap(12).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "Projection Inspector (marlin.projection.v1 · secrets hidden)").FontSize(15).Bold()
			ui.Spacer(c)
			if onClose != nil {
				if ui.Button(c.Key("close-projection"), "Close").Clicked() {
					onClose()
				}
			}
		})

		ui.Row(c).Gap(8).Children(func() {
			p.renderTabButton(c, ProjectionTabGraph, fmt.Sprintf("Graph Receipts (%d)", len(snap.GraphNodes)), tab)
			p.renderTabButton(c, ProjectionTabDiff, fmt.Sprintf("Workspace Diff (%d)", len(snap.DiffFiles)), tab)
			p.renderTabButton(c, ProjectionTabProcesses, fmt.Sprintf("Processes (%d)", len(snap.Processes)), tab)
		})

		switch tab {
		case ProjectionTabDiff:
			p.renderDiffSurface(c, t, snap.DiffFiles, selDiff)
		case ProjectionTabProcesses:
			p.renderProcessSurface(c, t, snap.Processes, selProc)
		default:
			p.renderGraphSurface(c, t, snap.GraphNodes, selNode)
		}
	})
}

func (p *ProjectionController) renderTabButton(c *ui.Context, target ProjectionTab, label string, current ProjectionTab) {
	var clicked bool
	if current == target {
		clicked = ui.PrimaryButton(c.Key("proj-tab-"+string(target)), label).Clicked()
	} else {
		clicked = ui.Button(c.Key("proj-tab-"+string(target)), label).Clicked()
	}
	if clicked {
		p.SetActiveTab(target)
	}
}

func (p *ProjectionController) renderGraphSurface(c *ui.Context, t *ui.Theme, nodes []GraphNodeReceiptView, selectedID string) {
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
			var clicked bool
			if n.NodeID == selectedID {
				clicked = ui.PrimaryButton(c.Key("graph-node-"+n.NodeID), label).Clicked()
			} else {
				clicked = ui.Button(c.Key("graph-node-"+n.NodeID), label).Clicked()
			}
			if clicked {
				p.mu.Lock()
				p.selectedNodeID = n.NodeID
				p.mu.Unlock()
			}
		}
		if active != nil {
			ui.Column(c).Padding(10).Gap(4).Radius(8).Background(t.Surface).Border(1, t.Border).Children(func() {
				ui.Textf(c, "Node Receipt: %s", active.ReceiptID).Bold().TextColor(t.Accent)
				if active.Summary != "" {
					ui.Text(c, active.Summary)
				}
				if active.GatewayNote != "" {
					ui.Textf(c, "Gateway Evidence: %s", active.GatewayNote).FontSize(12).TextColor(t.Warning)
				}
			})
		}
	})
}

func (p *ProjectionController) renderDiffSurface(c *ui.Context, t *ui.Theme, files []WorkspaceDiffFileView, selectedPath string) {
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
			var clicked bool
			if f.Path == selectedPath {
				clicked = ui.PrimaryButton(c.Key("diff-file-"+f.Path), label).Clicked()
			} else {
				clicked = ui.Button(c.Key("diff-file-"+f.Path), label).Clicked()
			}
			if clicked {
				p.mu.Lock()
				p.selectedDiffPath = f.Path
				p.mu.Unlock()
			}
		}
		if active != nil {
			ui.Column(c).Padding(10).Gap(2).Radius(8).Background(t.Surface).Border(1, t.Border).Children(func() {
				ui.Textf(c, "Diff Preview: %s", active.Path).Bold().TextColor(t.Accent)
				lineLimit := min(len(active.HunkLines), 24)
				for i := 0; i < lineLimit; i++ {
					ui.Text(c, active.HunkLines[i]).FontSize(12)
				}
			})
		}
	})
}

func (p *ProjectionController) renderProcessSurface(c *ui.Context, t *ui.Theme, procs []ManagedProcessView, selectedID string) {
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
			var clicked bool
			if pr.ProcessID == selectedID {
				clicked = ui.PrimaryButton(c.Key("proc-"+pr.ProcessID), label).Clicked()
			} else {
				clicked = ui.Button(c.Key("proc-"+pr.ProcessID), label).Clicked()
			}
			if clicked {
				p.mu.Lock()
				p.selectedProcID = pr.ProcessID
				p.mu.Unlock()
			}
		}
		if active != nil {
			ui.Column(c).Padding(10).Gap(2).Radius(8).Background(t.Surface).Border(1, t.Border).Children(func() {
				ui.Textf(c, "Process %s (exit=%d)", active.ProcessID, active.ExitCode).Bold().TextColor(t.Accent)
				tailLimit := min(len(active.TailLines), 16)
				for i := 0; i < tailLimit; i++ {
					ui.Text(c, active.TailLines[i]).FontSize(12).TextColor(t.TextMuted)
				}
			})
		}
	})
}
