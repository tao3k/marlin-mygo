// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

// Command marlin-mobile launches Marlin's compact, App-Store-2.5.2-compliant
// Mobile Companion shell (iOS / Android / desktop preview at 390x844) with
// remote Tier-0 Approval Gate review, proactive Orgize capture, and
// declarative-only OTA packs.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

func main() {
	hostAddr := flag.String("host", "", "optional remote Rust marlin-agent-app-server gRPC address (e.g. tailnet:50051)")
	token := flag.String("token", "marlin-dev-token", "Bearer token for remote Rust host")
	caFile := flag.String("ca", "", "optional TLS CA PEM file for mTLS/TLS remote host")
	certFile := flag.String("cert", "", "optional mTLS client certificate PEM file")
	keyFile := flag.String("key", "", "optional mTLS client private key PEM file")
	sessionID := flag.String("session", "mobile-session", "Marlin session ID")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	targetLabel := "iPhone Companion · Standalone Preview"
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
		targetLabel = fmt.Sprintf("iPhone Companion -> %s", *hostAddr)
	}

	mobile := mygoshell.NewMobileCompanionApp(targetLabel)
	mobile.Transcript.AppendUser("Check today's Org Agenda and pending approval gates.")
	mobile.SetPendingGates([]mygoshell.PendingGateView{
		{
			GateID:      "gate-mobile-1",
			ToolName:    "device.ios.notify.send",
			Prompt:      "Approve evening reminder dispatch from Desktop Agent?",
			Description: "Tier-0 one-shot approval synced over mTLS gRPC.",
		},
	})
	mobile.SetPacks([]mygoshell.MobileDeclarativePack{
		{
			PluginID: "tao3k.daily-planner-pack",
			Version:  "0.1.0",
			Summary:  "Obsidian Daily Planner theme + keymap overlay (0 executable code)",
			Runtime:  schema.RuntimeDeclarative,
			Active:   true,
		},
		{
			PluginID: "tao3k.ios-personal-overlay",
			Version:  "0.1.0",
			Summary:  "Compact iOS focus layout & capture shortcuts",
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
			Title:     "Marlin Mobile Companion (390x844 Preview)",
			Width:     390,
			Height:    844,
			MinWidth:  360,
			MinHeight: 640,
			Content:   ui.View(mobile.View),
		})
		mobile.OnSendPrompt = func(prompt string) {
			mobile.Transcript.AppendUser(prompt)
			if liveTarget != nil {
				log.Printf("[mobile] queued remote turn on session=%s: %s", *sessionID, prompt)
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
