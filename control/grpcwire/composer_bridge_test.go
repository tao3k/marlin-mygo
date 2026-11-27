// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/composer"
	appv1 "github.com/tao3k/marlin-mygo/gen/marlin/app/v1"
)

type composerMockAppServer struct {
	appv1.UnimplementedAppServerServer
	mu          sync.Mutex
	lastUserTxt string
	startCalls  int
}

func (s *composerMockAppServer) StartSessionTurn(_ context.Context, req *appv1.StartSessionTurnRequest) (*appv1.StartSessionTurnResponse, error) {
	s.mu.Lock()
	s.lastUserTxt = req.GetUserText()
	s.startCalls++
	s.mu.Unlock()
	return &appv1.StartSessionTurnResponse{
		Turn: &appv1.SessionTurnRef{
			SessionId: req.GetSessionId(),
			Ordinal:   req.GetAfterTurn() + 1,
		},
	}, nil
}

func (s *composerMockAppServer) GetSessionSummary(_ context.Context, req *appv1.GetSessionSummaryRequest) (*appv1.GetSessionSummaryResponse, error) {
	return &appv1.GetSessionSummaryResponse{
		LatestTurn: &appv1.SessionTurnRef{
			SessionId: req.GetSessionId(),
			Ordinal:   7,
		},
		RecoveryState: appv1.SessionRecoveryState_SESSION_RECOVERY_STATE_FINISHED,
	}, nil
}

type mockCASUploader struct {
	fail bool
}

func (u *mockCASUploader) UploadArtifact(_ context.Context, att composer.StagedAttachment) (string, error) {
	if u.fail {
		return "", errors.New("cas quota exceeded")
	}
	return "marlin-artifact://" + att.Ref.SHA256Hex, nil
}

func dialComposerMockTarget(t *testing.T, srv appv1.AppServerServer) *grpcwire.Target {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	appv1.RegisterAppServerServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(func() {
		gs.Stop()
		_ = lis.Close()
	})
	target, err := grpcwire.Dial(grpcwire.DialConfig{
		ID:          "composer-mock",
		DisplayName: "Composer Mock",
		Mode:        transport.ModeLocalSidecar,
		Address:     "passthrough:///bufnet",
		DialOptions: []grpc.DialOption{
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	if err != nil {
		t.Fatalf("dial bufconn failed: %v", err)
	}
	t.Cleanup(func() { _ = target.Close() })
	return target
}

func TestComposerBridgeMultimodalTurnFailClosedUploadAndSessionSummary(t *testing.T) {
	srv := &composerMockAppServer{}
	target := dialComposerMockTarget(t, srv)
	turns := grpcwire.NewTurnStreamController(target)

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, image.NewRGBA(image.Rect(0, 0, 200, 100))); err != nil {
		t.Fatal(err)
	}
	st := composer.NewState(composer.DefaultAttachmentPolicy())
	imgRef, err := st.AddAttachment(composer.AttachmentInput{Filename: "diagram.png", Data: pngBuf.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	st.AddMention(composer.ContextMention{Kind: "file", Target: "control/grpcwire/client.go", Label: "client.go"})
	sub, ok := st.BuildAndResetSubmission("Audit this diagram and client")
	if !ok {
		t.Fatal("expected valid TurnSubmission")
	}

	// 1. Successful CAS upload -> emits Uploading -> Uploaded statuses and dispatches CAS URI to AppServer.
	bridge := grpcwire.NewComposerBridge(turns, &mockCASUploader{})
	var statuses []composer.AttachmentStatus
	turnRef, uploadedRefs, err := bridge.StartMultimodalTurn(
		context.Background(), "sess-mm", sub, 3, "sub-1",
		func(_ string, status composer.AttachmentStatus, _ float32, _ string) {
			statuses = append(statuses, status)
		},
	)
	if err != nil {
		t.Fatalf("StartMultimodalTurn failed: %v", err)
	}
	if turnRef.GetOrdinal() != 4 || len(uploadedRefs) != 1 || uploadedRefs[0].Status != composer.StatusUploaded {
		t.Fatalf("unexpected multimodal turn result: ref=%+v uploaded=%+v", turnRef, uploadedRefs)
	}
	if len(statuses) != 2 || statuses[0] != composer.StatusUploading || statuses[1] != composer.StatusUploaded {
		t.Fatalf("expected Uploading->Uploaded status transitions, got %v", statuses)
	}
	srv.mu.Lock()
	gotTxt := srv.lastUserTxt
	startCalls := srv.startCalls
	srv.mu.Unlock()
	for _, want := range []string{
		"Audit this diagram and client",
		"[context @file:control/grpcwire/client.go]",
		"marlin-artifact://" + imgRef.SHA256Hex,
		"dims=200x100",
	} {
		if !strings.Contains(gotTxt, want) {
			t.Fatalf("expected wire prompt to contain %q, got:\n%s", want, gotTxt)
		}
	}

	// 2. Failed CAS upload MUST fail closed and NEVER call StartSessionTurn!
	failBridge := grpcwire.NewComposerBridge(turns, &mockCASUploader{fail: true})
	var failedStatus composer.AttachmentStatus
	if _, _, err := failBridge.StartMultimodalTurn(
		context.Background(), "sess-mm", sub, 4, "sub-2",
		func(_ string, status composer.AttachmentStatus, _ float32, _ string) {
			failedStatus = status
		},
	); err == nil {
		t.Fatal("expected StartMultimodalTurn to fail closed when CAS upload fails")
	}
	if failedStatus != composer.StatusFailed {
		t.Fatalf("expected StatusFailed callback, got %s", failedStatus)
	}
	srv.mu.Lock()
	if srv.startCalls != startCalls {
		t.Fatalf("fail-closed violation: StartSessionTurn called despite failed upload (%d -> %d)", startCalls, srv.startCalls)
	}
	srv.mu.Unlock()

	// 3. FetchSessionSummary projects Rust GetSessionSummary into SessionSummaryView.
	summary, err := bridge.FetchSessionSummary(context.Background(), "sess-mm", "Multimodal Audit")
	if err != nil {
		t.Fatalf("FetchSessionSummary failed: %v", err)
	}
	if summary.SessionID != "sess-mm" || summary.Title != "Multimodal Audit" || summary.TurnCount != 7 {
		t.Fatalf("unexpected SessionSummaryView: %+v", summary)
	}
}
