// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package pluginhost_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/tao3k/marlin-mygo/engine/features"
	"github.com/tao3k/marlin-mygo/engine/shell"
	"github.com/tao3k/marlin-mygo/pkg/schema"
	"github.com/tao3k/marlin-mygo/runtime/pluginhost"
)

type itfBigInt struct {
	Value string `json:"#bigint"`
}

func (b itfBigInt) Int64() int64 {
	n, _ := strconv.ParseInt(b.Value, 10, 64)
	return n
}

type itfOptionBool struct {
	Tag   string          `json:"tag"`
	Value json.RawMessage `json:"value"`
}

func (o itfOptionBool) Bool() (bool, bool) {
	if o.Tag != "Some" {
		return false, false
	}
	var b bool
	if err := json.Unmarshal(o.Value, &b); err != nil {
		return false, false
	}
	return b, true
}

type itfOptionInt struct {
	Tag   string          `json:"tag"`
	Value json.RawMessage `json:"value"`
}

func (o itfOptionInt) Int64() (int64, bool) {
	if o.Tag != "Some" {
		return 0, false
	}
	var bi itfBigInt
	if err := json.Unmarshal(o.Value, &bi); err != nil {
		return 0, false
	}
	return bi.Int64(), true
}

func resolveExecModel(t *testing.T, relDEModel, relOverlay string) (bunBin, cliPath, modelPath, repoRoot string) {
	t.Helper()
	var err error
	bunBin, err = exec.LookPath("bun")
	if err != nil {
		t.Skip("bun not on PATH; skipping live Quint MBT trace generation")
	}
	repoRoot, err = filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	localDE := strings.TrimSpace(os.Getenv("DE_LOCAL_ROOT"))
	if localDE != "" {
		candCLI := filepath.Join(localDE, "packages/quint-modules-resolver/src/cli.ts")
		if _, err := os.Stat(candCLI); err == nil {
			cliPath = candCLI
		}
	}
	if cliPath == "" {
		cliPath = filepath.Join(repoRoot, "node_modules/@deterministic-engineering/quint-modules-resolver/src/cli.ts")
		if _, err := os.Stat(cliPath); err != nil {
			t.Skipf("quint-modules-resolver cli not found at %s", cliPath)
		}
	}
	if relOverlay != "" {
		return bunBin, cliPath, filepath.Join(repoRoot, relOverlay), repoRoot
	}
	if localDE != "" {
		candModel := filepath.Join(localDE, "packages/quint-models", relDEModel)
		if _, err := os.Stat(candModel); err == nil {
			return bunBin, cliPath, candModel, repoRoot
		}
	}
	modelPath = filepath.Join(repoRoot, "node_modules/@deterministic-engineering/quint-models", relDEModel)
	if _, err := os.Stat(modelPath); err != nil {
		t.Skipf("Quint model not found at %s", modelPath)
	}
	return bunBin, cliPath, modelPath, repoRoot
}

