// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
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
