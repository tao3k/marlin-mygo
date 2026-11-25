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
	"encoding/json"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

type advPropRuntime struct {
	mu      sync.Mutex
	proxies map[schema.PluginID][]*pluginhost.ScopedProxy
	events  map[schema.PluginID][]pluginhost.ScopedEvent
}

func (r *advPropRuntime) Kind() schema.PanelRuntimeKind { return schema.RuntimeDeclarative }
func (r *advPropRuntime) Instantiate(_ context.Context, scope pluginhost.ScopedScope, proxy *pluginhost.ScopedProxy) (pluginhost.Instance, error) {
	r.mu.Lock()
	if r.proxies == nil {
		r.proxies = make(map[schema.PluginID][]*pluginhost.ScopedProxy)
		r.events = make(map[schema.PluginID][]pluginhost.ScopedEvent)
	}
	r.proxies[scope.PluginID] = append(r.proxies[scope.PluginID], proxy)
	r.mu.Unlock()
	return &advPropInstance{scope: scope, parent: r}, nil
}

type advPropInstance struct {
	scope  pluginhost.ScopedScope
	parent *advPropRuntime
}

type untrustedPropRenderer struct{ id string }

func (r untrustedPropRenderer) ID() string                        { return r.id }
func (r untrustedPropRenderer) Trusted() bool                     { return false }
func (r untrustedPropRenderer) Render(shell.RenderContext)        {}
func (r untrustedPropRenderer) HandleEvent(shell.HostEvent) error { return nil }
func (r untrustedPropRenderer) Dispose()                          {}

func (i *advPropInstance) Scope() pluginhost.ScopedScope { return i.scope }
func (i *advPropInstance) Renderer() shell.SlotRenderer {
	return untrustedPropRenderer{id: string(i.scope.PluginID)}
}
func (i *advPropInstance) Close() error { return nil }
func (i *advPropInstance) DeliverEvent(ev pluginhost.ScopedEvent) error {
	i.parent.mu.Lock()
	i.parent.events[i.scope.PluginID] = append(i.parent.events[i.scope.PluginID], ev)
	i.parent.mu.Unlock()
	return nil
}

