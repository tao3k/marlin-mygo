// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

// Command marlin-mobile launches Marlin's authentic phone-first Mobile
// Companion shell for iOS (iPhone 16 Pro 393x852, Apple HIG) and Android
// (Pixel 9 412x915, Material 3) with App-Store-2.5.2-compliant declarative
// OTA packs, Tier-0 Approval Gate review, and proactive capture routing.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

func wireMobileGovernance(
	ctx context.Context,
	liveTarget *grpcwire.Target,
	schemeHostBin, schemeGovMode, schemeGovDB, sessionID string,
	onSnap func(mygoshell.BackendGovernanceSnapshot),
) (*mygoshell.GovernanceController, func()) {
	govUI := mygoshell.NewGovernanceController(
		features.NewRegistry(),
		features.NewGovernanceLedger(features.DefaultGovernancePolicy()),
		features.NewHost(),
	)
	if liveTarget != nil {
		auth := grpcwire.NewRustGovernanceAuthority(grpcwire.RustGovernanceAuthorityConfig{
			Target:     liveTarget,
			ProjectID:  "default",
			SessionID:  sessionID,
			OnSnapshot: onSnap,
		})
		govUI.AttachBackendAuthority(auth)
		_ = auth.Start(ctx)
		return govUI, auth.Stop
	}

	if !strings.EqualFold(strings.TrimSpace(schemeHostBin), "off") {
		dbPath := strings.TrimSpace(schemeGovDB)
		if dbPath == "" {
			if cacheDir, err := os.UserCacheDir(); err == nil && cacheDir != "" {
				dbPath = filepath.Join(cacheDir, "marlin", "mobile-governance-authority.db")
			}
		}
		if h, err := grpcwire.LaunchGovernanceSidecar(ctx, grpcwire.GovernanceSidecarConfig{
			ExplicitPath: schemeHostBin,
			Mode:         schemeGovMode,
			TursoPath:    dbPath,
			ProjectID:    "default",
			SessionID:    sessionID,
			OnSnapshot:   onSnap,
		}); err == nil {
			log.Printf("[mobile] governance authority connected to %s (%s)", h.BinaryPath, h.DiscoverySource)
			govUI.AttachBackendAuthority(h.Authority)
			return govUI, func() { _ = h.Close() }
		} else if strings.TrimSpace(schemeHostBin) != "" {
			log.Printf("[mobile] explicit -scheme-host %q failed (%v); falling back to preview bridge", schemeHostBin, err)
		}
	}

	govUI.AttachBackendAuthority(mygoshell.NewLocalBackendAuthorityBridge(mygoshell.BackendGovernanceSnapshot{
		Admitted:         true,
		RevocationEpoch:  1,
		PolicyRevision:   1,
		BreakerTripEpoch: 1,
		SynthesizedCedarRules: []mygoshell.SynthesizedCedarRule{
			{PolicyID: "marlin.forbid.agent-privileged-governance", Source: `forbid (principal in Marlin::Role::"Agent", action in [Marlin::Action::"rearm_breaker", Marlin::Action::"promote_context_scope"], resource);`},
			{PolicyID: "marlin.permit.verified-human-governance", Source: `permit (principal in Marlin::Role::"VerifiedHuman", action in [Marlin::Action::"rearm_breaker", Marlin::Action::"promote_context_scope"], resource) when { context.step_up_fresh == true };`},
		},
		LastEventNote: "mobile standalone preview (connect -host or build marlin-scheme-host-aot for live Rust+Scheme authority)",
	}))
	return govUI, nil
}

