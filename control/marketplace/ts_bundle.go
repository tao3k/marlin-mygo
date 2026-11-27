// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package marketplace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var tsScriptSrcRe = regexp.MustCompile(`(?i)<script\b([^>]*?)\bsrc=["']([^"']+\.tsx?)["']([^>]*)>\s*</script>`)

// ErrBunNotFound is returned when bundling a TypeScript source plugin without
// `bun` available on PATH (dev/build tooling only; runtime never needs Bun).
var ErrBunNotFound = errors.New("marketplace: bun CLI is required to bundle TypeScript source plugins")

// DetectTSScriptEntry inspects htmlBytes for a `<script src="...ts|tsx">` tag.
func DetectTSScriptEntry(htmlBytes []byte) (fullMatch, relSrc string, found bool) {
	m := tsScriptSrcRe.FindSubmatch(htmlBytes)
	if len(m) < 3 {
		return "", "", false
	}
	return string(m[0]), strings.TrimSpace(string(m[2])), true
}

// BundleTSWebViewDir compiles a TypeScript WebView plugin using `bun build`
// (dev/build tooling only, matching MyGo's philosophy) into a self-contained
// browser ESM staging directory (bundle.js + rewritten index.html + static assets)
// with zero runtime Bun/Node dependency.
//
// If the plugin's entry HTML does not reference a .ts/.tsx script (e.g. a
// pre-bundled static HTML/JS plugin), bundled=false and stageDir=pluginDir.
func BundleTSWebViewDir(ctx context.Context, pluginDir, htmlEntry string) (stageDir string, cleanup func(), bundled bool, bundleBytes int, err error) {
	noop := func() {}
	if htmlEntry == "" {
		htmlEntry = "index.html"
	}
	absPluginDir, err := filepath.Abs(pluginDir)
	if err != nil {
		return "", noop, false, 0, err
	}
	htmlPath := filepath.Join(absPluginDir, filepath.FromSlash(htmlEntry))
	htmlRaw, err := os.ReadFile(htmlPath)
	if err != nil {
		return "", noop, false, 0, fmt.Errorf("read webview entry %q: %w", htmlEntry, err)
	}
	fullTag, relTS, isTS := DetectTSScriptEntry(htmlRaw)
	if !isTS {
		return absPluginDir, noop, false, 0, nil
	}

	bunBin, err := exec.LookPath("bun")
	if err != nil {
		return "", noop, false, 0, fmt.Errorf("%w: %v", ErrBunNotFound, err)
	}

	entryAbs := filepath.Join(filepath.Dir(htmlPath), filepath.FromSlash(relTS))
	if _, statErr := os.Stat(entryAbs); statErr != nil {
		return "", noop, false, 0, fmt.Errorf("missing TypeScript entry %q referenced by %s: %w", relTS, htmlEntry, statErr)
	}

	tmpDir, err := os.MkdirTemp("", "marlin-ts-stage-*")
	if err != nil {
		return "", noop, false, 0, err
	}
	clean := func() { _ = os.RemoveAll(tmpDir) }

	// Copy manifest and static runtime assets (excluding raw .ts/.tsx sources, package/tsconfig files, node_modules).
	err = filepath.WalkDir(absPluginDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if path != absPluginDir && (name == ".git" || name == "node_modules" || name == "dist" || name == "src" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(absPluginDir, path)
		if relErr != nil {
			return relErr
		}
		lower := strings.ToLower(rel)
		if lower == "package.json" || lower == "tsconfig.json" || lower == "bun.lock" || lower == "bun.lockb" ||
			strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".tsx") || strings.HasSuffix(lower, ".marlinpkg") {
			return nil
		}
		dst := filepath.Join(tmpDir, rel)
		if mkErr := os.MkdirAll(filepath.Dir(dst), 0o755); mkErr != nil {
			return mkErr
		}
		return copyFile(path, dst)
	})
	if err != nil {
		clean()
		return "", noop, false, 0, err
	}

	// Bundle TS + npm dependencies (and @tao3k/marlin-panel-sdk) into self-contained browser ESM.
	bundleRel := "bundle.js"
	if dirPart := filepath.Dir(filepath.FromSlash(htmlEntry)); dirPart != "." && dirPart != "" {
		bundleRel = filepath.ToSlash(filepath.Join(dirPart, "bundle.js"))
	}
	bundleOut := filepath.Join(tmpDir, filepath.FromSlash(bundleRel))
	if err := os.MkdirAll(filepath.Dir(bundleOut), 0o755); err != nil {
		clean()
		return "", noop, false, 0, err
	}

	args := []string{
		"build", entryAbs,
		"--target=browser",
		"--format=esm",
		"--bundle",
		"--minify",
		`--define:process.env.NODE_ENV="production"`,
		"--outfile=" + bundleOut,
	}

	// If the plugin hasn't run `bun install` locally yet, transparently wire
	// workspace @tao3k/marlin-panel-sdk (+ /react) and workspace React 19 via a
	// temp tsconfig override without mutating the author's source directory.
	wsNodeModules := ""
	if _, nmErr := os.Stat(filepath.Join(absPluginDir, "node_modules", "@tao3k", "marlin-panel-sdk")); nmErr != nil {
		if sdkEntry := locateWorkspacePanelSDK(absPluginDir); sdkEntry != "" {
			tmpCfgDir, cfgErr := os.MkdirTemp("", "marlin-ts-cfg-*")
			if cfgErr == nil {
				defer os.RemoveAll(tmpCfgDir)
				tsconfigPath := filepath.Join(tmpCfgDir, "tsconfig.json")
				pathsMap := buildWorkspaceTSPaths(sdkEntry)
				wsRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(sdkEntry))))
				if st, sErr := os.Stat(filepath.Join(wsRoot, "node_modules")); sErr == nil && st.IsDir() {
					wsNodeModules = filepath.Join(wsRoot, "node_modules")
				}
				cfgDoc := map[string]any{
					"compilerOptions": map[string]any{
						"moduleResolution": "bundler",
						"target":           "ES2022",
						"jsx":              "react-jsx",
						"paths":            pathsMap,
					},
				}
				if rawCfg, mErr := json.Marshal(cfgDoc); mErr == nil && os.WriteFile(tsconfigPath, rawCfg, 0o644) == nil {
					args = append(args, "--tsconfig-override="+tsconfigPath)
				}
			}
		}
	}

	cmd := exec.CommandContext(ctx, bunBin, args...)
	cmd.Dir = absPluginDir
	cmdEnv := append(os.Environ(), "NODE_ENV=production")
	if wsNodeModules != "" {
		cmdEnv = append(cmdEnv, "NODE_PATH="+wsNodeModules)
	}
	cmd.Env = cmdEnv
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		clean()
		return "", noop, false, 0, fmt.Errorf("bun build %s failed: %w\n%s", relTS, runErr, strings.TrimSpace(string(out)))
	}
	info, err := os.Stat(bundleOut)
	if err != nil {
		clean()
		return "", noop, false, 0, err
	}

	// Rewrite staged index.html to load the self-contained browser bundle.js.
	rewritten := strings.Replace(string(htmlRaw), fullTag, `<script type="module" src="./bundle.js"></script>`, 1)
	stagedHTML := filepath.Join(tmpDir, filepath.FromSlash(htmlEntry))
	if err := os.MkdirAll(filepath.Dir(stagedHTML), 0o755); err != nil {
		clean()
		return "", noop, false, 0, err
	}
	if err := os.WriteFile(stagedHTML, []byte(rewritten), 0o644); err != nil {
		clean()
		return "", noop, false, 0, err
	}
	return tmpDir, clean, true, int(info.Size()), nil
}

