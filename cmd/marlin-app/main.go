// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

// Command marlin-app launches the modular Marlin MyGo GPU workbench with
// multi-machine federated sidebar switching (FederationSupervisor), live
// HTTP/2 gRPC SessionDriver support, Scheme UI-IR rendering, Emacs composable
// windows, Approval Gate overlays, and all 3 plugin runtimes.
package main

import (
	"context"
	"flag"
	"log"
	"time"
)

type appConfig struct {
	sidecarBin       string
	realSession      bool
	tursoPath        string
	pubKeyFile       string
	hostAddr         string
	token            string
	caFile           string
	certFile         string
	keyFile          string
	sessionID        string
	catalogURL       string
	catalogDir       string
	catalogPub       string
	trustRingFile    string
	catalogPublisher string
	pluginsProfile   string
	maxActiveLazy    int
	idleReapInterval time.Duration
	wasmCacheDir     string
	devPluginDir     string
	showcase         bool
	showcasePlugin   string
}

func parseFlags() appConfig {
	var cfg appConfig
	flag.StringVar(&cfg.sidecarBin, "sidecar", "", "optional local Rust marlin-mygo-host binary to spawn with ephemeral token")
	flag.BoolVar(&cfg.realSession, "real-session", false, "enable MARLIN_BRIDGE_REAL_SESSION=1 durable ConversationJournal streaming on supervised sidecar")
	flag.StringVar(&cfg.tursoPath, "turso", "", "optional Turso SQLite file path for crash-resilient session persistence (MARLIN_BRIDGE_TURSO_PATH)")
	flag.StringVar(&cfg.pubKeyFile, "pubkey", "", "optional hex Ed25519 publisher pubkey file passed to supervised sidecar")
	flag.StringVar(&cfg.hostAddr, "host", "", "optional live Rust marlin-agent-app-server gRPC address (e.g. 127.0.0.1:50051)")
	flag.StringVar(&cfg.token, "token", "marlin-dev-token", "Bearer token for live Rust host")
	flag.StringVar(&cfg.caFile, "ca", "", "optional TLS CA PEM file for remote gRPC hosts")
	flag.StringVar(&cfg.certFile, "cert", "", "optional mTLS client certificate PEM file")
	flag.StringVar(&cfg.keyFile, "key", "", "optional mTLS client private key PEM file")
	flag.StringVar(&cfg.sessionID, "session", "gui-session", "Marlin session ID")
	flag.StringVar(&cfg.catalogURL, "catalog-url", "", "optional HTTP/HTTPS signed Marketplace catalog URL (e.g. http://127.0.0.1:8787)")
	flag.StringVar(&cfg.catalogDir, "catalog-dir", "", "optional local directory containing signed catalog.json and .marlinpkg archives")
	flag.StringVar(&cfg.catalogPub, "catalog-pub", "", "optional hex Ed25519 public key file verifying the Marketplace catalog")
	flag.StringVar(&cfg.trustRingFile, "trust-ring", "", "optional multi-publisher trust-ring.toml overriding single -catalog-pub key")
	flag.StringVar(&cfg.catalogPublisher, "catalog-publisher", "tao3k.official", "trusted Marketplace publisher identifier")
	flag.StringVar(&cfg.pluginsProfile, "plugins-profile", "", "optional declarative marlin-plugins.toml lockfile persisted across restarts (Emacs-style lazy loading)")
	flag.IntVar(&cfg.maxActiveLazy, "max-active-lazy", 8, "maximum simultaneously active lazy plugins in O(1) LRU working set (0 = unbounded)")
	flag.DurationVar(&cfg.idleReapInterval, "idle-reap-interval", 15*time.Second, "background O(1) idle plugin reclamation check interval")
	flag.StringVar(&cfg.wasmCacheDir, "wasm-cache-dir", "", "optional persistent disk directory for wazero AOT compiled WASM cache (defaults to user cache dir)")
	flag.StringVar(&cfg.devPluginDir, "dev-plugin", "", "optional local plugin directory to watch and hot-reload live into the GPU workbench")
	flag.BoolVar(&cfg.showcase, "showcase", false, "one-command macOS/desktop showcase: auto-build & mount React 19 UI + Go WASI GPU Focus Clock")
	flag.StringVar(&cfg.showcasePlugin, "showcase-plugin", "agenda", "initial showcase main plugin: agenda | memory | workflow | git | github")
	flag.Parse()
	return cfg
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := runStudio(ctx, parseFlags()); err != nil {
		log.Fatal(err)
	}
}