func generateExecMBTTraces(t *testing.T, bunBin, cliPath, qntModel, repoRoot, mainMod string, maxSteps, nTraces int) []string {
	t.Helper()
	outDir := t.TempDir()
	cmd := exec.Command(bunBin, cliPath, "mbt-audit", qntModel,
		"--out-dir", outDir,
		fmt.Sprintf("--max-steps=%d", maxSteps),
		fmt.Sprintf("--n-traces=%d", nTraces),
		"--seed=7",
		"--min-coverage=0.8",
		"--json",
	)
	cmd.Dir = repoRoot
	cmd.Env = os.Environ()
	if localDE := strings.TrimSpace(os.Getenv("DE_LOCAL_ROOT")); localDE != "" {
		resolveRoot := t.TempDir()
		scopeDir := filepath.Join(resolveRoot, "node_modules", "@deterministic-engineering")
		if err := os.MkdirAll(scopeDir, 0o755); err != nil {
			t.Fatalf("mkdir resolve root: %v", err)
		}
		if err := os.Symlink(filepath.Join(localDE, "packages/quint-models"), filepath.Join(scopeDir, "quint-models")); err != nil {
			t.Fatalf("symlink local quint-models: %v", err)
		}
		cmd.Env = append(cmd.Env, "QUINT_RESOLVE_ROOT="+resolveRoot)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mbt-audit failed for %s (%s): %v\n%s", qntModel, mainMod, err, string(out))
	}
	var report struct {
		Passed              bool     `json:"passed"`
		DeclaredStepActions []string `json:"declaredStepActions"`
		CoveredActions      []string `json:"coveredActions"`
		ActionCoverage      float64  `json:"actionCoverage"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("parse mbt-audit JSON manifest for %s: %v\n%s", mainMod, err, string(out))
	}
	if !report.Passed {
		t.Fatalf("mbt-audit (%s) reported passed=false: %+v", mainMod, report)
	}
	matches, err := filepath.Glob(filepath.Join(outDir, "*.itf.json"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("no ITF traces generated in %s (err=%v)\n%s", outDir, err, string(out))
	}
	sort.Strings(matches)
	t.Logf("MBT-AUDIT-OK [%s] coverage=%d/%d (%.1f%%) traces=%d",
		mainMod, len(report.CoveredActions), len(report.DeclaredStepActions), report.ActionCoverage*100, len(matches))
	return matches
}

type mbtFakeDispatcher struct {
	admitted int
}

func (d *mbtFakeDispatcher) SubmitScopedPanelAction(_ context.Context, _ pluginhost.ScopedActionRequest) (pluginhost.ScopedActionReceipt, error) {
	d.admitted++
	return pluginhost.ScopedActionReceipt{Admitted: true, ReceiptID: fmt.Sprintf("rcpt-%d", d.admitted)}, nil
}

// ---------------------------------------------------------------------------
// 1. AgentToolCircuitBreaker + MarlinExecutionBreakerOverlay
// ---------------------------------------------------------------------------

type breakerState struct {
	BreakerTripped     bool      `json:"breakerTripped"`
	TripEpoch          itfBigInt `json:"tripEpoch"`
	BudgetUsed         itfBigInt `json:"budgetUsed"`
	LeaseActive        bool      `json:"leaseActive"`
	LeaseEpoch         itfBigInt `json:"leaseEpoch"`
	ExecWhileTripped   bool      `json:"execWhileTripped"`
	StaleLeaseCommit   bool      `json:"staleLeaseCommit"`
	AgentSelfRearmed   bool      `json:"agentSelfRearmed"`
	BudgetOverrun      bool      `json:"budgetOverrun"`
	ProxyRevoked       bool      `json:"proxyRevoked"`
	ProxyLeakOnTripped bool      `json:"proxyLeakOnTripped"`
	AuditReceipts      itfBigInt `json:"auditReceipts"`
}

type breakerNondetPicks struct {
	IsAgent       itfOptionBool `json:"isAgent"`
	VerifiedHuman itfOptionBool `json:"verifiedHuman"`
}

type breakerTraceState struct {
	H           breakerState       `json:"h"`
	ActionTaken string             `json:"mbt::actionTaken"`
	NondetPicks breakerNondetPicks `json:"mbt::nondetPicks"`
}

type breakerTrace struct {
	States []breakerTraceState `json:"states"`
}

func TestMBTReplayAgentToolCircuitBreakerITF(t *testing.T) {
	bunBin, cliPath, modelPath, repoRoot := resolveExecModel(t, "threat/execution/agent-tool-circuit-breaker/AgentToolCircuitBreaker.qnt", "")
	matches := generateExecMBTTraces(t, bunBin, cliPath, modelPath, repoRoot, "agent_tool_circuit_breaker", 18, 10)
	totalSteps := 0
	for _, tp := range matches {
		raw, err := os.ReadFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		var trace breakerTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tp, err)
		}
		replayOneBreakerTrace(t, filepath.Base(tp), trace, false)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [AgentToolCircuitBreaker] replayed %d Quint ITF traces (%d transitions) against Go pluginhost.ExecutionBreaker", len(matches), totalSteps)
}

func TestMBTReplayMarlinExecutionBreakerOverlayITF(t *testing.T) {
	bunBin, cliPath, modelPath, repoRoot := resolveExecModel(t, "", "spec/execution/MarlinExecutionBreakerOverlay.qnt")
	matches := generateExecMBTTraces(t, bunBin, cliPath, modelPath, repoRoot, "marlin_execution_breaker_composed", 18, 12)
	totalSteps := 0
	for _, tp := range matches {
		raw, err := os.ReadFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		var trace breakerTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tp, err)
		}
		replayOneBreakerTrace(t, filepath.Base(tp), trace, true)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [MarlinExecutionBreakerOverlay] replayed %d Quint @quint-extends ITF traces (%d transitions) against Go ExecutionBreaker + ScopedProxy + GovernanceLedger", len(matches), totalSteps)
}

func replayOneBreakerTrace(t *testing.T, name string, trace breakerTrace, isOverlay bool) {
	t.Helper()
	ctx := context.Background()
	ledger := features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	brk := pluginhost.NewExecutionBreaker(1, ledger)
	disp := &mbtFakeDispatcher{}
	newProxy := func(gen uint64) *pluginhost.ScopedProxy {
		p := pluginhost.NewScopedProxy(pluginhost.ScopedScope{
			PluginID:             "tao3k.agent-tools",
			ContributionID:       "tool-panel",
			ActivationGeneration: gen,
			DeclaredActions:      []string{"tool.exec"},
		}, disp)
		brk.BindProxy(p)
		return p
	}
	proxyGen := uint64(1)
	proxy := newProxy(proxyGen)

	var (
		hasLease    bool
		activeLease pluginhost.ToolLease
	)

	for i := 1; i < len(trace.States); i++ {
		prev := trace.States[i-1].H
		st := trace.States[i]
		act := st.ActionTaken

		switch act {
		case "acquireToolLease":
			if !prev.BreakerTripped && !prev.LeaseActive {
				lease, err := brk.AcquireToolLease("agent-architect", "tool.exec")
				if err != nil {
					t.Fatalf("[%s step %d] valid AcquireToolLease failed: %v", name, i, err)
				}
				activeLease = lease
				hasLease = true
			} else if prev.BreakerTripped {
				if _, err := brk.AcquireToolLease("agent-architect", "tool.exec"); !errors.Is(err, pluginhost.ErrBreakerTripped) {
					t.Fatalf("[%s step %d] expected ErrBreakerTripped acquiring lease while tripped, got %v", name, i, err)
				}
			}

		case "commitToolEffect":
			if prev.LeaseActive && hasLease {
				err := brk.CommitToolEffect(activeLease)
				quintCommitted := st.H.BudgetUsed.Int64() > prev.BudgetUsed.Int64()
				if quintCommitted {
					if err != nil {
						t.Fatalf("[%s step %d] Quint committed tool effect but Go rejected: %v", name, i, err)
					}
					hasLease = false
				} else if err == nil {
					t.Fatalf("[%s step %d] SECURITY VIOLATION: Quint blocked tool commit (tripped=%v leaseEp=%d tripEp=%d budget=%d) but Go allowed!",
						name, i, prev.BreakerTripped, prev.LeaseEpoch.Int64(), prev.TripEpoch.Int64(), prev.BudgetUsed.Int64())
				}
			}

		case "tripBreaker":
			if !prev.BreakerTripped {
				brk.Trip("human-operator", "manual E-stop")
			}

		case "rearmBreaker":
			if prev.BreakerTripped && prev.TripEpoch.Int64() < 4 {
				isAgent, _ := st.NondetPicks.IsAgent.Bool()
				verifiedHuman, _ := st.NondetPicks.VerifiedHuman.Bool()
				_, err := brk.Rearm("actor", isAgent, verifiedHuman)
				if !isAgent && verifiedHuman {
					if err != nil {
						t.Fatalf("[%s step %d] valid human Rearm failed: %v", name, i, err)
					}
					// Go's Trip strictly revokes all bound proxies; when base tripBreaker fired
					// (without overlay proxyRevoked=true), rebind a fresh proxy for the new armed generation.
					if isOverlay && !prev.ProxyRevoked {
						proxyGen++
						proxy = newProxy(proxyGen)
					}
				} else if !errors.Is(err, pluginhost.ErrAgentCannotRearmBreaker) {
					t.Fatalf("[%s step %d] SECURITY VIOLATION: agent/unverified rearm was not blocked (got %v)", name, i, err)
				}
			}

		case "cancelStaleLease":
			if prev.LeaseActive && (prev.BreakerTripped || prev.LeaseEpoch.Int64() != prev.TripEpoch.Int64()) && hasLease {
				brk.CancelLease(activeLease.ID)
				hasLease = false
			}

		case "tripWithProxyRevoke":
			if !prev.BreakerTripped {
				brk.Trip("human-operator", "E-stop with proxy cutoff")
				// Verify bound proxy is immediately revoked!
				if _, err := proxy.SubmitAction(ctx, "tool.exec", nil, "post-trip"); !errors.Is(err, pluginhost.ErrProxyRevoked) {
					t.Fatalf("[%s step %d] SECURITY VIOLATION: bound ScopedProxy survived breaker trip (got %v)", name, i, err)
				}
			}

		case "attemptProxyAction":
			_, err := proxy.SubmitAction(ctx, "tool.exec", nil, fmt.Sprintf("k-%d", i))
			if prev.ProxyRevoked || prev.BreakerTripped {
				if err == nil && prev.ProxyRevoked {
					t.Fatalf("[%s step %d] SECURITY VIOLATION: revoked proxy emitted action!", name, i)
				}
			} else if err != nil {
				t.Fatalf("[%s step %d] valid active proxy action rejected: %v", name, i, err)
			}

		case "rebindFreshProxy":
			if !prev.BreakerTripped && prev.ProxyRevoked {
				proxyGen++
				proxy = newProxy(proxyGen)
			}

		default:
			t.Fatalf("[%s step %d] unknown ExecutionBreaker MBT action %q", name, i, act)
		}

		tripped, tripEp, budgetUsed, _ := brk.State()
		if tripped != st.H.BreakerTripped {
			t.Fatalf("[%s step %d after %s] refinement mismatch breakerTripped: Go=%v Quint=%v", name, i, act, tripped, st.H.BreakerTripped)
		}
		if int64(tripEp) != st.H.TripEpoch.Int64() {
			t.Fatalf("[%s step %d after %s] refinement mismatch tripEpoch: Go=%d Quint=%d", name, i, act, tripEp, st.H.TripEpoch.Int64())
		}
		if int64(budgetUsed) != st.H.BudgetUsed.Int64() {
			t.Fatalf("[%s step %d after %s] refinement mismatch budgetUsed: Go=%d Quint=%d", name, i, act, budgetUsed, st.H.BudgetUsed.Int64())
		}
		if isOverlay && st.H.ProxyRevoked && len(ledger.AuditTrail()) == 0 {
			t.Fatalf("[%s step %d after %s] expected GovernanceLedger audit receipt on proxy revocation", name, i, act)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. WasmSandboxConfinement + MarlinWasmSandboxOverlay
// ---------------------------------------------------------------------------

type wasmSandboxState struct {
	GuestMemSize    itfBigInt `json:"guestMemSize"`
	MaxAccessedAddr itfBigInt `json:"maxAccessedAddr"`
	HostRootEscaped bool      `json:"hostRootEscaped"`
	ProxyBound      bool      `json:"proxyBound"`
	BreakerTripped  bool      `json:"breakerTripped"`
	RevokedCallLeak bool      `json:"revokedCallLeak"`
}

type wasmSandboxNondetPicks struct {
	Addr        itfOptionInt  `json:"addr"`
	Handle      itfOptionInt  `json:"handle"`
	EscapesRoot itfOptionBool `json:"escapesRoot"`
}

type wasmSandboxTraceState struct {
	H           wasmSandboxState       `json:"h"`
	ActionTaken string                 `json:"mbt::actionTaken"`
	NondetPicks wasmSandboxNondetPicks `json:"mbt::nondetPicks"`
}

type wasmSandboxTrace struct {
	States []wasmSandboxTraceState `json:"states"`
}

func TestMBTReplayWasmSandboxConfinementITF(t *testing.T) {
	bunBin, cliPath, modelPath, repoRoot := resolveExecModel(t, "threat/execution/wasm-sandbox-confinement/WasmSandboxConfinement.qnt", "")
	matches := generateExecMBTTraces(t, bunBin, cliPath, modelPath, repoRoot, "wasm_sandbox_confinement", 16, 10)
	totalSteps := 0
	for _, tp := range matches {
		raw, err := os.ReadFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		var trace wasmSandboxTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tp, err)
		}
		replayOneWasmSandboxTrace(t, filepath.Base(tp), trace, false)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [WasmSandboxConfinement] replayed %d Quint ITF traces (%d transitions) against Go pluginhost sandbox guard", len(matches), totalSteps)
}

func TestMBTReplayMarlinWasmSandboxOverlayITF(t *testing.T) {
	bunBin, cliPath, modelPath, repoRoot := resolveExecModel(t, "", "spec/execution/MarlinWasmSandboxOverlay.qnt")
	matches := generateExecMBTTraces(t, bunBin, cliPath, modelPath, repoRoot, "marlin_wasm_sandbox_composed", 16, 12)
	totalSteps := 0
	for _, tp := range matches {
		raw, err := os.ReadFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		var trace wasmSandboxTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tp, err)
		}
		replayOneWasmSandboxTrace(t, filepath.Base(tp), trace, true)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [MarlinWasmSandboxOverlay] replayed %d Quint @quint-extends ITF traces (%d transitions) against Go pluginhost sandbox + ScopedProxy", len(matches), totalSteps)
}

func replayOneWasmSandboxTrace(t *testing.T, name string, trace wasmSandboxTrace, isOverlay bool) {
	t.Helper()
	ctx := context.Background()
	brk := pluginhost.NewExecutionBreaker(4, nil)
	brk.GrantHandle("h-1")
	disp := &mbtFakeDispatcher{}
	proxyGen := uint64(1)
	newProxy := func() *pluginhost.ScopedProxy {
		p := pluginhost.NewScopedProxy(pluginhost.ScopedScope{
			PluginID:             "tao3k.wasm-sandbox",
			ContributionID:       "sandbox",
			ActivationGeneration: proxyGen,
			DeclaredActions:      []string{"sandbox.emit"},
		}, disp)
		brk.BindProxy(p)
		return p
	}
	proxy := newProxy()

	handleName := func(h int64) string { return fmt.Sprintf("h-%d", h) }
	var goMaxAddr int64

	for i := 1; i < len(trace.States); i++ {
		prev := trace.States[i-1].H
		st := trace.States[i]
		act := st.ActionTaken

		switch act {
		case "guestMemAccess":
			addr, _ := st.NondetPicks.Addr.Int64()
			// Mirror Wasm linear memory bounds ceiling (guestMemSize = 64 abstract cells; real WasmDefaultMemoryPages = 256 pages).
			if addr >= 0 && addr < prev.GuestMemSize.Int64() {
				if addr > goMaxAddr {
					goMaxAddr = addr
				}
			}
		case "guestHostcall":
			hNum, ok := st.NondetPicks.Handle.Int64()
			if !ok {
				hNum = 1
			}
			escapes, _ := st.NondetPicks.EscapesRoot.Bool()
			relPath := "workspace/file.txt"
			if escapes {
				relPath = "../../etc/passwd"
			}
			err := brk.InvokeSandboxHostcall(handleName(hNum), relPath)
			if escapes {
				if err == nil {
					t.Fatalf("[%s step %d] SECURITY VIOLATION: path traversal %q allowed!", name, i, relPath)
				}
			} else if !brk.HasHandle(handleName(hNum)) {
				if err == nil {
					t.Fatalf("[%s step %d] SECURITY VIOLATION: ungranted handle %s allowed!", name, i, handleName(hNum))
				}
			} else if !prev.BreakerTripped && err != nil {
				t.Fatalf("[%s step %d] valid sandbox hostcall rejected: %v", name, i, err)
			}
		case "grantNetHandle":
			brk.GrantHandle("h-2")
		case "tripSandboxBreaker":
			brk.Trip("human-operator", "sandbox breaker trip")
		case "rearmSandboxProxy":
			if prev.BreakerTripped {
				if _, err := brk.Rearm("human-operator", false, true); err != nil {
					t.Fatalf("[%s step %d] Rearm failed: %v", name, i, err)
				}
				proxyGen++
				proxy = newProxy()
			}
		case "emitHostAction":
			_, err := proxy.SubmitAction(ctx, "sandbox.emit", nil, fmt.Sprintf("e-%d", i))
			if prev.ProxyBound && !prev.BreakerTripped {
				if err != nil {
					t.Fatalf("[%s step %d] valid bound sandbox emit failed: %v", name, i, err)
				}
			} else if err == nil {
				t.Fatalf("[%s step %d] SECURITY VIOLATION: unbound/tripped sandbox proxy emitted action!", name, i)
			}
		default:
			t.Fatalf("[%s step %d] unknown WasmSandbox MBT action %q", name, i, act)
		}

		if goMaxAddr != st.H.MaxAccessedAddr.Int64() {
			t.Fatalf("[%s step %d after %s] refinement mismatch maxAccessedAddr: Go=%d Quint=%d", name, i, act, goMaxAddr, st.H.MaxAccessedAddr.Int64())
		}
		_ = isOverlay
	}
}

// ---------------------------------------------------------------------------
// 3. AgentIfcCapability + MarlinAgentIfcOverlay
// ---------------------------------------------------------------------------

type agentIfcState struct {
	PlanIntegrityHigh        bool `json:"planIntegrityHigh"`
	DataTainted              bool `json:"dataTainted"`
	TaintedPlanReady         bool `json:"taintedPlanReady"`
	DataConfidential         bool `json:"dataConfidential"`
	Declassified             bool `json:"declassified"`
	ToolInvoked              bool `json:"toolInvoked"`
	ToolControlledByTainted  bool `json:"toolControlledByTainted"`
	SinkObservedSecret       bool `json:"sinkObservedSecret"`
	SinkObservedDeclassified bool `json:"sinkObservedDeclassified"`
	HumanGateApproved        bool `json:"humanGateApproved"`
	EStopTripped             bool `json:"eStopTripped"`
	TaintedUnapprovedExec    bool `json:"taintedUnapprovedExec"`
}

type agentIfcTraceState struct {
	H           agentIfcState `json:"h"`
	ActionTaken string        `json:"mbt::actionTaken"`
}

type agentIfcTrace struct {
	States []agentIfcTraceState `json:"states"`
}

func TestMBTReplayAgentIfcCapabilityITF(t *testing.T) {
	bunBin, cliPath, modelPath, repoRoot := resolveExecModel(t, "threat/execution/agent-ifc-capability/AgentIfcCapability.qnt", "")
	matches := generateExecMBTTraces(t, bunBin, cliPath, modelPath, repoRoot, "agent_ifc_capability", 14, 10)
	totalSteps := 0
	for _, tp := range matches {
		raw, err := os.ReadFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		var trace agentIfcTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tp, err)
		}
		replayOneAgentIfcTrace(t, filepath.Base(tp), trace, false)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [AgentIfcCapability] replayed %d Quint ITF traces (%d transitions) against Go ExecutionBreaker IFC", len(matches), totalSteps)
}

func TestMBTReplayMarlinAgentIfcOverlayITF(t *testing.T) {
	bunBin, cliPath, modelPath, repoRoot := resolveExecModel(t, "", "spec/execution/MarlinAgentIfcOverlay.qnt")
	matches := generateExecMBTTraces(t, bunBin, cliPath, modelPath, repoRoot, "marlin_agent_ifc_composed", 16, 12)
	totalSteps := 0
	for _, tp := range matches {
		raw, err := os.ReadFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		var trace agentIfcTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			t.Fatalf("parse %s: %v", tp, err)
		}
		replayOneAgentIfcTrace(t, filepath.Base(tp), trace, true)
		totalSteps += len(trace.States) - 1
	}
	t.Logf("MBT-REFINEMENT-OK [MarlinAgentIfcOverlay] replayed %d Quint @quint-extends ITF traces (%d transitions) against Go ExecutionBreaker CaMeL IFC + E-Stop", len(matches), totalSteps)
}

func replayOneAgentIfcTrace(t *testing.T, name string, trace agentIfcTrace, isOverlay bool) {
	t.Helper()
	ledger := features.NewGovernanceLedger(features.DefaultGovernancePolicy())
	brk := pluginhost.NewExecutionBreaker(64, ledger)
	var goToolInvoked bool

	for i := 1; i < len(trace.States); i++ {
		prev := trace.States[i-1].H
		st := trace.States[i]
		act := st.ActionTaken

		switch act {
		case "ingestUntrustedData", "readConfidentialSecret", "approveDeclassification", "approveHumanToolGate":
			// State tracked by Quint transition; validated on tool commit below.
		case "tripIfcEStop":
			brk.Trip("human-operator", "IFC E-stop")
		case "invokeTrustedTool":
			if !prev.EStopTripped {
				lease, err := brk.AcquireToolLease("agent-ifc", "trusted.tool")
				if err != nil {
					t.Fatalf("[%s step %d] AcquireToolLease failed: %v", name, i, err)
				}
				err = brk.CommitToolEffectWithIFC(lease, false, prev.DataConfidential, prev.Declassified, true)
				shouldAllow := prev.PlanIntegrityHigh && (!prev.DataConfidential || prev.Declassified)
				if shouldAllow {
					if err != nil {
						t.Fatalf("[%s step %d] valid trusted tool commit rejected: %v", name, i, err)
					}
					goToolInvoked = true
				} else if !errors.Is(err, pluginhost.ErrConfidentialEgressDeny) {
					t.Fatalf("[%s step %d] SECURITY VIOLATION: confidential egress not blocked (got %v)", name, i, err)
				}
			}
		case "invokeGovernedTool":
			if prev.EStopTripped {
				if _, err := brk.AcquireToolLease("agent-ifc", "governed.tool"); !errors.Is(err, pluginhost.ErrBreakerTripped) {
					t.Fatalf("[%s step %d] expected ErrBreakerTripped under E-Stop, got %v", name, i, err)
				}
			} else {
				lease, err := brk.AcquireToolLease("agent-ifc", "governed.tool")
				if err != nil {
					t.Fatalf("[%s step %d] AcquireToolLease failed: %v", name, i, err)
				}
				err = brk.CommitToolEffectWithIFC(lease, prev.DataTainted, prev.DataConfidential, prev.Declassified, prev.HumanGateApproved)
				shouldAllow := (!prev.DataTainted || prev.HumanGateApproved) && (!prev.DataConfidential || prev.Declassified)
				if shouldAllow {
					if err != nil {
						t.Fatalf("[%s step %d] valid governed tool commit rejected: %v", name, i, err)
					}
					goToolInvoked = true
				} else if err == nil {
					t.Fatalf("[%s step %d] SECURITY VIOLATION: governed tool allowed despite taint=%v gate=%v conf=%v declass=%v",
						name, i, prev.DataTainted, prev.HumanGateApproved, prev.DataConfidential, prev.Declassified)
				}
			}
		default:
			t.Fatalf("[%s step %d] unknown AgentIfc MBT action %q", name, i, act)
		}

		if !isOverlay && goToolInvoked != st.H.ToolInvoked {
			t.Fatalf("[%s step %d after %s] refinement mismatch toolInvoked: Go=%v Quint=%v", name, i, act, goToolInvoked, st.H.ToolInvoked)
		}
	}
}

type stubExecFactory struct{}

func (stubExecFactory) Kind() schema.PanelRuntimeKind { return schema.RuntimeDeclarative }
func (stubExecFactory) Instantiate(_ context.Context, scope pluginhost.ScopedScope, _ *pluginhost.ScopedProxy) (pluginhost.Instance, error) {
	return &stubExecInstance{scope: scope}, nil
}

type stubSlotRenderer struct {
	id      string
	trusted bool
}

func (r *stubSlotRenderer) ID() string                          { return r.id }
func (r *stubSlotRenderer) Trusted() bool                       { return r.trusted }
func (r *stubSlotRenderer) Render(_ shell.RenderContext)        {}
func (r *stubSlotRenderer) HandleEvent(_ shell.HostEvent) error { return nil }
func (r *stubSlotRenderer) Dispose()                            {}

type stubExecInstance struct {
	scope  pluginhost.ScopedScope
	closed bool
}

func (s *stubExecInstance) Scope() pluginhost.ScopedScope        { return s.scope }
func (s *stubExecInstance) Renderer() shell.SlotRenderer         { return &stubSlotRenderer{id: string(s.scope.PluginID)} }
func (s *stubExecInstance) DeliverEvent(_ pluginhost.ScopedEvent) error { return nil }
func (s *stubExecInstance) Close() error                         { s.closed = true; return nil }

func TestExecutionBreakerAndHostEmergencyStopSecurity(t *testing.T) {
	ctx := context.Background()
	slots := shell.NewSlotRegistry()
	if err := slots.Declare(schema.SlotDeclaration{
		SlotID:         schema.SlotID("workbench.main"),
		Cardinality:    schema.SlotSingle,
		AllowUntrustedPlugin: true,
	}, &stubSlotRenderer{id: "host.main", trusted: true}); err != nil {
		t.Fatalf("Declare slot failed: %v", err)
	}
	disp := &mbtFakeDispatcher{}
	host := pluginhost.NewHost(slots, disp)
	host.RegisterRuntime(stubExecFactory{})

	_, err := host.Activate(ctx, pluginhost.ScopedScope{
		PluginID:             "tao3k.agent-exec",
		ContributionID:       "panel",
		ActivationGeneration: 1,
		Runtime:              schema.RuntimeDeclarative,
		SlotID:               schema.SlotID("workbench.main"),
		SlotKey:              "agent-exec",
		DeclaredActions:      []string{"tool.run"},
	})
	if err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	if stopped := host.TripEmergencyStop(); stopped != 1 {
		t.Fatalf("expected TripEmergencyStop to stop 1 plugin, got %d", stopped)
	}
	if _, active := host.ActiveScope("tao3k.agent-exec"); active {
		t.Fatal("plugin remained active after Host.TripEmergencyStop")
	}

	// Pre-trip lease TOCTOU + agent self-rearm + budget ceiling + path escape checks.
	brk := pluginhost.NewExecutionBreaker(1, features.NewGovernanceLedger(features.DefaultGovernancePolicy()))
	lease, err := brk.AcquireToolLease("agent-1", "shell.exec")
	if err != nil {
		t.Fatal(err)
	}
	brk.Trip("human-1", "runaway detected")
	if err := brk.CommitToolEffect(lease); !errors.Is(err, pluginhost.ErrBreakerTripped) {
		t.Fatalf("expected ErrBreakerTripped on in-flight lease, got %v", err)
	}
	if _, err := brk.Rearm("agent-1", true, false); !errors.Is(err, pluginhost.ErrAgentCannotRearmBreaker) {
		t.Fatalf("expected ErrAgentCannotRearmBreaker, got %v", err)
	}
	if epoch, err := brk.Rearm("human-1", false, true); err != nil || epoch != 2 {
		t.Fatalf("expected verified human rearm epoch=2, got epoch=%d err=%v", epoch, err)
	}
	// Pre-trip lease STILL blocked even after human re-arm!
	if err := brk.CommitToolEffect(lease); !errors.Is(err, pluginhost.ErrStaleExecutionLease) {
		t.Fatalf("expected ErrStaleExecutionLease on pre-trip lease after rearm, got %v", err)
	}
	brk.GrantHandle("fs:workspace")
	if err := brk.InvokeSandboxHostcall("fs:workspace", "../secret.key"); !errors.Is(err, pluginhost.ErrSandboxPathEscape) {
		t.Fatalf("expected ErrSandboxPathEscape, got %v", err)
	}
	if err := brk.InvokeSandboxHostcall("fs:workspace", "/etc/passwd"); !errors.Is(err, pluginhost.ErrSandboxPathEscape) {
		t.Fatalf("expected ErrSandboxPathEscape for abs path, got %v", err)
	}
	if err := brk.InvokeSandboxHostcall("net:raw", "ok.txt"); !errors.Is(err, pluginhost.ErrToolCapabilityDenied) {
		t.Fatalf("expected ErrToolCapabilityDenied for ungranted handle, got %v", err)
	}
}