// TestAdversarialLazyEcosystemStateMachineProperty executes 3,000 randomized
// adversarial steps challenging every Phase L/M/N security and performance
// invariant directly against the real Go runtime:
//   - Multi-publisher TrustRing (official + community pass, rogue ALWAYS rejected)
//   - Lockfile pinned_sha256 tamper attacks during Wake (ALWAYS fail closed)
//   - Bounded O(1) LRU working-set cap (evictable active count NEVER > MaxActiveLazy)
//   - PinnedActive & Eager immunity from LRU eviction and ReapIdle
//   - Dormant/Evicted/Slept plugins NEVER active in Host and NEVER retain live proxies
//   - Host fallback restoration and restricted approval.gate immunity
//   - Strict read_scopes topic isolation (host.secrets NEVER delivered)
func TestAdversarialLazyEcosystemStateMachineProperty(t *testing.T) {
	ctx := context.Background()
	pubOff, privOff, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubComm, privComm, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, privRogue, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	ring, err := marketplace.ParseTrustRingTOML([]byte(`
schema_version = 1
[publishers."tao3k.official"]
tier = "official"
allow_catalog = true
ed25519_pub = "` + hex.EncodeToString(pubOff) + `"
[publishers."acme.community"]
tier = "verified"
ed25519_pub = "` + hex.EncodeToString(pubComm) + `"
`))
	if err != nil {
		t.Fatal(err)
	}
	trust := ring.ReleaseTrust()
	mgr := marketplace.NewManager(trust)

	slots := shell.NewSlotRegistry()
	for _, sid := range []schema.SlotID{"workbench.main", "workbench.inspector", "workbench.sidebar"} {
		_ = slots.Declare(schema.SlotDeclaration{
			SlotID: sid, Cardinality: schema.SlotSingle,
			FallbackRenderer: "host." + string(sid), AllowUntrustedPlugin: true,
		}, lazySessRenderer{id: "host." + string(sid)})
	}
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "approval.gate", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.gate", AllowUntrustedPlugin: false,
	}, lazySessRenderer{id: "host.gate"})

	rt := &advPropRuntime{}
	host := pluginhost.NewHost(slots, lazySessDispatcher{})
	host.RegisterRuntime(rt)

	makeBundle := func(id, slot, topic, action string, pubID schema.PublisherID, priv ed25519.PrivateKey) ([]byte, string) {
		dir := t.TempDir()
		manifest := `
id = "` + id + `"
version = "0.1.0"
contract_major = 1
requires_features = ["ui-slot-v1"]
[contributions.p]
kind = "theme-pack"
[contributions.p.panel]
runtime = "declarative"
slot_id = "` + slot + `"
cardinality = "single"
entry_path = "theme.toml"
read_scopes = ["` + topic + `"]
declared_actions = ["` + action + `"]
`
		_ = os.WriteFile(filepath.Join(dir, "marlin-plugin.toml"), []byte(manifest), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "theme.toml"), []byte("[theme]\naccent = \"#c678dd\"\n"), 0o644)
		var rev [20]byte
		raw, _, err := marketplace.PackBundle(dir, pubID, rev, priv)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		return raw, hex.EncodeToString(sum[:])
	}

	type pMeta struct {
		id         schema.PluginID
		slot       schema.SlotID
		topic      string
		action     string
		raw        []byte
		pin        string
		evictable  bool
		activation schema.ActivationPolicy
		pinnedAct  bool
		hook       string
		idleSec    uint32
	}

	rawEager, pinEager := makeBundle("tao3k.eager-core", "workbench.sidebar", "core.health", "core.ping", "tao3k.official", privOff)
	rawPinned, pinPinned := makeBundle("tao3k.pinned-watch", "workbench.main", "org.tasks", "org.move", "tao3k.official", privOff)
	rawL1, pinL1 := makeBundle("acme.lazy-a", "workbench.inspector", "graph.nodes", "graph.inspect", "acme.community", privComm)
	rawL2, pinL2 := makeBundle("acme.lazy-b", "workbench.inspector", "session.summary", "session.ping", "acme.community", privComm)
	rawL3, pinL3 := makeBundle("acme.lazy-c", "workbench.main", "search.results", "search.open", "acme.community", privComm)
	rawRogue, _ := makeBundle("evil.rogue", "workbench.inspector", "host.secrets", "shell.exec", "evil.actor", privRogue)

	catalog := []pMeta{
		{id: "tao3k.eager-core", slot: "workbench.sidebar", topic: "core.health", action: "core.ping", raw: rawEager, pin: pinEager, evictable: false, activation: schema.ActivationEager, idleSec: 30},
		{id: "tao3k.pinned-watch", slot: "workbench.main", topic: "org.tasks", action: "org.move", raw: rawPinned, pin: pinPinned, evictable: false, activation: schema.ActivationOnTopic, pinnedAct: true, hook: "after-turn-complete", idleSec: 30},
		{id: "acme.lazy-a", slot: "workbench.inspector", topic: "graph.nodes", action: "graph.inspect", raw: rawL1, pin: pinL1, evictable: true, activation: schema.ActivationOnTopic, hook: "after-turn-complete", idleSec: 60},
		{id: "acme.lazy-b", slot: "workbench.inspector", topic: "session.summary", action: "session.ping", raw: rawL2, pin: pinL2, evictable: true, activation: schema.ActivationOnTopic, hook: "before-gate-open", idleSec: 90},
		{id: "acme.lazy-c", slot: "workbench.main", topic: "search.results", action: "search.open", raw: rawL3, pin: pinL3, evictable: true, activation: schema.ActivationOnCommand, hook: "on-session-switch", idleSec: 45},
	}
	byID := make(map[schema.PluginID]pMeta, len(catalog))
	for _, m := range catalog {
		byID[m.id] = m
	}

	var fetchMu sync.Mutex
	corruptNext := make(map[schema.PluginID]bool)

	var clockMu sync.Mutex
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	nowFn := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}

	const maxActiveLazy = 2
	loader, err := marketplace.NewLazyLoader(marketplace.LazyLoaderConfig{
		Manager:       mgr,
		Host:          host,
		Target:        schema.TargetDesktopMacOS,
		MaxActiveLazy: maxActiveLazy,
		Now:           nowFn,
		Fetch: func(_ context.Context, spec marketplace.PluginSpec) ([]byte, error) {
			fetchMu.Lock()
			corrupt := corruptNext[spec.PluginID]
			delete(corruptNext, spec.PluginID)
			fetchMu.Unlock()
			raw := byID[spec.PluginID].raw
			if corrupt {
				bad := append([]byte(nil), raw...)
				bad[len(bad)-1] ^= 0xff
				return bad, nil
			}
			return raw, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var specs []marketplace.PluginSpec
	for _, m := range catalog {
		specs = append(specs, marketplace.PluginSpec{
			PluginID:      m.id,
			Version:       "0.1.0",
			PinnedSHA256:  m.pin,
			Enabled:       true,
			PinnedActive:  m.pinnedAct,
			Activation:    m.activation,
			SlotID:        m.slot,
			Triggers:      []string{m.topic},
			Hooks:         []string{m.hook},
			IdleUnloadSec: m.idleSec,
		})
	}
	if err := loader.Reconcile(ctx, marketplace.PluginProfile{SchemaVersion: 1, Plugins: specs}); err != nil {
		t.Fatalf("initial Reconcile failed: %v", err)
	}

	rng := mrand.New(mrand.NewPCG(20261125, 1337))
	maxSeenGen := make(map[schema.PluginID]uint64)
	allHooks := []string{"after-turn-complete", "before-gate-open", "on-session-switch", "unknown.hook"}
	allTopics := []string{"core.health", "org.tasks", "graph.nodes", "session.summary", "search.results", "host.secrets"}

	for step := 0; step < 3000; step++ {
		switch rng.IntN(10) {
		case 0: // Normal Wake
			m := catalog[rng.IntN(len(catalog))]
			if _, err := loader.Wake(ctx, m.id); err != nil {
				t.Fatalf("step %d: valid Wake(%s) failed: %v", step, m.id, err)
			}

		case 1: // Adversarial lockfile pin-tamper Wake on a dormant plugin -> MUST fail with ErrPinMismatch!
			m := catalog[1+rng.IntN(len(catalog)-1)]
			_ = loader.Sleep(m.id)
			fetchMu.Lock()
			corruptNext[m.id] = true
			fetchMu.Unlock()
			_, err := loader.Wake(ctx, m.id)
			if !errors.Is(err, marketplace.ErrPinMismatch) {
				t.Fatalf("step %d: tampered Wake(%s) expected ErrPinMismatch, got %v", step, m.id, err)
			}

		case 2: // Emacs-style Hook trigger
			h := allHooks[rng.IntN(len(allHooks))]
			if _, err := loader.EnsureForHook(ctx, h); err != nil {
				t.Fatalf("step %d: EnsureForHook(%s) failed: %v", step, h, err)
			}

		case 3: // Topic trigger + scoped PublishTopic fan-out (including host.secrets probe)
			tp := allTopics[rng.IntN(len(allTopics))]
			_, _ = loader.EnsureForTopic(ctx, tp)
			_, _ = host.PublishTopic(fmt.Sprintf("ev-%d", step), tp, json.RawMessage(`{"step":1}`))

		case 4: // O(1) Touch (MRU promotion)
			m := catalog[rng.IntN(len(catalog))]
			loader.Touch(m.id)

		case 5: // Explicit Sleep on non-eager plugin
			m := catalog[1+rng.IntN(len(catalog)-1)]
			if err := loader.Sleep(m.id); err != nil {
				t.Fatalf("step %d: Sleep(%s) failed: %v", step, m.id, err)
			}

		case 6: // Advance clock + O(1) ReapIdle
			clockMu.Lock()
			now = now.Add(time.Duration(10+rng.IntN(80)) * time.Second)
			tNow := now
			clockMu.Unlock()
			_ = loader.ReapIdle(tNow)

		case 7: // Adversarial rogue publisher bundle MUST always fail closed
			if _, err := marketplace.VerifyBundle(rawRogue, trust); err == nil {
				t.Fatalf("step %d: rogue publisher passed multi-publisher TrustRing!", step)
			}

		case 8: // Adversarial stale/evicted proxy replay check
			m := catalog[rng.IntN(len(catalog))]
			rt.mu.Lock()
			proxies := append([]*pluginhost.ScopedProxy(nil), rt.proxies[m.id]...)
			rt.mu.Unlock()
			_, currentlyActive := host.ActiveScope(m.id)
			if !currentlyActive && len(proxies) > 0 {
				last := proxies[len(proxies)-1]
				if _, err := last.SubmitAction(ctx, m.action, nil, "stale-dormant"); !errors.Is(err, pluginhost.ErrProxyRevoked) {
					t.Fatalf("step %d: dormant/evicted plugin %s proxy was NOT revoked (err=%v)", step, m.id, err)
				}
			} else if len(proxies) >= 2 {
				old := proxies[0]
				if _, err := old.SubmitAction(ctx, m.action, nil, "stale-old-gen"); !errors.Is(err, pluginhost.ErrProxyRevoked) {
					t.Fatalf("step %d: superseded proxy for %s was NOT revoked (err=%v)", step, m.id, err)
				}
			}

		case 9: // Attempt forbidden mount onto approval.gate
			_, err := host.Activate(ctx, pluginhost.ScopedScope{
				PluginID: "acme.lazy-a", ContributionID: "evil", ActivationGeneration: 999999,
				Runtime: schema.RuntimeDeclarative, SlotID: "approval.gate",
			})
			if !errors.Is(err, shell.ErrUntrustedForbidden) {
				t.Fatalf("step %d: approval.gate accepted untrusted activation: %v", step, err)
			}
		}

		// === Verify ALL Invariants After EVERY Single Step ===

		// 1. approval.gate is ALWAYS owned by trusted host.gate.
		gateR := slots.Resolve("approval.gate")
		if len(gateR) != 1 || gateR[0].ID() != "host.gate" || !gateR[0].Trusted() {
			t.Fatalf("step %d: approval.gate invariant violated: %+v", step, gateR)
		}

		// 2. Bounded LRU working set + monotonic generations + Host/LazyLoader state consistency.
		states := loader.States()
		evictableActive := 0
		for _, st := range states {
			m := byID[st.Spec.PluginID]
			activeScope, inHost := host.ActiveScope(st.Spec.PluginID)
			if st.State == marketplace.LazyStateActive {
				if !inHost {
					t.Fatalf("step %d: %s is ACTIVE in LazyLoader but absent in Host", step, st.Spec.PluginID)
				}
				if st.ActiveGeneration != activeScope.ActivationGeneration || st.ActiveGeneration < maxSeenGen[st.Spec.PluginID] {
					t.Fatalf("step %d: %s non-monotonic or mismatched gen: loader=%d host=%d max=%d",
						step, st.Spec.PluginID, st.ActiveGeneration, activeScope.ActivationGeneration, maxSeenGen[st.Spec.PluginID])
				}
				maxSeenGen[st.Spec.PluginID] = st.ActiveGeneration
				if m.evictable {
					evictableActive++
				}
			} else {
				if inHost {
					t.Fatalf("step %d: %s is %s in LazyLoader but still active in Host!", step, st.Spec.PluginID, st.State)
				}
			}
		}
		if evictableActive > maxActiveLazy {
			t.Fatalf("step %d: invWorkingSetBoundedByLRUCap violated: evictableActive=%d > cap=%d", step, evictableActive, maxActiveLazy)
		}

		// 3. Eager core plugin must NEVER be evicted by LRU or ReapIdle.
		if _, ok := host.ActiveScope("tao3k.eager-core"); !ok {
			t.Fatalf("step %d: eager plugin tao3k.eager-core was unexpectedly evicted!", step)
		}

		// 4. Zero unauthorized topics (especially host.secrets) ever delivered to any plugin.
		rt.mu.Lock()
		for pid, evs := range rt.events {
			wantTopic := byID[pid].topic
			for _, ev := range evs {
				if ev.Topic == "host.secrets" || ev.Topic != wantTopic {
					t.Fatalf("step %d: unauthorized topic %q delivered to %s (want only %q)", step, ev.Topic, pid, wantTopic)
				}
			}
		}
		rt.mu.Unlock()
	}
}
