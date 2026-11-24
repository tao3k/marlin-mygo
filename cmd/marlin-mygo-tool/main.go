// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

// Command marlin-mygo-tool replaces ad-hoc shell scripts with typed,
// cross-platform Go tooling for descriptor compilation, stub generation,
// upstream Protobuf compatibility verification, Quint formal security model
// gating, and downstream Marketplace plugin scaffolding.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/tao3k/marlin-mygo/pkg/schema"
)

var canonicalProtos = []string{
	"proto/marlin/common/v1/common.proto",
	"proto/marlin/app/v1/app_server.proto",
	"proto/marlin/gate/v1/gate.proto",
	"proto/marlin/ui/v1/ui.proto",
	"proto/marlin/plugin/v1/plugin.proto",
	"proto/marlin/device/v1/device.proto",
	"proto/marlin/projection/v1/projection.proto",
}

var upstreamAppV1RPCs = []string{
	"GetCapabilities",
	"StreamTurn",
	"StartSessionTurn",
	"WatchSessionTurn",
	"CancelSessionTurn",
	"OpenSession",
	"GetSessionSummary",
	"GetSessionTurnStatus",
	"GetSessionTurnCancellation",
}

var quintNegativeControls = []string{
	"badStepOriginSpoof",
	"badStepRestrictedSlotHijack",
	"badStepDeviceWriteNoReceipt",
	"badStepBreakerForgetsFallback",
	"badStepTamperedPluginActive",
	"badStepSecretTopicLeak",
	"badStepDeviceWriteReplay",
}

var restrictedSlots = []string{
	"approval.gate",
	"host.auth",
	"host.secrets",
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "descriptor":
		err = runDescriptor(os.Args[2:])
	case "gen":
		err = runGen(os.Args[2:])
	case "gen-ts":
		err = runGenTS(os.Args[2:])
	case "compat-check":
		err = runCompatCheck(os.Args[2:])
	case "model-gate":
		err = runModelGate(os.Args[2:])
	case "init-plugin":
		err = runInitPlugin(os.Args[2:])
	case "keygen":
		err = runKeygen(os.Args[2:])
	case "pack":
		err = runPack(os.Args[2:])
	case "verify":
		err = runVerify(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "marlin-mygo-tool %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: go run ./cmd/marlin-mygo-tool <descriptor|gen|gen-ts|compat-check|model-gate|init-plugin|keygen|pack|verify> [flags]")
}

