// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/egoist/mygo"

	"github.com/tao3k/marlin-mygo/control/grpcwire"
	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/notify"
	"github.com/tao3k/marlin-mygo/engine/promptgate"
	"github.com/tao3k/marlin-mygo/engine/resilience"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

// compiledFeatureModules holds all opt-in FeatureModules linked into this
// binary via Go build tags (`marlin_feat_*` / `marlin_full`). In a minimal
// core build (`-tags marlin_minimal`), this slice stays empty with zero dead code.
var compiledFeatureModules []features.FeatureModule

func registerCompiledFeature(mod features.FeatureModule) {
	if mod != nil {
		compiledFeatureModules = append(compiledFeatureModules, mod)
	}
}

// wireFeaturesAndGovernance initializes the Rust-style Feature Registry and
// Tier-0 UI Governance Console, evaluates `-features` against GovernancePolicy,
// and mounts admitted feature buffers/commands onto WorkbenchApp.
func wireFeaturesAndGovernance(
	ctx context.Context,
	cfg appConfig,
	provider grpcwire.TargetProvider,
	app *mygoshell.WorkbenchApp,
	notifyCenter *notify.Center,
	win *mygo.Window,
) (*features.Registry, *features.GovernanceLedger, *features.Host, func()) {
	reg := features.NewRegistry()
	for _, mod := range compiledFeatureModules {
		if err := reg.RegisterCompiled(mod); err != nil {
			log.Printf("skip compiled feature registration: %v", err)
		}
	}
	ledger := features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	type governanceAware interface {
		SetGovernanceLedger(*features.GovernanceLedger)
	}
	type revisionAware interface {
		SetRevisionHandler(func(docID string, version int, instruction string))
	}
	type winnerAware interface {
		SetPromoteHandler(func(modelLabel, text string))
	}
	type resilienceAware interface {
		SetResilience(*resilience.SessionResilience, func(scopeID, text string))
	}
	type promptGateAware interface {
		SetPromptGate(*promptgate.Queue, func(gateID string))
		ApplyGateDecision(promptgate.PromptRequest, promptgate.PromptResponse)
	}
	var gateReceivers []promptGateAware
	for _, mod := range compiledFeatureModules {
		if ga, ok := mod.(governanceAware); ok {
			ga.SetGovernanceLedger(ledger)
		}
		if resAware, ok := mod.(resilienceAware); ok && app != nil && app.Transcript != nil && app.Transcript.Resilience != nil {
			resAware.SetResilience(app.Transcript.Resilience, app.Transcript.OnDraftChanged)
		}
		if pgAware, ok := mod.(promptGateAware); ok && app != nil && app.PromptGate != nil {
			pgAware.SetPromptGate(app.PromptGate.Queue, func(_ string) {
				if win != nil {
					win.Invalidate()
				}
			})
			gateReceivers = append(gateReceivers, pgAware)
		}
		if ra, ok := mod.(revisionAware); ok {
			ra.SetRevisionHandler(func(docID string, version int, instruction string) {
				if app != nil && app.OnSendPrompt != nil {
					app.OnSendPrompt(fmt.Sprintf("[canvas %s@v%d] %s", docID, version, instruction))
				}
			})
		}
		if wa, ok := mod.(winnerAware); ok {
			wa.SetPromoteHandler(func(modelLabel, text string) {
				if app != nil && app.Transcript != nil && text != "" {
					ord := app.Transcript.ActiveTurn() + 1
					app.Transcript.BeginAssistantTurn(ord)
					app.Transcript.ApplyCheckpoint(ord, fmt.Sprintf("promote-%d", ord), fmt.Sprintf("[%s promoted] %s", modelLabel, text))
					app.Transcript.CompleteTurn(ord, "PROMOTED")
				}
			})
		}
	}
	host := features.NewHost()

	sel := features.ParseFeatureSpec(cfg.featuresSpec)
	reg.Activate(sel, ledger, host)

	syncHostToApp := func() {
		if app == nil {
			return
		}
		app.FeatureHost = host
		for _, buf := range host.Buffers() {
			app.RegisterWindowBuffer(buf)
		}
		existing := make(map[string]struct{}, len(app.Commands))
		for _, c := range app.Commands {
			existing[c.Name] = struct{}{}
		}
		for _, cmd := range host.Commands() {
			if _, ok := existing[cmd.Name]; !ok {
				app.Commands = append(app.Commands, cmd)
			}
		}
	}
	syncHostToApp()

	govUI := mygoshell.NewGovernanceController(reg, ledger, host)
	var stopGovernance func()
	if provider != nil {
		rustGovAuth := grpcwire.NewRustGovernanceAuthority(grpcwire.RustGovernanceAuthorityConfig{
			Provider:  provider,
			ProjectID: "default",
			SessionID: cfg.sessionID,
			OnSnapshot: func(mygoshell.BackendGovernanceSnapshot) {
				if win != nil {
					win.Invalidate()
				}
			},
		})
		govUI.AttachBackendAuthority(rustGovAuth)
		_ = rustGovAuth.Start(ctx)
		stopGovernance = rustGovAuth.Stop
	} else {
		govUI.AttachBackendAuthority(mygoshell.NewLocalBackendAuthorityBridge(mygoshell.BackendGovernanceSnapshot{
			Admitted:         true,
			RevocationEpoch:  1,
			PolicyRevision:   1,
			BreakerTripEpoch: 1,
			LastEventNote:    "standalone preview (connect -host or marlin-scheme-host for live Rust+Scheme authority)",
		}))
	}
	govUI.OnFeatureChanged = func(id features.FeatureID, enabled bool) {
		syncHostToApp()
		if notifyCenter != nil {
			state := "disabled"
			if enabled {
				state = "enabled"
			}
			_, _ = notifyCenter.Publish(notify.NotificationSpec{
				Severity: notify.SeverityInfo,
				Source:   notify.SourceHost,
				Title:    "UI Feature Updated",
				Message:  fmt.Sprintf("Feature %q %s under Tier-0 Governance", id, state),
			})
		}
		if win != nil {
			win.Invalidate()
		}
	}
	if app != nil {
		app.GovernanceUI = govUI
		if app.PromptGate != nil {
			prevResolve := app.PromptGate.OnResolve
			app.PromptGate.OnResolve = func(req promptgate.PromptRequest, resp promptgate.PromptResponse) {
				outcome := features.AuditGateClaimed
				if resp.Action == promptgate.ActionReject || resp.Action == promptgate.ActionCancel {
					outcome = features.AuditDenied
				}
				ledger.RecordAudit("user", req.ID, string(resp.Action), outcome, resp.IdempotencyKey)
				for _, recv := range gateReceivers {
					recv.ApplyGateDecision(req, resp)
				}
				if prevResolve != nil {
					prevResolve(req, resp)
				}
			}
		}
	}
	return reg, ledger, host, stopGovernance
}
