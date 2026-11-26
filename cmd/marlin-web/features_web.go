// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build js && wasm

package main

import (
	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/runtime/mygoshell"
)

var webCompiledFeatures []features.FeatureModule

func registerWebFeature(mod features.FeatureModule) {
	if mod != nil {
		webCompiledFeatures = append(webCompiledFeatures, mod)
	}
}

func wireWebFeaturesAndGovernance(adaptive *mygoshell.AdaptiveShell) {
	if adaptive == nil {
		return
	}
	reg := features.NewRegistry()
	ledger := features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	type governanceAware interface {
		SetGovernanceLedger(*features.GovernanceLedger)
	}
	for _, mod := range webCompiledFeatures {
		if ga, ok := mod.(governanceAware); ok {
			ga.SetGovernanceLedger(ledger)
		}
		_ = reg.RegisterCompiled(mod)
	}
	host := features.NewHost()
	reg.Activate(features.ParseFeatureSpec("all"), ledger, host)
	govUI := mygoshell.NewGovernanceController(reg, ledger, host)
	adaptive.SetFeatureHost(host, govUI)
}
