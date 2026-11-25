// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
	"github.com/tao3k/marlin-mygo/runtime/wazeroir"
)

type cliNoopRenderer struct{ id string }

func (r cliNoopRenderer) ID() string                        { return r.id }
func (r cliNoopRenderer) Trusted() bool                     { return true }
func (r cliNoopRenderer) Render(shell.RenderContext)        {}
func (r cliNoopRenderer) HandleEvent(shell.HostEvent) error { return nil }
func (r cliNoopRenderer) Dispose()                          {}

type cliNoopDispatcher struct{}

func (cliNoopDispatcher) SubmitScopedPanelAction(context.Context, pluginhost.ScopedActionRequest) (pluginhost.ScopedActionReceipt, error) {
	return pluginhost.ScopedActionReceipt{Admitted: true, ReceiptID: "rcpt-dev-cli"}, nil
}

func runServeCatalog(args []string) error {
	fs := flag.NewFlagSet("serve-catalog", flag.ContinueOnError)
	dir := fs.String("dir", "./dist/marketplace", "directory containing signed catalog.json, catalog.json.sig, and .marlinpkg bundles")
	addr := fs.String("addr", "127.0.0.1:8787", "listen address for local Marketplace HTTP registry")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := os.Stat(*dir); err != nil {
		return fmt.Errorf("catalog directory %s unavailable: %w", *dir, err)
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           marketplace.ServeCatalogHandler(*dir),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	fmt.Printf("Serving signed Marlin Marketplace catalog from %s at http://%s\n", *dir, *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func runDevPlugin(args []string) error {
	fs := flag.NewFlagSet("dev-plugin", flag.ContinueOnError)
	dir := fs.String("dir", ".", "plugin source directory containing marlin-plugin.toml")
	interval := fs.Duration("interval", 300*time.Millisecond, "source change poll interval")
	once := fs.Bool("once", false, "run one build/sign/activate cycle and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slots := shell.NewSlotRegistry()
	for _, slotID := range []schema.SlotID{"workbench.main", "workbench.inspector", "workbench.sidebar"} {
		_ = slots.Declare(schema.SlotDeclaration{
			SlotID: slotID, Cardinality: schema.SlotSingle,
			FallbackRenderer: "host." + string(slotID), AllowUntrustedPlugin: true,
		}, cliNoopRenderer{id: "host." + string(slotID)})
	}
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID: "approval.gate", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.gate", AllowUntrustedPlugin: false,
	}, cliNoopRenderer{id: "host.gate"})

	eng, err := wazeroir.NewModuleEngine(ctx)
	if err != nil {
		return err
	}
	defer eng.Close(ctx)

	host := pluginhost.NewHost(slots, cliNoopDispatcher{})
	host.RegisterRuntime(wazeroir.NewRuntime(slots, eng))
	host.RegisterRuntime(&pluginhost.DeclarativeRuntimeFactory{})

	reloader, err := marketplace.NewDevReloader(marketplace.DevReloaderConfig{
		PluginDir: *dir,
		Host:      host,
		Target:    schema.TargetDesktopMacOS,
	})
	if err != nil {
		return err
	}

	logResult := func(res marketplace.ReloadResult, reloadErr error) {
		if reloadErr != nil {
			fmt.Fprintf(os.Stderr, "[dev-plugin] RELOAD FAILED (gen:%d, active_gen:%d, rolled_back:%v, %s): %v\n",
				res.AttemptedGen, res.ActiveGeneration, res.RolledBack, res.Elapsed.Round(time.Millisecond), reloadErr)
			return
		}
		fmt.Printf("[dev-plugin] HOT-RELOADED %s v%s (%s -> %s) gen:%d in %s\n",
			res.PluginID, res.Version, res.Runtime, res.SlotID, res.ActiveGeneration, res.Elapsed.Round(time.Millisecond))
	}

	if *once {
		res, err := reloader.ReloadOnce(ctx)
		logResult(res, err)
		return err
	}

	fmt.Printf("Watching plugin directory %s (poll=%s, Ctrl-C to stop)...\n", *dir, *interval)
	err = reloader.Watch(ctx, *interval, logResult)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	dir := fs.String("dir", ".", "plugin directory containing marlin-plugin.toml")
	asJSON := fs.Bool("json", false, "emit machine-readable JSON audit report (ideal for CI and coding agents)")
	withPerf := fs.Bool("perf", false, "also run the plugin performance budget gate (bench-plugin)")
	scale := fs.Float64("scale", 1.0, "latency budget scale multiplier when -perf is set")
	iters := fs.Int("iterations", 150, "microbenchmark iterations when -perf is set")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	rep, auditErr := marketplace.AuditPluginDir(ctx, *dir)

	var perfRep *marketplace.PluginPerfReport
	var perfErr error
	if *withPerf {
		perfRep, perfErr = marketplace.BenchPluginDir(ctx, *dir, marketplace.PluginPerfConfig{
			Iterations:   *iters,
			LatencyScale: *scale,
		})
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if *withPerf {
			_ = enc.Encode(map[string]any{
				"audit":  rep,
				"perf":   perfRep,
				"passed": auditErr == nil && perfErr == nil,
			})
		} else {
			_ = enc.Encode(rep)
		}
		if auditErr != nil {
			return auditErr
		}
		return perfErr
	}

	fmt.Printf("Marlin Plugin Doctor (%s v%s, runtime=%s, slot=%s)\n", rep.PluginID, rep.Version, rep.Runtime, rep.SlotID)
	for _, chk := range rep.Checks {
		mark := "PASS"
		if !chk.Passed {
			mark = "FAIL"
		}
		fmt.Printf("  [%s] %-24s %s\n", mark, chk.Name, chk.Details)
	}
	if perfRep != nil {
		fmt.Println()
		fmt.Print(marketplace.FormatPluginPerfReport(perfRep))
	}
	if auditErr != nil {
		return auditErr
	}
	return perfErr
}

func runBenchPlugin(args []string) error {
	fs := flag.NewFlagSet("bench-plugin", flag.ContinueOnError)
	dir := fs.String("dir", ".", "plugin directory containing marlin-plugin.toml")
	asJSON := fs.Bool("json", false, "emit machine-readable JSON performance report with remediation hints")
	iters := fs.Int("iterations", 150, "warm microbenchmark iterations")
	scale := fs.Float64("scale", 1.0, "latency budget scale multiplier (e.g. 2.0 on shared CI VMs)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rep, err := marketplace.BenchPluginDir(context.Background(), *dir, marketplace.PluginPerfConfig{
		Iterations:   *iters,
		LatencyScale: *scale,
	})
	if *asJSON && rep != nil {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
		return err
	}
	if rep != nil {
		fmt.Print(marketplace.FormatPluginPerfReport(rep))
	}
	return err
}

func runPublish(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	dir := fs.String("dir", ".", "plugin source directory containing marlin-plugin.toml")
	publisher := fs.String("publisher", "", "publisher identifier (e.g. acme.dev)")
	keyPath := fs.String("key", "", "path to hex-encoded Ed25519 private key file")
	pubPath := fs.String("pub", "", "path to hex-encoded Ed25519 public key file")
	outDir := fs.String("out", "./dist/marketplace", "output Marketplace catalog directory")
	revHex := fs.String("rev", strings.Repeat("0", 40), "40-char hex Git source revision")
	replaceOld := fs.Bool("replace", true, "remove older .marlinpkg versions of the same plugin in -out")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *publisher == "" || *keyPath == "" || *pubPath == "" {
		return errors.New("flags -publisher, -key, and -pub are required")
	}
	privBytes, err := readHexKey(*keyPath, ed25519.PrivateKeySize)
	if err != nil {
		return err
	}
	pubBytes, err := readHexKey(*pubPath, ed25519.PublicKeySize)
	if err != nil {
		return err
	}
	revBytes, err := hex.DecodeString(strings.TrimSpace(*revHex))
	if err != nil || len(revBytes) != 20 {
		return fmt.Errorf("invalid 40-char hex -rev %q", *revHex)
	}
	var rev [20]byte
	copy(rev[:], revBytes)

	rcpt, err := marketplace.PublishPlugin(context.Background(), marketplace.PublishConfig{
		PluginDir:       *dir,
		PublisherID:     schema.PublisherID(*publisher),
		PrivateKey:      ed25519.PrivateKey(privBytes),
		PublicKey:       ed25519.PublicKey(pubBytes),
		SourceRev:       rev,
		OutCatalogDir:   *outDir,
		ReplaceExisting: *replaceOld,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Published %s v%s (%s -> %s)\n  package : %s (%d bytes, sha256=%s)\n  audit   : %d/%d checks PASSED\n  catalog : %s/catalog.json (%d entries, Ed25519 signed)\n",
		rcpt.PluginID, rcpt.Version, rcpt.Runtime, rcpt.SlotID,
		rcpt.PackageFile, rcpt.PackageBytes, rcpt.PackageSHA256,
		rcpt.AuditChecks, rcpt.AuditChecks, *outDir, rcpt.CatalogEntries)
	return nil
}
