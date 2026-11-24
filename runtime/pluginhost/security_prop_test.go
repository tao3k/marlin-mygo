// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package pluginhost_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/engine/bus"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/device"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

type modelCallerKey struct{}

type stubRenderer struct {
	id      string
	trusted bool
}

func (r *stubRenderer) ID() string                      { return r.id }
func (r *stubRenderer) Trusted() bool                   { return r.trusted }
func (r *stubRenderer) Render(shell.RenderContext)      {}
func (r *stubRenderer) HandleEvent(shell.HostEvent) error { return nil }
func (r *stubRenderer) Dispose()                        {}

type modelInstance struct {
	scope    pluginhost.ScopedScope
	proxy    *pluginhost.ScopedProxy
	renderer shell.SlotRenderer
	mu       sync.Mutex
	events   []pluginhost.ScopedEvent
}

func (m *modelInstance) Scope() pluginhost.ScopedScope  { return m.scope }
func (m *modelInstance) Renderer() shell.SlotRenderer   { return m.renderer }
func (m *modelInstance) Close() error                   { return nil }
func (m *modelInstance) DeliverEvent(ev pluginhost.ScopedEvent) error {
	m.mu.Lock()
	m.events = append(m.events, ev)
	m.mu.Unlock()
	return nil
}

type modelFactory struct {
	mu      sync.Mutex
	proxies map[schema.PluginID][]*pluginhost.ScopedProxy
	insts   map[schema.PluginID]*modelInstance
}

func (f *modelFactory) Kind() schema.PanelRuntimeKind { return schema.RuntimeWebViewUntrusted }
func (f *modelFactory) Instantiate(_ context.Context, scope pluginhost.ScopedScope, proxy *pluginhost.ScopedProxy) (pluginhost.Instance, error) {
	inst := &modelInstance{
		scope:    scope,
		proxy:    proxy,
		renderer: &stubRenderer{id: string(scope.PluginID), trusted: false},
	}
	f.mu.Lock()
	f.proxies[scope.PluginID] = append(f.proxies[scope.PluginID], proxy)
	f.insts[scope.PluginID] = inst
	f.mu.Unlock()
	return inst, nil
}

type recordingDispatcher struct {
	mu       sync.Mutex
	requests []pluginhost.ScopedActionRequest
}

func (d *recordingDispatcher) SubmitScopedPanelAction(_ context.Context, req pluginhost.ScopedActionRequest) (pluginhost.ScopedActionReceipt, error) {
	d.mu.Lock()
	d.requests = append(d.requests, req)
	d.mu.Unlock()
	return pluginhost.ScopedActionReceipt{Admitted: true, ReceiptID: "ok"}, nil
}

type modelDeviceProvider struct {
	mu     sync.Mutex
	perm   device.OsPermissionState
	writes []string
}

func (p *modelDeviceProvider) Family() string { return "ios" }
func (p *modelDeviceProvider) Descriptors(context.Context) ([]device.CapabilityDescriptor, error) {
	p.mu.Lock()
	perm := p.perm
	p.mu.Unlock()
	return []device.CapabilityDescriptor{
		{CapabilityID: "ios.calendar.create", ProtocolFamily: "pim", Permission: perm, ReadOnly: false},
	}, nil
}
func (p *modelDeviceProvider) Invoke(_ context.Context, req device.InvocationRequest) (device.InvocationResult, error) {
	p.mu.Lock()
	p.writes = append(p.writes, req.GateReceiptID)
	p.mu.Unlock()
	return device.InvocationResult{InvocationID: req.InvocationID, OK: true}, nil
}

