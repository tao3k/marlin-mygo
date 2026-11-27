// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
	"github.com/tao3k/marlin-mygo/runtime/wazeroir"
)

type recordedAction struct {
	PluginID schema.PluginID
	Action   string
	Args     json.RawMessage
}

type recordingUseCaseDispatcher struct {
	mu      sync.Mutex
	actions []recordedAction
}

func (d *recordingUseCaseDispatcher) SubmitScopedPanelAction(_ context.Context, req pluginhost.ScopedActionRequest) (pluginhost.ScopedActionReceipt, error) {
	d.mu.Lock()
	d.actions = append(d.actions, recordedAction{
		PluginID: req.PluginID,
		Action:   req.ActionName,
		Args:     append(json.RawMessage(nil), req.Args...),
	})
	d.mu.Unlock()
	return pluginhost.ScopedActionReceipt{Admitted: true, ReceiptID: "rcpt-" + req.ActionName}, nil
}

func (d *recordingUseCaseDispatcher) snapshot() []recordedAction {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]recordedAction, len(d.actions))
	copy(out, d.actions)
	return out
}

type mockWebViewOrDeclRuntime struct {
	kind schema.PanelRuntimeKind
}

func (m mockWebViewOrDeclRuntime) Kind() schema.PanelRuntimeKind { return m.kind }

func (m mockWebViewOrDeclRuntime) Instantiate(_ context.Context, scope pluginhost.ScopedScope, _ *pluginhost.ScopedProxy) (pluginhost.Instance, error) {
	return &mockUseCaseInst{
		scope:    scope,
		renderer: lazySessRenderer{id: string(scope.PluginID)},
	}, nil
}

type mockUseCaseInst struct {
	scope    pluginhost.ScopedScope
	renderer shell.SlotRenderer
	mu       sync.Mutex
	events   []pluginhost.ScopedEvent
}

func (m *mockUseCaseInst) Scope() pluginhost.ScopedScope { return m.scope }
func (m *mockUseCaseInst) Renderer() shell.SlotRenderer  { return m.renderer }
func (m *mockUseCaseInst) DeliverEvent(ev pluginhost.ScopedEvent) error {
	m.mu.Lock()
	m.events = append(m.events, ev)
	m.mu.Unlock()
	return nil
}
func (m *mockUseCaseInst) Close() error { return nil }

