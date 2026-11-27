// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire_test

import (
	"context"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/promptgate"
	appv1 "github.com/tao3k/marlin-mygo/gen/marlin/app/v1"
	commonv1 "github.com/tao3k/marlin-mygo/gen/marlin/common/v1"
	gatev1 "github.com/tao3k/marlin-mygo/gen/marlin/gate/v1"
	"github.com/tao3k/marlin-mygo/pkg/schema"
)

type mockPromptGateService struct {
	gatev1.UnimplementedApprovalGateServiceServer
	mu          sync.Mutex
	lastToolReq *gatev1.SubmitToolReviewDecisionRequest
	lastGraph   *gatev1.SubmitGraphHumanDecisionRequest
}

func (s *mockPromptGateService) SubmitToolReviewDecision(_ context.Context, req *gatev1.SubmitToolReviewDecisionRequest) (*gatev1.SubmitToolReviewDecisionResponse, error) {
	s.mu.Lock()
	s.lastToolReq = req
	s.mu.Unlock()
	return &gatev1.SubmitToolReviewDecisionResponse{
		Receipt: &gatev1.GateDecisionReceipt{
			GateId:  req.GetGateId(),
			Kind:    gatev1.GateKind_GATE_KIND_MODEL_TOOL_REVIEW,
			Claimed: true,
			Receipt: &commonv1.ReceiptRef{ReceiptId: "rcpt-tool-1"},
		},
	}, nil
}

func (s *mockPromptGateService) SubmitGraphHumanDecision(_ context.Context, req *gatev1.SubmitGraphHumanDecisionRequest) (*gatev1.SubmitGraphHumanDecisionResponse, error) {
	s.mu.Lock()
	s.lastGraph = req
	s.mu.Unlock()
	return &gatev1.SubmitGraphHumanDecisionResponse{
		Receipt: &gatev1.GateDecisionReceipt{
			GateId:  req.GetGateId(),
			Kind:    gatev1.GateKind_GATE_KIND_GRAPH_HUMAN_GATE,
			Claimed: true,
			Receipt: &commonv1.ReceiptRef{ReceiptId: "rcpt-graph-1"},
		},
	}, nil
}

func TestPromptGateBridgePendingGateToGRPCToolAndGraphDecisions(t *testing.T) {
	srv := &mockPromptGateService{}
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	gatev1.RegisterApprovalGateServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(func() {
		gs.Stop()
		_ = lis.Close()
	})

	target, err := grpcwire.Dial(grpcwire.DialConfig{
		ID:          "gate-mock",
		DisplayName: "Gate Mock",
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
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = target.Close() })

	driver := grpcwire.NewSessionDriver(grpcwire.DriverConfig{
		Target:     target,
		TargetKind: schema.TargetDesktopMacOS,
		SessionID:  "sess-gate-bridge",
	})

	// 1. Model Tool Review gate with rationale -> SubmitToolReviewDecision.
	callID := "call-42"
	toolGate := &gatev1.PendingGate{
		GateId:         "gate-tool-1",
		Kind:           gatev1.GateKind_GATE_KIND_MODEL_TOOL_REVIEW,
		CallId:         &callID,
		ToolOrNodeName: "git.push",
		Prompt:         "Push branch to origin?",
		DetailsHeading: "Remote: origin/feat/demo",
		Card:           &appv1.ToolCard{Title: "Git Push", Summary: "2 commits ahead"},
	}
	toolReq := grpcwire.PromptRequestFromPendingGate(toolGate)
	if toolReq.AuthFamily != promptgate.AuthToolReview || toolReq.CallID != "call-42" || toolReq.DetailsSummary != "Git Push — 2 commits ahead" {
		t.Fatalf("unexpected converted tool PromptRequest: %+v", toolReq)
	}
	rcpt, err := grpcwire.DispatchPromptResponseToDriver(context.Background(), driver, toolReq, promptgate.PromptResponse{
		PromptID:       toolReq.ID,
		Kind:           promptgate.PromptAuth,
		Action:         promptgate.ActionApproveOnce,
		TextInput:      "Reviewed diff carefully",
		IdempotencyKey: "idem-tool-1",
	})
	if err != nil || !rcpt.GetClaimed() {
		t.Fatalf("DispatchPromptResponseToDriver (tool): rcpt=%+v err=%v", rcpt, err)
	}
	srv.mu.Lock()
	if srv.lastToolReq == nil || srv.lastToolReq.GetRationale() != "Reviewed diff carefully" || srv.lastToolReq.GetCallId() != "call-42" {
		t.Fatalf("unexpected SubmitToolReviewDecisionRequest: %+v", srv.lastToolReq)
	}
	srv.mu.Unlock()

	// 2. Graph Human Gate with choices & RequestPatch -> SubmitGraphHumanDecision.
	graphGate := &gatev1.PendingGate{
		GateId:         "gate-graph-1",
		Kind:           gatev1.GateKind_GATE_KIND_GRAPH_HUMAN_GATE,
		ToolOrNodeName: "node.deploy",
		Prompt:         "Approve production rollout plan?",
		Choices: []*gatev1.GateChoicePresentation{
			{SelectionId: "canary", Label: "Canary 10%", Description: "Safe rollout"},
		},
	}
	graphReq := grpcwire.PromptRequestFromPendingGate(graphGate)
	if graphReq.AuthFamily != promptgate.AuthGraphHumanGate || len(graphReq.Choices) != 1 || graphReq.Choices[0].ID != "canary" {
		t.Fatalf("unexpected converted graph PromptRequest: %+v", graphReq)
	}
	gRcpt, err := grpcwire.DispatchPromptResponseToDriver(context.Background(), driver, graphReq, promptgate.PromptResponse{
		PromptID:       graphReq.ID,
		Kind:           promptgate.PromptAuth,
		Action:         promptgate.ActionRequestPatch,
		SelectedIDs:    []string{"canary"},
		TextInput:      "Add rollback hook",
		IdempotencyKey: "idem-graph-1",
	})
	if err != nil || !gRcpt.GetClaimed() {
		t.Fatalf("DispatchPromptResponseToDriver (graph): rcpt=%+v err=%v", gRcpt, err)
	}
	srv.mu.Lock()
	if srv.lastGraph == nil || srv.lastGraph.GetDecision() != gatev1.GraphHumanDecision_GRAPH_HUMAN_DECISION_REQUEST_PATCH || srv.lastGraph.GetNote() != "selected=canary; Add rollback hook" {
		t.Fatalf("unexpected SubmitGraphHumanDecisionRequest: %+v", srv.lastGraph)
	}
	srv.mu.Unlock()
}
