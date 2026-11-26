// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js

package main

import (
	"fmt"
	"log"

	"github.com/egoist/mygo"

	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/notify"
	"github.com/tao3k/marlin-mygo/engine/promptgate"
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
	cfg appConfig,
	app *mygoshell.WorkbenchApp,
	notifyCenter *notify.Center,
	win *mygo.Window,
) (*features.Registry, *features.GovernanceLedger, *features.Host) {
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
	for _, mod := range compiledFeatureModules {
		if ga, ok := mod.(governanceAware); ok {
			ga.SetGovernanceLedger(ledger)
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
				if prevResolve != nil {
					prevResolve(req, resp)
				}
			}
		}
	}
	return reg, ledger, host
}
