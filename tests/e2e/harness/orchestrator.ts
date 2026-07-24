import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

import {
  DemoLifecycle,
  DemoValidationError,
  load,
  type DemoProject,
  type LifecycleResult,
  ScriptExecutionError,
} from './demo';
import {
  consolidate,
  loadFindings,
  record,
  type FindingsDocument,
  type FindingsPaths,
  type ProductOutcome,
} from './findings';
import {
  formatCoverageTable,
  isFullSuiteRun,
  loadExpectedServices,
  verifyCoverage,
  type CoverageGateResult,
} from './coverage';
import { stageCiArtifacts } from './artifacts';
import { HostPreflightError, preflightHosts } from './gateway';
import { PreflightError, preflight } from './platform';
import { writeReport } from './report';

/**
 * Platform E2E orchestrator - single entry for `make test-platform-e2e`.
 *
 * Discovers demos/<id>/demo.json, runs each selected product through the lifecycle
 * in numeric order, aggregates pass/degraded/fail + findings, and exits 0 iff
 * every selected product passed or degraded (non-blocker findings), there are
 * zero blocker findings, and (on a full 01–05 suite) every platform service in
 * `services.json` is covered by at least one product's `demo.json.services`.
 *
 * Default (no PROJECTS) runs the five demo products `01`–`05` in order. The
 * harness self-test (`50`) is opt-in via PROJECTS. Degraded (non-blocker) products
 * continue the suite; a failed/blocker product makes the aggregate exit non-zero
 * (stop-eligible) while remaining products still run for a complete rollup.
 *
 * CI semantics (56.04): `CI=1` implies headless + KEEP=0; `CI_SUBSET=1` (with
 * empty PROJECTS) selects the PR gate subset `01,03`; full five is the nightly.
 */

/** Default suite: demos 1→5 (epics 51–55). Harness self-test (50) is opt-in. */
export const DEFAULT_SUITE_SELECTORS = ['01', '02', '03', '04', '05'] as const;

/** PR / CI_SUBSET gate: TaskFlow + AskDocs (mirrors capstone fast-path idea). */
export const CI_SUBSET_PROJECTS = ['01', '03'] as const;

export interface OrchestratorEnv {
  headless: boolean;
  /** Selectors like "01", "50", "01-taskflow". Empty = default suite 01–05. */
  projects: string[];
  keep: boolean;
  /** Skip Playwright browser specs; still run deploy -> seed -> host -> teardown. */
  findingsOnly: boolean;
  /** True when CI=1/true (forces headless + KEEP=0). Set by parseEnv. */
  ci?: boolean;
  /** True when CI_SUBSET=1/true (default PROJECTS → 01,03 when unset). */
  ciSubset?: boolean;
}

export interface ProductRunResult {
  project: DemoProject;
  outcome: ProductOutcome;
  error?: string;
  lifecycle?: LifecycleResult;
  playwrightCode?: number;
  durationMs: number;
}

export interface OrchestratorResult {
  env: OrchestratorEnv;
  products: ProductRunResult[];
  findings: FindingsDocument;
  /** Coverage gate result when a full suite run enforced coverage (56.02). */
  coverage?: CoverageGateResult;
  /** True when every selected product passed and blocker findings == 0. */
  ok: boolean;
  exitCode: number;
}

export interface OrchestratorOptions {
  repoRoot?: string;
  /** Directory containing `<id>/demo.json` products (default: `<repo>/demos`). */
  demosRoot?: string;
  /** Override env parsing (tests). */
  env?: Partial<NodeJS.ProcessEnv> | OrchestratorEnv;
  findingsPaths?: FindingsPaths;
  /** Skip or inject platform preflight (tests). */
  platformPreflight?: () => Promise<void>;
  /** Skip or inject host preflight (tests). */
  hostPreflight?: (project: DemoProject) => Promise<void>;
  /** Inject Playwright runner (tests). */
  runPlaywright?: (project: DemoProject, env: OrchestratorEnv) => Promise<number>;
  /** When true, skip writing artifacts/orchestrator-result.json and report. */
  skipResultWrite?: boolean;
  /** Timeout for deploy/seed/teardown scripts. */
  timeoutMs?: number;
  /**
   * Skip the full-suite coverage gate (tests with fixture demos that do not
   * claim every platform service). Production full runs always enforce.
   */
  skipCoverageGate?: boolean;
  /** Override path to services.json (tests). */
  servicesPath?: string;
}

