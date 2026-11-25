// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

/**
 * Declarative performance-budget "test macro" for Marlin TypeScript plugins.
 *
 * Allows both human developers and coding LLMs to attach deterministic latency
 * budgets and actionable remediation hints to plugin hot paths (filtering,
 * scheduling, state projection, virtual-list prep).
 *
 * Compatible with both `bun test` (via `runPluginPerfSuite`) and the engine
 * CLI (`marlin-mygo-tool bench-plugin -dir . -json` via `runPluginPerfCLI`).
 */

export interface PluginPerfCaseSpec {
  name: string;
  /** Median (p50) latency ceiling in microseconds. */
  budgetUs: number;
  /** Optional p95 latency ceiling in microseconds (defaults to 3x budgetUs). */
  p95BudgetUs?: number;
  /** Warmup iterations excluded from timings (defaults to 20). */
  warmup?: number;
  /** Measured iterations (defaults to 200). */
  iterations?: number;
  /** Actionable optimization guidance emitted when this budget is violated. */
  remediation: string;
  /** Hot-path callback under test. */
  run: () => void;
}

export interface PluginPerfSuiteSpec {
  pluginId: string;
  cases: PluginPerfCaseSpec[];
}

export interface PluginPerfCaseResult {
  name: string;
  measuredUs: number;
  p95Us: number;
  budgetUs: number;
  p95BudgetUs: number;
  opsPerSec: number;
  iterations: number;
  passed: boolean;
  remediation?: string;
}

export interface PluginPerfSuiteReport {
  pluginId: string;
  passed: boolean;
  cases: PluginPerfCaseResult[];
}

export interface PluginPerfRunOptions {
  /** Latency multiplier for slower CI runners (default 1.0). */
  scale?: number;
  /** Optional iteration count override. */
  iterationsOverride?: number;
}

export interface DefinedPluginPerfSuite {
  readonly spec: PluginPerfSuiteSpec;
  run(opts?: PluginPerfRunOptions): PluginPerfSuiteReport;
}

function nowNanoseconds(): number {
  const g = globalThis as unknown as { Bun?: { nanoseconds?: () => number } };
  if (typeof g.Bun?.nanoseconds === "function") {
    return g.Bun.nanoseconds();
  }
  return performance.now() * 1_000_000;
}

function percentile(sorted: Float64Array, p: number): number {
  if (sorted.length === 0) return 0;
  const idx = Math.min(sorted.length - 1, Math.max(0, Math.floor((sorted.length - 1) * p)));
  return sorted[idx];
}

export function runPluginPerfSuite(
  spec: PluginPerfSuiteSpec,
  opts: PluginPerfRunOptions = {},
): PluginPerfSuiteReport {
  const scale = opts.scale && opts.scale > 0 ? opts.scale : 1.0;
  const results: PluginPerfCaseResult[] = [];
  let allPassed = true;

  for (const c of spec.cases) {
    const warmup = Math.max(1, c.warmup ?? 20);
    const iters = Math.max(5, opts.iterationsOverride ?? c.iterations ?? 200);

    for (let i = 0; i < warmup; i++) {
      c.run();
    }

    const samplesNs = new Float64Array(iters);
    let totalNs = 0;
    for (let i = 0; i < iters; i++) {
      const t0 = nowNanoseconds();
      c.run();
      const dt = Math.max(0, nowNanoseconds() - t0);
      samplesNs[i] = dt;
      totalNs += dt;
    }

    samplesNs.sort();
    const p50Us = percentile(samplesNs, 0.5) / 1000;
    const p95Us = percentile(samplesNs, 0.95) / 1000;
    const effBudgetUs = c.budgetUs * scale;
    const effP95BudgetUs = (c.p95BudgetUs ?? c.budgetUs * 3) * scale;
    const passed = p50Us <= effBudgetUs && p95Us <= effP95BudgetUs;
    if (!passed) {
      allPassed = false;
    }
    const meanSec = totalNs > 0 ? totalNs / iters / 1e9 : 1e-9;

    results.push({
      name: c.name,
      measuredUs: Number(p50Us.toFixed(2)),
      p95Us: Number(p95Us.toFixed(2)),
      budgetUs: Number(effBudgetUs.toFixed(2)),
      p95BudgetUs: Number(effP95BudgetUs.toFixed(2)),
      opsPerSec: Math.round(1 / meanSec),
      iterations: iters,
      passed,
      remediation: passed ? undefined : c.remediation,
    });
  }

  return {
    pluginId: spec.pluginId,
    passed: allPassed,
    cases: results,
  };
}

