// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	pluginv1 "github.com/tao3k/marlin-mygo/gen/marlin/plugin/v1"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/device"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
	"github.com/tao3k/marlin-mygo/runtime/wazeroir"
)

func generateTestTLSFiles(t *testing.T, dir string) (caPath, certPath, keyPath string) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Marlin E2E Root CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srvTpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTpl, caTpl, &srvKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	srvPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srvDER})
	srvKeyDER, err := x509.MarshalPKCS8PrivateKey(srvKey)
	if err != nil {
		t.Fatal(err)
	}
	srvKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: srvKeyDER})

	caPath = filepath.Join(dir, "ca.pem")
	certPath = filepath.Join(dir, "server.pem")
	keyPath = filepath.Join(dir, "server.key")
	if err := os.WriteFile(caPath, caPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, srvPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, srvKeyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return caPath, certPath, keyPath
}

// TestGrandUnifiedFullStackRealIntegration exercises EVERY architectural plane
// live across real compiled Rust marlin-mygo-host processes + Go SidecarSupervisor
// + TLS 1.3 encrypted remote gRPC + FederationSupervisor + real compiled Go 1.27
// WASI Reactor (.marlinpkg + signed catalog.json) + Rust PanelPluginHost lifecycle
// & evidence accounting + live OpenScopedPanelStream -> WASM OnEvent -> ScopedProxy
// -> Rust ReceiptRef + live ApproveGateOnce -> TLS OpenDeviceChannel -> device.Bridge
// + headless MyGo GPU WorkbenchApp rendering + graceful clean shutdown.
// Skipped unless MARLIN_RUST_SIDECAR_BIN is set.
func TestGrandUnifiedFullStackRealIntegration(t *testing.T) {
	binPath := os.Getenv("MARLIN_RUST_SIDECAR_BIN")
	if binPath == "" {
		t.Skip("set MARLIN_RUST_SIDECAR_BIN to run Grand Unified real integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	workDir := t.TempDir()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubHex := hex.EncodeToString(pub)
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{
		"tao3k.official": pub,
	})

	// 1. Compile real Go 1.27 WASI Reactor + pack & Ed25519-sign .marlinpkg + signed catalog.json!
	repoRoot := filepath.Join("..", "..")
	reactorStage := filepath.Join(workDir, "reactor")
	if err := os.MkdirAll(reactorStage, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := os.ReadFile(filepath.Join(repoRoot, "examples", "plugins", "go-graph-inspector-reactor", "marlin-plugin.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reactorStage, "marlin-plugin.toml"), manifestRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	buildCmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", filepath.Join(reactorStage, "panel.wasm"), "./examples/plugins/go-graph-inspector-reactor")
	buildCmd.Dir = repoRoot
	buildCmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("compile reactor wasm failed: %v\n%s", err, string(out))
	}

	var rev [20]byte
	copy(rev[:], []byte("grand-unified-e2e-v1"))
	reactorPkg, reactorAtt, err := marketplace.PackBundle(reactorStage, "tao3k.official", rev, priv)
	if err != nil {
		t.Fatal(err)
	}
	pkgPath := filepath.Join(workDir, "tao3k.graph-inspector.marlinpkg")
	if err := os.WriteFile(pkgPath, reactorPkg, 0o644); err != nil {
		t.Fatal(err)
	}
	catJSON, catSig, _, err := marketplace.BuildSignedCatalog([]string{pkgPath}, "tao3k.official", rev, trust, priv)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Spawn Local Rust Authority Sidecar via SidecarSupervisor (crypto/rand ephemeral token!).
	localManaged, err := grpcwire.LaunchSidecarTarget(ctx, "local-mac", "Local Rust Authority", transport.SidecarConfig{
		BinaryPath:      binPath,
		PublisherID:     "tao3k.official",
		PublisherKeyHex: pubHex,
		ExtraEnv:        []string{"MARLIN_BRIDGE_GATE_ID=gate-local-1"},
	})
	if err != nil {
		t.Fatalf("LaunchSidecarTarget(local) failed: %v", err)
	}
	defer localManaged.Close()

	// 3. Spawn SECOND real Rust Authority Host configured with REAL TLS 1.3!
	caPath, certPath, keyPath := generateTestTLSFiles(t, workDir)
	tlsSup, err := transport.StartSidecar(ctx, transport.SidecarConfig{
		BinaryPath:      binPath,
		PublisherID:     "tao3k.official",
		PublisherKeyHex: pubHex,
		Stderr:          os.Stderr,
		ExtraEnv: []string{
			"MARLIN_BRIDGE_TLS_CERT=" + certPath,
			"MARLIN_BRIDGE_TLS_KEY=" + keyPath,
			"MARLIN_BRIDGE_GATE_ID=gate-remote-tls-1",
		},
	})
	if err != nil {
		t.Fatalf("StartSidecar(remote-tls) failed: %v", err)
	}
	defer tlsSup.Stop(context.Background())

	// Dial TLS 1.3 Rust host with pinned CA!
	remoteTLSTarget, err := grpcwire.Dial(grpcwire.DialConfig{
		ID:          "remote-gpu-tls",
		DisplayName: "Remote GPU (TLS 1.3)",
		Mode:        transport.ModeRemoteGRPC,
		Address:     tlsSup.Address(),
		AuthToken:   tlsSup.AuthHeader(),
		TLS: &grpcwire.TLSConfig{
			CAFile:     caPath,
			ServerName: "localhost",
		},
	})
	if err != nil {
		t.Fatalf("Dial TLS 1.3 Rust host failed: %v", err)
	}
	defer remoteTLSTarget.Close()

	// 4. Register BOTH real Rust hosts in FederationSupervisor & ProbeAll over HTTP/2 + TLS 1.3!
	reg := transport.NewTargetRegistry()
	_ = reg.Register(localManaged)
	_ = reg.Register(remoteTLSTarget)
	fed := transport.NewFederationSupervisor(reg)
	fed.SetRole("remote-gpu-tls", transport.RoleRemoteAgent)
	fedSnap := fed.ProbeAll(ctx, 3*time.Second)
	if len(fedSnap.Nodes) != 2 {
		t.Fatalf("expected 2 live federated Rust nodes, got %d", len(fedSnap.Nodes))
	}
	for _, n := range fedSnap.Nodes {
		if n.Health.Phase != transport.PhaseReady || len(n.Meta.SupportedSvcs) != 6 {
			t.Fatalf("expected node %q PhaseReady with 6 services, got %+v", n.Meta.ID, n)
		}
	}

	// 5. Boot MyGo GPU Workbench + host.* fallbacks + wazeroir + PanelActionDispatcher -> real Rust!
	slots := shell.NewSlotRegistry()
	declareHost := func(slotID schema.SlotID, hostID, title string, allowUntrusted bool) {
		fb := mygoshell.NewUIElementRenderer(hostID, true, slots, func() schema.UIElement {
			return schema.UIElement{
				Kind:  schema.ElemPanel,
				Title: title,
				Tone:  schema.ToneInfo,
				Children: []schema.UIElement{
					{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: title, Tone: schema.ToneMuted}}},
				},
			}
		})
		if err := slots.Declare(schema.SlotDeclaration{
			SlotID: slotID, Cardinality: schema.SlotSingle,
			FallbackRenderer: hostID, AllowUntrustedPlugin: allowUntrusted,
		}, fb); err != nil {
			t.Fatal(err)
		}
	}
	declareHost("workbench.main", "host.main", "Host Main Fallback", true)
	declareHost("workbench.inspector", "host.inspector", "Host Inspector Fallback", true)
	declareHost("approval.gate", "host.gate", "Host Gate", false)

	localWB := shell.NewWorkbenchController(slots)
	remoteWB := shell.NewWorkbenchController(slots)

	wasmEng, err := wazeroir.NewModuleEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer wasmEng.Close(ctx)

	rustDisp := &grpcwire.PanelActionDispatcher{
		Target:    localManaged.Target,
		ProjectID: "proj-grand",
		SessionID: "sess-grand",
	}
	pHost := pluginhost.NewHost(slots, rustDisp)
	pHost.RegisterRuntime(wazeroir.NewRuntime(slots, wasmEng))

	// 6. Start BOTH live SessionDrivers (local plaintext sidecar + remote TLS 1.3 agent!).
	localDriver := grpcwire.NewSessionDriver(grpcwire.DriverConfig{
		Target:     localManaged.Target,
		TargetKind: schema.TargetDesktopMacOS,
		SessionID:  "sess-grand",
		Workbench:  localWB,
		Federation: fed,
		PluginHost: pHost,
	})
	if _, err := localDriver.Start(ctx); err != nil {
		t.Fatalf("localDriver.Start failed: %v", err)
	}
	defer localDriver.Stop()

	remoteNotify := &e2eNotify{}
	remoteDevBridge := device.NewBridge(schema.TargetMobileIOS)
	_ = remoteDevBridge.RegisterProvider(ctx, &device.IOSPersonalProvider{Notify: remoteNotify})
	remoteDeviceDone := make(chan device.InvocationResult, 1)

	remoteDriver := grpcwire.NewSessionDriver(grpcwire.DriverConfig{
		Target:       remoteTLSTarget,
		TargetKind:   schema.TargetDesktopLinux,
		SessionID:    "sess-remote-tls",
		Workbench:    remoteWB,
		Federation:   fed,
		DeviceBridge: remoteDevBridge,
		DeviceID:     "iphone-companion-tls",
		DisplayName:  "iPhone Companion",
		OnDeviceInvoked: func(_ device.InvocationRequest, res device.InvocationResult) {
			select {
			case remoteDeviceDone <- res:
			default:
			}
		},
	})
	if _, err := remoteDriver.Start(ctx); err != nil {
		t.Fatalf("remoteDriver.Start(TLS 1.3) failed: %v", err)
	}
	defer remoteDriver.Stop()

	// Both Rust hosts have 1 seeded pending gate -> Federation aggregates TotalPendingGates == 2!
	if got := fed.Snapshot().TotalPendingGates; got != 2 {
		t.Fatalf("expected 2 total pending gates across local+remote TLS Rust hosts, got %d", got)
	}

	// 7. Install signed .marlinpkg into Rust PanelPluginHost + Go wazeroir via signed catalog.json!
	vb, err := marketplace.VerifyBundle(reactorPkg, trust)
	if err != nil {
		t.Fatal(err)
	}
	rustSig := ed25519.Sign(priv, schema.SigningBytes(vb.Verified.ManifestTOML, vb.Verified.Attestation))
	instResp, err := localManaged.Plugin().InstallSignedRelease(ctx, &pluginv1.InstallSignedReleaseRequest{
		Release: &pluginv1.SignedReleaseEnvelope{
			ManifestToml: vb.Verified.ManifestTOML,
			Attestation: &pluginv1.ReleaseAttestation{
				PublisherId:         string(reactorAtt.Publisher),
				SourceRevision:      vb.Verified.Attestation.SourceRevision[:],
				BuildEvidenceDigest: vb.Verified.Attestation.BuildEvidenceDigest[:],
			},
			Ed25519Signature: rustSig,
		},
	})
	if err != nil || !instResp.GetInstalled() {
		t.Fatalf("Rust InstallSignedRelease failed: err=%v resp=%+v", err, instResp)
	}

	enResp, err := localManaged.Plugin().EnableWorkflow(ctx, &pluginv1.EnableWorkflowRequest{
		ProjectId:      "proj-grand",
		PluginId:       "tao3k.graph-inspector",
		ContributionId: "graph_panel",
	})
	if err != nil || !enResp.GetReceipt().GetActivated() {
		t.Fatalf("Rust EnableWorkflow failed: err=%v resp=%+v", err, enResp)
	}
	activationID := enResp.GetReceipt().GetActivationId()
	gen := enResp.GetReceipt().GetGeneration()

	mkt := marketplace.NewManager(trust)
	installedPanel, err := mkt.InstallFromCatalog(
		ctx, catJSON, catSig, pub, "tao3k.graph-inspector",
		func(marketplace.CatalogEntry) ([]byte, error) { return reactorPkg, nil },
		pHost, schema.TargetDesktopMacOS, gen,
	)
	if err != nil {
		t.Fatalf("Go InstallFromCatalog failed: %v", err)
	}

	// Mount workbench snapshot containing workbench.inspector and render headless GPU UI!
	_ = localWB.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation:  99,
		ProfileName: "desktop-dev",
		Theme:       schema.DefaultUITheme(),
		FrameTree: schema.UIElement{
			Kind: schema.ElemRows,
			Children: []schema.UIElement{
				{Kind: schema.ElemSlot, SlotID: "workbench.main"},
				{Kind: schema.ElemSlot, SlotID: "workbench.inspector"},
			},
		},
	})
	app := mygoshell.NewWorkbenchApp(reg, slots, localWB)
	app.Federation = fed
	app.TargetWorkbenches = map[string]*shell.WorkbenchController{
		"local-mac":      localWB,
		"remote-gpu-tls": remoteWB,
	}
	tt := ui.NewTester(app.View, 1100, 720)
	if !tt.HasText("Graph Trajectory (Resident Reactor)") {
		t.Fatalf("expected live WASM-IR reactor rendered in MyGo UI, got %q", tt.Texts())
	}
	if !tt.HasText("Jump to Gate (remote-gpu-tls)") {
		t.Fatalf("expected federated Jump to Gate button for remote TLS host, got %q", tt.Texts())
	}

	// 8. Drive REAL WASM OnEvent -> marlin_host_v1.emit_action -> ScopedProxy -> Rust PanelPluginHost!
	if err := pHost.RouteEvent(pluginhost.ScopedEvent{
		EventID:              "ev-grand-1",
		PluginID:             "tao3k.graph-inspector",
		ActivationGeneration: gen,
		Topic:                "graph.trajectory",
		Payload:              []byte(`{"nodeId":"verify_panel_evidence"}`),
	}); err != nil {
		t.Fatalf("RouteEvent to live WASM reactor failed: %v", err)
	}

	// Query Rust PanelPluginHost GetWorkflowEvidence -> MUST contain real recorded action receipt!
	evResp, err := localManaged.Plugin().GetWorkflowEvidence(ctx, &pluginv1.GetWorkflowEvidenceRequest{
		ActivationId: activationID,
	})
	if err != nil || len(evResp.GetEffectReceipts()) != 1 {
		t.Fatalf("expected 1 recorded effect receipt in Rust PanelPluginHost evidence, got err=%v resp=%+v", err, evResp)
	}

	// Verify 64-command zero-JSON binary DrawList directly from the mounted reactor!
	type dlProvider interface {
		RenderDrawList(context.Context, []schema.DrawCmd) ([]schema.DrawCmd, error)
	}
	cmds, err := installedPanel.Instance.(dlProvider).RenderDrawList(ctx, nil)
	if err != nil || len(cmds) != 64 {
		t.Fatalf("expected 64 binary DrawCmds, got len=%d err=%v", len(cmds), err)
	}

	// 9. Disable workflow in Rust PanelPluginHost -> real closed_generation == 1 + atomic host.inspector fallback!
	disResp, err := localManaged.Plugin().DisableWorkflow(ctx, &pluginv1.DisableWorkflowRequest{
		ActivationId: activationID,
	})
	if err != nil || !disResp.GetReceipt().GetDisabled() || disResp.GetReceipt().GetClosedGeneration() != gen {
		t.Fatalf("expected Rust DisableWorkflow to close gen %d, got err=%v resp=%+v", gen, err, disResp)
	}
	if err := pHost.Deactivate("tao3k.graph-inspector"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Host Inspector Fallback") {
		t.Fatalf("expected host.inspector fallback restored after deactivation, got %q", tt.Texts())
	}

	// 10. Approve pending gate on REMOTE TLS 1.3 Rust host -> live TLS OpenDeviceChannel -> device.Bridge!
	if _, err := remoteDriver.ApproveGateOnce(ctx, "gate-remote-tls-1", "call-e2e-1", "grand-tls-approve-1"); err != nil {
		t.Fatalf("ApproveGateOnce on remote TLS Rust host failed: %v", err)
	}
	select {
	case res := <-remoteDeviceDone:
		if !res.OK {
			t.Fatalf("expected TLS OpenDeviceChannel device execution OK, got %+v", res)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for TLS 1.3 OpenDeviceChannel device invocation")
	}

	// 11. Verify graceful SIGINT shutdown of both Rust processes (exit code 0, zero forced kill!).
	localDriver.Stop()
	remoteDriver.Stop()
	if err := localManaged.Close(); err != nil {
		t.Fatalf("localManaged.Close failed: %v", err)
	}
	if err := tlsSup.Stop(context.Background()); err != nil {
		t.Fatalf("tlsSup.Stop failed: %v", err)
	}
	if err := localManaged.Sidecar.ExitErr(); err != nil {
		t.Fatalf("expected clean exit code 0 from Rust marlin-mygo-host on graceful shutdown, got %v", err)
	}
	if err := tlsSup.ExitErr(); err != nil {
		t.Fatalf("expected clean exit code 0 from TLS Rust marlin-mygo-host on graceful shutdown, got %v", err)
	}
}