function e2eRoot(): string {
  return path.resolve(__dirname, '..');
}

function defaultRepoRoot(): string {
  return path.resolve(e2eRoot(), '../..');
}

function truthyEnv(value: string | undefined): boolean {
  return value === '1' || value === 'true';
}

/** Parse HEADLESS / CI / CI_SUBSET / PROJECTS / KEEP / FINDINGS_ONLY from process env. */
export function parseEnv(
  source: NodeJS.ProcessEnv | Partial<NodeJS.ProcessEnv> = process.env,
): OrchestratorEnv {
  const ci = truthyEnv(source.CI);
  const ciSubset = truthyEnv(source.CI_SUBSET);
  const headless = source.HEADLESS === '1' || ci;
  // CI always tears down product stacks — KEEP is forced off.
  const keep = ci ? false : source.KEEP === '1';
  const findingsOnly = source.FINDINGS_ONLY === '1';
  const raw = (source.PROJECTS ?? '').trim();
  let projects = raw
    ? raw
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean)
    : [];
  // PR subset when CI_SUBSET is set and PROJECTS was not explicitly provided.
  if (projects.length === 0 && ciSubset) {
    projects = [...CI_SUBSET_PROJECTS];
  }
  return { headless, projects, keep, findingsOnly, ci, ciSubset };
}

function isOrchestratorEnv(value: unknown): value is OrchestratorEnv {
  return (
    typeof value === 'object' &&
    value !== null &&
    'headless' in value &&
    'projects' in value &&
    'keep' in value &&
    'findingsOnly' in value
  );
}

/** Normalize a partial/test OrchestratorEnv so ci/ciSubset always exist. */
function normalizeEnv(env: OrchestratorEnv): OrchestratorEnv {
  return {
    ...env,
    ci: env.ci ?? false,
    ciSubset: env.ciSubset ?? false,
  };
}

/** Numeric prefix from a product id or selector (`50-e2e-harness` -> 50, `01` -> 1). */
export function numericPrefix(idOrSelector: string): number {
  const match = idOrSelector.match(/^(\d+)/);
  return match ? Number(match[1]) : Number.POSITIVE_INFINITY;
}

/** True when a PROJECTS selector matches a discovered product. */
export function matchesProjectSelector(
  project: DemoProject,
  selector: string,
  demoDirName?: string,
): boolean {
  const sel = selector.trim();
  if (!sel) return false;
  if (project.id === sel) return true;
  if (demoDirName && demoDirName === sel) return true;
  // Bare numeric / zero-padded prefix: "50" or "01" matches "50-e2e-harness" / "01-taskflow".
  if (/^\d+$/.test(sel)) {
    const n = Number(sel);
    if (numericPrefix(project.id) === n) return true;
    if (demoDirName && numericPrefix(demoDirName) === n) return true;
  }
  return false;
}

export interface DiscoveredProduct {
  project: DemoProject;
  demoJsonPath: string;
  /** Directory basename under demosRoot, e.g. `50-e2e-harness`. */
  dirName: string;
}

/**
 * Discover products by scanning `<demosRoot>/<id>/demo.json`, validate, and sort
 * by numeric id prefix ascending.
 */