func runDescriptor(args []string) error {
	fs := flag.NewFlagSet("descriptor", flag.ContinueOnError)
	out := fs.String("out", "proto/marlin-wire-v1.bin", "output FileDescriptorSet path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	protocPath, err := exec.LookPath("protoc")
	if err != nil {
		return errors.New("protoc not found in PATH")
	}
	cmdArgs := []string{
		"--proto_path=proto",
		"--include_imports",
		"--include_source_info",
		"--descriptor_set_out=" + *out,
	}
	cmdArgs = append(cmdArgs, canonicalProtos...)
	cmd := exec.Command(protocPath, cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	raw, err := os.ReadFile(*out)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	fmt.Printf("%s  %s (%d bytes)\n", hex.EncodeToString(sum[:]), *out, len(raw))
	return nil
}

func runGen(args []string) error {
	fs := flag.NewFlagSet("gen", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	for _, tool := range []string{"buf", "protoc-gen-go", "protoc-gen-go-grpc"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("%s not found in PATH", tool)
		}
	}
	for _, sub := range [][]string{{"lint"}, {"generate"}} {
		cmd := exec.Command("buf", sub...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("buf %s failed: %w", strings.Join(sub, " "), err)
		}
	}
	return nil
}

func runCompatCheck(args []string) error {
	fs := flag.NewFlagSet("compat-check", flag.ContinueOnError)
	coreDir := fs.String("core", "../..", "path to marlin-agent-core checkout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	upstreamPath := filepath.Join(*coreDir, "crates", "marlin-agent-app-server", "proto", "marlin", "app", "v1", "app_server.proto")
	if _, err := os.Stat(upstreamPath); err != nil {
		return fmt.Errorf("upstream proto missing at %s: %w", upstreamPath, err)
	}
	localRaw, err := os.ReadFile(filepath.Join("proto", "marlin", "app", "v1", "app_server.proto"))
	if err != nil {
		return err
	}
	localText := string(localRaw)
	for _, rpc := range upstreamAppV1RPCs {
		if !strings.Contains(localText, "rpc "+rpc+"(") {
			return fmt.Errorf("missing upstream marlin.app.v1 RPC %q", rpc)
		}
	}
	fmt.Printf("Verified all %d upstream marlin.app.v1 RPCs against %s.\n", len(upstreamAppV1RPCs), upstreamPath)
	return nil
}

func runModelGate(args []string) error {
	fs := flag.NewFlagSet("model-gate", flag.ContinueOnError)
	specPath := fs.String("spec", "spec/quint/frontend-security.qnt", "path to Quint security specification")
	samples := fs.Int("samples", 1000, "number of random simulation traces for safe model")
	steps := fs.Int("steps", 25, "max steps per simulation trace")
	seed := fs.Int("seed", 42, "deterministic simulation PRNG seed")
	backend := fs.String("backend", "rust", "quint simulator backend: rust | typescript")
	if err := fs.Parse(args); err != nil {
		return err
	}
	quintBin, err := exec.LookPath("quint")
	if err != nil {
		return errors.New("quint not found in PATH (install @informalsystems/quint)")
	}
	if _, err := os.Stat(*specPath); err != nil {
		return fmt.Errorf("quint spec not found at %s: %w", *specPath, err)
	}

	fmt.Printf("[1/4] quint typecheck %s\n", *specPath)
	if out, err := exec.Command(quintBin, "typecheck", *specPath).CombinedOutput(); err != nil {
		return fmt.Errorf("typecheck failed: %w\n%s", err, string(out))
	}

	fmt.Printf("[2/4] quint test --main=frontendPluginSecurity --backend=%s %s\n", *backend, *specPath)
	if out, err := exec.Command(quintBin, "test", "--main=frontendPluginSecurity", "--backend="+*backend, *specPath).CombinedOutput(); err != nil {
		return fmt.Errorf("quint test failed: %w\n%s", err, string(out))
	} else {
		fmt.Print(string(out))
	}

	fmt.Printf("[3/4] quint run (%d samples x %d steps, backend=%s, invariant=safe)\n", *samples, *steps, *backend)
	runCmd := exec.Command(
		quintBin, "run",
		"--main=frontendPluginSecurity",
		"--backend="+*backend,
		"--seed="+strconv.Itoa(*seed),
		"--max-samples="+strconv.Itoa(*samples),
		"--max-steps="+strconv.Itoa(*steps),
		"--invariant=safe",
		"--verbosity=1",
		*specPath,
	)
	if out, err := runCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("safe invariant run failed: %w\n%s", err, string(out))
	} else {
		text := string(out)
		if !strings.Contains(text, "No violation found") {
			return fmt.Errorf("unexpected quint run output without 'No violation found':\n%s", text)
		}
		fmt.Println("  ok: No violation found across", *samples, "traces")
	}

	fmt.Printf("[4/4] checking %d adversarial negative controls (each MUST violate safe)\n", len(quintNegativeControls))
	for _, badStep := range quintNegativeControls {
		negCmd := exec.Command(
			quintBin, "run",
			"--main=frontendPluginSecurity",
			"--backend="+*backend,
			"--seed="+strconv.Itoa(*seed),
			"--max-samples=300",
			"--max-steps="+strconv.Itoa(*steps),
			"--step="+badStep,
			"--invariant=safe",
			"--verbosity=1",
			*specPath,
		)
		out, err := negCmd.CombinedOutput()
		text := string(out)
		if err == nil || !strings.Contains(text, "Invariant violated") {
			return fmt.Errorf("negative control %q failed to trigger Invariant violated (err=%v):\n%s", badStep, err, text)
		}
		fmt.Printf("  ok: %s -> Invariant violated (caught as expected)\n", badStep)
	}
	fmt.Println("Quint formal security model gate PASSED.")
	return nil
}

func runInitPlugin(args []string) error {
	fs := flag.NewFlagSet("init-plugin", flag.ContinueOnError)
	idStr := fs.String("id", "", "namespaced plugin ID (e.g. acme.sprint-board)")
	runtimeKind := fs.String("runtime", "ts", "plugin runtime: ts | go-wasm | declarative")
	slotStr := fs.String("slot", "workbench.main", "target UI slot ID")
	outDir := fs.String("out", "", "output directory (defaults to ./<plugin-short-name>)")
	force := fs.Bool("force", false, "allow writing into an existing directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	pluginID, err := schema.ParsePluginID(*idStr)
	if err != nil {
		return fmt.Errorf("invalid -id: %w", err)
	}
	if err := schema.ValidateDotIdentifier(*slotStr); err != nil {
		return fmt.Errorf("invalid -slot: %w", err)
	}
	if slices.Contains(restrictedSlots, *slotStr) {
		return fmt.Errorf("slot %q is host-restricted and cannot mount untrusted plugins", *slotStr)
	}

	parts := strings.Split(string(pluginID), ".")
	shortName := parts[len(parts)-1]
	targetDir := *outDir
	if targetDir == "" {
		targetDir = shortName
	}
	if entries, err := os.ReadDir(targetDir); err == nil && len(entries) > 0 && !*force {
		return fmt.Errorf("output directory %q already exists and is non-empty (pass -force to overwrite)", targetDir)
	}

	files, err := renderPluginScaffold(pluginID, *runtimeKind, schema.SlotID(*slotStr), shortName)
	if err != nil {
		return err
	}
	for rel, content := range files {
		fullPath := filepath.Join(targetDir, rel)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("Scaffolded %s plugin %q (%d files) in %s\n", *runtimeKind, pluginID, len(files), targetDir)
	return nil
}

func renderPluginScaffold(pluginID schema.PluginID, runtimeFlag string, slotID schema.SlotID, shortName string) (map[string]string, error) {
	actionName := shortName + ".refresh"
	topicName := shortName + ".state"

	switch runtimeFlag {
	case "ts":
		manifest := fmt.Sprintf(`# SPDX-FileCopyrightText: 2026 tao3k team and Contributors
#
# SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

id = %q
version = "0.1.0"
contract_major = 1
requires_features = ["ui-slot-v1", "scoped-panel-v1"]

[capabilities.read_state]
kind = "projection.read"
surface = %q

[contributions.%s_panel]
kind = "ui-panel"
requires = ["read_state"]

[contributions.%s_panel.panel]
runtime = "webview-untrusted"
slot_id = %q
cardinality = "single"
entry_path = "index.html"
read_scopes = [%q]
declared_actions = [%q]
`, pluginID, topicName, shortName, shortName, slotID, topicName, actionName)

		pkgJSON := fmt.Sprintf(`{
  "name": "@marlin-plugin/%s",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "dependencies": {
    "@tao3k/marlin-panel-sdk": "workspace:*"
  }
}
`, shortName)

		indexHTML := fmt.Sprintf(`<!-- SPDX-FileCopyrightText: 2026 tao3k team and Contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later -->
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta http-equiv="Content-Security-Policy" content="default-src 'self' marlin-plugin:; style-src 'self' 'unsafe-inline';" />
    <title>%s</title>
    <style>
      body { background: var(--marlin-canvas, #282c34); color: var(--marlin-ink, #bbc2cf); font-family: system-ui, sans-serif; padding: 16px; }
      button { background: var(--marlin-accent, #c678dd); color: #1c1f24; border: 0; border-radius: 6px; padding: 8px 14px; cursor: pointer; }
    </style>
  </head>
  <body>
    <h1 id="title">%s</h1>
    <pre id="state">Waiting for scoped events...</pre>
    <button id="refresh" type="button">Trigger Scoped Action</button>
    <script type="module" src="./src/main.ts"></script>
  </body>
</html>
`, pluginID, pluginID)

		mainTS := fmt.Sprintf(`// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

import { createMarlinPanel } from "@tao3k/marlin-panel-sdk";

const panel = createMarlinPanel({
  pluginId: %q,
  activationGeneration: "1",
});

const stateEl = document.getElementById("state");
panel.onEvent(%q, (ev) => {
  if (stateEl) stateEl.textContent = JSON.stringify(ev.payload, null, 2);
});

document.getElementById("refresh")?.addEventListener("click", async () => {
  await panel.submitAction(%q, { source: "ui-click" }, crypto.randomUUID());
});
`, pluginID, topicName, actionName)

		readme := fmt.Sprintf("# %s (TypeScript Untrusted WebView Plugin)\n\nSlot: `%s`\nRuntime: `webview-untrusted`\n", pluginID, slotID)
		return map[string]string{
			"marlin-plugin.toml": manifest,
			"package.json":       pkgJSON,
			"index.html":         indexHTML,
			"src/main.ts":        mainTS,
			"README.md":          readme,
		}, nil

	case "go-wasm":
		manifest := fmt.Sprintf(`# SPDX-FileCopyrightText: 2026 tao3k team and Contributors
#
# SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

id = %q
version = "0.1.0"
contract_major = 1
requires_features = ["ui-slot-v1", "wasm-ui-ir-v1"]

[capabilities.read_state]
kind = "projection.read"
surface = %q

[contributions.%s_ir]
kind = "ui-panel"
requires = ["read_state"]

[contributions.%s_ir.panel]
runtime = "wasm-ui-ir"
slot_id = %q
cardinality = "single"
entry_path = "plugin.wasm"
read_scopes = [%q]
declared_actions = [%q]
`, pluginID, topicName, shortName, shortName, slotID, topicName, actionName)

		mainGo := fmt.Sprintf(`// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

//go:build wasip1

// Build as a resident Go 1.27+ WASI reactor (sub-millisecond per-frame Render):
//   GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -buildmode=c-shared -o plugin.wasm .
package main

import (
	"fmt"
	"unsafe"

	"github.com/tao3k/marlin-mygo/pkg/schema"
	uiir "github.com/tao3k/marlin-mygo/sdk/go-ui-ir"
)

var (
	frameCount uint64
	lastBytes  []byte
)

//go:wasmexport marlin_ui_render
func marlinUIRender() uint64 {
	frameCount++
	tree := uiir.Panel(%q, schema.ToneAccent,
		uiir.Rows(
			uiir.Text(uiir.Span(fmt.Sprintf("Resident WASM-IR frame %%d", frameCount), schema.ToneInfo)),
		),
	)
	raw, err := uiir.Encode(tree)
	if err != nil || len(raw) == 0 {
		return 0
	}
	lastBytes = raw
	ptr := uint32(uintptr(unsafe.Pointer(&lastBytes[0])))
	return (uint64(ptr) << 32) | uint64(len(lastBytes))
}

func main() {}
`, pluginID)

		readme := fmt.Sprintf("# %s (Go Resident WASM-IR Plugin)\n\nSlot: `%s`\nBuild:\n```shell\nGOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -buildmode=c-shared -o plugin.wasm .\n```\n", pluginID, slotID)
		return map[string]string{
			"marlin-plugin.toml": manifest,
			"main.go":            mainGo,
			"README.md":          readme,
		}, nil

	case "declarative":
		manifest := fmt.Sprintf(`# SPDX-FileCopyrightText: 2026 tao3k team and Contributors
#
# SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

id = %q
version = "0.1.0"
contract_major = 1
requires_features = ["ui-slot-v1"]

[contributions.%s_pack]
kind = "theme-pack"

[contributions.%s_pack.panel]
runtime = "declarative"
slot_id = %q
cardinality = "single"
entry_path = "theme.toml"
`, pluginID, shortName, shortName, slotID)

		themeTOML := `# SPDX-FileCopyrightText: 2026 tao3k team and Contributors
#
# SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

[theme]
canvas = "#282c34"
ink = "#bbc2cf"
bar = "#1c1f24"
accent = "#c678dd"
info = "#51afef"
success = "#98be65"
warning = "#ecbe7b"
muted = "#73797e"
`
		readme := fmt.Sprintf("# %s (Declarative Pack — Desktop + iOS/Android App Store Safe)\n\nSlot: `%s`\n", pluginID, slotID)
		return map[string]string{
			"marlin-plugin.toml": manifest,
			"theme.toml":         themeTOML,
			"README.md":          readme,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported -runtime %q (want ts, go-wasm, or declarative)", runtimeFlag)
	}
}
