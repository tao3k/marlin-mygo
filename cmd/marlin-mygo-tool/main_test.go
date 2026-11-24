// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalProtosAndQuintSpecExistOnDisk(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, rel := range canonicalProtos {
		p := filepath.Join(root, rel)
		if info, err := os.Stat(p); err != nil || info.Size() == 0 {
			t.Fatalf("expected non-empty canonical proto %s: err=%v", p, err)
		}
	}
	qnt := filepath.Join(root, "spec", "quint", "frontend-security.qnt")
	if info, err := os.Stat(qnt); err != nil || info.Size() == 0 {
		t.Fatalf("expected non-empty quint spec %s: err=%v", qnt, err)
	}
}

func TestInitPluginScaffoldsAllRuntimesAndBlocksRestrictedSlots(t *testing.T) {
	for _, rt := range []string{"ts", "go-wasm", "declarative"} {
		dir := filepath.Join(t.TempDir(), rt)
		if err := runInitPlugin([]string{"-id", "acme.demo-" + rt, "-runtime", rt, "-slot", "workbench.main", "-out", dir}); err != nil {
			t.Fatalf("init-plugin %s failed: %v", rt, err)
		}
		raw, err := os.ReadFile(filepath.Join(dir, "marlin-plugin.toml"))
		if err != nil {
			t.Fatalf("missing marlin-plugin.toml for %s: %v", rt, err)
		}
		if !strings.Contains(string(raw), `id = "acme.demo-`+rt+`"`) {
			t.Fatalf("manifest missing plugin id: %s", string(raw))
		}
	}

	if err := runInitPlugin([]string{"-id", "unnamespaced", "-out", t.TempDir()}); err == nil {
		t.Fatal("expected error for non-namespaced plugin ID")
	}
	if err := runInitPlugin([]string{"-id", "acme.evil", "-slot", "approval.gate", "-out", t.TempDir()}); err == nil {
		t.Fatal("expected error when targeting host-restricted approval.gate slot")
	}
}
