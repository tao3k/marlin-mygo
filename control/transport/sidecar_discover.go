// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package transport

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultAppServerBinaryNames lists the Rust App Server sidecar executable names
// in long-term preference order: client-neutral `marlin-app-server` first, then
// the standalone Linked-AOT `marlin-scheme-host-aot` authority daemon, with
// automatic fallback to the legacy `marlin-mygo-host` binary name.
var DefaultAppServerBinaryNames = []string{
	"marlin-app-server",
	"marlin-scheme-host-aot",
	"marlin-mygo-host",
}

// SidecarDiscoverOptions controls deterministic discovery of the local Rust
// Marlin App Server sidecar binary when the desktop UI boots.
type SidecarDiscoverOptions struct {
	ExplicitPath string   // explicit CLI flag (-app-server / -sidecar)
	ExeDir       string   // directory containing the running UI binary (defaults to os.Executable dir)
	WorkDir      string   // working directory for dev target/ lookup (defaults to os.Getwd)
	BinaryNames  []string // defaults to DefaultAppServerBinaryNames
	MaxWalkUp    int      // ancestor directories to scan for target/{release,debug} (default 5)
	EnvLookup    func(string) string
	PathLookup   func(string) (string, error)
}

// DiscoverAppServerBinary locates the local Rust App Server executable in
// deterministic priority order:
//  1. Explicit CLI path (-app-server / -sidecar)
//  2. Environment override: MARLIN_APP_SERVER_BIN, then MARLIN_RUST_SIDECAR_BIN
//  3. Sibling executable next to the running MyGo UI binary (app bundle / release layout)
//  4. System PATH (exec.LookPath)
//  5. Local Cargo workspace build output (target/release then target/debug)
//
// Returns (resolvedPath, discoverySource, found).
func DiscoverAppServerBinary(opts SidecarDiscoverOptions) (string, string, bool) {
	if p := strings.TrimSpace(opts.ExplicitPath); p != "" {
		if resolved, ok := validateExecutableFile(p); ok {
			return resolved, "flag", true
		}
		return p, "flag", false
	}

	envLookup := opts.EnvLookup
	if envLookup == nil {
		envLookup = os.Getenv
	}
	for _, envKey := range []string{"MARLIN_APP_SERVER_BIN", "MARLIN_RUST_SIDECAR_BIN"} {
		if p := strings.TrimSpace(envLookup(envKey)); p != "" {
			if resolved, ok := validateExecutableFile(p); ok {
				return resolved, "env:" + envKey, true
			}
		}
	}

	names := opts.BinaryNames
	if len(names) == 0 {
		names = DefaultAppServerBinaryNames
	}
	platformNames := make([]string, 0, len(names))
	for _, n := range names {
		if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(n), ".exe") {
			platformNames = append(platformNames, n+".exe")
		} else {
			platformNames = append(platformNames, n)
		}
	}

	exeDir := strings.TrimSpace(opts.ExeDir)
	if exeDir == "" {
		if exePath, err := os.Executable(); err == nil {
			exeDir = filepath.Dir(exePath)
		}
	}
	if exeDir != "" {
		for _, name := range platformNames {
			if resolved, ok := validateExecutableFile(filepath.Join(exeDir, name)); ok {
				return resolved, "bundle-sibling", true
			}
		}
	}

	pathLookup := opts.PathLookup
	if pathLookup == nil {
		pathLookup = exec.LookPath
	}
	for _, name := range platformNames {
		if found, err := pathLookup(name); err == nil {
			if resolved, ok := validateExecutableFile(found); ok {
				return resolved, "path", true
			}
		}
	}

	workDir := strings.TrimSpace(opts.WorkDir)
	if workDir == "" {
		if wd, err := os.Getwd(); err == nil {
			workDir = wd
		}
	}
	maxUp := opts.MaxWalkUp
	if maxUp <= 0 {
		maxUp = 5
	}
	if workDir != "" {
		dir := workDir
		for depth := 0; depth <= maxUp; depth++ {
			for _, profile := range []string{"release", "debug"} {
				for _, name := range platformNames {
					cand := filepath.Join(dir, "target", profile, name)
					if resolved, ok := validateExecutableFile(cand); ok {
						return resolved, "workspace-target-" + profile, true
					}
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	return "", "", false
}

func validateExecutableFile(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", false
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path, true
	}
	return abs, true
}