/**
 * Declares a reusable plugin performance suite that runs both inside `bun test`
 * and via `marlin-mygo-tool bench-plugin`.
 */
export function definePluginPerfSuite(spec: PluginPerfSuiteSpec): DefinedPluginPerfSuite {
  return {
    spec,
    run(opts?: PluginPerfRunOptions): PluginPerfSuiteReport {
      return runPluginPerfSuite(spec, opts);
    },
  };
}

/**
 * One-line performance assertion macro for standard `bun test` suites.
 * Throws a structured `[MARLIN_PERF_VIOLATION]` JSON diagnostic on budget failure
 * so coding agents can inspect measured vs budget and apply remediation directly.
 */
export function assertPerfBudget(
  name: string,
  budgetUs: number,
  run: () => void,
  remediation: string,
  opts: { iterations?: number; scale?: number; p95BudgetUs?: number } = {},
): PluginPerfCaseResult {
  const rep = runPluginPerfSuite(
    {
      pluginId: "inline",
      cases: [
        {
          name,
          budgetUs,
          p95BudgetUs: opts.p95BudgetUs,
          iterations: opts.iterations,
          remediation,
          run,
        },
      ],
    },
    { scale: opts.scale },
  );
  const res = rep.cases[0];
  if (!res.passed) {
    throw new Error(`[MARLIN_PERF_VIOLATION] ${JSON.stringify(res)}`);
  }
  return res;
}

/**
 * CLI entrypoint for a plugin's `src/perf.ts` file (`if (import.meta.main) runPluginPerfCLI(perfSuite)`).
 * Supports `--json`, `--scale=<float>`, and `--iterations=<int>` invoked by `marlin-mygo-tool bench-plugin`.
 */
export function runPluginPerfCLI(suite: DefinedPluginPerfSuite, argv?: string[]): PluginPerfSuiteReport {
  const proc = globalThis as unknown as {
    process?: { argv?: string[]; exitCode?: number };
  };
  const args = argv ?? proc.process?.argv?.slice(2) ?? [];
  let asJSON = false;
  let scale = 1.0;
  let iterationsOverride: number | undefined;

  for (const arg of args) {
    if (arg === "--json") {
      asJSON = true;
    } else if (arg.startsWith("--scale=")) {
      const v = Number(arg.slice("--scale=".length));
      if (Number.isFinite(v) && v > 0) scale = v;
    } else if (arg.startsWith("--iterations=")) {
      const v = Number(arg.slice("--iterations=".length));
      if (Number.isInteger(v) && v >= 5) iterationsOverride = v;
    }
  }

  const report = suite.run({ scale, iterationsOverride });
  if (asJSON) {
    console.log(JSON.stringify(report, null, 2));
  } else {
    console.log(`Marlin TS Plugin Perf Suite (${report.pluginId})`);
    for (const c of report.cases) {
      const status = c.passed ? "PASS" : "FAIL";
      console.log(
        `  [${status}] ${c.name.padEnd(36)} p50=${c.measuredUs.toFixed(1)}us (budget<=${c.budgetUs.toFixed(1)}us) p95=${c.p95Us.toFixed(1)}us`,
      );
      if (!c.passed && c.remediation) {
        console.log(`         hint: ${c.remediation}`);
      }
    }
  }
  if (!report.passed && proc.process) {
    proc.process.exitCode = 1;
  }
  return report;
}
