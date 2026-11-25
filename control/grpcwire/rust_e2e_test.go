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
	"sync"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	commonv1 "github.com/tao3k/marlin-mygo/gen/marlin/common/v1"
	devicev1 "github.com/tao3k/marlin-mygo/gen/marlin/device/v1"
	gatev1 "github.com/tao3k/marlin-mygo/gen/marlin/gate/v1"
	pluginv1 "github.com/tao3k/marlin-mygo/gen/marlin/plugin/v1"
	uiv1 "github.com/tao3k/marlin-mygo/gen/marlin/ui/v1"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/device"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

type e2eNotify struct {
	mu   sync.Mutex
	sent []device.NotificationRequest
}

func (n *e2eNotify) PermissionState(context.Context) (device.OsPermissionState, error) {
	return device.PermissionGranted, nil
}

func (n *e2eNotify) SendNotification(_ context.Context, req device.NotificationRequest) error {
	n.mu.Lock()
	n.sent = append(n.sent, req)
	n.mu.Unlock()
	return nil
}

type e2eCapture struct{ items []device.CapturePayload }

func (c *e2eCapture) DrainPendingCaptures(context.Context) ([]device.CapturePayload, error) {
	out := c.items
	c.items = nil
	return out, nil
}

// TestLiveCrossProcessGoToRustBridgeE2E connects Go marlin-mygo directly over
// HTTP/2 gRPC to a running Rust marlin-agent-app-server (--features ui-grpc)
// and validates ALL 6 orthogonal services + FederationSupervisor + live
// SessionDriver reactive streams (WatchPendingGates -> ApproveGateOnce ->
// Rust ReceiptRef -> OpenDeviceChannel -> device.Bridge.Dispatch + SyncCaptures).
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
		Mode:        transport.ModeLocalSidecar,
		Address:     addr,
		AuthToken:   "Bearer " + token,
	})
	if err != nil {
		t.Fatalf("Dial Rust bridge failed: %v", err)
	}
	defer target.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 0. FederationSupervisor.ProbeAll -> live AppServer.GetCapabilities over HTTP/2!
	reg := transport.NewTargetRegistry()
	if err := reg.Register(target); err != nil {
		t.Fatal(err)
	}
	fed := transport.NewFederationSupervisor(reg)
	fedSnap := fed.ProbeAll(ctx, 2*time.Second)
	if len(fedSnap.Nodes) != 1 || fedSnap.Nodes[0].Health.Phase != transport.PhaseReady {
		t.Fatalf("expected Rust host PhaseReady from AppServer.GetCapabilities, got %+v", fedSnap)
	}
	if len(fedSnap.Nodes[0].Meta.SupportedSvcs) != 6 {
		t.Fatalf("expected 6 advertised gRPC services from Rust host, got %v", fedSnap.Nodes[0].Meta.SupportedSvcs)
	}

	// 1. Live SessionDriver: OpenWorkbench + WatchPendingGates + OpenDeviceChannel!
	slots := shell.NewSlotRegistry()
	wb := shell.NewWorkbenchController(slots)
	notifyImpl := &e2eNotify{}
	devBridge := device.NewBridge(schema.TargetMobileIOS)
	if err := devBridge.RegisterProvider(ctx, &device.IOSPersonalProvider{
		Notify: notifyImpl,
	}); err != nil {
		t.Fatal(err)
	}

	deviceDone := make(chan device.InvocationResult, 1)
	driver := grpcwire.NewSessionDriver(grpcwire.DriverConfig{
		Target:       target,
		TargetKind:   schema.TargetDesktopMacOS,
		SessionID:    "session-e2e",
		Workbench:    wb,
		Federation:   fed,
		DeviceBridge: devBridge,
		DeviceID:     "iphone-e2e-live",
		DisplayName:  "Guangtao iPhone",
		OnDeviceInvoked: func(_ device.InvocationRequest, res device.InvocationResult) {
			select {
			case deviceDone <- res:
			default:
			}
		},
	})
	snap, err := driver.Start(ctx)
	if err != nil {
		t.Fatalf("SessionDriver.Start failed: %v", err)
	}
	defer driver.Stop()

	if snap.Generation != 1 || snap.ProfileName != "desktop-dev" {
		t.Fatalf("unexpected Rust workbench snapshot: %+v", snap)
	}
	foundLockedGate := false
	for _, sl := range snap.Slots {
		if sl.SlotID == "approval.gate" && sl.FallbackRenderer == "host.gate" && !sl.AllowUntrustedPlugin {
			foundLockedGate = true
		}
	}
	if !foundLockedGate {
		t.Fatalf("expected locked approval.gate (host.gate) slot from Rust host, got %+v", snap.Slots)
	}
	if fed.Snapshot().TotalPendingGates != 1 {
		t.Fatalf("expected FederationSupervisor to hydrate 1 pending gate from Rust, got %d", fed.Snapshot().TotalPendingGates)
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

	// 5. Full Reactive Human Gate -> Rust Mint Receipt -> OpenDeviceChannel -> device.Bridge.Dispatch!
	g1, err := driver.ApproveGateOnce(ctx, "gate-e2e-1", "call-e2e-1", "g-1")
	if err != nil || !g1.GetClaimed() || g1.GetReceipt().GetReceiptId() == "" {
		t.Fatalf("expected first gate claim to succeed, got err=%v resp=%+v", err, g1)
	}
	if fed.Snapshot().TotalPendingGates != 0 {
		t.Fatalf("expected FederationSupervisor pending gate count to clear to 0, got %d", fed.Snapshot().TotalPendingGates)
	}

	// Verify Rust automatically pushed DeviceInvocationRequest over OpenDeviceChannel with the minted ReceiptRef
	// and Go's runtime/device.Bridge validated the receipt and executed device.ios.notify.send!
	select {
	case res := <-deviceDone:
		if !res.OK {
			t.Fatalf("expected live OpenDeviceChannel device.ios.notify.send OK, got %+v", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for live OpenDeviceChannel device invocation after Rust gate approval")
	}
	notifyImpl.mu.Lock()
	if len(notifyImpl.sent) != 1 {
		t.Fatalf("expected 1 gated notification executed via live Rust->Go stream, got %d", len(notifyImpl.sent))
	}
	notifyImpl.mu.Unlock()

	// Replay of the same one-shot gate MUST be denied by Rust!
	g2, err := target.Gate().SubmitToolReviewDecision(ctx, &gatev1.SubmitToolReviewDecisionRequest{
		GateId:         "gate-e2e-1",
		CallId:         "call-e2e-1",
		Decision:       gatev1.ToolReviewDecision_TOOL_REVIEW_DECISION_APPROVE_ONCE,
		IdempotencyKey: "g-2",
	})
	if err != nil || g2.GetReceipt().GetClaimed() {
		t.Fatalf("expected second gate replay to be denied, got err=%v resp=%+v", err, g2)
	}

	// 6. SyncCaptures over CaptureSinkAdapter -> Rust DeviceBridgeService.IngestCapture!
	capProto := &e2eCapture{items: []device.CapturePayload{
		{SourceChannel: "device.ios.share.drain", Text: "Shared design paper", OCRText: "RFC 40.183"},
	}}
	synced, err := devBridge.SyncCaptures(ctx, "iphone-e2e-live", "session-e2e", capProto, &grpcwire.CaptureSinkAdapter{Target: target})
	if err != nil || synced != 1 {
		t.Fatalf("expected 1 capture synced to Rust IngestCapture, got synced=%d err=%v", synced, err)
	}

	// 7. iOS Device Registration -> Rust selects Scheme "ios-personal" overlay!
	devReg, err := target.Device().RegisterDeviceNode(ctx, &devicev1.RegisterDeviceNodeRequest{
		DeviceId:    "iphone-e2e",
		DisplayName: "Guangtao iPhone",
		TargetKind:  commonv1.TargetKind_TARGET_KIND_MOBILE_IOS,
		OsVersion:   "19.0",
		AppVersion:  "0.2.0",
	})
	if err != nil || devReg.GetActiveProfileOverlay() != "ios-personal" {
		t.Fatalf("expected ios-personal overlay, got err=%v resp=%+v", err, devReg)
	}
	_ = uiv1.OpenWorkbenchRequest{}
}