func TestComplexPersonalPlanningExamplePluginsEndToEnd(t *testing.T) {
	ctx := context.Background()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	examplesRoot := filepath.Join("..", "..", "examples", "plugins")
	agendaDir := filepath.Join(examplesRoot, "ts-org-agenda-planner")
	clockDir := filepath.Join(examplesRoot, "go-focus-clock-reactor")
	packDir := filepath.Join(examplesRoot, "declarative-daily-planner-pack")

	// 1. Audit & publish all 3 complex personal-planning examples into one signed catalog.
	outCat := t.TempDir()
	var rev [20]byte
	for i, dir := range []string{agendaDir, clockDir, packDir} {
		rcpt, err := marketplace.PublishPlugin(ctx, marketplace.PublishConfig{
			PluginDir:       dir,
			PublisherID:     "tao3k.official",
			PrivateKey:      priv,
			PublicKey:       pub,
			SourceRev:       rev,
			OutCatalogDir:   outCat,
			ReplaceExisting: true,
		})
		if err != nil {
			t.Fatalf("PublishPlugin(%s) failed: %v", dir, err)
		}
		if rcpt.CatalogEntries != i+1 {
			t.Fatalf("expected %d catalog entries after publishing %s, got %d", i+1, dir, rcpt.CatalogEntries)
		}
	}

	// 2. Verify Go WASI focus-clock reactor from the signed .marlinpkg directly in wazero
	// (UI-IR + interactive GPU ActionButtons + zero-JSON binary DrawList + host action).
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{"tao3k.official": pub})
	clockPkgBytes, err := os.ReadFile(filepath.Join(outCat, "tao3k.focus-clock-reactor-0.1.0.marlinpkg"))
	if err != nil {
		t.Fatal(err)
	}
	clockVerified, err := marketplace.VerifyBundle(clockPkgBytes, trust)
	if err != nil {
		t.Fatalf("VerifyBundle(focus-clock-reactor) failed: %v", err)
	}
	wasmBytes := clockVerified.Assets["panel.wasm"]
	if len(wasmBytes) == 0 {
		t.Fatal("published focus-clock-reactor .marlinpkg missing panel.wasm")
	}

	eng, err := wazeroir.NewModuleEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close(ctx)

	disp := &recordingUseCaseDispatcher{}
	clockScope := pluginhost.ScopedScope{
		PluginID:        "tao3k.focus-clock-reactor",
		SlotID:          "workbench.inspector",
		ReadScopes:      []string{"org.clock", "org.agenda"},
		DeclaredActions: []string{"org.clock.in", "org.clock.out", "org.capture.plan"},
	}
	proxy := pluginhost.NewScopedProxy(clockScope, disp)
	resident, err := eng.InstantiateResidentWithProxy(ctx, wasmBytes, proxy)
	if err != nil {
		t.Fatalf("InstantiateResidentWithProxy failed: %v", err)
	}
	defer resident.Close(ctx)

	if err := resident.OnEvent(ctx, pluginhost.ScopedEvent{
		EventID: "ev-ag-1",
		Topic:   "org.agenda",
		Payload: json.RawMessage(`{"schemaVersion":1,"cards":[{"displayDate":"2026-11-25","time":"14:00","title":"Review orgize projections","todoState":"todo","urgency":{"total":96}}]}`),
	}); err != nil {
		t.Fatalf("OnEvent(org.agenda) failed: %v", err)
	}
	if err := resident.OnEvent(ctx, pluginhost.ScopedEvent{
		EventID: "ev-clk-1",
		Topic:   "org.clock",
		Payload: json.RawMessage(`{"schemaVersion":1,"records":[{"title":"Focus Sprint","localClock":{"runningEntries":1,"totalSeconds":1500},"effort":{"status":"onEffort"}}],"tickSeconds":1500,"quickNote":"Completed sprint"}`),
	}); err != nil {
		t.Fatalf("OnEvent(org.clock) failed: %v", err)
	}

	// Verify alarm completion emitted `org.clock.out` through ScopedProxy.
	actions := disp.snapshot()
	if len(actions) != 1 || actions[0].Action != "org.clock.out" {
		t.Fatalf("expected wazero reactor to emit org.clock.out on alarm ring, got %+v", actions)
	}

	tree, err := resident.Render(ctx)
	if err != nil {
		t.Fatalf("resident.Render failed: %v", err)
	}
	var btnActions []string
	var collectButtons func(schema.UIElement)
	collectButtons = func(el schema.UIElement) {
		if el.Kind == schema.ElemButton {
			btnActions = append(btnActions, el.Action)
		}
		for _, ch := range el.Children {
			collectButtons(ch)
		}
	}
	collectButtons(tree)
	if len(btnActions) != 3 || btnActions[0] != "org.clock.in" || btnActions[1] != "org.clock.out" || btnActions[2] != "org.capture.plan" {
		t.Fatalf("unexpected GPU ActionButtons from focus-clock reactor: %v", btnActions)
	}

	cmds, err := resident.RenderDrawList(ctx, make([]schema.DrawCmd, 0, 64))
	if err != nil || len(cmds) != 18 {
		t.Fatalf("expected 18 binary GPU DrawCmds from focus-clock reactor, got len=%d err=%v", len(cmds), err)
	}

	// 3. Real LazySession + real wazeroir.NewRuntime across all 3 published plugins!
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

	host := pluginhost.NewHost(slots, disp)
	host.RegisterRuntime(wazeroir.NewRuntime(slots, eng))
	host.RegisterRuntime(mockWebViewOrDeclRuntime{kind: schema.RuntimeWebViewUntrusted})
	host.RegisterRuntime(mockWebViewOrDeclRuntime{kind: schema.RuntimeDeclarative})

	regClient, err := marketplace.NewRegistryClient(marketplace.RegistryConfig{
		LocalDir:      outCat,
		CatalogPubKey: pub,
		CacheDir:      t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(t.TempDir(), "marlin-plugins.toml")

	sess, err := marketplace.NewLazySession(ctx, marketplace.LazySessionConfig{
		Registry:      regClient,
		Trust:         trust,
		Host:          host,
		Target:        schema.TargetDesktopMacOS,
		ProfilePath:   profilePath,
		MaxActiveLazy: 4,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []schema.PluginID{
		"tao3k.org-agenda-planner",
		"tao3k.focus-clock-reactor",
		"tao3k.daily-planner-pack",
	} {
		if _, err := sess.InstallOrUpgrade(ctx, id); err != nil {
			t.Fatalf("InstallOrUpgrade(%s) failed: %v", id, err)
		}
		if err := sess.Sleep(id); err != nil {
			t.Fatalf("Sleep(%s) failed: %v", id, err)
		}
	}

	// All 3 are dormant (0 active plugins).
	if st := sess.Stats(); st.ActiveCount != 0 || st.DormantCount != 3 {
		t.Fatalf("expected 0 active / 3 dormant, got %+v", st)
	}

	// TriggerCommand("agenda.open") wakes ONLY tao3k.org-agenda-planner.
	wokenCmd, err := sess.TriggerCommand(ctx, "agenda.open")
	if err != nil || len(wokenCmd) != 1 || wokenCmd[0] != "tao3k.org-agenda-planner" {
		t.Fatalf("expected TriggerCommand(agenda.open) to wake tao3k.org-agenda-planner, got %v err=%v", wokenCmd, err)
	}

	// PublishProjection("org.clock", ...) wakes tao3k.focus-clock-reactor in REAL wazero
	// and delivers org.clock to both authorized active plugins!
	wokenTopic, delivered, err := sess.PublishProjection(ctx, "org.clock", map[string]any{
		"schemaVersion": 1,
		"activeTitle":   "Live E2E Sprint",
		"tickSeconds":   1500,
	})
	if err != nil {
		t.Fatalf("PublishProjection(org.clock) failed: %v", err)
	}
	if len(wokenTopic) != 1 || wokenTopic[0] != "tao3k.focus-clock-reactor" {
		t.Fatalf("expected org.clock projection to wake tao3k.focus-clock-reactor, got %v", wokenTopic)
	}
	if len(delivered) != 2 {
		t.Fatalf("expected org.clock delivered to 2 authorized active plugins, got %v", delivered)
	}

	// TriggerSlot("workbench.inspector") wakes tao3k.daily-planner-pack (on-slot).
	wokenSlot, err := sess.TriggerSlot(ctx, "workbench.inspector")
	if err != nil || len(wokenSlot) != 1 || wokenSlot[0] != "tao3k.daily-planner-pack" {
		t.Fatalf("expected TriggerSlot(workbench.inspector) to wake tao3k.daily-planner-pack, got %v err=%v", wokenSlot, err)
	}
}