export function discoverProducts(demosRoot: string): DiscoveredProduct[] {
  if (!fs.existsSync(demosRoot)) {
    return [];
  }
  const entries = fs.readdirSync(demosRoot, { withFileTypes: true });
  const found: DiscoveredProduct[] = [];

  for (const entry of entries) {
    if (!entry.isDirectory()) continue;
    const demoJsonPath = path.join(demosRoot, entry.name, 'demo.json');
    if (!fs.existsSync(demoJsonPath)) continue;
    try {
      const project = load(demoJsonPath);
      found.push({ project, demoJsonPath, dirName: entry.name });
    } catch (err) {
      if (err instanceof DemoValidationError) {
        throw new DemoValidationError(
          `${demoJsonPath}: ${err.message.replace(/^invalid demo\.json:\n/, '')}`,
        );
      }
      throw err;
    }
  }

  found.sort((a, b) => {
    const na = numericPrefix(a.project.id);
    const nb = numericPrefix(b.project.id);
    if (na !== nb) return na - nb;
    return a.project.id.localeCompare(b.project.id);
  });

  return found;
}

/**
 * Resolve PROJECTS selectors. Empty / unset → default suite `01,02,03,04,05`.
 * Explicit PROJECTS (including `50`) overrides the default set.
 */
export function resolveProjectSelectors(projects: string[]): string[] {
  return projects.length > 0 ? [...projects] : [...DEFAULT_SUITE_SELECTORS];
}

/** Filter discovered products by PROJECTS selectors (empty resolved by caller). */
export function selectProducts(
  discovered: DiscoveredProduct[],
  selectors: string[],
): DiscoveredProduct[] {
  if (selectors.length === 0) {
    return selectProducts(discovered, [...DEFAULT_SUITE_SELECTORS]);
  }
  const selected: DiscoveredProduct[] = [];
  const missing: string[] = [];

  for (const sel of selectors) {
    const matches = discovered.filter((d) =>
      matchesProjectSelector(d.project, sel, d.dirName),
    );
    if (matches.length === 0) {
      missing.push(sel);
      continue;
    }
    for (const m of matches) {
      if (!selected.some((s) => s.project.id === m.project.id)) {
        selected.push(m);
      }
    }
  }

  if (missing.length > 0) {
    throw new Error(
      `PROJECTS selectors matched no demo.json products: ${missing.join(', ')}` +
        (discovered.length === 0
          ? ' (no products discovered - add demos/<id>/demo.json)'
          : ` (available: ${discovered.map((d) => d.project.id).join(', ')})`),
    );
  }

  selected.sort((a, b) => {
    const na = numericPrefix(a.project.id);
    const nb = numericPrefix(b.project.id);
    if (na !== nb) return na - nb;
    return a.project.id.localeCompare(b.project.id);
  });

  return selected;
}

function worstOutcome(a: ProductOutcome, b: ProductOutcome): ProductOutcome {
  const rank: Record<ProductOutcome, number> = {
    passed: 0,
    degraded: 1,
    failed: 2,
  };
  return rank[a] >= rank[b] ? a : b;
}

function outcomeFromFindingsForDemo(
  findings: FindingsDocument,
  demoId: string,
): ProductOutcome {
  let outcome: ProductOutcome = 'passed';
  for (const f of findings.findings) {
    if (f.demo !== demoId) continue;
    if (f.severity === 'blocker') outcome = worstOutcome(outcome, 'failed');
    else outcome = worstOutcome(outcome, 'degraded');
  }
  return outcome;
}

async function defaultRunPlaywright(
  project: DemoProject,
  env: OrchestratorEnv,
  repoRoot: string,
): Promise<number> {
  const e2e = e2eRoot();
  const specPath = path.isAbsolute(project.spec)
    ? project.spec
    : path.resolve(repoRoot, project.spec);

  // Playwright testMatch is projects/**/spec.ts - pass the relative path from e2e root.
  let relSpec = path.relative(e2e, specPath);
  if (relSpec.startsWith('..')) {
    // Spec outside tests/e2e - still try absolute via cwd-relative path.
    relSpec = specPath;
  }

  const childEnv: NodeJS.ProcessEnv = {
    ...process.env,
    HEADLESS: env.headless ? '1' : '0',
    KEEP: env.keep ? '1' : '0',
  };

  const playwrightBin = path.join(e2e, 'node_modules/.bin/playwright');

  return new Promise((resolve, reject) => {
    const child = spawn(
      playwrightBin,
      ['test', relSpec, '--config', 'playwright.config.ts'],
      {
        cwd: e2e,
        env: childEnv,
        stdio: ['ignore', 'pipe', 'pipe'],
      },
    );
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (c: Buffer | string) => {
      stdout += c.toString();
      process.stdout.write(c);
    });
    child.stderr.on('data', (c: Buffer | string) => {
      stderr += c.toString();
      process.stderr.write(c);
    });
    child.on('error', reject);
    child.on('close', (code) => {
      if (code === null) {
        reject(new Error(`playwright killed for ${project.id}`));
        return;
      }
      if (code !== 0 && !stdout && !stderr) {
        process.stderr.write(
          `playwright exited ${code} for ${project.id} with no output\n`,
        );
      }
      resolve(code);
    });
  });
}

