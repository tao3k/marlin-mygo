// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

type lazySessRenderer struct{ id string }

func (r lazySessRenderer) ID() string                        { return r.id }
func (r lazySessRenderer) Trusted() bool                     { return true }
func (r lazySessRenderer) Render(shell.RenderContext)        {}
func (r lazySessRenderer) HandleEvent(shell.HostEvent) error { return nil }
func (r lazySessRenderer) Dispose()                          {}

type lazySessDispatcher struct{}

func (lazySessDispatcher) SubmitScopedPanelAction(context.Context, pluginhost.ScopedActionRequest) (pluginhost.ScopedActionReceipt, error) {
	return pluginhost.ScopedActionReceipt{Admitted: true, ReceiptID: "rcpt-lazy-sess"}, nil
}

func buildLazyTestBundle(t *testing.T, priv ed25519.PrivateKey, id, version, slot, activation, trigger string, idleSec int) []byte {
	t.Helper()
	dir := t.TempDir()
	trigLine := ""
	if trigger != "" {
		trigLine = "triggers = [\"" + trigger + "\"]\n"
	}
	manifest := `
id = "` + id + `"
version = "` + version + `"
contract_major = 1
requires_features = ["ui-slot-v1"]

[contributions.pack]
kind = "theme-pack"

[contributions.pack.panel]
runtime = "declarative"
slot_id = "` + slot + `"
cardinality = "single"
entry_path = "theme.toml"
activation = "` + activation + `"
` + trigLine + `idle_unload_sec = ` + itoa(idleSec) + `
`
	if err := os.WriteFile(filepath.Join(dir, "marlin-plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "theme.toml"), []byte("[theme]\naccent = \"#98c379\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var rev [20]byte
	pkg, _, err := marketplace.PackBundle(dir, "tao3k.official", rev, priv)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}

func TestLazySessionPersistAcrossRestartHookWakeRollbackAndIdleReap(t *testing.T) {
	ctx := context.Background()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := schema.NewReleaseTrust(map[schema.PublisherID]ed25519.PublicKey{"tao3k.official": pub})

	repoDir := t.TempDir()
	pkgA1 := buildLazyTestBundle(t, priv, "tao3k.turn-summary", "0.1.0", "workbench.inspector", "on-command", "summary.open", 60)
	pkgA2 := buildLazyTestBundle(t, priv, "tao3k.turn-summary", "0.2.0", "workbench.inspector", "on-command", "summary.open", 60)
	pkgB1 := buildLazyTestBundle(t, priv, "tao3k.cmd-palette-ext", "0.1.0", "workbench.main", "on-command", "palette.ext", 60)

	writeCatalog := func(files map[string][]byte) {
		var paths []string
		for name, raw := range files {
			p := filepath.Join(repoDir, name)
			if err := os.WriteFile(p, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			paths = append(paths, p)
		}
		var rev [20]byte
		catJSON, catSig, _, err := marketplace.BuildSignedCatalog(paths, "tao3k.official", rev, trust, priv)
		if err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(repoDir, "catalog.json"), catJSON, 0o644)
		_ = os.WriteFile(filepath.Join(repoDir, "catalog.json.sig"), []byte(hex.EncodeToString(catSig)+"\n"), 0o644)
	}
	writeCatalog(map[string][]byte{
		"tao3k.turn-summary-0.1.0.marlinpkg":    pkgA1,
		"tao3k.cmd-palette-ext-0.1.0.marlinpkg": pkgB1,
	})

	srv := httptest.NewServer(marketplace.ServeCatalogHandler(repoDir))
	defer srv.Close()

	casDir := filepath.Join(t.TempDir(), "cas")
	profilePath := filepath.Join(t.TempDir(), "marlin-plugins.toml")

	newHost := func() (*shell.SlotRegistry, *pluginhost.Host) {
		slots := shell.NewSlotRegistry()
		for _, sid := range []schema.SlotID{"workbench.main", "workbench.inspector"} {
			_ = slots.Declare(schema.SlotDeclaration{
				SlotID: sid, Cardinality: schema.SlotSingle,
				FallbackRenderer: "host." + string(sid), AllowUntrustedPlugin: true,
			}, lazySessRenderer{id: "host." + string(sid)})
		}
		h := pluginhost.NewHost(slots, lazySessDispatcher{})
		h.RegisterRuntime(&pluginhost.DeclarativeRuntimeFactory{})
		return slots, h
	}

	var clockMu sync.Mutex
	now := time.Date(2026, 11, 25, 11, 30, 0, 0, time.UTC)
	nowFn := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}

	// === Session 1: Install from signed HTTP registry, attach hook, upgrade & rollback ===
	_, host1 := newHost()
	reg1, err := marketplace.NewRegistryClient(marketplace.RegistryConfig{
		BaseURL: srv.URL, CacheDir: casDir, CatalogPubKey: pub,
	})
	if err != nil {
		t.Fatal(err)
	}
	sess1, err := marketplace.NewLazySession(ctx, marketplace.LazySessionConfig{
		Registry:      reg1,
		Trust:         trust,
		Host:          host1,
		Target:        schema.TargetDesktopMacOS,
		ProfilePath:   profilePath,
		MaxActiveLazy: 4,
		Now:           nowFn,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess1.InstallOrUpgrade(ctx, "tao3k.turn-summary"); err != nil {
		t.Fatalf("install turn-summary v0.1.0 failed: %v", err)
	}
	if err := sess1.PersistHooks(ctx, "tao3k.turn-summary", []string{"after-turn-complete"}); err != nil {
		t.Fatalf("PersistHooks failed: %v", err)
	}
	if _, err := sess1.InstallOrUpgrade(ctx, "tao3k.cmd-palette-ext"); err != nil {
		t.Fatalf("install cmd-palette-ext failed: %v", err)
	}

	// Upgrade turn-summary to v0.2.0, then rollback back to v0.1.0.
	_ = os.Remove(filepath.Join(repoDir, "tao3k.turn-summary-0.1.0.marlinpkg"))
	writeCatalog(map[string][]byte{
		"tao3k.turn-summary-0.2.0.marlinpkg":    pkgA2,
		"tao3k.cmd-palette-ext-0.1.0.marlinpkg": pkgB1,
	})
	if _, err := sess1.RefreshCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	if p, err := sess1.InstallOrUpgrade(ctx, "tao3k.turn-summary"); err != nil || p.Manifest.Version != "0.2.0" {
		t.Fatalf("upgrade to v0.2.0 failed: p=%+v err=%v", p, err)
	}
	if prevVer, ok := sess1.HasRollback("tao3k.turn-summary"); !ok || prevVer != "0.1.0" {
		t.Fatalf("expected rollback v0.1.0 available, got %q %t", prevVer, ok)
	}
	if p, err := sess1.Rollback(ctx, "tao3k.turn-summary"); err != nil || p.Manifest.Version != "0.1.0" {
		t.Fatalf("rollback to v0.1.0 failed: p=%+v err=%v", p, err)
	}

	// Confirm marlin-plugins.toml was persisted atomically with SHA-256 pins and hook.
	rawProf, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("expected persisted marlin-plugins.toml: %v", err)
	}
	if !strings.Contains(string(rawProf), `pinned_sha256 = "`) || !strings.Contains(string(rawProf), `hooks = ["after-turn-complete"]`) {
		t.Fatalf("persisted profile missing expected pin/hook:\n%s", string(rawProf))
	}

	// === Session 2 (Simulated Full App Restart, OFFLINE from HTTP server!) ===
	srv.Close() // prove offline restart + CAS pin wake works with zero network!

	slots2, host2 := newHost()
	reg2, err := marketplace.NewRegistryClient(marketplace.RegistryConfig{
		BaseURL: srv.URL, CacheDir: casDir, CatalogPubKey: pub,
	})
	if err != nil {
		t.Fatal(err)
	}
	sess2, err := marketplace.NewLazySession(ctx, marketplace.LazySessionConfig{
		Registry:      reg2,
		Trust:         trust,
		Host:          host2,
		Target:        schema.TargetDesktopMacOS,
		ProfilePath:   profilePath,
		MaxActiveLazy: 4,
		Now:           nowFn,
	})
	if err != nil {
		t.Fatalf("restart NewLazySession failed: %v", err)
	}

	// Zero startup RAM: both plugins start DORMANT!
	st0 := sess2.Stats()
	if st0.TotalDeclared != 2 || st0.ActiveCount != 0 || st0.DormantCount != 2 {
		t.Fatalf("expected 0 active / 2 dormant after restart, got %+v", st0)
	}

	// Fire Emacs-style hook "after-turn-complete" -> wakes ONLY tao3k.turn-summary offline from CAS!
	woken, err := sess2.TriggerHook(ctx, "after-turn-complete")
	if err != nil || len(woken) != 1 || woken[0] != "tao3k.turn-summary" {
		t.Fatalf("expected after-turn-complete hook to wake tao3k.turn-summary, got woken=%v err=%v", woken, err)
	}
	st1 := sess2.Stats()
	if st1.ActiveCount != 1 || st1.DormantCount != 1 || st1.TotalWakes != 1 {
		t.Fatalf("unexpected stats after hook wake: %+v", st1)
	}
	statusMap := sess2.StatusMap()
	if statusMap["tao3k.turn-summary"].LastWakeDuration <= 0 {
		t.Fatalf("expected positive LastWakeDuration telemetry, got %+v", statusMap["tao3k.turn-summary"])
	}

	// Advance clock past idle_unload_sec (60s) -> O(1) ReapIdle unloads plugin and restores host.inspector fallback!
	clockMu.Lock()
	now = now.Add(90 * time.Second)
	clockMu.Unlock()
	reaped := sess2.ReapIdle(nowFn())
	if len(reaped) != 1 || reaped[0] != "tao3k.turn-summary" {
		t.Fatalf("expected tao3k.turn-summary reaped after idle timeout, got %v", reaped)
	}
	st2 := sess2.Stats()
	if st2.ActiveCount != 0 || st2.DormantCount != 2 || st2.TotalReaped != 1 {
		t.Fatalf("unexpected stats after idle reap: %+v", st2)
	}
	if resolved := slots2.Resolve("workbench.inspector"); len(resolved) != 1 || resolved[0].ID() != "host.workbench.inspector" {
		t.Fatalf("expected host.workbench.inspector fallback restored after idle reap, got %+v", resolved)
	}
}
