import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import type { DemoProject } from './demo';
import type { FindingsDocument } from './findings';
import type { OrchestratorResult } from './orchestrator';
import {
  coverageRollup,
  findProductArtifacts,
  loadMatrixServices,
  normalizeCoverageService,
  parseMatrixServices,
  renderReportMarkdown,
  writeReport,
} from './report';

const repoRoot = path.resolve(__dirname, '../../..');
const matrixPath = path.join(
  repoRoot,
  'docs/demo-projects/service-coverage-matrix.md',
);

function sampleProject(
  overrides: Partial<DemoProject> & Pick<DemoProject, 'id' | 'services'>,
): DemoProject {
  return {
    title: overrides.title ?? overrides.id,
    epic: overrides.epic ?? '50',
    deploy: overrides.deploy ?? 'deploy.sh',
    hosts: overrides.hosts ?? [],
    baseURL: overrides.baseURL ?? 'http://example.localhost:4000',
    spec: overrides.spec ?? 'tests/e2e/projects/smoke/spec.ts',
    teardown: overrides.teardown ?? 'teardown.sh',
    ...overrides,
  };
}

function emptyFindings(): FindingsDocument {
  return {
    findings: [
      {
        id: 'F-001',
        service: 'forge-observe',
        demo: '02-snapnote',
        severity: 'major',
        title: 'sample major finding',
        status: 'Open',
        firstSeen: '2026-07-24',
      },
      {
        id: 'F-002',
        service: 'forge-gateway',
        demo: '03-askdocs',
        severity: 'blocker',
        title: 'sample blocker finding',
        status: 'Open',
        firstSeen: '2026-07-24',
      },
    ],
    summary: { total: 2, open: 2, blocker: 1, major: 1, minor: 0 },
    byService: {
      'forge-observe': { open: 1, blocker: 0, major: 1, minor: 0 },
      'forge-gateway': { open: 1, blocker: 1, major: 0, minor: 0 },
    },
    byDemo: {
      '01-taskflow': 0,
      '02-snapnote': 1,
      '03-askdocs': 1,
      '04-orderpipe': 0,
      '05-pulseboard': 0,
    },
  };
}

function mixedResult(): OrchestratorResult {
  return {
    env: {
      headless: true,
      projects: ['01', '02', '03'],
      keep: false,
      findingsOnly: false,
    },
    products: [
      {
        project: sampleProject({
          id: '01-taskflow',
          title: 'TaskFlow',
          services: ['control', 'gateway', 'identity'],
        }),
        outcome: 'passed',
        durationMs: 1200,
      },
      {
        project: sampleProject({
          id: '02-snapnote',
          title: 'SnapNote',
          services: ['control', 'storage', 'events'],
          spec: 'tests/e2e/projects/02-snapnote/spec.ts',
        }),
        outcome: 'degraded',
        durationMs: 3400,
        error: undefined,
      },
      {
        project: sampleProject({
          id: '03-askdocs',
          title: 'AskDocs',
          services: ['models', 'memory'],
          spec: 'tests/e2e/projects/03-askdocs/spec.ts',
        }),
        outcome: 'failed',
        durationMs: 800,
        error: 'playwright exited 1 for 03-askdocs',
      },
    ],
    findings: emptyFindings(),
    ok: false,
    exitCode: 1,
  };
}

test('normalizeCoverageService maps short names and postgres alias', () => {
  assert.equal(normalizeCoverageService('control'), 'forge-control');
  assert.equal(normalizeCoverageService('forge-gateway'), 'forge-gateway');
  assert.equal(normalizeCoverageService('postgres'), 'managed PostgreSQL');
  assert.equal(
    normalizeCoverageService('apply'),
    'Declarative API (`forge apply`)',
  );
});

test('parseMatrixServices reads services from coverage matrix', () => {
  const md = fs.readFileSync(matrixPath, 'utf8');
  const services = parseMatrixServices(md);
  assert.ok(services.includes('forge-control'));
  assert.ok(services.includes('forge-gateway'));
  assert.ok(services.includes('managed PostgreSQL'));
  assert.ok(services.includes('Declarative API (`forge apply`)'));
  assert.equal(services.length, 20);
});

