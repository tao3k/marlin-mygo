// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"context"
	"fmt"
	"strings"

	"github.com/tao3k/marlin-mygo/engine/promptgate"
	gatev1 "github.com/tao3k/marlin-mygo/gen/marlin/gate/v1"
)

// PromptRequestFromPendingGate translates an authoritative Rust
// marlin.gate.v1.PendingGate message (including choices, tool card metadata,
// and expiry) into a transport-free Tier-0 PromptRequest for Desktop & Mobile UI.
func PromptRequestFromPendingGate(g *gatev1.PendingGate) promptgate.PromptRequest {
	if g == nil {
		return promptgate.PromptRequest{}
	}
	family := promptgate.AuthToolReview
	risk := promptgate.RiskMedium
	switch g.GetKind() {
	case gatev1.GateKind_GATE_KIND_GRAPH_HUMAN_GATE:
		family = promptgate.AuthGraphHumanGate
	case gatev1.GateKind_GATE_KIND_DEVICE_CAPABILITY:
		family = promptgate.AuthDeviceCapability
		risk = promptgate.RiskHigh
	}
	choices := make([]promptgate.OptionChoice, 0, len(g.GetChoices()))
	for _, ch := range g.GetChoices() {
		choices = append(choices, promptgate.OptionChoice{
			ID:          ch.GetSelectionId(),
			Label:       ch.GetLabel(),
			Description: ch.GetDescription(),
		})
	}
	var summary string
	if card := g.GetCard(); card != nil {
		summary = strings.TrimSpace(card.GetSummary())
		if title := strings.TrimSpace(card.GetTitle()); title != "" && summary != "" {
			summary = title + " — " + summary
		} else if title != "" {
			summary = title
		}
	}
	return promptgate.PromptRequest{
		ID:              g.GetGateId(),
		Kind:            promptgate.PromptAuth,
		Title:           "Authoritative Approval Gate",
		Prompt:          g.GetPrompt(),
		ToolName:        g.GetToolOrNodeName(),
		CallID:          g.GetCallId(),
		AuthFamily:      family,
		Risk:            risk,
		DetailsHeading:  g.GetDetailsHeading(),
		DetailsSummary:  summary,
		Choices:         choices,
		ExpiresAtUnixMS: g.GetExpiresAtUnixMs(),
	}
}

// DispatchPromptResponseToDriver submits a validated Tier-0 PromptAuth response
// back to Rust ApprovalGateService via SessionDriver with its one-shot
// idempotency key and optional rationale/note/selected choice.
func DispatchPromptResponseToDriver(
	ctx context.Context,
	driver *SessionDriver,
	req promptgate.PromptRequest,
	resp promptgate.PromptResponse,
) (*gatev1.GateDecisionReceipt, error) {
	if driver == nil {
		return nil, fmt.Errorf("grpcwire: SessionDriver is nil")
	}
	note := strings.TrimSpace(resp.TextInput)
	if len(resp.SelectedIDs) > 0 {
		sel := "selected=" + strings.Join(resp.SelectedIDs, ",")
		if note != "" {
			note = sel + "; " + note
		} else {
			note = sel
		}
	}
	if req.AuthFamily == promptgate.AuthGraphHumanGate {
		decision := gatev1.GraphHumanDecision_GRAPH_HUMAN_DECISION_REJECT
		switch resp.Action {
		case promptgate.ActionApproveOnce:
			decision = gatev1.GraphHumanDecision_GRAPH_HUMAN_DECISION_APPROVE
		case promptgate.ActionAskQuestion:
			decision = gatev1.GraphHumanDecision_GRAPH_HUMAN_DECISION_ASK_QUESTION
		case promptgate.ActionRequestPatch:
			decision = gatev1.GraphHumanDecision_GRAPH_HUMAN_DECISION_REQUEST_PATCH
		}
		return driver.SubmitGraphHumanDecision(ctx, req.ID, decision, note, resp.IdempotencyKey)
	}
	callID := req.CallID
	if callID == "" {
		callID = "call-" + req.ID
	}
	approve := resp.Action == promptgate.ActionApproveOnce
	return driver.SubmitToolReviewWithRationale(ctx, req.ID, callID, approve, note, resp.IdempotencyKey)
}
