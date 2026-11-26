// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package transport

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFakeExec(t *testing.T, path string, executable bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0o644)
	if executable {
		mode = 0o755
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverAppServerBinaryPrecedence(t *testing.T) {
	root := t.TempDir()
	exeDir := filepath.Join(root, "bundle")
	workSubDir := filepath.Join(root, "repo", ".data", "marlin-mygo")
	if err := os.MkdirAll(workSubDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Workspace target binaries (release + debug, canonical + legacy).
	wsDebugLegacy := filepath.Join(root, "repo", "target", "debug", "marlin-mygo-host")
	wsReleaseCanonical := filepath.Join(root, "repo", "target", "release", "marlin-app-server")
	writeFakeExec(t, wsDebugLegacy, true)

	noEnv := func(string) string { return "" }
	noPath := func(string) (string, error) { return "", errors.New("not in path") }

	// 1. Discovers ancestor workspace target/debug/marlin-mygo-host when nothing else exists.
	got, src, ok := DiscoverAppServerBinary(SidecarDiscoverOptions{
		ExeDir:     exeDir,
		WorkDir:    workSubDir,
		EnvLookup:  noEnv,
		PathLookup: noPath,
	})
	if !ok || got != wsDebugLegacy || src != "workspace-target-debug" {
		t.Fatalf("expected debug legacy discovery %q, got path=%q src=%q ok=%v", wsDebugLegacy, got, src, ok)
	}

	// 2. Workspace target/release/marlin-app-server beats debug legacy.
	writeFakeExec(t, wsReleaseCanonical, true)
	got, src, ok = DiscoverAppServerBinary(SidecarDiscoverOptions{
		ExeDir:     exeDir,
		WorkDir:    workSubDir,
		EnvLookup:  noEnv,
		PathLookup: noPath,
	})
	if !ok || got != wsReleaseCanonical || src != "workspace-target-release" {
		t.Fatalf("expected release canonical discovery %q, got path=%q src=%q ok=%v", wsReleaseCanonical, got, src, ok)
	}

	// 3. PATH lookup beats workspace target.
	pathBin := filepath.Join(root, "usr-bin", "marlin-app-server")
	writeFakeExec(t, pathBin, true)
	got, src, ok = DiscoverAppServerBinary(SidecarDiscoverOptions{
		ExeDir:    exeDir,
		WorkDir:   workSubDir,
		EnvLookup: noEnv,
		PathLookup: func(name string) (string, error) {
			if name == "marlin-app-server" {
				return pathBin, nil
			}
			return "", errors.New("not found")
		},
	})
	if !ok || got != pathBin || src != "path" {
		t.Fatalf("expected PATH discovery %q, got path=%q src=%q ok=%v", pathBin, got, src, ok)
	}

	// 4. Bundle sibling marlin-app-server beats sibling marlin-mygo-host and PATH.
	sibLegacy := filepath.Join(exeDir, "marlin-mygo-host")
	sibCanonical := filepath.Join(exeDir, "marlin-app-server")
	writeFakeExec(t, sibLegacy, true)
	writeFakeExec(t, sibCanonical, true)
	got, src, ok = DiscoverAppServerBinary(SidecarDiscoverOptions{
		ExeDir:    exeDir,
		WorkDir:   workSubDir,
		EnvLookup: noEnv,
		PathLookup: func(string) (string, error) {
			return pathBin, nil
		},
	})
	if !ok || got != sibCanonical || src != "bundle-sibling" {
		t.Fatalf("expected bundle sibling canonical %q, got path=%q src=%q ok=%v", sibCanonical, got, src, ok)
	}

	// 5. Env MARLIN_APP_SERVER_BIN beats MARLIN_RUST_SIDECAR_BIN and bundle sibling.
	envLegacy := filepath.Join(root, "env", "legacy")
	envCanonical := filepath.Join(root, "env", "canonical")
	writeFakeExec(t, envLegacy, true)
	writeFakeExec(t, envCanonical, true)
	got, src, ok = DiscoverAppServerBinary(SidecarDiscoverOptions{
		ExeDir:  exeDir,
		WorkDir: workSubDir,
		EnvLookup: func(k string) string {
			switch k {
			case "MARLIN_APP_SERVER_BIN":
				return envCanonical
			case "MARLIN_RUST_SIDECAR_BIN":
				return envLegacy
			default:
				return ""
			}
		},
		PathLookup: noPath,
	})
	if !ok || got != envCanonical || src != "env:MARLIN_APP_SERVER_BIN" {
		t.Fatalf("expected env canonical %q, got path=%q src=%q ok=%v", envCanonical, got, src, ok)
	}

	// 6. Explicit CLI flag beats everything, and rejects non-executable files.
	explicitExec := filepath.Join(root, "explicit", "marlin-app-server")
	explicitNonExec := filepath.Join(root, "explicit", "not-exec")
	writeFakeExec(t, explicitExec, true)
	writeFakeExec(t, explicitNonExec, false)

	got, src, ok = DiscoverAppServerBinary(SidecarDiscoverOptions{
		ExplicitPath: explicitExec,
		ExeDir:       exeDir,
		WorkDir:      workSubDir,
		EnvLookup:    func(string) string { return envCanonical },
		PathLookup:   noPath,
	})
	if !ok || got != explicitExec || src != "flag" {
		t.Fatalf("expected explicit flag %q, got path=%q src=%q ok=%v", explicitExec, got, src, ok)
	}
	if _, _, ok := DiscoverAppServerBinary(SidecarDiscoverOptions{ExplicitPath: explicitNonExec}); ok {
		t.Fatal("expected non-executable explicit binary to be rejected")
	}
}

func TestDiscoverSchemeHostBinaryPrefersLinkedAOT(t *testing.T) {
	root := t.TempDir()
	workSubDir := filepath.Join(root, "repo", ".data", "marlin-mygo")
	if err := os.MkdirAll(workSubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	noEnv := func(string) string { return "" }
	noPath := func(string) (string, error) { return "", errors.New("not in path") }

	// Linked-AOT `marlin-scheme-host-aot` MUST NOT hijack full-session `DiscoverAppServerBinary`.
	aotBin := filepath.Join(root, "repo", "target", "debug", "marlin-scheme-host-aot")
	dynBin := filepath.Join(root, "repo", "target", "debug", "marlin-scheme-host")
	writeFakeExec(t, dynBin, true)
	writeFakeExec(t, aotBin, true)

	if got, _, ok := DiscoverAppServerBinary(SidecarDiscoverOptions{
		ExeDir:     filepath.Join(root, "empty"),
		WorkDir:    workSubDir,
		EnvLookup:  noEnv,
		PathLookup: noPath,
	}); ok {
		t.Fatalf("expected DiscoverAppServerBinary to ignore scheme-host-only binaries, got %q", got)
	}

	got, src, ok := DiscoverSchemeHostBinary(SidecarDiscoverOptions{
		ExeDir:     filepath.Join(root, "empty"),
		WorkDir:    workSubDir,
		EnvLookup:  noEnv,
		PathLookup: noPath,
	})
	if !ok || got != aotBin || src != "workspace-target-debug" {
		t.Fatalf("expected DiscoverSchemeHostBinary to prefer marlin-scheme-host-aot %q, got path=%q src=%q ok=%v", aotBin, got, src, ok)
	}
}

func TestDiscoverSchemeHostBinaryMacOSAppAndReleaseBundleSibling(t *testing.T) {
	root := t.TempDir()
	workSubDir := filepath.Join(root, "repo", ".data", "marlin-mygo")
	wsReleaseAOT := filepath.Join(root, "repo", "target", "release", "marlin-scheme-host-aot")
	writeFakeExec(t, wsReleaseAOT, true)

	noEnv := func(string) string { return "" }
	noPath := func(string) (string, error) { return "", errors.New("not in path") }

	// 1. macOS Marlin.app/Contents/MacOS sibling beats workspace target/release.
	macOSBundleDir := filepath.Join(root, "dist", "Marlin.app", "Contents", "MacOS")
	macOSBundleAOT := filepath.Join(macOSBundleDir, "marlin-scheme-host-aot")
	writeFakeExec(t, macOSBundleAOT, true)
	got, src, ok := DiscoverSchemeHostBinary(SidecarDiscoverOptions{
		ExeDir:     macOSBundleDir,
		WorkDir:    workSubDir,
		EnvLookup:  noEnv,
		PathLookup: noPath,
	})
	if !ok || got != macOSBundleAOT || src != "bundle-sibling" {
		t.Fatalf("expected macOS .app sibling %q (bundle-sibling), got path=%q src=%q ok=%v", macOSBundleAOT, got, src, ok)
	}

	// 2. Desktop release archive sibling (`marlin-mygo-darwin-arm64/marlin-scheme-host-aot`) beats workspace target/release.
	archiveDir := filepath.Join(root, "dist", "marlin-mygo-darwin-arm64")
	archiveAOT := filepath.Join(archiveDir, "marlin-scheme-host-aot")
	writeFakeExec(t, archiveAOT, true)
	got, src, ok = DiscoverSchemeHostBinary(SidecarDiscoverOptions{
		ExeDir:     archiveDir,
		WorkDir:    workSubDir,
		EnvLookup:  noEnv,
		PathLookup: noPath,
	})
	if !ok || got != archiveAOT || src != "bundle-sibling" {
		t.Fatalf("expected release archive sibling %q (bundle-sibling), got path=%q src=%q ok=%v", archiveAOT, got, src, ok)
	}
}
