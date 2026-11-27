// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/device"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

type iosShareProvider struct{}

func (iosShareProvider) Family() string { return "ios" }
func (iosShareProvider) Descriptors(context.Context) ([]device.CapabilityDescriptor, error) {
	return []device.CapabilityDescriptor{
		{CapabilityID: "device.ios.share.drain", ProtocolFamily: "share", Permission: device.PermissionGranted, ReadOnly: true},
		{CapabilityID: "device.ios.notify.send", ProtocolFamily: "notify", Permission: device.PermissionGranted, ReadOnly: false},
	}, nil
}
func (iosShareProvider) Invoke(_ context.Context, req device.InvocationRequest) (device.InvocationResult, error) {
	return device.InvocationResult{
		InvocationID: req.InvocationID,
		OK:           true,
		Result:       json.RawMessage(`{"synced":true}`),
	}, nil
}

// TestCrossPlatformDeclarativeOTAAndAppStoreComplianceEndToEnd verifies the
// Desktop + Mobile dual-track policy:
//   - Declarative packs (tao3k.daily-planner-pack) sync OTA onto macOS, iOS, and Android.
//   - Untrusted WebView (React 19) & Go WASI plugins are admitted on Desktop,
//     but strictly rejected for OTA install on iOS & Android (Apple App Store 2.5.2).
//   - Paired iOS device.Bridge enforces read-only vs one-shot gate-receipt writes.
func TestCrossPlatformDeclarativeOTAAndAppStoreComplianceEndToEnd(t *testing.T) {
	ctx := context.Background()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{"tao3k.official": pub})

	outCat := t.TempDir()
	examplesRoot := filepath.Join("..", "..", "examples", "plugins")
	var rev [20]byte
	for _, rel := range []string{
		"declarative-daily-planner-pack",
		"ts-org-agenda-planner",
		"go-focus-clock-reactor",
	} {
		if _, err := marketplace.PublishPlugin(ctx, marketplace.PublishConfig{
			PluginDir:       filepath.Join(examplesRoot, rel),
			PublisherID:     "tao3k.official",
			PrivateKey:      priv,
			PublicKey:       pub,
			SourceRev:       rev,
			OutCatalogDir:   outCat,
			ReplaceExisting: true,
		}); err != nil {
			t.Fatalf("PublishPlugin(%s) failed: %v", rel, err)
		}
	}

	newSessionForTarget := func(target schema.TargetKind) *marketplace.LazySession {
		slots := shell.NewSlotRegistry()
		for _, sid := range []schema.SlotID{"workbench.main", "workbench.inspector"} {
			if err := slots.Declare(schema.SlotDeclaration{
				SlotID:               sid,
				Cardinality:          schema.SlotSingle,
				FallbackRenderer:     "host." + string(sid),
				AllowUntrustedPlugin: true,
			}, lazySessRenderer{id: "host." + string(sid)}); err != nil {
				t.Fatal(err)
			}
		}
		host := pluginhost.NewHost(slots, lazySessDispatcher{})
		host.RegisterRuntime(&pluginhost.DeclarativeRuntimeFactory{})
		host.RegisterRuntime(mockWebViewOrDeclRuntime{kind: schema.RuntimeWebViewUntrusted})
		host.RegisterRuntime(mockWebViewOrDeclRuntime{kind: schema.RuntimeWasmUIIR})

		reg, err := marketplace.NewRegistryClient(marketplace.RegistryConfig{
			LocalDir:      outCat,
			CatalogPubKey: pub,
			CacheDir:      t.TempDir(),
		})
		if err != nil {
			t.Fatal(err)
		}
		sess, err := marketplace.NewLazySession(ctx, marketplace.LazySessionConfig{
			Registry:    reg,
			Trust:       trust,
			Host:        host,
			Target:      target,
			ProfilePath: filepath.Join(t.TempDir(), "marlin-plugins.toml"),
		})
		if err != nil {
			t.Fatal(err)
		}
		return sess
	}

	// 1. Mobile targets (iOS & Android): admit Declarative OTA pack, reject executable OTA runtimes!
	for _, mobileTarget := range []schema.TargetKind{schema.TargetMobileIOS, schema.TargetMobileAndroid} {
		sess := newSessionForTarget(mobileTarget)

		if _, err := sess.InstallOrUpgrade(ctx, "tao3k.daily-planner-pack"); err != nil {
			t.Fatalf("%s: expected declarative pack OTA install to succeed, got %v", mobileTarget, err)
		}
		if err := sess.Sleep("tao3k.daily-planner-pack"); err != nil {
			t.Fatalf("%s: Sleep failed: %v", mobileTarget, err)
		}
		if woken, err := sess.TriggerSlot(ctx, "workbench.inspector"); err != nil || len(woken) != 1 || woken[0] != "tao3k.daily-planner-pack" {
			t.Fatalf("%s: expected TriggerSlot wake for declarative pack, got woken=%v err=%v", mobileTarget, woken, err)
		}

		// App Store 2.5.2 / Play Store policy: reject dynamic WebView & WASM OTA installs on mobile.
		if _, err := sess.InstallOrUpgrade(ctx, "tao3k.org-agenda-planner"); err == nil {
			t.Fatalf("%s: expected webview-untrusted OTA install to be rejected", mobileTarget)
		}
		if _, err := sess.InstallOrUpgrade(ctx, "tao3k.focus-clock-reactor"); err == nil {
			t.Fatalf("%s: expected wasm-ui-ir OTA install to be rejected", mobileTarget)
		}
	}

	// 2. Desktop macOS target: admits all 3 runtimes from the exact same signed catalog.
	desktopSess := newSessionForTarget(schema.TargetDesktopMacOS)
	for _, id := range []schema.PluginID{
		"tao3k.daily-planner-pack",
		"tao3k.org-agenda-planner",
		"tao3k.focus-clock-reactor",
	} {
		if _, err := desktopSess.InstallOrUpgrade(ctx, id); err != nil {
			t.Fatalf("desktop-macos: expected %s to install cleanly, got %v", id, err)
		}
	}

	// 3. Paired iOS device.Bridge enforces read-only vs one-shot receipt-gated writes.
	br := device.NewBridge(schema.TargetMobileIOS)
	if err := br.RegisterProvider(ctx, iosShareProvider{}); err != nil {
		t.Fatal(err)
	}
	if res, err := br.Dispatch(ctx, device.InvocationRequest{
		InvocationID: "ios-read-1",
		CapabilityID: "device.ios.share.drain",
	}); err != nil || !res.OK {
		t.Fatalf("expected iOS read-only share drain to succeed, got res=%+v err=%v", res, err)
	}
	if _, err := br.Dispatch(ctx, device.InvocationRequest{
		InvocationID: "ios-write-no-rcpt",
		CapabilityID: "device.ios.notify.send",
	}); !errors.Is(err, device.ErrGateReceiptMissing) {
		t.Fatalf("expected ErrGateReceiptMissing on iOS notify without gate receipt, got %v", err)
	}
	if res, err := br.Dispatch(ctx, device.InvocationRequest{
		InvocationID:  "ios-write-ok",
		CapabilityID:  "device.ios.notify.send",
		GateReceiptID: "rcpt-ios-notify-1",
	}); err != nil || !res.OK {
		t.Fatalf("expected receipt-gated iOS notify to succeed, got res=%+v err=%v", res, err)
	}
	if _, err := br.Dispatch(ctx, device.InvocationRequest{
		InvocationID:  "ios-write-replay",
		CapabilityID:  "device.ios.notify.send",
		GateReceiptID: "rcpt-ios-notify-1",
	}); !errors.Is(err, device.ErrGateReceiptReplayed) {
		t.Fatalf("expected ErrGateReceiptReplayed on replayed iOS notify receipt, got %v", err)
	}
}