func locateWorkspacePanelSDK(startDir string) string {
	if envPath := strings.TrimSpace(os.Getenv("MARLIN_PANEL_SDK_PATH")); envPath != "" {
		if info, err := os.Stat(envPath); err == nil {
			if info.IsDir() {
				candidate := filepath.Join(envPath, "src", "index.ts")
				if _, err := os.Stat(candidate); err == nil {
					return candidate
				}
			} else {
				return envPath
			}
		}
	}
	searchRoots := []string{startDir}
	if wd, err := os.Getwd(); err == nil && wd != startDir {
		searchRoots = append(searchRoots, wd)
	}
	for _, root := range searchRoots {
		cur := root
		for i := 0; i < 8; i++ {
			candidate := filepath.Join(cur, "packages", "panel-sdk", "src", "index.ts")
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
	}
	return ""
}

func buildWorkspaceTSPaths(sdkIndexPath string) map[string][]string {
	sdkDir := filepath.Dir(sdkIndexPath)
	paths := map[string][]string{
		"@tao3k/marlin-panel-sdk": {sdkIndexPath},
	}
	reactEntry := filepath.Join(sdkDir, "react.ts")
	if _, err := os.Stat(reactEntry); err == nil {
		paths["@tao3k/marlin-panel-sdk/react"] = []string{reactEntry}
	}
	wsRoot := filepath.Dir(filepath.Dir(filepath.Dir(sdkDir)))
	nm := filepath.Join(wsRoot, "node_modules")
	for pkg, rel := range map[string]string{
		"react":                 filepath.Join(nm, "react", "index.js"),
		"react/jsx-runtime":     filepath.Join(nm, "react", "jsx-runtime.js"),
		"react/jsx-dev-runtime": filepath.Join(nm, "react", "jsx-dev-runtime.js"),
		"react-dom":             filepath.Join(nm, "react-dom", "index.js"),
		"react-dom/client":      filepath.Join(nm, "react-dom", "client.js"),
	} {
		if _, err := os.Stat(rel); err == nil {
			paths[pkg] = []string{rel}
		}
	}
	return paths
}
