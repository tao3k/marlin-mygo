// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build js && wasm

package main

import (
	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/promptgate"
	"github.com/tao3k/marlin-mygo/engine/resilience"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

var webCompiledFeatures []features.FeatureModule

func registerWebFeature(mod features.FeatureModule) {
	if mod != nil {
		webCompiledFeatures = append(webCompiledFeatures, mod)
	}
}

func wireWebFeaturesAndGovernance(adaptive *mygoshell.AdaptiveShell, onDraftChanged func(scopeID, text string)) {
	if adaptive == nil {
		return
	}
	reg := features.NewRegistry()
	ledger := features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	type governanceAware interface {
		SetGovernanceLedger(*features.GovernanceLedger)
	}
	type resilienceAware interface {
		SetResilience(*resilience.SessionResilience, func(scopeID, text string))
	}
	type promptGateAware interface {
		SetPromptGate(*promptgate.Queue, func(gateID string))
		ApplyGateDecision(promptgate.PromptRequest, promptgate.PromptResponse)
	}
	var gateReceivers []promptGateAware
	var promptGate *mygoshell.PromptGateController
	if adaptive.Desktop != nil {
		promptGate = adaptive.Desktop.PromptGate
	}
	for _, mod := range webCompiledFeatures {
		if ga, ok := mod.(governanceAware); ok {
			ga.SetGovernanceLedger(ledger)
		}
		if resAware, ok := mod.(resilienceAware); ok && adaptive.Desktop != nil && adaptive.Desktop.Transcript != nil && adaptive.Desktop.Transcript.Resilience != nil {
			resAware.SetResilience(adaptive.Desktop.Transcript.Resilience, onDraftChanged)
		}
		if pgAware, ok := mod.(promptGateAware); ok && promptGate != nil {
			pgAware.SetPromptGate(promptGate.Queue, nil)
			gateReceivers = append(gateReceivers, pgAware)
		}
		_ = reg.RegisterCompiled(mod)
	}
	if promptGate != nil && len(gateReceivers) > 0 {
		prevResolve := promptGate.OnResolve
		promptGate.OnResolve = func(req promptgate.PromptRequest, resp promptgate.PromptResponse) {
			for _, recv := range gateReceivers {
				recv.ApplyGateDecision(req, resp)
			}
			if prevResolve != nil {
				prevResolve(req, resp)
			}
		}
	}
	host := features.NewHost()
	reg.Activate(features.ParseFeatureSpec("all"), ledger, host)
	govUI := mygoshell.NewGovernanceController(reg, ledger, host)
	adaptive.SetFeatureHost(host, govUI)
}