async function runOneProduct(
  discovered: DiscoveredProduct,
  env: OrchestratorEnv,
  options: OrchestratorOptions,
  repoRoot: string,
): Promise<ProductRunResult> {
  const started = Date.now();
  const { project } = discovered;
  let outcome: ProductOutcome = 'passed';
  let error: string | undefined;
  let lifecycle: LifecycleResult | undefined;
  let playwrightCode: number | undefined;

  const hostPreflight =
    options.hostPreflight ??
    ((p: DemoProject) => preflightHosts(p.hosts).then(() => undefined));
  const runPw =
    options.runPlaywright ??
    ((p, e) => defaultRunPlaywright(p, e, repoRoot));

  const lifecycleRunner = new DemoLifecycle(project, {
    repoRoot,
    keep: env.keep,
    timeoutMs: options.timeoutMs,
    env: {
      ...process.env,
      HEADLESS: env.headless ? '1' : '0',
      KEEP: env.keep ? '1' : '0',
      FINDINGS_ONLY: env.findingsOnly ? '1' : '0',
    },
  });

  try {
    await lifecycleRunner.up();
    await lifecycleRunner.seed();

    try {
      await hostPreflight(project);
    } catch (err) {
      outcome = 'failed';
      error =
        err instanceof HostPreflightError
          ? err.message
          : err instanceof Error
            ? err.message
            : String(err);
      if (err instanceof HostPreflightError) {
        record(
          {
            service: 'forge-gateway',
            demo: project.id,
            severity: 'blocker',
            title: `Gateway host preflight failed for ${project.id}`,
            tested: 'preflightHosts(demo.json hosts)',
            expected: 'each host returns expected status',
            actual: err.message,
            repro: [
              `make test-platform-e2e PROJECTS=${numericPrefix(project.id)}`,
            ],
          },
          options.findingsPaths ?? {},
        );
      }
    }

    if (outcome !== 'failed' && !env.findingsOnly) {
      playwrightCode = await runPw(project, env);
      if (playwrightCode !== 0) {
        outcome = 'failed';
        error = `playwright exited ${playwrightCode} for ${project.id}`;
      }
    }

    const findingsDoc = loadFindings(options.findingsPaths ?? {});
    outcome = worstOutcome(
      outcome,
      outcomeFromFindingsForDemo(findingsDoc, project.id),
    );
  } catch (err) {
    outcome = 'failed';
    if (err instanceof ScriptExecutionError) {
      error = err.message;
    } else if (err instanceof Error) {
      error = err.message;
    } else {
      error = String(err);
    }
  } finally {
    if (!env.keep) {
      try {
        await lifecycleRunner.down();
      } catch (teardownErr) {
        const msg =
          teardownErr instanceof Error
            ? teardownErr.message
            : String(teardownErr);
        error = error ? `${error}\nteardown: ${msg}` : `teardown: ${msg}`;
        outcome = 'failed';
      }
    }
    lifecycle = {
      project,
      baseURL: project.baseURL,
      steps: [...lifecycleRunner.steps],
    };
  }

  return {
    project,
    outcome,
    error,
    lifecycle,
    playwrightCode,
    durationMs: Date.now() - started,
  };
}

