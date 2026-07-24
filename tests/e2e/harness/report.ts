import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

import type { DemoProject } from './demo';
import {
  coverageRollup,
  loadExpectedServices,
  loadMatrixServices,
  normalizeCoverageService,
  parseMatrixServices,
  type CoverageRollup,
  type CoverageRow,
} from './coverage';
import type { FindingsDocument } from './findings';
import type { OrchestratorResult, ProductRunResult } from './orchestrator';

/**
 * Per-run markdown/HTML report + service-coverage rollup.
 * Coverage enforcement on full suite runs is owned by coverage.ts (56.02).
 */

export type { CoverageRollup, CoverageRow };
export {
  coverageRollup,
  loadExpectedServices,
  loadMatrixServices,
  normalizeCoverageService,
  parseMatrixServices,
};

export interface ProductArtifactLinks {
  productId: string;
  /** Paths relative to the artifacts directory. */
  video?: string;
  trace?: string;
  screenshot?: string;
}

export interface ReportPaths {
  markdownPath?: string;
  htmlPath?: string;
  artifactsDir?: string;
  /** Override path to services.json (tests). */
  servicesPath?: string;
  /** @deprecated Prefer servicesPath; kept for matrix-sync tests. */
  matrixPath?: string;
}

export interface WriteReportResult {
  markdownPath: string;
  htmlPath: string;
  coverage: CoverageRollup;
}

function e2eRoot(): string {
  return path.resolve(__dirname, '..');
}

function defaultArtifactsDir(): string {
  return path.join(e2eRoot(), 'artifacts');
}

function walkFiles(dir: string): string[] {
  if (!fs.existsSync(dir)) return [];
  const out: string[] = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...walkFiles(full));
    } else {
      out.push(full);
    }
  }
  return out;
}

/**
 * Locate Playwright video/trace/screenshot artifacts for a product under artifacts/.
 * Matches directories that mention the product id, epic folder, or spec path segment.
 */