test('coverage rollup marks a service uncovered when no product lists it', () => {
  const expected = [
    'forge-control',
    'forge-gateway',
    'forge-events',
    'forge-models',
  ];
  const rollup = coverageRollup(
    [
      { id: '01-taskflow', services: ['control', 'gateway'] },
      { id: '02-snapnote', services: ['gateway'] },
    ],
    expected,
  );

  assert.equal(rollup.total, 4);
  assert.equal(rollup.covered, 2);
  assert.equal(rollup.uncovered, 2);

  const byService = Object.fromEntries(
    rollup.rows.map((r) => [r.service, r]),
  );
  assert.equal(byService['forge-control']?.status, 'covered');
  assert.deepEqual(byService['forge-control']?.products, ['01-taskflow']);
  assert.equal(byService['forge-gateway']?.status, 'covered');
  assert.deepEqual(byService['forge-gateway']?.products, [
    '01-taskflow',
    '02-snapnote',
  ]);
  assert.equal(byService['forge-events']?.status, 'uncovered');
  assert.deepEqual(byService['forge-events']?.products, []);
  assert.equal(byService['forge-models']?.status, 'uncovered');
});

test('report renders from a fixture result with mixed pass/degraded/fail', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'forge-report-'));
  try {
    const productDir = path.join(
      dir,
      'projects-01-taskflow-spec.ts-example',
    );
    fs.mkdirSync(productDir, { recursive: true });
    fs.writeFileSync(path.join(productDir, 'video.webm'), 'fake-video');
    fs.writeFileSync(path.join(productDir, 'trace.zip'), 'fake-trace');

    const result = mixedResult();
    const written = writeReport(result, {
      artifactsDir: dir,
      markdownPath: path.join(dir, 'report.md'),
      htmlPath: path.join(dir, 'report.html'),
      matrixPath,
    });

    assert.equal(written.coverage.total, loadMatrixServices(matrixPath).length);
    assert.ok(written.coverage.uncovered > 0);

    const md = fs.readFileSync(written.markdownPath, 'utf8');
    assert.match(md, /Platform E2E run report/);
    assert.match(md, /01-taskflow.*passed/s);
    assert.match(md, /02-snapnote.*degraded/s);
    assert.match(md, /03-askdocs.*failed/s);
    assert.match(md, /playwright exited 1 for 03-askdocs/);
    assert.match(md, /Service coverage rollup/);
    assert.match(md, /forge-models.*covered/s);
    assert.match(md, /forge-workflows.*uncovered/);
    assert.match(md, /\[video\]\(\.\/projects-01-taskflow/);
    assert.match(md, /\[trace\]\(\.\/projects-01-taskflow/);
    assert.match(md, /blocker=1/);

    const html = fs.readFileSync(written.htmlPath, 'utf8');
    assert.match(html, /<title>Platform E2E run report<\/title>/);
    assert.match(html, /class="pass">passed/);
    assert.match(html, /class="degraded">degraded/);
    assert.match(html, /class="fail">failed/);
    assert.match(html, /href="\.\/projects-01-taskflow[^"]*video\.webm"/);
    assert.match(html, /class="uncovered">uncovered/);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('findProductArtifacts links video and trace when present', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'forge-arts-'));
  try {
    const nested = path.join(dir, 'projects-smoke-spec.ts-smoke-opens');
    fs.mkdirSync(nested, { recursive: true });
    fs.writeFileSync(path.join(nested, 'video.webm'), 'v');
    fs.writeFileSync(path.join(nested, 'trace.zip'), 't');

    const links = findProductArtifacts(dir, {
      id: '50-e2e-harness',
      spec: 'tests/e2e/projects/smoke/spec.ts',
    });
    assert.ok(links.video?.includes('video.webm'));
    assert.ok(links.trace?.includes('trace.zip'));
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('renderReportMarkdown includes coverage uncovered rows', () => {
  const result = mixedResult();
  const coverage = coverageRollup(
    result.products.map((p) => ({
      id: p.project.id,
      services: p.project.services,
    })),
    ['forge-control', 'forge-workflows'],
  );
  const md = renderReportMarkdown(result, coverage, []);
  assert.match(md, /forge-control \| covered/);
  assert.match(md, /forge-workflows \| uncovered/);
});