/**
 * Run the full orchestrator: preflight -> selected products -> aggregate exit.
 */
export async function runOrchestrator(
  options: OrchestratorOptions = {},
): Promise<OrchestratorResult> {
  const repoRoot = options.repoRoot ?? defaultRepoRoot();
  const demosRoot = options.demosRoot ?? path.join(repoRoot, 'demos');
  const env = normalizeEnv(
    isOrchestratorEnv(options.env)
      ? options.env
      : parseEnv((options.env as NodeJS.ProcessEnv | undefined) ?? process.env),
  );

  const discovered = discoverProducts(demosRoot);
  const selectors = resolveProjectSelectors(env.projects);
  const selected = selectProducts(discovered, selectors);

  const platformPreflight =
    options.platformPreflight ?? (() => preflight({ repoRoot }));

  try {
    await platformPreflight();
  } catch (err) {
    if (err instanceof PreflightError) {
      record(
        {
          service: err.finding.service,
          demo: err.finding.demo,
          severity: err.finding.severity,
          title: err.finding.title,
          tested: err.finding.tested,
          expected: err.finding.expected,
          actual: err.finding.actual,
          repro: err.finding.repro,
        },
        options.findingsPaths ?? {},
      );
    } else {
      const actual = err instanceof Error ? err.message : String(err);
      record(
        {
          service: 'platform',
          demo: 'harness',
          severity: 'blocker',
          title: 'Platform preflight failed - local infra not healthy',
          tested: 'orchestrator platform preflight',
          expected: 'platform healthy',
          actual,
          repro: ['make dev', 'make test-platform-e2e'],
        },
        options.findingsPaths ?? {},
      );
    }

    // 56.03: consolidate even on preflight failure so tables/triage stay accurate.
    const findings = consolidate(options.findingsPaths ?? {}).document;
    const result: OrchestratorResult = {
      env,
      products: [],
      findings,
      ok: false,
      exitCode: 1,
    };
    writeResultArtifact(result, options);
    return result;
  }

  process.stdout.write(
    `[orchestrator] suite: ${selected.map((p) => p.project.id).join(', ')}` +
      ` (selectors=${selectors.join(',')})\n`,
  );

  const products: ProductRunResult[] = [];
  for (const product of selected) {
    const run = await runOneProduct(product, env, options, repoRoot);
    products.push(run);
    const status =
      run.outcome === 'passed'
        ? 'PASS'
        : run.outcome === 'degraded'
          ? 'DEGRADED'
          : 'FAIL';
    process.stdout.write(
      `[orchestrator] ${run.project.id}: ${status}` +
        (run.error ? ` - ${run.error.split('\n')[0]}` : '') +
        ` (${run.durationMs}ms)\n`,
    );
    // Continue on degraded; keep running after fail so the aggregate rollup is
    // complete. Aggregate exit below is stop-eligible when any product fails
    // or blocker findings remain.
  }

  // 56.03: dedupe/rank/group, refresh PLATFORM_FINDINGS.md tables + triage list.
  const findings = consolidate(options.findingsPaths ?? {}).document;
  // Non-blocker findings mark a product degraded but must not fail the suite
  // (e2e-harness.md §3). Only failed outcomes / blocker findings exit non-zero.
  const productsSucceeded = products.every(
    (p) => p.outcome === 'passed' || p.outcome === 'degraded',
  );
  const noBlockers = findings.summary.blocker === 0;

  // 56.02: on a full 01–05 suite, every services.json entry must be covered.
  let coverage: CoverageGateResult | undefined;
  const enforceCoverage =
    !options.skipCoverageGate && isFullSuiteRun(selectors) && products.length > 0;
  if (enforceCoverage) {
    const expected = loadExpectedServices(options.servicesPath);
    coverage = verifyCoverage(
      products.map((p) => ({
        id: p.project.id,
        services: p.project.services,
      })),
      expected,
    );
    process.stdout.write(`[orchestrator] ${coverage.message}\n`);
    process.stdout.write(formatCoverageTable(coverage.coverage));
  }

  const coverageOk = coverage ? coverage.ok : true;
  const ok =
    productsSucceeded && noBlockers && products.length > 0 && coverageOk;
  const result: OrchestratorResult = {
    env,
    products,
    findings,
    coverage,
    ok,
    exitCode: ok ? 0 : 1,
  };

  const rollup = products
    .map((p) => {
      const tag =
        p.outcome === 'passed'
          ? 'PASS'
          : p.outcome === 'degraded'
            ? 'DEGRADED'
            : 'FAIL';
      return `${p.project.id}=${tag}`;
    })
    .join(' ');
  process.stdout.write(
    `[orchestrator] aggregate: ${rollup || '(none)'}` +
      ` blockers=${findings.summary.blocker}` +
      (coverage
        ? ` coverage=${coverage.coverage.covered}/${coverage.coverage.total}`
        : '') +
      ` exit=${result.exitCode}\n`,
  );

  writeResultArtifact(result, options);
  return result;
}

