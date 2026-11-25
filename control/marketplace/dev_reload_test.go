// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
	"github.com/tao3k/marlin-mygo/runtime/wazeroir"
)

type devNoopRenderer struct{ id string }

func (r devNoopRenderer) ID() string                        { return r.id }
func (r devNoopRenderer) Trusted() bool                     { return true }
func (r devNoopRenderer) Render(shell.RenderContext)        {}
func (r devNoopRenderer) HandleEvent(shell.HostEvent) error { return nil }
func (r devNoopRenderer) Dispose()                          {}

type devDispatcher struct{}

func (devDispatcher) SubmitScopedPanelAction(context.Context, pluginhost.ScopedActionRequest) (pluginhost.ScopedActionReceipt, error) {
	return pluginhost.ScopedActionReceipt{Admitted: true, ReceiptID: "rcpt-dev"}, nil
}

func writeDemoPlugin(t *testing.T, dir, version, slotID, accent string) {
	t.Helper()
	manifest := `
id = "acme.live-theme"
version = "` + version + `"
contract_major = 1
requires_features = ["ui-slot-v1"]

[contributions.theme_pack]
kind = "theme-pack"

[contributions.theme_pack.panel]
runtime = "declarative"
slot_id = "` + slotID + `"
cardinality = "single"
entry_path = "theme.toml"
read_scopes = ["theme.live"]
declared_actions = ["theme.apply"]
`
	if err := os.WriteFile(filepath.Join(dir, "marlin-plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "theme.toml"), []byte("[theme]\naccent = \""+accent+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDevReloaderHotReloadRevokesOldGenAndAutoRollsBackOnFailure(t *testing.T) {
	slots := shell.NewSlotRegistry()
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.inspector", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.inspector", AllowUntrustedPlugin: true,
	}, devNoopRenderer{id: "host.inspector"})
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "approval.gate", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.gate", AllowUntrustedPlugin: false,
	}, devNoopRenderer{id: "host.gate"})

	host := pluginhost.NewHost(slots, devDispatcher{})
	host.RegisterRuntime(&pluginhost.DeclarativeRuntimeFactory{})

	pluginDir := t.TempDir()
	writeDemoPlugin(t, pluginDir, "0.1.0", "workbench.inspector", "#c678dd")

	var failVerify atomic.Bool
	reloader, err := marketplace.NewDevReloader(marketplace.DevReloaderConfig{
		PluginDir: pluginDir,
		Host:      host,
		Target:    schema.TargetDesktopMacOS,
		VerifyActive: func(_ context.Context, panel *marketplace.InstalledPanel) error {
			if failVerify.Load() {
				return errors.New("simulated smoke check crash")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewDevReloader failed: %v", err)
	}

	ctx := context.Background()

	// 1. Initial load -> v0.1.0 at gen:1.
	res1, err := reloader.ReloadOnce(ctx)
	if err != nil || res1.ActiveGeneration != 1 || res1.ActiveVersion != "0.1.0" {
		t.Fatalf("initial ReloadOnce failed: res=%+v err=%v", res1, err)
	}
	if scope, ok := host.ActiveScope("acme.live-theme"); !ok || scope.ActivationGeneration != 1 {
		t.Fatalf("expected active gen 1, got ok=%v scope=%+v", ok, scope)
	}

	// 2. Watch detects file edit (v0.1.0 -> v0.2.0) and bumps gen:1 -> gen:2, revoking gen:1!
	watchCtx, cancelWatch := context.WithCancel(ctx)
	reloadedCh := make(chan marketplace.ReloadResult, 4)
	go func() {
		_ = reloader.Watch(watchCtx, 40*time.Millisecond, func(r marketplace.ReloadResult, _ error) {
			reloadedCh <- r
		})
	}()

	// Drain initial Watch reload (gen:2 at v0.1.0).
	select {
	case <-reloadedCh:
	case <-time.After(3 * time.Second):
		cancelWatch()
		t.Fatal("timed out waiting for initial Watch reload")
	}

	writeDemoPlugin(t, pluginDir, "0.2.0", "workbench.inspector", "#51afef")
	select {
	case r := <-reloadedCh:
		if r.ActiveVersion != "0.2.0" || r.ActiveGeneration < 3 {
			cancelWatch()
			t.Fatalf("expected watch hot-reload to v0.2.0 gen>=3, got %+v", r)
		}
	case <-time.After(3 * time.Second):
		cancelWatch()
		t.Fatal("timed out waiting for watch hot-reload on file edit")
	}
	cancelWatch()

	// Stale gen:1 event MUST be rejected!
	if err := host.RouteEvent(pluginhost.ScopedEvent{
		EventID: "ev-stale", PluginID: "acme.live-theme",
		ActivationGeneration: 1, Topic: "theme.live", Payload: json.RawMessage(`{}`),
	}); !errors.Is(err, pluginhost.ErrStaleGeneration) {
		t.Fatalf("expected ErrStaleGeneration for gen 1 after hot-reload, got %v", err)
	}

	// 3. Author saves broken manifest targeting restricted slot `approval.gate`:
	// MUST fail closed AND preserve previous v0.2.0 live!
	beforeBroken, _ := host.ActiveScope("acme.live-theme")
	writeDemoPlugin(t, pluginDir, "0.2.1", "approval.gate", "#ff0000")
	resBad, badErr := reloader.ReloadOnce(ctx)
	if badErr == nil || !strings.Contains(badErr.Error(), "host-restricted slot") {
		t.Fatalf("expected host-restricted slot denial, got %v", badErr)
	}
	if !resBad.PreservedPrior || resBad.ActiveGeneration != beforeBroken.ActivationGeneration {
		t.Fatalf("expected prior v0.2.0 preserved intact, got %+v", resBad)
	}
	afterBroken, ok := host.ActiveScope("acme.live-theme")
	if !ok || afterBroken.ActivationGeneration != beforeBroken.ActivationGeneration {
		t.Fatalf("expected active generation preserved across invalid edit, before=%d after=%d",
			beforeBroken.ActivationGeneration, afterBroken.ActivationGeneration)
	}

	// 4. Author saves v0.3.0 that fails post-activation VerifyActive:
	// MUST automatically roll back to last-known-good v0.2.0 at next monotonic generation!
	writeDemoPlugin(t, pluginDir, "0.3.0", "workbench.inspector", "#98be65")
	failVerify.Store(true)
	resRB, rbErr := reloader.ReloadOnce(ctx)
	if rbErr == nil || !strings.Contains(rbErr.Error(), "rolled back to v0.2.0") || !resRB.RolledBack {
		t.Fatalf("expected automatic rollback to v0.2.0, got res=%+v err=%v", resRB, rbErr)
	}
	if resRB.ActiveVersion != "0.2.0" || resRB.ActiveGeneration <= resRB.AttemptedGen {
		t.Fatalf("expected rollback to bump generation above broken attempt: %+v", resRB)
	}
	afterRB, ok := host.ActiveScope("acme.live-theme")
	if !ok || afterRB.ActivationGeneration != resRB.ActiveGeneration {
		t.Fatalf("expected host mounted at rollback gen %d, got %+v", resRB.ActiveGeneration, afterRB)
	}

	// 5. Fix and reload v0.4.0 cleanly!
	failVerify.Store(false)
	writeDemoPlugin(t, pluginDir, "0.4.0", "workbench.inspector", "#98be65")
	resFixed, err := reloader.ReloadOnce(ctx)
	if err != nil || resFixed.ActiveVersion != "0.4.0" || resFixed.ActiveGeneration <= resRB.ActiveGeneration {
		t.Fatalf("expected clean v0.4.0 reload after fix, got res=%+v err=%v", resFixed, err)
	}
}

func TestDevReloaderCompilesAndHotLoadsRealGoWASIReactor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	slots := shell.NewSlotRegistry()
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.inspector", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.inspector", AllowUntrustedPlugin: true,
	}, devNoopRenderer{id: "host.inspector"})

	eng, err := wazeroir.NewModuleEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close(ctx)

	host := pluginhost.NewHost(slots, devDispatcher{})
	host.RegisterRuntime(wazeroir.NewRuntime(slots, eng))

	reactorDir := filepath.Join("..", "..", "examples", "plugins", "go-graph-inspector-reactor")
	reloader, err := marketplace.NewDevReloader(marketplace.DevReloaderConfig{
		PluginDir: reactorDir,
		Host:      host,
		Target:    schema.TargetDesktopMacOS,
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := reloader.ReloadOnce(ctx)
	if err != nil {
		t.Fatalf("Go WASI Reactor ReloadOnce failed: %v", err)
	}
	if res.PluginID != "tao3k.graph-inspector" || res.Runtime != schema.RuntimeWasmUIIR || res.ActiveGeneration != 1 {
		t.Fatalf("unexpected Go WASI Reactor reload result: %+v", res)
	}
}