// TestAdversarialSecurityStateMachineProperty executes 2,000 randomized
// adversarial steps mirroring spec/quint/frontend-security.qnt directly against
// the production Go packages (marketplace, shell, pluginhost, bus, device).
func TestAdversarialSecurityStateMachineProperty(t *testing.T) {
	ctx := context.Background()
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{"tao3k.official": pubKey})
	mkt := marketplace.NewManager(trust)

	slots := shell.NewSlotRegistry()
	if err := slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle,
		FallbackRenderer: "native.main", AllowUntrustedPlugin: true,
	}, &stubRenderer{id: "native.main", trusted: true}); err != nil {
		t.Fatal(err)
	}
	if err := slots.Declare(schema.SlotDeclaration{
		SlotID: "approval.gate", Cardinality: schema.SlotSingle,
		FallbackRenderer: "native.gate", AllowUntrustedPlugin: false,
	}, &stubRenderer{id: "native.gate", trusted: true}); err != nil {
		t.Fatal(err)
	}

	disp := &recordingDispatcher{}
	host := pluginhost.NewHost(slots, disp)
	factory := &modelFactory{
		proxies: make(map[schema.PluginID][]*pluginhost.ScopedProxy),
		insts:   make(map[schema.PluginID]*modelInstance),
	}
	host.RegisterRuntime(factory)

	protoHandler := pluginhost.NewWebViewProtocolHandler(func(ctx context.Context) (schema.PluginID, bool) {
		id, ok := ctx.Value(modelCallerKey{}).(schema.PluginID)
		return id, ok
	})

	devProv := &modelDeviceProvider{perm: device.PermissionGranted}
	devBridge := device.NewBridge(schema.TargetMobileIOS)
	if err := devBridge.RegisterProvider(ctx, devProv); err != nil {
		t.Fatal(err)
	}

	pluginSpecs := map[schema.PluginID]struct {
		actions []string
		topics  []string
	}{
		"tao3k.pa":   {actions: []string{"org.move"}, topics: []string{"org.tasks"}},
		"tao3k.pb":   {actions: []string{"diff.view"}, topics: []string{"workspace.diff"}},
		"evil.rogue": {actions: nil, topics: nil},
	}
	pluginIDs := []schema.PluginID{"tao3k.pa", "tao3k.pb", "evil.rogue"}
	allActions := []string{"org.move", "diff.view", "shell.exec"}
	allTopics := []string{"org.tasks", "workspace.diff", "host.secrets"}

	liveGen := make(map[schema.PluginID]uint64)
	breakers := map[schema.PluginID]*bus.CircuitBreaker{
		"tao3k.pa": bus.NewCircuitBreaker(2, time.Hour),
		"tao3k.pb": bus.NewCircuitBreaker(2, time.Hour),
	}
	var nextReceiptSeq int
	var issuedReceipts []string

	rng := mrand.New(mrand.NewPCG(20261124, 42))
	now := time.Now()

	for step := 0; step < 2000; step++ {
		switch rng.IntN(8) {
		case 0: // Marketplace stage (valid vs tampered signature or asset)
			manifest := []byte(`id = "tao3k.pa"`)
			att := schema.ReleaseAttestation{Publisher: "tao3k.official"}
			sig := ed25519.Sign(privKey, schema.SigningBytes(manifest, att))
			var sigArr [ed25519.SignatureSize]byte
			copy(sigArr[:], sig)
			asset := []byte("<h1>ok</h1>")
			wantHash := sha256.Sum256(asset)
			tamper := rng.IntN(2) == 0
			if tamper {
				asset = []byte("<h1>TAMPERED</h1>")
			}
			_, stageErr := mkt.VerifyAndStage(
				schema.SignedRelease{ManifestTOML: manifest, Attestation: att, Signature: sigArr},
				map[schema.ContributionID][32]byte{"panel": wantHash},
				map[schema.ContributionID][]byte{"panel": asset},
			)
			if tamper && stageErr == nil {
				t.Fatalf("step %d: tampered asset passed VerifyAndStage", step)
			}
			if !tamper && stageErr != nil {
				t.Fatalf("step %d: valid bundle rejected: %v", step, stageErr)
			}

		case 1: // Activate or hot-reload into workbench.main OR attempt restricted approval.gate
			pid := pluginIDs[rng.IntN(2)] // pa or pb
			slotID := schema.SlotID("workbench.main")
			if rng.IntN(3) == 0 {
				slotID = "approval.gate"
			}
			nextGen := liveGen[pid] + 1
			spec := pluginSpecs[pid]
			scope := pluginhost.ScopedScope{
				PluginID:             pid,
				ContributionID:       "panel",
				ActivationGeneration: nextGen,
				Runtime:              schema.RuntimeWebViewUntrusted,
				SlotID:               slotID,
				ReadScopes:           spec.topics,
				DeclaredActions:      spec.actions,
				Assets:               fstest.MapFS{"index.html": {Data: []byte("ok")}},
			}
			_, actErr := host.Activate(ctx, scope)
			if slotID == "approval.gate" {
				if !errors.Is(actErr, shell.ErrUntrustedForbidden) {
					t.Fatalf("step %d: expected ErrUntrustedForbidden on approval.gate, got %v", step, actErr)
				}
			} else if actErr == nil {
				liveGen[pid] = nextGen
				breakers[pid].RecordSuccess()
				inst := factory.insts[pid]
				protoHandler.Bind(pid, scope.Assets, inst.proxy)
			}

		case 2: // Trip circuit breaker -> must atomically deactivate + restore native fallback + revoke proxy
			pid := pluginIDs[rng.IntN(2)]
			b := breakers[pid]
			if b.RecordFailure(now) && liveGen[pid] > 0 {
				_ = host.Deactivate(pid)
				protoHandler.Unbind(pid)
			}

		case 3: // Adversarial HTTP action post via marlin-plugin:// (random caller, host, action)
			caller := pluginIDs[rng.IntN(len(pluginIDs))]
			target := pluginIDs[rng.IntN(len(pluginIDs))]
			act := allActions[rng.IntN(len(allActions))]
			payload, _ := json.Marshal(map[string]any{"action": act, "idempotencyKey": fmt.Sprintf("k-%d", step)})
			req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("marlin-plugin://%s/__marlin/action", target), bytes.NewReader(payload))
			req = req.WithContext(context.WithValue(req.Context(), modelCallerKey{}, caller))
			rec := httptest.NewRecorder()
			protoHandler.ServeHTTP(rec, req)
			if caller != target && rec.Code != http.StatusForbidden {
				t.Fatalf("step %d: cross-origin caller %q -> target %q got %d (want 403)", step, caller, target, rec.Code)
			}

		case 4: // Direct stale/revoked proxy replay attempt
			pid := pluginIDs[rng.IntN(2)]
			proxies := factory.proxies[pid]
			if len(proxies) >= 2 {
				stale := proxies[0]
				if _, err := stale.SubmitAction(ctx, pluginSpecs[pid].actions[0], nil, "stale"); !errors.Is(err, pluginhost.ErrProxyRevoked) {
					t.Fatalf("step %d: stale proxy for %s was not revoked: err=%v", step, pid, err)
				}
			}

		case 5: // Adversarial event routing (random plugin, topic, gen)
			pid := pluginIDs[rng.IntN(len(pluginIDs))]
			topic := allTopics[rng.IntN(len(allTopics))]
			gen := uint64(rng.IntN(4))
			_ = host.RouteEvent(pluginhost.ScopedEvent{
				EventID:              fmt.Sprintf("ev-%d", step),
				PluginID:             pid,
				ActivationGeneration: gen,
				Topic:                topic,
			})

		case 6: // Issue a new Rust GateReceipt & attempt valid, unreceipted, or replayed device writes
			if rng.IntN(2) == 0 {
				devProv.mu.Lock()
				devProv.perm = device.PermissionGranted
				devProv.mu.Unlock()
			} else {
				devProv.mu.Lock()
				devProv.perm = device.PermissionDenied
				devProv.mu.Unlock()
			}
			mode := rng.IntN(3)
			var rcpt string
			switch mode {
			case 0: // missing receipt -> MUST fail
				rcpt = ""
			case 1: // fresh receipt
				nextReceiptSeq++
				rcpt = fmt.Sprintf("rcpt-%d", nextReceiptSeq)
				issuedReceipts = append(issuedReceipts, rcpt)
			case 2: // replay old receipt if any
				if len(issuedReceipts) > 0 {
					rcpt = issuedReceipts[rng.IntN(len(issuedReceipts))]
				}
			}
			_, _ = devBridge.Dispatch(ctx, device.InvocationRequest{
				InvocationID:  fmt.Sprintf("inv-%d", step),
				CapabilityID:  "ios.calendar.create",
				GateReceiptID: rcpt,
			})

		case 7: // Explicit unmount check
			pid := pluginIDs[rng.IntN(2)]
			_ = host.Deactivate(pid)
			protoHandler.Unbind(pid)
		}

		// --- Assert ALL Formal Quint Invariants After Every Single Step ---

		// Inv 1: approval.gate is ALWAYS owned by trusted native.gate.
		gateRenderers := slots.Resolve("approval.gate")
		if len(gateRenderers) != 1 || gateRenderers[0].ID() != "native.gate" || !gateRenderers[0].Trusted() {
			t.Fatalf("step %d: invRestrictedSlotAlwaysTrusted violated: %+v", step, gateRenderers)
		}

		// Inv 3: Every dispatched action matches plugin's declared allowlist and gen > 0.
		disp.mu.Lock()
		for _, r := range disp.requests {
			if r.ActivationGeneration == 0 {
				t.Fatalf("step %d: action dispatched with gen 0: %+v", step, r)
			}
			allowed := false
			for _, a := range pluginSpecs[r.PluginID].actions {
				if a == r.ActionName {
					allowed = true
					break
				}
			}
			if !allowed {
				t.Fatalf("step %d: invNoCrossOriginOrUndeclaredAction violated: %+v", step, r)
			}
		}
		disp.mu.Unlock()

		// Inv 4: No plugin ever received host.secrets or an unauthorized topic.
		for pid, inst := range factory.insts {
			inst.mu.Lock()
			for _, ev := range inst.events {
				if ev.Topic == "host.secrets" || !inst.scope.AllowsTopic(ev.Topic) {
					t.Fatalf("step %d: invNoUnauthorizedTopicDelivered violated for %s: %+v", step, pid, ev)
				}
			}
			inst.mu.Unlock()
		}

		// Inv 5: Every executed device write has non-empty unique receipt ID.
		devProv.mu.Lock()
		seenRcpt := make(map[string]struct{}, len(devProv.writes))
		for _, w := range devProv.writes {
			if w == "" {
				t.Fatalf("step %d: invDeviceWritesHaveReceipt violated: empty receipt", step)
			}
			if _, dup := seenRcpt[w]; dup {
				t.Fatalf("step %d: invDeviceWritesHaveUniqueClaim violated: replayed receipt %q", step, w)
			}
			seenRcpt[w] = struct{}{}
		}
		devProv.mu.Unlock()

		// Inv 6: No open circuit breaker owns workbench.main.
		mainRenderers := slots.Resolve("workbench.main")
		for _, r := range mainRenderers {
			if b, ok := breakers[schema.PluginID(r.ID())]; ok && b.State() == bus.BreakerOpen {
				t.Fatalf("step %d: invOpenBreakerOwnsNoSlot violated: %s still mounted while breaker open", step, r.ID())
			}
		}
	}
}

