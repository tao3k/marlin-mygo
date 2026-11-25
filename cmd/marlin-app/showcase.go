// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"

	"github.com/tao3k/marlin-mygo/control/marketplace"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

var showcaseExampleDirs = []string{
	filepath.Join("examples", "plugins", "ts-org-agenda-planner"),
	filepath.Join("examples", "plugins", "react-ai-memory-inbox"),
	filepath.Join("examples", "plugins", "react-ai-workflow-cockpit"),
	filepath.Join("examples", "plugins", "go-focus-clock-reactor"),
	filepath.Join("examples", "plugins", "declarative-daily-planner-pack"),
}

func resolveShowcasePluginID(alias string) schema.PluginID {
	switch alias {
	case "memory", "inbox", "tao3k.ai-memory-inbox":
		return "tao3k.ai-memory-inbox"
	case "workflow", "cockpit", "tao3k.ai-workflow-cockpit":
		return "tao3k.ai-workflow-cockpit"
	default:
		return "tao3k.org-agenda-planner"
	}
}

// prepareShowcaseCatalog compiles, audits, signs, and packages the flagship
// React 19 + Go WASI + Declarative plugins into an ephemeral Marketplace dir
// so `just showcase` opens a live multi-runtime desktop session in one command.
func prepareShowcaseCatalog(ctx context.Context) (catalogDir, pubKeyFile string, cleanup func(), err error) {
	noop := func() {}
	tmpDir, err := os.MkdirTemp("", "marlin-showcase-catalog-*")
	if err != nil {
		return "", "", noop, err
	}
	clean := func() { _ = os.RemoveAll(tmpDir) }

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		clean()
		return "", "", noop, err
	}
	pubPath := filepath.Join(tmpDir, "publisher.pub")
	if err := os.WriteFile(pubPath, []byte(hex.EncodeToString(pub)+"\n"), 0o644); err != nil {
		clean()
		return "", "", noop, err
	}

	var rev [20]byte
	log.Printf("[showcase] certifying & signing %d example plugins (React 19 + Go WASI + Declarative)...", len(showcaseExampleDirs))
	for _, dir := range showcaseExampleDirs {
		rcpt, pubErr := marketplace.PublishPlugin(ctx, marketplace.PublishConfig{
			PluginDir:       dir,
			PublisherID:     "tao3k.official",
			PrivateKey:      priv,
			PublicKey:       pub,
			SourceRev:       rev,
			OutCatalogDir:   tmpDir,
			ReplaceExisting: true,
		})
		if pubErr != nil {
			clean()
			return "", "", noop, fmt.Errorf("publish showcase plugin %s: %w", dir, pubErr)
		}
		log.Printf("[showcase] ready: %-28s (%s, %d bytes)", rcpt.PluginID, rcpt.Runtime, rcpt.PackageBytes)
	}
	return tmpDir, pubPath, clean, nil
}

// showcaseDispatcher wires live bidirectional cross-runtime linkage during
// `-showcase`: actions submitted by a React 19 WebView (e.g. `org.clock.in`,
// `org.capture.plan`, `workflow.gate.focus`) immediately update the Go WASI GPU
// Focus Clock reactor or open the Tier-0 Host Approval Gate floating overlay!
type showcaseDispatcher struct {
	mu           sync.Mutex
	host         *pluginhost.Host
	app          *mygoshell.WorkbenchApp
	win          *mygo.Window
	clockRunning bool
	activeTitle  string
	evSeq        atomic.Uint64
}

func (d *showcaseDispatcher) attach(host *pluginhost.Host, app *mygoshell.WorkbenchApp, win *mygo.Window) {
	d.mu.Lock()
	d.host = host
	d.app = app
	d.win = win
	d.mu.Unlock()
}

func (d *showcaseDispatcher) publishTopic(topic string, payload any) {
	d.mu.Lock()
	h, w := d.host, d.win
	d.mu.Unlock()
	if h == nil {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	evID := fmt.Sprintf("showcase-%d", d.evSeq.Add(1))
	_, _ = h.PublishTopic(evID, topic, raw)
	if w != nil {
		w.Invalidate()
	}
}

func (d *showcaseDispatcher) SubmitScopedPanelAction(_ context.Context, req pluginhost.ScopedActionRequest) (pluginhost.ScopedActionReceipt, error) {
	var body map[string]any
	_ = json.Unmarshal(req.Args, &body)
	title, _ := body["title"].(string)

	switch req.ActionName {
	case "org.clock.in":
		if title == "" {
			title = "Deep Work Block"
		}
		d.mu.Lock()
		d.clockRunning = true
		d.activeTitle = title
		d.mu.Unlock()
		go d.publishTopic("org.clock", map[string]any{
			"schemaVersion": 1,
			"activeTitle":   title,
			"quickNote":     "Clocked in: " + title,
		})

	case "org.clock.out":
		d.mu.Lock()
		d.clockRunning = false
		if title == "" {
			title = d.activeTitle
		}
		d.mu.Unlock()
		go d.publishTopic("org.clock", map[string]any{
			"schemaVersion": 1,
			"quickNote":     "Clocked out: " + title,
		})

	case "org.capture.plan":
		if title == "" {
			title = "Captured org entry"
		}
		go d.publishTopic("org.clock", map[string]any{
			"schemaVersion": 1,
			"quickNote":     "Captured: " + title,
		})

	case "agent.workflow.plan-day":
		go d.publishTopic("org.clock", map[string]any{
			"schemaVersion": 1,
			"quickNote":     "AI Daily Plan scheduled",
		})

	case "workflow.gate.focus":
		gateID, _ := body["gateId"].(string)
		if gateID == "" {
			gateID = "gate-demo-1"
		}
		d.mu.Lock()
		a, w := d.app, d.win
		d.mu.Unlock()
		if a != nil {
			a.ShowPendingGate(mygoshell.PendingGateView{
				GateID:      gateID,
				ToolName:    "device.ios.notify.send",
				Prompt:      "Approve AI Workflow dispatch to iPhone Personal?",
				Description: "Tier-0 host.gate modal floats cleanly above the React 19 WKWebView.",
			})
			if w != nil {
				w.Invalidate()
			}
		}
	}

	return pluginhost.ScopedActionReceipt{
		Admitted:  true,
		ReceiptID: fmt.Sprintf("rcpt-showcase-%d", d.evSeq.Add(1)),
		Result:    json.RawMessage(`{"status":"admitted"}`),
	}, nil
}

// startTickLoop ticks the Go WASI GPU Focus Clock reactor once per second while
// a focus block is active so live countdown updates are visible in real time.
func (d *showcaseDispatcher) startTickLoop(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.mu.Lock()
				running := d.clockRunning
				d.mu.Unlock()
				if running {
					d.publishTopic("org.clock", map[string]any{
						"schemaVersion": 1,
						"tickSeconds":   1,
					})
				}
			}
		}
	}()
}
