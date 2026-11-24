// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

// Command marlin-app demonstrates assembling a complete modular Marlin shell on
// MyGo's native GPU UI with multi-target sidebar switching, Scheme UI IR
// rendering, Approval Gate overlays, and live hot-plugging of an Untrusted
// WebView Marketplace plugin (examples/plugins/ts-org-kanban).
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

type demoTarget struct{ meta transport.TargetMeta }

func (t *demoTarget) Meta() transport.TargetMeta { return t.meta }
func (t *demoTarget) Ping(context.Context) error { return nil }
func (t *demoTarget) Close() error               { return nil }

type demoDispatcher struct{}

func (demoDispatcher) SubmitScopedPanelAction(_ context.Context, req pluginhost.ScopedActionRequest) (pluginhost.ScopedActionReceipt, error) {
	return pluginhost.ScopedActionReceipt{
		Admitted:  true,
		ReceiptID: "rcpt-demo-" + req.IdempotencyKey,
		Result:    json.RawMessage(`{"status":"admitted"}`),
	}, nil
}

func main() {
	targets := transport.NewTargetRegistry()
	_ = targets.Register(&demoTarget{meta: transport.TargetMeta{
		ID: "local-mac", DisplayName: "Local Sidecar", Mode: transport.ModeLocalSidecar,
		TargetKind: schema.TargetDesktopMacOS, Address: "127.0.0.1:50051", Phase: transport.PhaseReady,
	}})
	_ = targets.Register(&demoTarget{meta: transport.TargetMeta{
		ID: "remote-linux", DisplayName: "Remote Build Box", Mode: transport.ModeRemoteGRPC,
		TargetKind: schema.TargetRemoteHeadless, Address: "10.0.0.42:50051", Phase: transport.PhaseReady,
	}})

	slots := shell.NewSlotRegistry()
	fallback := mygoshell.NewUIElementRenderer("host.main", true, slots, func() schema.UIElement {
		return schema.UIElement{
			Kind:  schema.ElemPanel,
			Title: "Host Core UI-IR Fallback (workbench.main)",
			Tone:  schema.ToneInfo,
			Children: []schema.UIElement{
				{Kind: schema.ElemText, Spans: []schema.UISpan{
					{Text: "Status: ", Tone: schema.ToneMuted},
					{Text: "Ready — click 'Mount Plugin' in the sidebar to hot-plug the Untrusted Org Kanban WebView.", Tone: schema.ToneSuccess},
				}},
			},
		}
	})
	if err := slots.Declare(schema.SlotDeclaration{
		SlotID: "workbench.main", Cardinality: schema.SlotSingle,
		FallbackRenderer: "host.main", AllowUntrustedPlugin: true,
	}, fallback); err != nil {
		log.Fatal(err)
	}

	wb := shell.NewWorkbenchController(slots)
	if err := wb.AdmitSnapshot(schema.UIWorkbenchSnapshot{
		Generation:   1,
		ProfileName:  "macos-dev",
		SessionLabel: "gui-session",
		Theme:        schema.DefaultUITheme(),
		FrameTree: schema.UIElement{
			Kind: schema.ElemRows,
			Children: []schema.UIElement{
				{Kind: schema.ElemSlot, SlotID: "workbench.main"},
			},
		},
	}); err != nil {
		log.Fatal(err)
	}

	wvRuntime := mygoshell.NewWebViewRuntime(schema.RuntimeWebViewUntrusted)
	if err := wvRuntime.RegisterProtocol(); err != nil {
		log.Fatal(err)
	}
	host := pluginhost.NewHost(slots, demoDispatcher{})
	host.RegisterRuntime(wvRuntime)

	app := mygoshell.NewWorkbenchApp(targets, slots, wb)
	var mounted atomic.Bool
	app.PluginMountedFn = mounted.Load
	app.ShowPendingGate(mygoshell.PendingGateView{
		GateID:      "gate-demo-1",
		ToolName:    "workspace.create-file",
		Prompt:      "Allow Marlin to create docs/draft.org?",
		Description: "Overlay floats above Untrusted plugin WebViews.",
	})

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "Marlin MyGo Modular Shell",
			Width:     1080,
			Height:    700,
			MinWidth:  640,
			MinHeight: 420,
			Content:   ui.View(app.View),
		})
		wvRuntime.AttachWindow(win)
		app.OnTogglePlugin = func() {
			if mounted.Load() {
				_ = host.Deactivate("tao3k.org-kanban")
				mounted.Store(false)
			} else {
				_, err := host.Activate(context.Background(), pluginhost.ScopedScope{
					PluginID:             "tao3k.org-kanban",
					ContributionID:       "kanban-panel",
					ActivationGeneration: 1,
					Runtime:              schema.RuntimeWebViewUntrusted,
					SlotID:               "workbench.main",
					ReadScopes:           []string{"org.tasks"},
					DeclaredActions:      []string{"org.task.move-state"},
					Assets:               os.DirFS("examples/plugins/ts-org-kanban"),
				})
				if err != nil {
					log.Printf("hot-plug failed: %v", err)
					return
				}
				mounted.Store(true)
			}
			win.Invalidate()
		}
		win.Invalidate()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
