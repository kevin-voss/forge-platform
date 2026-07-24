import fs from 'node:fs';
import path from 'node:path';

import { normalizeService } from './findings';

/**
 * Service coverage verification gate (56.02).
 *
 * Expected services live in `services.json` (single source of truth; the
 * markdown matrix documents the same list). On a full suite run (demos 01–05),
 * the union of each product's `demo.json.services` must cover every entry or
 * the orchestrator exits non-zero with a precise uncovered-service message.
 */

export interface CoverageRow {
  service: string;
  status: 'covered' | 'uncovered';
  /** Product ids that listed this service in demo.json.services. */
  products: string[];
}

export interface CoverageRollup {
  rows: CoverageRow[];
  covered: number;
  uncovered: number;
  total: number;
}

export interface CoverageGateResult {
  ok: boolean;
  coverage: CoverageRollup;
  uncoveredServices: string[];
  /** Human-readable line: `coverage: N/N services` or a failure naming gaps. */
  message: string;
}

export interface ServicesFile {
  services: string[];
}

/** Aliases from demo.json.services short names → canonical service labels. */
const COVERAGE_ALIASES: Record<string, string> = {
  postgres: 'managed PostgreSQL',
  postgresql: 'managed PostgreSQL',
  'managed-postgresql': 'managed PostgreSQL',
  'managed postgresql': 'managed PostgreSQL',
  'forge-postgres': 'managed PostgreSQL',
  apply: 'Declarative API (`forge apply`)',
  declarative: 'Declarative API (`forge apply`)',
  'declarative-api': 'Declarative API (`forge apply`)',
  'declarative api': 'Declarative API (`forge apply`)',
  'declarative api (forge apply)': 'Declarative API (`forge apply`)',
};

function e2eRoot(): string {
  return path.resolve(__dirname, '..');
}

function defaultServicesPath(): string {
  return path.join(__dirname, 'services.json');
}

/**
 * Normalize a demo.json service token to a canonical coverage service name.
 * Reuses forge-* prefixing for ordinary services; maps postgres/apply aliases.
 */
export function normalizeCoverageService(service: string): string {
  const trimmed = service.trim();
  if (!trimmed) {
    throw new Error('service is required');
  }
  const lower = trimmed.toLowerCase();
  if (COVERAGE_ALIASES[lower]) {
    return COVERAGE_ALIASES[lower];
  }
  if (trimmed === 'managed PostgreSQL') {
    return trimmed;
  }
  if (trimmed.startsWith('Declarative API')) {
    return 'Declarative API (`forge apply`)';
  }
  return normalizeService(trimmed);
}

/** Load the expected service list from services.json (or an override path). */
export function loadExpectedServices(servicesPath?: string): string[] {
  const file = servicesPath ?? defaultServicesPath();
  const raw = fs.readFileSync(file, 'utf8');
  const parsed = JSON.parse(raw) as ServicesFile;
  if (!parsed || !Array.isArray(parsed.services) || parsed.services.length === 0) {
    throw new Error(`services.json must contain a non-empty services array: ${file}`);
  }
  const services: string[] = [];
  for (const entry of parsed.services) {
    if (typeof entry !== 'string' || !entry.trim()) {
      throw new Error(`invalid service entry in ${file}`);
    }
    const name = entry.trim();
    if (!services.includes(name)) {
      services.push(name);
    }
  }
  return services;
}

/**
 * Parse the primary service table from service-coverage-matrix.md.
 * Used to keep the markdown matrix aligned with services.json.
 */
export function parseMatrixServices(markdown: string): string[] {
  const services: string[] = [];
  let inTable = false;

  for (const line of markdown.split('\n')) {
    if (!inTable) {
      if (/^\|\s*Service\s*\|/i.test(line)) {
        inTable = true;
      }
      continue;
    }
    if (!line.startsWith('|')) {
      break;
    }
    if (/^\|\s*[-:| ]+\|\s*$/.test(line) || line.includes('---')) {
      continue;
    }
    const cells = line.split('|').map((c) => c.trim());
    const name = cells[1];
    if (!name || /^service$/i.test(name)) continue;
    if (!services.includes(name)) {
      services.push(name);
    }
  }

  return services;
}

