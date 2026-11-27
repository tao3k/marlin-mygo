// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build !js && (marlin_feat_compare || marlin_full)

package main

import "github.com/tao3k/marlin-mygo/features/compare"

func init() {
	registerCompiledFeature(compare.NewModule(nil))
}