export function findProductArtifacts(
  artifactsDir: string,
  product: Pick<DemoProject, 'id' | 'spec'>,
): ProductArtifactLinks {
  const links: ProductArtifactLinks = { productId: product.id };
  if (!fs.existsSync(artifactsDir)) {
    return links;
  }

  const idSlug = product.id.replace(/[^a-zA-Z0-9]+/g, '-');
  const specDir = path.basename(path.dirname(product.spec));
  const specFile = path.basename(product.spec, path.extname(product.spec));

  const dirs = fs
    .readdirSync(artifactsDir, { withFileTypes: true })
    .filter((d) => d.isDirectory())
    .map((d) => d.name);

  // Only link artifacts whose directory clearly matches this product/spec.
  // Do not fall back to unrelated leftover runs (avoids cross-product false links).
  const scored = dirs
    .map((name) => {
      let score = 0;
      if (name.includes(product.id)) score += 3;
      if (name.includes(idSlug)) score += 2;
      if (specDir && name.includes(specDir)) score += 2;
      if (specFile && name.includes(specFile)) score += 1;
      return { name, score };
    })
    .filter((d) => d.score > 0)
    .sort((a, b) => b.score - a.score);

  for (const { name } of scored) {
    const dirPath = path.join(artifactsDir, name);
    const files = walkFiles(dirPath);
    for (const file of files) {
      const rel = path.relative(artifactsDir, file);
      const base = path.basename(file).toLowerCase();
      if (!links.video && (base === 'video.webm' || base.endsWith('.webm'))) {
        links.video = rel;
      } else if (
        !links.trace &&
        (base === 'trace.zip' || base.endsWith('.zip'))
      ) {
        links.trace = rel;
      } else if (
        !links.screenshot &&
        (base.endsWith('.png') || base.endsWith('.jpg') || base.endsWith('.jpeg'))
      ) {
        links.screenshot = rel;
      }
    }
    if (links.video && links.trace) {
      break;
    }
  }

  return links;
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function findingsForProduct(
  findings: FindingsDocument,
  productId: string,
): { blocker: number; major: number; minor: number; total: number } {
  let blocker = 0;
  let major = 0;
  let minor = 0;
  for (const f of findings.findings) {
    if (f.demo !== productId) continue;
    if (f.severity === 'blocker') blocker += 1;
    else if (f.severity === 'major') major += 1;
    else minor += 1;
  }
  return { blocker, major, minor, total: blocker + major + minor };
}

function artifactLinkMd(
  label: string,
  rel: string | undefined,
): string {
  if (!rel) return '—';
  return `[${label}](./${rel.replace(/\\/g, '/')})`;
}

function artifactLinkHtml(
  label: string,
  rel: string | undefined,
): string {
  if (!rel) return '—';
  const href = `./${rel.replace(/\\/g, '/')}`;
  return `<a href="${escapeHtml(href)}">${escapeHtml(label)}</a>`;
}

/** Resolve expected services: services.json by default; matrixPath for legacy tests. */
function resolveExpectedServices(options: ReportPaths): string[] {
  if (options.servicesPath) {
    return loadExpectedServices(options.servicesPath);
  }
  if (options.matrixPath) {
    return loadMatrixServices(options.matrixPath);
  }
  return loadExpectedServices();
}

/** Render markdown report body from an orchestrator result + coverage rollup. */
export function renderReportMarkdown(
  result: OrchestratorResult,
  coverage: CoverageRollup,
  artifacts: ProductArtifactLinks[],
): string {
  const artifactById = new Map(artifacts.map((a) => [a.productId, a]));
  const lines: string[] = [];

  lines.push('# Platform E2E run report');
  lines.push('');
  lines.push(`Generated: ${new Date().toISOString()}`);
  lines.push('');
  lines.push('## Summary');
  lines.push('');
  lines.push(`| Field | Value |`);
  lines.push(`|---|---|`);
  lines.push(`| Overall | ${result.ok ? 'PASS' : 'FAIL'} (exit ${result.exitCode}) |`);
  lines.push(`| Products | ${result.products.length} |`);
  lines.push(
    `| Findings | total=${result.findings.summary.total}, blocker=${result.findings.summary.blocker}, major=${result.findings.summary.major}, minor=${result.findings.summary.minor} |`,
  );
  lines.push(
    `| Headless | ${result.env.headless ? 'yes' : 'no'} |`,
  );
  lines.push(
    `| PROJECTS | ${result.env.projects.length > 0 ? result.env.projects.join(',') : '(all)'} |`,
  );
  lines.push(
    `| Coverage | ${coverage.covered}/${coverage.total} covered` +
      (coverage.uncovered > 0
        ? ` (${coverage.uncovered} uncovered)`
        : '') +
      ` |`,
  );
  lines.push('');

  lines.push('## Products');
  lines.push('');
  lines.push(
    '| Product | Title | Result | Duration | Findings (B/M/m) | Video | Trace |',
  );
  lines.push('|---|---|---|---|---|---|---|');

  for (const product of result.products) {
    const counts = findingsForProduct(result.findings, product.project.id);
    const arts = artifactById.get(product.project.id);
    lines.push(
      `| ${product.project.id} | ${product.project.title} | ${product.outcome} | ${formatDuration(product.durationMs)} | ${counts.blocker}/${counts.major}/${counts.minor} | ${artifactLinkMd('video', arts?.video)} | ${artifactLinkMd('trace', arts?.trace)} |`,
    );
  }
  if (result.products.length === 0) {
    lines.push('| — | — | — | — | — | — | — |');
  }
  lines.push('');

  for (const product of result.products) {
    if (!product.error) continue;
    lines.push(`### ${product.project.id} error`);
    lines.push('');
    lines.push('```');
    lines.push(product.error);
    lines.push('```');
    lines.push('');
  }

  lines.push('## Findings summary');
  lines.push('');
  lines.push(`| Severity | Count |`);
  lines.push(`|---|---:|`);
  lines.push(`| Blocker | ${result.findings.summary.blocker} |`);
  lines.push(`| Major | ${result.findings.summary.major} |`);
  lines.push(`| Minor | ${result.findings.summary.minor} |`);
  lines.push(`| Total | ${result.findings.summary.total} |`);
  lines.push('');

  lines.push('## Service coverage rollup');
  lines.push('');
  lines.push(
    'Union of `demo.json.services` for products in this run vs ' +
      '`tests/e2e/harness/services.json` (see ' +
      '[service-coverage-matrix.md](../../../docs/demo-projects/service-coverage-matrix.md)). ' +
      'Full-suite runs enforce zero uncovered services (56.02).',
  );
  lines.push('');
  lines.push('| Service | Status | Products |');
  lines.push('|---|---|---|');
  for (const row of coverage.rows) {
    lines.push(
      `| ${row.service} | ${row.status} | ${row.products.length > 0 ? row.products.join(', ') : '—'} |`,
    );
  }
  lines.push('');
  lines.push(
    `Coverage: **${coverage.covered}/${coverage.total}** covered` +
      (coverage.uncovered > 0
        ? `, ${coverage.uncovered} uncovered`
        : ''),
  );
  lines.push('');

  return `${lines.join('\n')}\n`;
}

/** Render a self-contained HTML report (same content as markdown). */
export function renderReportHtml(
  result: OrchestratorResult,
  coverage: CoverageRollup,
  artifacts: ProductArtifactLinks[],
): string {
  const artifactById = new Map(artifacts.map((a) => [a.productId, a]));
  const productRows = result.products
    .map((product) => {
      const counts = findingsForProduct(result.findings, product.project.id);
      const arts = artifactById.get(product.project.id);
      const outcomeClass =
        product.outcome === 'passed'
          ? 'pass'
          : product.outcome === 'degraded'
            ? 'degraded'
            : 'fail';
      return `<tr>
  <td>${escapeHtml(product.project.id)}</td>
  <td>${escapeHtml(product.project.title)}</td>
  <td class="${outcomeClass}">${escapeHtml(product.outcome)}</td>
  <td>${escapeHtml(formatDuration(product.durationMs))}</td>
  <td>${counts.blocker}/${counts.major}/${counts.minor}</td>
  <td>${artifactLinkHtml('video', arts?.video)}</td>
  <td>${artifactLinkHtml('trace', arts?.trace)}</td>
</tr>`;
    })
    .join('\n');

  const errorBlocks = result.products
    .filter((p) => p.error)
    .map(
      (p) =>
        `<h3>${escapeHtml(p.project.id)} error</h3><pre>${escapeHtml(p.error!)}</pre>`,
    )
    .join('\n');

  const coverageRows = coverage.rows
    .map(
      (row) => `<tr>
  <td>${escapeHtml(row.service)}</td>
  <td class="${row.status}">${row.status}</td>
  <td>${escapeHtml(row.products.length > 0 ? row.products.join(', ') : '—')}</td>
</tr>`,
    )
    .join('\n');

  const coverageSummary =
    `${coverage.covered}/${coverage.total} covered` +
    (coverage.uncovered > 0 ? ` (${coverage.uncovered} uncovered)` : '');

  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<title>Platform E2E run report</title>
<style>
  :root { color-scheme: light dark; font-family: ui-sans-serif, system-ui, sans-serif; }
  body { margin: 2rem; line-height: 1.45; max-width: 960px; }
  h1, h2 { margin-top: 1.6rem; }
  table { border-collapse: collapse; width: 100%; margin: 0.75rem 0 1.25rem; }
  th, td { border: 1px solid #8884; padding: 0.4rem 0.6rem; text-align: left; }
  th { background: #8881; }
  .pass { color: #0a7; font-weight: 600; }
  .degraded { color: #b80; font-weight: 600; }
  .fail, .uncovered { color: #c33; font-weight: 600; }
  .covered { color: #0a7; }
  pre { background: #8881; padding: 0.75rem; overflow: auto; }
  .meta { color: #666; }
</style>
</head>
<body>
<h1>Platform E2E run report</h1>
<p class="meta">Generated: ${escapeHtml(new Date().toISOString())}</p>

<h2>Summary</h2>
<table>
<tr><th>Field</th><th>Value</th></tr>
<tr><td>Overall</td><td class="${result.ok ? 'pass' : 'fail'}">${result.ok ? 'PASS' : 'FAIL'} (exit ${result.exitCode})</td></tr>
<tr><td>Products</td><td>${result.products.length}</td></tr>
<tr><td>Findings</td><td>total=${result.findings.summary.total}, blocker=${result.findings.summary.blocker}, major=${result.findings.summary.major}, minor=${result.findings.summary.minor}</td></tr>
<tr><td>Headless</td><td>${result.env.headless ? 'yes' : 'no'}</td></tr>
<tr><td>PROJECTS</td><td>${escapeHtml(result.env.projects.length > 0 ? result.env.projects.join(',') : '(all)')}</td></tr>
<tr><td>Coverage</td><td>${escapeHtml(coverageSummary)}</td></tr>
</table>

<h2>Products</h2>
<table>
<tr><th>Product</th><th>Title</th><th>Result</th><th>Duration</th><th>Findings (B/M/m)</th><th>Video</th><th>Trace</th></tr>
${productRows || '<tr><td colspan="7">No products in this run</td></tr>'}
</table>
${errorBlocks}

<h2>Findings summary</h2>
<table>
<tr><th>Severity</th><th>Count</th></tr>
<tr><td>Blocker</td><td>${result.findings.summary.blocker}</td></tr>
<tr><td>Major</td><td>${result.findings.summary.major}</td></tr>
<tr><td>Minor</td><td>${result.findings.summary.minor}</td></tr>
<tr><td>Total</td><td>${result.findings.summary.total}</td></tr>
</table>

<h2>Service coverage rollup</h2>
<p>Union of <code>demo.json.services</code> for products in this run vs
<code>tests/e2e/harness/services.json</code>. Full-suite runs enforce zero uncovered services (56.02).</p>
<table>
<tr><th>Service</th><th>Status</th><th>Products</th></tr>
${coverageRows}
</table>
<p>Coverage: <strong>${coverage.covered}/${coverage.total}</strong> covered${
    coverage.uncovered > 0 ? `, ${coverage.uncovered} uncovered` : ''
  }</p>
</body>
</html>
`;
}

function productsForCoverage(
  products: ProductRunResult[],
): Array<{ id: string; services: string[] }> {
  return products.map((p) => ({
    id: p.project.id,
    services: p.project.services,
  }));
}

/**
 * Write artifacts/report.md + artifacts/report.html from an orchestrator result.
 */
export function writeReport(
  result: OrchestratorResult,
  options: ReportPaths = {},
): WriteReportResult {
  const artifactsDir = options.artifactsDir ?? defaultArtifactsDir();
  fs.mkdirSync(artifactsDir, { recursive: true });

  const markdownPath =
    options.markdownPath ?? path.join(artifactsDir, 'report.md');
  const htmlPath = options.htmlPath ?? path.join(artifactsDir, 'report.html');

  const expected = resolveExpectedServices(options);
  const coverage = coverageRollup(productsForCoverage(result.products), expected);
  const artifacts = result.products.map((p) =>
    findProductArtifacts(artifactsDir, p.project),
  );

  const md = renderReportMarkdown(result, coverage, artifacts);
  const html = renderReportHtml(result, coverage, artifacts);

  fs.mkdirSync(path.dirname(markdownPath), { recursive: true });
  fs.mkdirSync(path.dirname(htmlPath), { recursive: true });
  fs.writeFileSync(markdownPath, md, 'utf8');
  fs.writeFileSync(htmlPath, html, 'utf8');

  return { markdownPath, htmlPath, coverage };
}

/** Open the last HTML report in the platform default browser. */
export function openReport(htmlPath?: string): void {
  const target = htmlPath ?? path.join(defaultArtifactsDir(), 'report.html');
  if (!fs.existsSync(target)) {
    throw new Error(
      `No report at ${target}. Run make test-platform-e2e first.`,
    );
  }

  const platform = process.platform;
  let cmd: string;
  let args: string[];
  if (platform === 'darwin') {
    cmd = 'open';
    args = [target];
  } else if (platform === 'win32') {
    cmd = 'cmd';
    args = ['/c', 'start', '', target];
  } else {
    cmd = 'xdg-open';
    args = [target];
  }

  const child = spawn(cmd, args, {
    detached: true,
    stdio: 'ignore',
  });
  child.unref();
  process.stdout.write(`[report] opened ${target}\n`);
}

async function main(): Promise<void> {
  const args = process.argv.slice(2);
  if (args.includes('--open') || args.includes('open')) {
    openReport();
    return;
  }
  process.stderr.write(
    'Usage: node harness/report.js --open\n' +
      '(Reports are written automatically by the orchestrator after each run.)\n',
  );
  process.exitCode = 1;
}

if (require.main === module) {
  main().catch((err) => {
    process.stderr.write(
      `[report] fatal: ${err instanceof Error ? err.stack ?? err.message : String(err)}\n`,
    );
    process.exitCode = 1;
  });
}
