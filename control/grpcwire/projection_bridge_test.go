// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire_test

import (
	"context"
	"crypto/sha256"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
	commonv1 "github.com/tao3k/marlin-mygo/gen/marlin/common/v1"
	projectionv1 "github.com/tao3k/marlin-mygo/gen/marlin/projection/v1"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

type fakeProjectionServer struct {
	projectionv1.UnimplementedProjectionQueryServiceServer
	mu             sync.Mutex
	lastSecretMode projectionv1.SecretVisibility
}

func (s *fakeProjectionServer) QueryProjectGraph(_ context.Context, req *projectionv1.QueryProjectGraphRequest) (*projectionv1.QueryProjectGraphResponse, error) {
	s.mu.Lock()
	s.lastSecretMode = req.GetSecretVisibility()
	s.mu.Unlock()
	return &projectionv1.QueryProjectGraphResponse{
		Records: []*projectionv1.GraphRecordView{
			{
				RecordId: "node-hydrated",
				Surface:  projectionv1.GraphVisibleSurface_GRAPH_VISIBLE_SURFACE_TOPOLOGY,
				Kind:     "planner",
				Summary:  "Hydrated from QueryProjectGraph",
				Facets:   map[string]string{"receipt_id": "rcpt-hydrate-1"},
			},
		},
	}, nil
}

func (s *fakeProjectionServer) WatchGraphTrajectory(_ *projectionv1.WatchGraphTrajectoryRequest, stream projectionv1.ProjectionQueryService_WatchGraphTrajectoryServer) error {
	return stream.Send(&projectionv1.WatchGraphTrajectoryResponse{
		Event: &projectionv1.GraphTrajectoryEvent{
			EventId:     "ev-1",
			RunId:       "run-1",
			NodeId:      "node-live-1",
			NodeKind:    "executor",
			Status:      "succeeded",
			NodeReceipt: &commonv1.ReceiptRef{ReceiptId: "rcpt-live-node-1"},
			Metadata: map[string]string{
				"summary":      "Executed graph verification node",
				"gateway_note": "no-live-llm denial receipt recorded",
			},
		},
	})
}

func (s *fakeProjectionServer) WatchWorkspaceDiff(_ *projectionv1.WatchWorkspaceDiffRequest, stream projectionv1.ProjectionQueryService_WatchWorkspaceDiffServer) error {
	sum := sha256.Sum256([]byte("+diff hunk"))
	return stream.Send(&projectionv1.WatchWorkspaceDiffResponse{
		Snapshot: &projectionv1.WorkspaceDiffSnapshot{
			Revision: 7,
			Branch:   "feat/marlin-mygo-frontend-engine",
			Files: []*projectionv1.FileDiffEntry{
				{
					Path:         "control/grpcwire/projection_bridge.go",
					Status:       "A",
					AddedLines:   210,
					RemovedLines: 0,
					DiffArtifact: &commonv1.ArtifactRef{
						ArtifactId: "art-diff-1",
						Sha256:     sum[:],
						MimeType:   "text/x-diff",
						ByteSize:   10,
					},
				},
			},
		},
	})
}

func (s *fakeProjectionServer) WatchManagedProcesses(_ *projectionv1.WatchManagedProcessesRequest, stream projectionv1.ProjectionQueryService_WatchManagedProcessesServer) error {
	exit0 := int32(0)
	return stream.Send(&projectionv1.WatchManagedProcessesResponse{
		Snapshot: &projectionv1.ManagedProcessSnapshot{
			Revision: 3,
			Processes: []*projectionv1.ManagedProcessView{
				{
					ProcessId:       "proc-sidecar",
					Command:         "marlin-mygo-host",
					State:           "running",
					ExitCode:        &exit0,
					StartedAtUnixMs: uint64(time.Now().Add(-30 * time.Second).UnixMilli()),
					ShutdownReceipt: &commonv1.ReceiptRef{ReceiptId: "rcpt-proc-1"},
				},
			},
		},
	})
}

func TestProjectionStreamBridgeStreamsAllThreeSurfacesAndDeniesSecrets(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	fake := &fakeProjectionServer{}
	projectionv1.RegisterProjectionQueryServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	target, err := grpcwire.Dial(grpcwire.DialConfig{
		ID:      "fake-proj",
		Mode:    transport.ModeLocalSidecar,
		Address: "passthrough:///bufnet",
		DialOptions: []grpc.DialOption{
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	ctrl := mygoshell.NewProjectionController(mygoshell.ProjectionSnapshot{})
	ready := make(chan mygoshell.ProjectionSnapshot, 8)
	bridge := grpcwire.NewProjectionStreamBridge(grpcwire.ProjectionBridgeConfig{
		Target:     target,
		ProjectID:  "default",
		SessionID:  "sess-proj-test",
		Controller: ctrl,
		OnUpdate: func(snap mygoshell.ProjectionSnapshot) {
			if len(snap.GraphNodes) >= 2 && len(snap.DiffFiles) == 1 && len(snap.Processes) == 1 {
				select {
				case ready <- snap:
				default:
				}
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("bridge.Start failed: %v", err)
	}
	defer bridge.Stop()

	select {
	case snap := <-ready:
		if snap.DiffFiles[0].Path != "control/grpcwire/projection_bridge.go" || len(snap.DiffFiles[0].HunkLines) < 2 {
			t.Fatalf("unexpected diff projection: %+v", snap.DiffFiles)
		}
		if snap.Processes[0].ProcessID != "proc-sidecar" {
			t.Fatalf("unexpected process projection: %+v", snap.Processes)
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for all 3 projection surfaces to sync")
	}

	fake.mu.Lock()
	secMode := fake.lastSecretMode
	fake.mu.Unlock()
	if secMode != projectionv1.SecretVisibility_SECRET_VISIBILITY_DENIED {
		t.Fatalf("expected SECRET_VISIBILITY_DENIED, got %v", secMode)
	}
}