function writeResultArtifact(
  result: OrchestratorResult,
  options: OrchestratorOptions,
): void {
  if (options.skipResultWrite) return;
  const artifactsDir = path.join(e2eRoot(), 'artifacts');
  const outPath = path.join(artifactsDir, 'orchestrator-result.json');
  fs.mkdirSync(artifactsDir, { recursive: true });
  const report = writeReport(result, {
    artifactsDir,
    servicesPath: options.servicesPath,
  });
  const serializable = {
    ok: result.ok,
    exitCode: result.exitCode,
    env: result.env,
    products: result.products.map((p) => ({
      id: p.project.id,
      title: p.project.title,
      outcome: p.outcome,
      error: p.error,
      playwrightCode: p.playwrightCode,
      durationMs: p.durationMs,
      services: p.project.services,
      steps: p.lifecycle?.steps.map((s) => ({
        command: s.command,
        code: s.code,
        durationMs: s.durationMs,
      })),
    })),
    findingsSummary: result.findings.summary,
    coverage: {
      covered: report.coverage.covered,
      uncovered: report.coverage.uncovered,
      total: report.coverage.total,
      ok: result.coverage ? result.coverage.ok : undefined,
      message: result.coverage?.message,
      uncoveredServices: result.coverage?.uncoveredServices,
    },
    report: {
      markdown: path.relative(e2eRoot(), report.markdownPath),
      html: path.relative(e2eRoot(), report.htmlPath),
    },
  };
  fs.writeFileSync(outPath, `${JSON.stringify(serializable, null, 2)}\n`, 'utf8');
  process.stdout.write(
    `[orchestrator] report: ${report.markdownPath}` +
      ` (coverage ${report.coverage.covered}/${report.coverage.total})\n`,
  );

  // 56.04: on CI, stage upload bundle (findings copy + manifest) under artifacts/.
  if (result.env.ci) {
    const findingsMarkdownPath =
      options.findingsPaths?.markdownPath ??
      path.join(
        options.repoRoot ?? defaultRepoRoot(),
        'docs/demo-projects/PLATFORM_FINDINGS.md',
      );
    const bundle = stageCiArtifacts({
      artifactsDir,
      findingsMarkdownPath,
      repoRoot: options.repoRoot ?? defaultRepoRoot(),
    });
    process.stdout.write(
      `[orchestrator] ci-artifacts: staged ${bundle.relativePaths.length} files` +
        ` under ${bundle.artifactsDir} (manifest=${path.basename(bundle.manifestPath)})\n`,
    );
  }
}

async function main(): Promise<void> {
  const result = await runOrchestrator();
  process.stdout.write(
    `[orchestrator] done: exit=${result.exitCode} products=${result.products.length}` +
      ` blockers=${result.findings.summary.blocker}\n`,
  );
  process.exitCode = result.exitCode;
}

if (require.main === module) {
  main().catch((err) => {
    process.stderr.write(
      `[orchestrator] fatal: ${err instanceof Error ? err.stack ?? err.message : String(err)}\n`,
    );
    process.exitCode = 1;
  });
}