func main() {
	platformFlag := flag.String("platform", "ios", "mobile platform profile: ios | android")
	deviceFlag := flag.String("device", "", "optional device viewport preset: iphone16 | iphone-se | pixel9")
	themeFlag := flag.String("theme", "midnight", "initial curated theme preset: midnight | paper | doom-one | tokyo-night | catppuccin")
	hostAddr := flag.String("host", "", "optional remote Rust marlin-agent-app-server gRPC address (e.g. tailnet:50051)")
	token := flag.String("token", "marlin-dev-token", "Bearer token for remote Rust host")
	caFile := flag.String("ca", "", "optional TLS CA PEM file for mTLS/TLS remote host")
	certFile := flag.String("cert", "", "optional mTLS client certificate PEM file")
	keyFile := flag.String("key", "", "optional mTLS client private key PEM file")
	sessionID := flag.String("session", "mobile-session", "Marlin session ID")
	schemeHostBin := flag.String("scheme-host", "", "optional Linked-AOT marlin-scheme-host-aot binary path (empty=auto-discover, 'off'=disable)")
	schemeGovMode := flag.String("scheme-gov-mode", "clean", "initial Scheme governance mode: clean | incident")
	schemeGovDB := flag.String("scheme-gov-db", "", "optional Turso SQLite WAL path for durable mobile governance checkpoints")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	plat := mygoshell.MobilePlatformIOS
	if strings.EqualFold(strings.TrimSpace(*platformFlag), "android") || runtime.GOOS == "android" {
		plat = mygoshell.MobilePlatformAndroid
	}
	spec := mygoshell.BuiltinMobileDeviceSpec(plat, *deviceFlag)

	targetLabel := fmt.Sprintf("%s · Standalone Preview", spec.Name)
	var liveTarget *grpcwire.Target
	if strings.TrimSpace(*hostAddr) != "" {
		authHeader := *token
		if authHeader != "" && !strings.HasPrefix(authHeader, "Bearer ") {
			authHeader = "Bearer " + authHeader
		}
		var tlsCfg *grpcwire.TLSConfig
		if *caFile != "" || *certFile != "" || *keyFile != "" {
			tlsCfg = &grpcwire.TLSConfig{CAFile: *caFile, CertFile: *certFile, KeyFile: *keyFile}
		}
		dialed, err := grpcwire.Dial(grpcwire.DialConfig{
			ID:          "remote-desktop-host",
			DisplayName: "Remote Desktop Agent",
			Mode:        transport.ModeRemoteGRPC,
			Address:     *hostAddr,
			AuthToken:   authHeader,
			TLS:         tlsCfg,
		})
		if err != nil {
			log.Fatalf("dial remote host %s: %v", *hostAddr, err)
		}
		defer dialed.Close()
		liveTarget = dialed
		targetLabel = fmt.Sprintf("%s -> %s", spec.Name, *hostAddr)
	}

	mobile := mygoshell.NewMobileCompanionAppForPlatform(spec.Platform, spec.ID, targetLabel)
	mobile.SetThemePreset(*themeFlag)

	var activeWin *mygo.Window
	govUI, stopGov := wireMobileGovernance(
		ctx,
		liveTarget,
		*schemeHostBin,
		*schemeGovMode,
		*schemeGovDB,
		*sessionID,
		func(mygoshell.BackendGovernanceSnapshot) {
			if activeWin != nil {
				activeWin.Invalidate()
			}
		},
	)
	if stopGov != nil {
		defer stopGov()
	}
	mobile.GovernanceUI = govUI

	mobile.AppendMobileMessage(true, "You", "Check today's Org Agenda and pending approval gates.", "9:40")
	mobile.AppendMobileMessage(false, "Marlin", "3 Org tasks scheduled today. 1 Tier-0 notification gate is awaiting your approval.", "9:41")

	gateTool := "device.ios.notify.send"
	if spec.Platform == mygoshell.MobilePlatformAndroid {
		gateTool = "device.android.notify.send"
	}
	mobile.SetPendingGates([]mygoshell.PendingGateView{
		{
			GateID:      "gate-mobile-1",
			ToolName:    gateTool,
			Prompt:      "Approve evening reminder dispatch from Desktop Agent?",
			Description: "Tier-0 one-shot approval synced over mTLS gRPC.",
		},
	})
	mobile.SetPacks([]mygoshell.MobileDeclarativePack{
		{
			PluginID: "tao3k.daily-planner-pack",
			Version:  "0.1.0",
			Summary:  "Midnight Daily Planner theme + keymap overlay (0 executable code)",
			Runtime:  schema.RuntimeDeclarative,
			Active:   true,
		},
		{
			PluginID: "tao3k.ios-personal-overlay",
			Version:  "0.1.0",
			Summary:  "Compact mobile focus layout & capture shortcuts",
			Runtime:  schema.RuntimeDeclarative,
		},
		{
			PluginID: "tao3k.org-agenda-planner",
			Version:  "0.2.0",
			Summary:  "Desktop React 19 plugin (blocked on mobile OTA by App Store 2.5.2)",
			Runtime:  schema.RuntimeWebViewUntrusted,
		},
	})

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:          fmt.Sprintf("Marlin Mobile — %s", spec.Name),
			Width:          spec.LogicalWidth,
			Height:         spec.LogicalHeight,
			UseContentSize: true,
			MinWidth:       375,
			MinHeight:      667,
			MaxWidth:       430,
			MaxHeight:      932,
			Content:        ui.View(mobile.View),
		})
		activeWin = win
		mobile.OnSendPrompt = func(prompt string) {
			if liveTarget != nil {
				log.Printf("[mobile] queued remote turn on session=%s: %s", *sessionID, prompt)
				mobile.AppendMobileMessage(false, "Marlin", fmt.Sprintf("Sent to %s (session %s).", *hostAddr, *sessionID), "remote")
			} else {
				mobile.AppendMobileMessage(false, "Marlin", "Preview reply: "+prompt, "local")
			}
			win.Invalidate()
		}
		mobile.OnGateDecision = func(gateID string, approve bool) {
			log.Printf("[mobile] gate %s decision approve=%t (ctx=%v)", gateID, approve, ctx.Err())
			win.Invalidate()
		}
		mobile.OnQuickCapture = func(channel, text string) {
			log.Printf("[mobile] capture [%s]: %s", channel, text)
			win.Invalidate()
		}
		win.Invalidate()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
