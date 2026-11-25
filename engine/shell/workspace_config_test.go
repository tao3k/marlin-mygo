// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package shell

import (
	"bytes"
	"errors"
	"testing"
)

func TestWorkspaceProfileExportImportAndFailClosedValidation(t *testing.T) {
	tree := NewWindowTree(PaneBuffer{BufferID: "slot:workbench.main", Title: "Main", Kind: "webview"})
	_, _ = tree.SplitRight(PaneBuffer{BufferID: "builtin:transcript", Title: "AI Transcript", Kind: "native-gpu"})

	exported := ExportWorkspaceProfile("coding-focus", "tokyo-night", true, "split", tree)
	raw, err := EncodeWorkspaceProfileJSON(exported)
	if err != nil {
		t.Fatalf("EncodeWorkspaceProfileJSON failed: %v", err)
	}

	decoded, err := DecodeWorkspaceProfileJSON(raw)
	if err != nil {
		t.Fatalf("DecodeWorkspaceProfileJSON failed: %v", err)
	}
	if decoded.Name != "coding-focus" || decoded.ThemeID != "tokyo-night" || !decoded.SidebarCollapsed || decoded.Layout == nil {
		t.Fatalf("unexpected decoded WorkspaceProfile: %+v", decoded)
	}

	// Re-apply imported LayoutSpec onto a fresh WindowTree and verify 2-pane split restored!
	bufMain := PaneBuffer{BufferID: "slot:workbench.main", Title: "Main", Kind: "webview"}
	bufChat := PaneBuffer{BufferID: "builtin:transcript", Title: "AI Transcript", Kind: "native-gpu"}
	catalog := map[string]PaneBuffer{
		bufMain.BufferID: bufMain,
		bufChat.BufferID: bufChat,
	}
	restored := NewWindowTree(bufMain)
	if err := ApplyLayoutSpec(restored, *decoded.Layout, catalog); err != nil {
		t.Fatalf("ApplyLayoutSpec on imported profile failed: %v", err)
	}
	if snap := restored.Snapshot(); snap.LeafCount != 2 {
		t.Fatalf("expected 2 restored split panes, got %d", snap.LeafCount)
	}

	// Fail-closed checks: unsupported schema version and >256 KiB payload.
	if _, err := DecodeWorkspaceProfileJSON([]byte(`{"schema_version":99,"name":"bad"}`)); !errors.Is(err, ErrWorkspaceProfileInvalid) {
		t.Fatalf("expected ErrWorkspaceProfileInvalid, got %v", err)
	}
	if _, err := DecodeWorkspaceProfileJSON(bytes.Repeat([]byte(" "), maxWorkspaceProfileBytes+1)); !errors.Is(err, ErrWorkspaceProfileTooBig) {
		t.Fatalf("expected ErrWorkspaceProfileTooBig, got %v", err)
	}
}
