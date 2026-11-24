// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire_test

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
	commonv1 "github.com/tao3k/marlin-mygo/gen/marlin/common/v1"
	devicev1 "github.com/tao3k/marlin-mygo/gen/marlin/device/v1"
	gatev1 "github.com/tao3k/marlin-mygo/gen/marlin/gate/v1"
	pluginv1 "github.com/tao3k/marlin-mygo/gen/marlin/plugin/v1"
	uiv1 "github.com/tao3k/marlin-mygo/gen/marlin/ui/v1"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

// TestLiveCrossProcessGoToRustBridgeE2E connects Go marlin-mygo directly over
// HTTP/2 gRPC to a running Rust marlin-agent-app-server (--features ui-grpc).
// Skipped during standalone Go tests unless MARLIN_RUST_BRIDGE_ADDR is set.
func TestLiveCrossProcessGoToRustBridgeE2E(t *testing.T) {
	addr := os.Getenv("MARLIN_RUST_BRIDGE_ADDR")
	if addr == "" {
		t.Skip("set MARLIN_RUST_BRIDGE_ADDR to run live Go <-> Rust gRPC E2E")
	}
	token := os.Getenv("MARLIN_RUST_BRIDGE_TOKEN")
	if token == "" {
		token = "marlin-dev-token"
	}
	privHex := os.Getenv("MARLIN_RUST_BRIDGE_PRIVKEY_HEX")
	if privHex == "" {
		t.Fatal("MARLIN_RUST_BRIDGE_PRIVKEY_HEX must be set for cross-language Ed25519 test")
	}
	privBytes, err := hex.DecodeString(strings.TrimSpace(privHex))
	if err != nil || len(privBytes) != ed25519.PrivateKeySize {
		t.Fatalf("invalid Ed25519 private key hex: %v", err)
	}
	privKey := ed25519.PrivateKey(privBytes)

	target, err := grpcwire.Dial(grpcwire.DialConfig{
		ID:          "rust-core",
		DisplayName: "Rust Marlin Core",
		Mode:        transport.ModeRemoteGRPC,
		Address:     addr,
		AuthToken:   "Bearer " + token,
	})
	if err != nil {
		t.Fatalf("Dial Rust bridge failed: %v", err)
	}
	defer target.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. OpenWorkbench over HTTP/2 -> verify Scheme slots incl. locked approval.gate
	wbResp, err := target.UI().OpenWorkbench(ctx, &uiv1.OpenWorkbenchRequest{
		SessionId:        "session-e2e",
		ClientTargetKind: commonv1.TargetKind_TARGET_KIND_DESKTOP_MACOS,
	})
	if err != nil {
		t.Fatalf("OpenWorkbench failed: %v", err)
	}
	snap, err := grpcwire.WorkbenchFromProto(wbResp.GetWorkbench())
	if err != nil {
		t.Fatalf("WorkbenchFromProto failed: %v", err)
	}
	if snap.Generation != 1 || snap.ProfileName != "desktop-dev" {
		t.Fatalf("unexpected Rust workbench snapshot: %+v", snap)
	}
	foundLockedGate := false
	for _, sl := range snap.Slots {
		if sl.SlotID == "approval.gate" && !sl.AllowUntrustedPlugin {
			foundLockedGate = true
		}
	}
	if !foundLockedGate {
		t.Fatalf("expected locked approval.gate slot from Rust host, got %+v", snap.Slots)
	}

	// 2. Cross-language Ed25519 signing: Go signs marlin.unified.release.v1 -> Rust verifies!
	manifestTOML := []byte(`
id = "tao3k.org-kanban"
version = "0.2.0"
contract_major = 1
requires_features = ["ui-slot-v1"]

[capabilities.read_tasks]
kind = "projection.read"
surface = "org.tasks"

[contributions.kanban]
kind = "ui-panel"
requires = ["read_tasks"]

[contributions.kanban.panel]
runtime = "webview-untrusted"
slot_id = "workbench.main"
cardinality = "single"
entry_path = "index.html"
read_scopes = ["org.tasks"]
declared_actions = ["org.task.move"]
`)
	att := schema.ReleaseAttestation{
		Publisher:           "tao3k.official",
		SourceRevision:      [20]byte{1, 2, 3},
		BuildEvidenceDigest: [32]byte{4, 5, 6},
	}
	sig := ed25519.Sign(privKey, schema.SigningBytes(manifestTOML, att))

	installResp, err := target.Plugin().InstallSignedRelease(ctx, &pluginv1.InstallSignedReleaseRequest{
		Release: &pluginv1.SignedReleaseEnvelope{
			ManifestToml: manifestTOML,
			Attestation: &pluginv1.ReleaseAttestation{
				PublisherId:         string(att.Publisher),
				SourceRevision:      att.SourceRevision[:],
				BuildEvidenceDigest: att.BuildEvidenceDigest[:],
			},
			Ed25519Signature: sig,
		},
	})
	if err != nil || !installResp.GetInstalled() {
		t.Fatalf("cross-language Go-signed -> Rust-verified release failed: err=%v resp=%+v", err, installResp)
	}

	// 3. Adversarial Go-signed bundle targeting restricted approval.gate -> Rust MUST deny!
	evilTOML := []byte(`
id = "evil.gate-hijack"
version = "0.1.0"
contract_major = 1

[contributions.hijack]
kind = "ui-panel"

[contributions.hijack.panel]
runtime = "webview-untrusted"
slot_id = "approval.gate"
cardinality = "single"
`)
	evilSig := ed25519.Sign(privKey, schema.SigningBytes(evilTOML, att))
	evilResp, err := target.Plugin().InstallSignedRelease(ctx, &pluginv1.InstallSignedReleaseRequest{
		Release: &pluginv1.SignedReleaseEnvelope{
			ManifestToml: evilTOML,
			Attestation: &pluginv1.ReleaseAttestation{
				PublisherId:         string(att.Publisher),
				SourceRevision:      att.SourceRevision[:],
				BuildEvidenceDigest: att.BuildEvidenceDigest[:],
			},
			Ed25519Signature: evilSig,
		},
	})
	if err != nil || evilResp.GetInstalled() {
		t.Fatalf("expected Rust to reject approval.gate plugin, got err=%v resp=%+v", err, evilResp)
	}

	// 4. Enable plugin on Rust -> dispatch allowed ScopedAction via Go PanelActionDispatcher!
	enResp, err := target.Plugin().EnableWorkflow(ctx, &pluginv1.EnableWorkflowRequest{
		ProjectId:      "proj-1",
		PluginId:       "tao3k.org-kanban",
		ContributionId: "kanban",
	})
	if err != nil || !enResp.GetReceipt().GetActivated() {
		t.Fatalf("EnableWorkflow failed: err=%v resp=%+v", err, enResp)
	}
	gen := enResp.GetReceipt().GetGeneration()

	disp := &grpcwire.PanelActionDispatcher{Target: target, ProjectID: "proj-1", SessionID: "session-e2e"}
	proxy := pluginhost.NewScopedProxy(pluginhost.ScopedScope{
		PluginID:             "tao3k.org-kanban",
		ContributionID:       "kanban",
		ActivationGeneration: gen,
		DeclaredActions:      []string{"org.task.move"},
	}, disp)
	receipt, err := proxy.SubmitAction(ctx, "org.task.move", json.RawMessage(`{"id":"T-1"}`), "idem-go-rust-1")
	if err != nil || !receipt.Admitted || receipt.ReceiptID == "" {
		t.Fatalf("expected admitted ScopedAction receipt from Rust, got receipt=%+v err=%v", receipt, err)
	}

	// 5. One-shot ApprovalGate decision -> succeeds once, replay denied!
	g1, err := target.Gate().SubmitToolReviewDecision(ctx, &gatev1.SubmitToolReviewDecisionRequest{
		GateId:         "gate-e2e-1",
		CallId:         "call-e2e-1",
		Decision:       gatev1.ToolReviewDecision_TOOL_REVIEW_DECISION_APPROVE_ONCE,
		IdempotencyKey: "g-1",
	})
	if err != nil || !g1.GetReceipt().GetClaimed() {
		t.Fatalf("expected first gate claim to succeed, got err=%v resp=%+v", err, g1)
	}
	g2, err := target.Gate().SubmitToolReviewDecision(ctx, &gatev1.SubmitToolReviewDecisionRequest{
		GateId:         "gate-e2e-1",
		CallId:         "call-e2e-1",
		Decision:       gatev1.ToolReviewDecision_TOOL_REVIEW_DECISION_APPROVE_ONCE,
		IdempotencyKey: "g-2",
	})
	if err != nil || g2.GetReceipt().GetClaimed() {
		t.Fatalf("expected second gate replay to be denied, got err=%v resp=%+v", err, g2)
	}

	// 6. iOS Device Registration -> Rust selects Scheme "ios-personal" overlay + accepts capture!
	devReg, err := target.Device().RegisterDeviceNode(ctx, &devicev1.RegisterDeviceNodeRequest{
		DeviceId:    "iphone-e2e",
		DisplayName: "Guangtao iPhone",
		TargetKind:  commonv1.TargetKind_TARGET_KIND_MOBILE_IOS,
		OsVersion:   "19.0",
		AppVersion:  "0.1.0",
	})
	if err != nil || devReg.GetActiveProfileOverlay() != "ios-personal" {
		t.Fatalf("expected ios-personal overlay, got err=%v resp=%+v", err, devReg)
	}
}
