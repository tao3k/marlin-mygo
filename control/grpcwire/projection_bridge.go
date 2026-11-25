// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"context"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	projectionv1 "github.com/tao3k/marlin-mygo/gen/marlin/projection/v1"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

const maxTrackedGraphNodes = 128

// ProjectionBridgeConfig wires a live marlin.projection.v1.ProjectionQueryService
// Target into a Tier-0 native GPU mygoshell.ProjectionController (secrets always denied).
type ProjectionBridgeConfig struct {
	Target     *Target
	ProjectID  string
	SessionID  string
	Controller *mygoshell.ProjectionController
	OnUpdate   func(mygoshell.ProjectionSnapshot)
}

// ProjectionStreamBridge consumes WatchGraphTrajectory, WatchWorkspaceDiff, and
// WatchManagedProcesses concurrently and pushes immutable secret-free snapshots
// to mygoshell.ProjectionController.
type ProjectionStreamBridge struct {
	cfg ProjectionBridgeConfig

	mu          sync.Mutex
	nodes       []mygoshell.GraphNodeReceiptView
	nodeIndex   map[string]int
	diffFiles   []mygoshell.WorkspaceDiffFileView
	processes   []mygoshell.ManagedProcessView
	diffRev     uint64
	procRev     uint64
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

func NewProjectionStreamBridge(cfg ProjectionBridgeConfig) *ProjectionStreamBridge {
	if cfg.ProjectID == "" {
		cfg.ProjectID = "default"
	}
	return &ProjectionStreamBridge{
		cfg:       cfg,
		nodeIndex: make(map[string]int),
	}
}

// HydrateGraph performs one bounded secret-denied QueryProjectGraph lookup so
// the Projection Inspector shows live records before streaming events arrive.
func (b *ProjectionStreamBridge) HydrateGraph(ctx context.Context) error {
	if b.cfg.Target == nil {
		return fmt.Errorf("grpcwire: ProjectionStreamBridge requires non-nil Target")
	}
	req := &projectionv1.QueryProjectGraphRequest{
		ProjectId: b.cfg.ProjectID,
		Surfaces: []projectionv1.GraphVisibleSurface{
			projectionv1.GraphVisibleSurface_GRAPH_VISIBLE_SURFACE_TOPOLOGY,
			projectionv1.GraphVisibleSurface_GRAPH_VISIBLE_SURFACE_EVIDENCE,
			projectionv1.GraphVisibleSurface_GRAPH_VISIBLE_SURFACE_WORKSPACE,
		},
		SecretVisibility: projectionv1.SecretVisibility_SECRET_VISIBILITY_DENIED,
	}
	if b.cfg.SessionID != "" {
		sid := b.cfg.SessionID
		req.SessionId = &sid
	}
	resp, err := b.cfg.Target.Projection().QueryProjectGraph(ctx, req)
	if err != nil {
		return err
	}
	for _, rec := range resp.GetRecords() {
		b.upsertGraphNode(mygoshell.GraphNodeReceiptView{
			NodeID:    rec.GetRecordId(),
			Kind:      rec.GetKind(),
			Phase:     rec.GetSurface().String(),
			ReceiptID: rec.GetFacets()["receipt_id"],
			Summary:   rec.GetSummary(),
		})
	}
	b.publish()
	return nil
}

// Start spawns background pumps for all 3 marlin.projection.v1 server streams.
func (b *ProjectionStreamBridge) Start(ctx context.Context) error {
	if b.cfg.Target == nil {
		return fmt.Errorf("grpcwire: ProjectionStreamBridge requires non-nil Target")
	}
	runCtx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	b.cancel = cancel
	b.mu.Unlock()

	_ = b.HydrateGraph(runCtx)

	b.wg.Add(3)
	go b.pumpGraphTrajectory(runCtx)
	go b.pumpWorkspaceDiff(runCtx)
	go b.pumpManagedProcesses(runCtx)
	return nil
}

// Stop cancels all projection streams and waits for clean shutdown.
func (b *ProjectionStreamBridge) Stop() {
	b.mu.Lock()
	cancel := b.cancel
	b.cancel = nil
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	b.wg.Wait()
}

func (b *ProjectionStreamBridge) pumpGraphTrajectory(ctx context.Context) {
	defer b.wg.Done()
	stream, err := b.cfg.Target.Projection().WatchGraphTrajectory(ctx, &projectionv1.WatchGraphTrajectoryRequest{
		SessionId: b.cfg.SessionID,
	})
	if err != nil {
		return
	}
	for {
		resp, err := stream.Recv()
		if err != nil {
			return
		}
		ev := resp.GetEvent()
		if ev == nil || ev.GetNodeId() == "" {
			continue
		}
		receiptID := ""
		if r := ev.GetNodeReceipt(); r != nil {
			receiptID = r.GetReceiptId()
		}
		meta := ev.GetMetadata()
		b.upsertGraphNode(mygoshell.GraphNodeReceiptView{
			NodeID:      ev.GetNodeId(),
			Kind:        ev.GetNodeKind(),
			Phase:       ev.GetStatus(),
			ReceiptID:   receiptID,
			GatewayNote: meta["gateway_note"],
			Summary:     meta["summary"],
		})
		b.publish()
	}
}

func (b *ProjectionStreamBridge) pumpWorkspaceDiff(ctx context.Context) {
	defer b.wg.Done()
	stream, err := b.cfg.Target.Projection().WatchWorkspaceDiff(ctx, &projectionv1.WatchWorkspaceDiffRequest{
		ProjectId: b.cfg.ProjectID,
	})
	if err != nil {
		return
	}
	for {
		resp, err := stream.Recv()
		if err != nil {
			return
		}
		snap := resp.GetSnapshot()
		if snap == nil {
			continue
		}
		files := make([]mygoshell.WorkspaceDiffFileView, 0, len(snap.GetFiles()))
		for _, f := range snap.GetFiles() {
			var lines []string
			if branch := snap.GetBranch(); branch != "" {
				lines = append(lines, fmt.Sprintf("branch: %s (rev %d)", branch, snap.GetRevision()))
			}
			if art := f.GetDiffArtifact(); art != nil && len(art.GetSha256()) > 0 {
				lines = append(lines, fmt.Sprintf("artifact: marlin-artifact://%s/ (%d bytes)", hex.EncodeToString(art.GetSha256()), art.GetByteSize()))
			}
			files = append(files, mygoshell.WorkspaceDiffFileView{
				Path:      f.GetPath(),
				Status:    f.GetStatus(),
				Additions: int(f.GetAddedLines()),
				Deletions: int(f.GetRemovedLines()),
				HunkLines: lines,
			})
		}
		b.mu.Lock()
		if snap.GetRevision() >= b.diffRev {
			b.diffRev = snap.GetRevision()
			b.diffFiles = files
		}
		b.mu.Unlock()
		b.publish()
	}
}

func (b *ProjectionStreamBridge) pumpManagedProcesses(ctx context.Context) {
	defer b.wg.Done()
	stream, err := b.cfg.Target.Projection().WatchManagedProcesses(ctx, &projectionv1.WatchManagedProcessesRequest{
		SessionId: b.cfg.SessionID,
	})
	if err != nil {
		return
	}
	for {
		resp, err := stream.Recv()
		if err != nil {
			return
		}
		snap := resp.GetSnapshot()
		if snap == nil {
			continue
		}
		nowMS := time.Now().UnixMilli()
		procs := make([]mygoshell.ManagedProcessView, 0, len(snap.GetProcesses()))
		for _, pr := range snap.GetProcesses() {
			var uptimeSec int64
			if started := int64(pr.GetStartedAtUnixMs()); started > 0 && nowMS > started {
				uptimeSec = (nowMS - started) / 1000
			}
			var tail []string
			if r := pr.GetShutdownReceipt(); r != nil && r.GetReceiptId() != "" {
				tail = append(tail, "shutdown receipt: "+r.GetReceiptId())
			}
			procs = append(procs, mygoshell.ManagedProcessView{
				ProcessID: pr.GetProcessId(),
				Command:   pr.GetCommand(),
				State:     pr.GetState(),
				ExitCode:  int(pr.GetExitCode()),
				UptimeSec: uptimeSec,
				TailLines: tail,
			})
		}
		b.mu.Lock()
		if snap.GetRevision() >= b.procRev {
			b.procRev = snap.GetRevision()
			b.processes = procs
		}
		b.mu.Unlock()
		b.publish()
	}
}

func (b *ProjectionStreamBridge) upsertGraphNode(node mygoshell.GraphNodeReceiptView) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if idx, ok := b.nodeIndex[node.NodeID]; ok {
		b.nodes[idx] = node
		return
	}
	if len(b.nodes) >= maxTrackedGraphNodes {
		dropped := b.nodes[0].NodeID
		delete(b.nodeIndex, dropped)
		copy(b.nodes, b.nodes[1:])
		b.nodes[len(b.nodes)-1] = node
		for i, n := range b.nodes {
			b.nodeIndex[n.NodeID] = i
		}
		return
	}
	b.nodeIndex[node.NodeID] = len(b.nodes)
	b.nodes = append(b.nodes, node)
}

func (b *ProjectionStreamBridge) publish() {
	b.mu.Lock()
	snap := mygoshell.ProjectionSnapshot{
		GraphNodes: append([]mygoshell.GraphNodeReceiptView(nil), b.nodes...),
		DiffFiles:  append([]mygoshell.WorkspaceDiffFileView(nil), b.diffFiles...),
		Processes:  append([]mygoshell.ManagedProcessView(nil), b.processes...),
	}
	b.mu.Unlock()
	if b.cfg.Controller != nil {
		b.cfg.Controller.SetSnapshot(snap)
	}
	if b.cfg.OnUpdate != nil {
		b.cfg.OnUpdate(snap)
	}
}
