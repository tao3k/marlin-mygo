// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

func buildTestDeclarativeBundle(t *testing.T, priv ed25519.PrivateKey, id, version, slotID string) ([]byte, string) {
	t.Helper()
	dir := t.TempDir()
	manifest := fmt.Sprintf(`
id = %q
version = %q
contract_major = 1
requires_features = ["ui-slot-v1"]

[contributions.pack]
kind = "theme-pack"

[contributions.pack.panel]
runtime = "declarative"
slot_id = %q
cardinality = "single"
entry_path = "theme.toml"
activation = "on-command"
triggers = ["open.%s"]
idle_unload_sec = 60
`, id, version, slotID, id)
	if err := os.WriteFile(filepath.Join(dir, "marlin-plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "theme.toml"), []byte("[theme]\naccent = \"#c678dd\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var rev [20]byte
	raw, _, err := marketplace.PackBundle(dir, "tao3k.official", rev, priv)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:])
}

func TestLazyLoaderEmacsProfileZeroCostStartupSingleflightWakeAndIdleReap(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{"tao3k.official": pub})
	mgr := marketplace.NewManager(trust)

	slots := shell.NewSlotRegistry()
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.inspector", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.inspector", AllowUntrustedPlugin: true,
	}, devNoopRenderer{id: "host.inspector"})

	host := pluginhost.NewHost(slots, devDispatcher{})
	host.RegisterRuntime(&pluginhost.DeclarativeRuntimeFactory{})

	pkgKanban, shaKanban := buildTestDeclarativeBundle(t, priv, "tao3k.lazy-kanban", "0.2.0", "workbench.inspector")
	pkgEager, shaEager := buildTestDeclarativeBundle(t, priv, "tao3k.eager-core", "0.2.0", "workbench.inspector")

	var fetchCalls atomic.Int32
	fakeNow := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	nowFn := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return fakeNow
	}

	loader, err := marketplace.NewLazyLoader(marketplace.LazyLoaderConfig{
		Manager: mgr,
		Host:    host,
		Target:  schema.TargetDesktopMacOS,
		Now:     nowFn,
		Fetch: func(_ context.Context, spec marketplace.PluginSpec) ([]byte, error) {
			fetchCalls.Add(1)
			time.Sleep(10 * time.Millisecond) // widen window to exercise singleflight deduplication
			switch spec.PluginID {
			case "tao3k.lazy-kanban":
				return pkgKanban, nil
			case "tao3k.eager-core":
				return pkgEager, nil
			default:
				return nil, marketplace.ErrPackageNotFound
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Build a 100-plugin declarative Emacs-style profile: 1 eager + 99 lazy dormant!
	profile := marketplace.PluginProfile{
		SchemaVersion: 1,
		Plugins: []marketplace.PluginSpec{
			{
				PluginID:      "tao3k.eager-core",
				Version:       "0.2.0",
				PublisherID:   "tao3k.official",
				PinnedSHA256:  shaEager,
				Enabled:       true,
				Activation:    schema.ActivationEager,
				SlotID:        "workbench.inspector",
				IdleUnloadSec: 60,
			},
			{
				PluginID:      "tao3k.lazy-kanban",
				Version:       "0.2.0",
				PublisherID:   "tao3k.official",
				PinnedSHA256:  shaKanban,
				Enabled:       true,
				Activation:    schema.ActivationOnCommand,
				SlotID:        "workbench.inspector",
				Triggers:      []string{"kanban.open"},
				IdleUnloadSec: 60,
				Config:        map[string]string{"columns": "todo,doing,done"},
			},
		},
	}
	for i := 0; i < 98; i++ {
		profile.Plugins = append(profile.Plugins, marketplace.PluginSpec{
			PluginID:   schema.PluginID(fmt.Sprintf("community.dormant-%03d", i)),
			Version:    "0.1.0",
			Enabled:    true,
			Activation: schema.ActivationOnCommand,
			Triggers:   []string{fmt.Sprintf("cmd.dormant-%03d", i)},
		})
	}

	// Round-trip TOML lockfile serialization & parsing (zero external deps).
	tomlBytes := marketplace.MarshalPluginProfileTOML(profile)
	reparsed, err := marketplace.ParsePluginProfileTOML(tomlBytes)
	if err != nil || len(reparsed.Plugins) != 100 {
		t.Fatalf("ParsePluginProfileTOML round-trip failed: len=%d err=%v", len(reparsed.Plugins), err)
	}

	ctx := context.Background()
	if err := loader.Reconcile(ctx, *reparsed); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	// ONLY the 1 eager plugin was fetched/activated; 99 lazy plugins cost ZERO loads!
	if fetchCalls.Load() != 1 || len(host.ActiveScopes()) != 1 {
		t.Fatalf("expected exactly 1 eager activation (99 dormant), fetches=%d active=%d",
			fetchCalls.Load(), len(host.ActiveScopes()))
	}

	// Fire 12 concurrent command triggers for "kanban.open" -> singleflight MUST fetch EXACTLY ONCE!
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := loader.EnsureForCommand(ctx, "kanban.open"); err != nil {
				t.Errorf("concurrent EnsureForCommand failed: %v", err)
			}
		}()
	}
	wg.Wait()
	if fetchCalls.Load() != 2 {
		t.Fatalf("expected singleflight to deduplicate 12 concurrent wakes into 1 fetch (total 2), got %d", fetchCalls.Load())
	}
	scope1, ok := host.ActiveScope("tao3k.lazy-kanban")
	if !ok || scope1.ActivationGeneration != 2 {
		t.Fatalf("expected tao3k.lazy-kanban active at gen 2, got ok=%v scope=%+v", ok, scope1)
	}

	// Advance clock by 65s -> ReapIdle reclaims ONLY the idle lazy plugin (eager plugin stays alive!).
	clockMu.Lock()
	fakeNow = fakeNow.Add(65 * time.Second)
	clockMu.Unlock()
	reaped := loader.ReapIdle(nowFn())
	if len(reaped) != 1 || reaped[0] != "tao3k.lazy-kanban" {
		t.Fatalf("expected only tao3k.lazy-kanban reaped on idle, got %v", reaped)
	}
	if _, stillActive := host.ActiveScope("tao3k.lazy-kanban"); stillActive {
		t.Fatalf("expected reaped lazy plugin deactivated from Host")
	}

	// Trigger command again -> seamlessly re-wakes at monotonic gen:3!
	woken, err := loader.EnsureForCommand(ctx, "kanban.open")
	if err != nil || len(woken) != 1 || woken[0] != "tao3k.lazy-kanban" {
		t.Fatalf("re-wake after idle reap failed: woken=%v err=%v", woken, err)
	}
	scope2, ok := host.ActiveScope("tao3k.lazy-kanban")
	if !ok || scope2.ActivationGeneration != 3 {
		t.Fatalf("expected re-woken plugin at gen 3, got ok=%v scope=%+v", ok, scope2)
	}

	// Pin mismatch MUST fail closed!
	badPinProfile := marketplace.PluginProfile{
		SchemaVersion: 1,
		Plugins: []marketplace.PluginSpec{
			{
				PluginID:     "tao3k.lazy-kanban",
				Version:      "0.2.0",
				PinnedSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
				Enabled:      true,
				Activation:   schema.ActivationManual,
			},
		},
	}
	_ = loader.Sleep("tao3k.lazy-kanban")
	_ = loader.Reconcile(ctx, badPinProfile)
	if _, err := loader.Wake(ctx, "tao3k.lazy-kanban"); !errors.Is(err, marketplace.ErrPinMismatch) {
		t.Fatalf("expected ErrPinMismatch on tampered lockfile pin, got %v", err)
	}
}

func TestLazyLoaderEmacsHooksAndBoundedLRUEviction(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{"tao3k.official": pub})
	mgr := marketplace.NewManager(trust)

	slots := shell.NewSlotRegistry()
	for _, s := range []schema.SlotID{"slot.a", "slot.b", "slot.c", "slot.pinned"} {
		_ = slots.Declare(schema.SlotDeclaration{
			SlotID: s, Cardinality: schema.SlotSingle,
			FallbackRenderer: "host." + string(s), AllowUntrustedPlugin: true,
		}, devNoopRenderer{id: "host." + string(s)})
	}
	host := pluginhost.NewHost(slots, devDispatcher{})
	host.RegisterRuntime(&pluginhost.DeclarativeRuntimeFactory{})

	bundles := map[schema.PluginID][]byte{}
	pins := map[schema.PluginID]string{}
	specs := []struct {
		id     schema.PluginID
		slot   schema.SlotID
		hook   string
		pinned bool
	}{
		{"acme.p1", "slot.a", "after-turn-complete", false},
		{"acme.p2", "slot.b", "before-gate-open", false},
		{"acme.p3", "slot.c", "on-session-switch", false},
		{"acme.pinned", "slot.pinned", "always-on", true},
	}
	var profileSpecs []marketplace.PluginSpec
	for _, s := range specs {
		raw, sum := buildTestDeclarativeBundle(t, priv, string(s.id), "0.1.0", string(s.slot))
		bundles[s.id] = raw
		pins[s.id] = sum
		profileSpecs = append(profileSpecs, marketplace.PluginSpec{
			PluginID:     s.id,
			Version:      "0.1.0",
			PublisherID:  "tao3k.official",
			PinnedSHA256: sum,
			Enabled:      true,
			PinnedActive: s.pinned,
			Activation:   schema.ActivationManual,
			SlotID:       s.slot,
			Hooks:        []string{s.hook},
		})
	}

	loader, err := marketplace.NewLazyLoader(marketplace.LazyLoaderConfig{
		Manager:       mgr,
		Host:          host,
		Target:        schema.TargetDesktopMacOS,
		MaxActiveLazy: 2, // bounded working set of at most 2 evictable lazy plugins
		Fetch: func(_ context.Context, sp marketplace.PluginSpec) ([]byte, error) {
			return bundles[sp.PluginID], nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := loader.Reconcile(ctx, marketplace.PluginProfile{SchemaVersion: 1, Plugins: profileSpecs}); err != nil {
		t.Fatal(err)
	}

	// Wake pinned plugin + p1 + p2 via Emacs-style hooks -> all 3 active (pinned doesn't count against LRU cap!).
	if _, err := loader.EnsureForHook(ctx, "always-on"); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.EnsureForHook(ctx, "after-turn-complete"); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.EnsureForHook(ctx, "before-gate-open"); err != nil {
		t.Fatal(err)
	}
	// Touch p1 so p2 becomes the LRU tail!
	loader.Touch("acme.p1")

	// Wake p3 via hook -> LRU working-set cap (2) MUST evict p2 in O(1), keeping pinned + p1 + p3 active!
	if woken, err := loader.EnsureForHook(ctx, "on-session-switch"); err != nil || len(woken) != 1 {
		t.Fatalf("EnsureForHook(on-session-switch) failed: woken=%v err=%v", woken, err)
	}
	if _, active := host.ActiveScope("acme.p2"); active {
		t.Fatalf("expected LRU tail acme.p2 evicted when working set exceeded MaxActiveLazy=2")
	}
	for _, mustStay := range []schema.PluginID{"acme.pinned", "acme.p1", "acme.p3"} {
		if _, active := host.ActiveScope(mustStay); !active {
			t.Fatalf("expected %s to remain active after LRU eviction", mustStay)
		}
	}
	// Verify EvictCount telemetry recorded for acme.p2.
	for _, st := range loader.States() {
		if st.Spec.PluginID == "acme.p2" && (st.State != marketplace.LazyStateDormant || st.EvictCount != 1) {
			t.Fatalf("expected acme.p2 DORMANT with EvictCount=1, got %+v", st)
		}
	}
}