/** Load expected services from the coverage matrix markdown file (docs sync). */
export function loadMatrixServices(matrixPath: string): string[] {
  const md = fs.readFileSync(matrixPath, 'utf8');
  const services = parseMatrixServices(md);
  if (services.length === 0) {
    throw new Error(`no services parsed from coverage matrix: ${matrixPath}`);
  }
  return services;
}

/**
 * Union demo.json.services across run products vs the expected list;
 * mark each service covered/uncovered.
 */
export function coverageRollup(
  products: Array<{ id: string; services: string[] }>,
  expectedServices: string[],
): CoverageRollup {
  const coveredBy = new Map<string, string[]>();

  for (const product of products) {
    for (const raw of product.services) {
      const service = normalizeCoverageService(raw);
      const list = coveredBy.get(service) ?? [];
      if (!list.includes(product.id)) {
        list.push(product.id);
      }
      coveredBy.set(service, list);
    }
  }

  const rows: CoverageRow[] = expectedServices.map((service) => {
    const productsFor = coveredBy.get(service) ?? [];
    return {
      service,
      status: productsFor.length > 0 ? 'covered' : 'uncovered',
      products: productsFor,
    };
  });

  const covered = rows.filter((r) => r.status === 'covered').length;
  const uncovered = rows.length - covered;

  return {
    rows,
    covered,
    uncovered,
    total: rows.length,
  };
}

/**
 * Verify coverage for a run. Fails when any expected service is uncovered.
 * Message names every uncovered service for a precise gate failure.
 */
export function verifyCoverage(
  products: Array<{ id: string; services: string[] }>,
  expectedServices?: string[],
): CoverageGateResult {
  const expected = expectedServices ?? loadExpectedServices();
  const coverage = coverageRollup(products, expected);
  const uncoveredServices = coverage.rows
    .filter((r) => r.status === 'uncovered')
    .map((r) => r.service);
  const ok = uncoveredServices.length === 0;
  const message = ok
    ? `coverage: ${coverage.covered}/${coverage.total} services`
    : `coverage gate failed: uncovered service(s): ${uncoveredServices.join(', ')}` +
      ` (${coverage.covered}/${coverage.total} covered)`;

  return { ok, coverage, uncoveredServices, message };
}

/** Default suite selectors (`01`–`05`) — coverage gate applies to this full set. */
export const FULL_SUITE_SELECTORS = ['01', '02', '03', '04', '05'] as const;

/**
 * True when PROJECTS selectors include every default-suite product (01–05).
 * Subset runs skip the coverage gate so partial PROJECTS stay useful.
 */
export function isFullSuiteRun(selectors: string[]): boolean {
  const prefixes = new Set(
    selectors
      .map((s) => {
        const match = s.trim().match(/^(\d+)/);
        return match ? String(Number(match[1])).padStart(2, '0') : '';
      })
      .filter(Boolean),
  );
  return FULL_SUITE_SELECTORS.every((s) => prefixes.has(s));
}

/** Render a plain-text coverage table for orchestrator stdout / reports. */
export function formatCoverageTable(coverage: CoverageRollup): string {
  const lines: string[] = [];
  lines.push('| Service | Status | Products |');
  lines.push('|---|---|---|');
  for (const row of coverage.rows) {
    lines.push(
      `| ${row.service} | ${row.status} | ${
        row.products.length > 0 ? row.products.join(', ') : '—'
      } |`,
    );
  }
  lines.push('');
  lines.push(
    `Coverage: ${coverage.covered}/${coverage.total} covered` +
      (coverage.uncovered > 0 ? `, ${coverage.uncovered} uncovered` : ''),
  );
  return `${lines.join('\n')}\n`;
}

/** Repo-relative path helper for tests that need a temp services.json. */
export function writeServicesFile(
  filePath: string,
  services: string[],
): void {
  fs.mkdirSync(path.dirname(filePath), { recursive: true });
  fs.writeFileSync(
    filePath,
    `${JSON.stringify({ services }, null, 2)}\n`,
    'utf8',
  );
}

/** Default path to the checked-in services.json (for tests / docs). */
export function defaultExpectedServicesPath(): string {
  return defaultServicesPath();
}

/** Harness package root (`tests/e2e`). */
export function harnessE2eRoot(): string {
  return e2eRoot();
}
