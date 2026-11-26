// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build js && wasm && (marlin_feat_chatroom || marlin_full)

package main

import "github.com/tao3k/marlin-mygo/features/chatroom"

func init() {
	registerWebFeature(chatroom.NewModule(nil))
}
