// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build js && wasm

// Command marlin-web compiles the exact same Marlin AdaptiveShell library
// (Emacs composable WindowTree + 34-token 5-theme Derivation Engine +
// iOS/Android compact mobile companion) into a static-deployable WebAssembly
// frontend (`GOOS=js GOARCH=wasm`).
package main

import (
	"context"
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/tao3k/marlin-mygo/control/auth"
	"github.com/tao3k/marlin-mygo/control/transport"
	"github.com/tao3k/marlin-mygo/engine/composer"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

type webDemoTarget struct{ meta transport.TargetMeta }

func (w *webDemoTarget) Meta() transport.TargetMeta { return w.meta }
func (w *webDemoTarget) Ping(context.Context) error { return nil }
func (w *webDemoTarget) Close() error               { return nil }

func main() {
	targets := transport.NewTargetRegistry()
	_ = targets.Register(&webDemoTarget{meta: transport.TargetMeta{
		ID:          "web-remote-core",
		DisplayName: "Marlin Cloud Host",
		Mode:        transport.ModeRemoteGRPC,
		TargetKind:  schema.TargetRemoteHeadless,
		Address:     "wss://host.marlin.local",
		Phase:       transport.PhaseReady,
	}})

	slots := shell.NewSlotRegistry()
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID:               "workbench.main",
		Cardinality:          schema.SlotSingle,
		FallbackRenderer:     "web.main",
		AllowUntrustedPlugin: true,
	}, mygoshell.NewUIElementRenderer("web.main", true, slots, func() schema.UIElement {
		return schema.UIElement{
			Kind:  schema.ElemPanel,
			Title: "Marlin Web Workspace (Go WASM · Shared AdaptiveShell Library)",
			Tone:  schema.ToneAccent,
			Children: []schema.UIElement{
				{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Same Emacs WindowTree, DerivedTheme Engine, and Responsive Phone Mode compiled to WebAssembly.", Tone: schema.ToneInfo}}},
				{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Resize browser below 720px to switch into Mobile Companion mode, or click Focus / Split / Cockpit / Zen.", Tone: schema.ToneMuted}}},
			},
		}
	}))
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID:               "workbench.secondary",
		Cardinality:          schema.SlotSingle,
		FallbackRenderer:     "web.secondary",
		AllowUntrustedPlugin: true,
	}, mygoshell.NewUIElementRenderer("web.secondary", true, slots, func() schema.UIElement {
		return schema.UIElement{
			Kind:  schema.ElemPanel,
			Title: "Composable Secondary Slot (WebAssembly)",
			Tone:  schema.ToneInfo,
			Children: []schema.UIElement{
				{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "Use Marketplace -> Layouts -> Git + GitHub Composable Studio or SPC b + to stack tabs.", Tone: schema.ToneMuted}}},
			},
		}
	}))
	_ = slots.Declare(schema.SlotDeclaration{
		SlotID:           "workbench.inspector",
		Cardinality:      schema.SlotSingle,
		FallbackRenderer: "web.dock",
	}, mygoshell.NewUIElementRenderer("web.dock", true, slots, func() schema.UIElement {
		return schema.UIElement{
			Kind:  schema.ElemPanel,
			Title: "Focus Clock Dock",
			Tone:  schema.ToneSuccess,
			Children: []schema.UIElement{
				{Kind: schema.ElemText, Spans: []schema.UISpan{{Text: "25:00 Deep Work · 0 MB WebView overhead", Tone: schema.ToneSuccess}}},
			},
		}
	}))

	proj := mygoshell.NewProjectionController(mygoshell.ProjectionSnapshot{
		GraphNodes: []mygoshell.GraphNodeReceiptView{
			{
				NodeID:      "node-web-admit",
				Kind:        "readiness-gate",
				Phase:       "succeeded",
				DurationMS:  3,
				ReceiptID:   "rcpt-web-001",
				GatewayNote: "no-live-llm denial receipt preserved",
				Summary:     "AdaptiveShell compiled for GOOS=js GOARCH=wasm",
			},
		},
		DiffFiles: []mygoshell.WorkspaceDiffFileView{
			{
				Path:      "runtime/mygoshell/adaptive_shell.go",
				Status:    "added",
				Additions: 190,
				Deletions: 0,
				HunkLines: []string{"+type AdaptiveShell struct { Desktop *WorkbenchApp; Mobile *MobileCompanionApp }"},
			},
		},
		Processes: []mygoshell.ManagedProcessView{
			{
				ProcessID: "proc-wasm",
				Command:   "marlin-web.wasm",
				State:     "running",
				ExitCode:  0,
				UptimeSec: 12,
				TailLines: []string{"WebGL2 / Canvas adaptive UI active"},
			},
		},
	})

	webAuthMgr := auth.NewManager(auth.ManagerConfig{})
	_ = webAuthMgr.RegisterProvider(auth.GoogleOAuthProvider("marlin-web-client"))
	_ = webAuthMgr.RegisterProvider(auth.GitHubOAuthProvider("marlin-web-client"))
	_ = webAuthMgr.RegisterProvider(auth.EmailOTPProvider())
	webAuth := mygoshell.NewAuthController(webAuthMgr)

	adaptive := mygoshell.NewAdaptiveShell(mygoshell.AdaptiveShellConfig{
		BrandTitle:     "Marlin Web",
		BrandSubtitle:  "WASM · Desktop / iOS / Android",
		InitialThemeID: "midnight",
		FormFactor:     mygoshell.FormFactorAuto,
		Targets:        targets,
		Slots:          slots,
		Projection:     proj,
		Auth:           webAuth,
	})
	adaptive.Desktop.Transcript.AppendUser("Inspect unified layout, stacked tabs, multimodal composer, and theme tokens on Web deploy.")
	adaptive.Desktop.Transcript.SetContextUsage(0, 200_000)
	_, _ = adaptive.Desktop.Transcript.AttachFile(composer.AttachmentInput{
		Filename: "adaptive_shell.go",
		Data:     []byte("package mygoshell\n// Shared Desktop, iOS, Android, and Web WASM shell\n"),
	})
	adaptive.Desktop.Transcript.OnModelChanged = func(m composer.ModelOption) {
		winToks := composer.ParseContextWindowTokens(m.ContextLabel)
		if winToks <= 0 {
			winToks = 200_000
		}
		used := adaptive.Desktop.Transcript.ContextUsage().UsedTokens
		adaptive.Desktop.Transcript.SetContextUsage(used, winToks)
	}
	adaptive.Desktop.Transcript.OnSlashCommand = func(cmd composer.SlashCommand) {
		switch cmd.ActionID {
		case "studio.open-auth":
			adaptive.Desktop.OpenAuth()
		case "studio.open-keymap-inspector":
			adaptive.Desktop.OpenKeymapInspector()
		case "studio.open-notifications":
			adaptive.Desktop.OpenActivityCenter()
		case "studio.open-marketplace":
			adaptive.Desktop.OpenMarketplace()
		case "studio.open-federation":
			adaptive.Desktop.OpenFederation()
		case "studio.cycle-theme":
			adaptive.CycleTheme()
		}
	}
	adaptive.Desktop.Transcript.OnSubmitTurn = func(sub composer.TurnSubmission) {
		refs := make([]composer.AttachmentRef, 0, len(sub.Attachments))
		for _, att := range sub.Attachments {
			refs = append(refs, att.Ref)
		}
		adaptive.Desktop.Transcript.AppendUserWithAttachments(sub.Prompt, refs)
		cur := adaptive.Desktop.Transcript.ContextUsage()
		if cur.MaxTokens <= 0 {
			cur.MaxTokens = 200_000
		}
		adaptive.Desktop.Transcript.SetContextUsage(cur.UsedTokens+composer.EstimateTokenCount(sub.Prompt), cur.MaxTokens)
	}
	adaptive.Desktop.ActiveSessionID = "web-session-1"
	adaptive.Desktop.Sessions = []mygoshell.SessionSummaryView{
		{SessionID: "web-session-1", Title: "Web Adaptive Session", TurnCount: 1},
	}
	_, _ = adaptive.Windows.OpenTabInFocused(shell.PaneBuffer{
		BufferID: "builtin:projection",
		Title:    "Projection",
		Kind:     "native-gpu",
	})
	adaptive.Mobile.SetPendingGates([]mygoshell.PendingGateView{
		{
			GateID:      "gate-web-1",
			ToolName:    "device.ios.notify.send",
			Prompt:      "Approve reminder notification?",
			Description: "Tier-0 gate synchronized across Web, Desktop, and Mobile.",
		},
	})

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:   "Marlin Studio Web (WASM)",
			Width:   1280,
			Height:  820,
			Content: ui.View(adaptive.View),
		})
		wireWebMultimodalCapture(adaptive.Desktop.Transcript, win)
		win.Invalidate()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
