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

func TestInitPluginKeygenPackAndVerifyEndToEnd(t *testing.T) {
	tmp := t.TempDir()
	for _, rt := range []string{"ts", "go-wasm", "declarative"} {
		dir := filepath.Join(tmp, rt)
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
		if !strings.Contains(string(raw), `activation = "on-slot"`) || !strings.Contains(string(raw), `idle_unload_sec = 300`) {
			t.Fatalf("manifest missing Emacs lazy activation defaults for %s: %s", rt, string(raw))
		}
		if _, err := os.Stat(filepath.Join(dir, "AGENT.md")); err != nil {
			t.Fatalf("missing embedded AGENT.md contract card for %s: %v", rt, err)
		}
	}

	// End-to-end doctor + keygen -> publish -> verify on scaffolded TS and Declarative plugins.
	if err := runDoctor([]string{"-dir", filepath.Join(tmp, "ts"), "-json"}); err != nil {
		t.Fatalf("doctor failed on scaffolded ts plugin: %v", err)
	}
	if err := runDoctor([]string{"-dir", filepath.Join(tmp, "declarative")}); err != nil {
		t.Fatalf("doctor failed on scaffolded declarative plugin: %v", err)
	}

	keyPrefix := filepath.Join(tmp, "keys", "acme")
	if err := runKeygen([]string{"-out", keyPrefix}); err != nil {
		t.Fatalf("keygen failed: %v", err)
	}
	catOut := filepath.Join(tmp, "dist-catalog")
	if err := runPublish([]string{
		"-dir", filepath.Join(tmp, "ts"),
		"-publisher", "acme.dev",
		"-key", keyPrefix + ".key",
		"-pub", keyPrefix + ".pub",
		"-out", catOut,
	}); err != nil {
		t.Fatalf("publish ts failed: %v", err)
	}
	if err := runPublish([]string{
		"-dir", filepath.Join(tmp, "declarative"),
		"-publisher", "acme.dev",
		"-key", keyPrefix + ".key",
		"-pub", keyPrefix + ".pub",
		"-out", catOut,
	}); err != nil {
		t.Fatalf("publish declarative failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(catOut, "catalog.json.sig")); err != nil {
		t.Fatalf("expected signed catalog.json.sig after publish: %v", err)
	}

	pkgPath := filepath.Join(tmp, "acme-demo-ts.marlinpkg")
	if err := runPack([]string{
		"-dir", filepath.Join(tmp, "ts"),
		"-publisher", "acme.dev",
		"-key", keyPrefix + ".key",
		"-out", pkgPath,
	}); err != nil {
		t.Fatalf("pack failed: %v", err)
	}
	if err := runVerify([]string{
		"-pkg", pkgPath,
		"-publisher", "acme.dev",
		"-pub", keyPrefix + ".pub",
	}); err != nil {
		t.Fatalf("verify failed: %v", err)
	}

	if err := runInitPlugin([]string{"-id", "unnamespaced", "-out", filepath.Join(tmp, "bad1")}); err == nil {
		t.Fatal("expected error for non-namespaced plugin ID")
	}
	if err := runInitPlugin([]string{"-id", "acme.evil", "-slot", "approval.gate", "-out", filepath.Join(tmp, "bad2")}); err == nil {
		t.Fatal("expected error when targeting host-restricted approval.gate slot")
	}
}