func FuzzWebViewProtocolHTTP(f *testing.F) {
	f.Add("POST", "tao3k.org-kanban", "tao3k.org-kanban", "/__marlin/action", []byte(`{"action":"org.move"}`))
	f.Add("POST", "evil.other", "tao3k.org-kanban", "/__marlin/action", []byte(`{"action":"org.move"}`))
	f.Add("GET", "tao3k.org-kanban", "tao3k.org-kanban", "/../../etc/passwd", []byte(nil))
	f.Add("POST", "tao3k.org-kanban", "tao3k.org-kanban", "/__marlin/action", bytes.Repeat([]byte("A"), pluginhost.MaxActionBodyBytes+32))

	f.Fuzz(func(t *testing.T, method, caller, hostHeader, rawPath string, body []byte) {
		validMethod := len(method) > 0 && len(method) <= 16
		for i := 0; validMethod && i < len(method); i++ {
			b := method[i]
			if b < 'A' || b > 'Z' {
				validMethod = false
			}
		}
		if !validMethod {
			method = http.MethodGet
		}
		disp := &recordingDispatcher{}
		scope := pluginhost.ScopedScope{
			PluginID:             "tao3k.org-kanban",
			ContributionID:       "panel",
			ActivationGeneration: 1,
			DeclaredActions:      []string{"org.move"},
		}
		h := pluginhost.NewWebViewProtocolHandler(func(ctx context.Context) (schema.PluginID, bool) {
			id, ok := ctx.Value(modelCallerKey{}).(schema.PluginID)
			return id, ok
		})
		h.Bind(scope.PluginID, fstest.MapFS{"index.html": {Data: []byte("<h1>ok</h1>")}}, pluginhost.NewScopedProxy(scope, disp))

		cleanURLPath := "/" + rawPath
		req := httptest.NewRequest(method, "http://example.invalid/", bytes.NewReader(body))
		req.URL.Scheme = pluginhost.PluginScheme
		req.URL.Host = hostHeader
		req.URL.Path = cleanURLPath
		req.Host = hostHeader
		req = req.WithContext(context.WithValue(req.Context(), modelCallerKey{}, schema.PluginID(caller)))

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		// Invariant: cross-origin caller NEVER gets 200 and NEVER dispatches an action.
		if strings.ToLower(caller) != strings.ToLower(hostHeader) || strings.Contains(hostHeader, ":") || caller == "" {
			if rec.Code != http.StatusForbidden {
				t.Fatalf("cross-origin caller=%q host=%q got %d want 403", caller, hostHeader, rec.Code)
			}
			if len(disp.requests) != 0 {
				t.Fatalf("cross-origin request leaked to dispatcher: %+v", disp.requests)
			}
		}
		// Invariant: only declared action "org.move" may ever be dispatched.
		for _, r := range disp.requests {
			if r.ActionName != "org.move" {
				t.Fatalf("undeclared action leaked via fuzz: %+v", r)
			}
		}
		// Invariant: oversized POST body to action endpoint MUST be rejected with 413.
		if method == http.MethodPost && strings.ToLower(caller) == "tao3k.org-kanban" &&
			strings.ToLower(hostHeader) == "tao3k.org-kanban" &&
			cleanURLPath == pluginhost.ActionEndpointPath && len(body) > pluginhost.MaxActionBodyBytes {
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("oversized body (%d bytes) got %d want 413", len(body), rec.Code)
			}
		}
	})
}

func FuzzDecodeAndAdmitWasmUITree(f *testing.F) {
	f.Add([]byte(`{"kind":"panel","title":"ok","children":[{"kind":"text","spans":[{"text":"hi","tone":"info"}]}]}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"kind":""}`))
	f.Add([]byte(strings.Repeat(`{"kind":"rows","children":[`, 40) + `{"kind":"text"}` + strings.Repeat(`]}`, 40)))
	f.Fuzz(func(t *testing.T, raw []byte) {
		elem, err := pluginhost.DecodeAndAdmitWasmUITree(raw)
		if err != nil {
			return
		}
		if vErr := elem.ValidateBounds(schema.MaxUITreeNodes, schema.MaxUITreeDepth); vErr != nil {
			t.Fatalf("admitted UI IR tree violated bounds: %v", vErr)
		}
	})
}
