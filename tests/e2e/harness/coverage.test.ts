import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { discoverProducts, runOrchestrator } from './orchestrator';
import {
  coverageRollup,
  defaultExpectedServicesPath,
  formatCoverageTable,
  isFullSuiteRun,
  loadExpectedServices,
  loadMatrixServices,
  normalizeCoverageService,
  parseMatrixServices,
  verifyCoverage,
  writeServicesFile,
} from './coverage';
import { load } from './demo';

const e2eRoot = path.resolve(__dirname, '..');
const repoRoot = path.resolve(e2eRoot, '../..');
const matrixPath = path.join(
  repoRoot,
  'docs/demo-projects/service-coverage-matrix.md',
);
const realDemosRoot = path.join(repoRoot, 'demos');

function tempFindingsPaths(): {
  markdownPath: string;
  jsonPath: string;
  dir: string;
} {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'forge-cov-findings-'));
  const markdownPath = path.join(dir, 'PLATFORM_FINDINGS.md');
  const jsonPath = path.join(dir, 'findings.json');
  fs.copyFileSync(
    path.join(repoRoot, 'docs/demo-projects/PLATFORM_FINDINGS.md'),
    markdownPath,
  );
  return { markdownPath, jsonPath, dir };
}

/** Copy demos/51–55 demo.json trees into a temp demos root for mutation tests. */
function cloneRealDemoProducts(destRoot: string): void {
  const sources = [
    '51-taskflow',
    '52-snapnote',
    '53-askdocs',
    '54-orderpipe',
    '55-pulseboard',
  ];
  for (const name of sources) {
    const srcDir = path.join(realDemosRoot, name);
    const destDir = path.join(destRoot, name);
    fs.mkdirSync(destDir, { recursive: true });
    fs.copyFileSync(
      path.join(srcDir, 'demo.json'),
      path.join(destDir, 'demo.json'),
    );
    fs.writeFileSync(
      path.join(destDir, 'noop.sh'),
      '#!/bin/sh\nexit 0\n',
      { mode: 0o755 },
    );
  }
}

function rewriteDemoJson(
  demosRoot: string,
  dirName: string,
  mutate: (demo: Record<string, unknown>) => void,
): void {
  const file = path.join(demosRoot, dirName, 'demo.json');
  const demo = JSON.parse(fs.readFileSync(file, 'utf8')) as Record<
    string,
    unknown
  >;
  mutate(demo);
  // Point deploy/seed/teardown at local no-ops so lifecycle stubs are unused.
  demo.deploy = path.relative(repoRoot, path.join(demosRoot, dirName, 'noop.sh'));
  if (demo.seed) {
    demo.seed = path.relative(repoRoot, path.join(demosRoot, dirName, 'noop.sh'));
  }
  demo.teardown = path.relative(
    repoRoot,
    path.join(demosRoot, dirName, 'noop.sh'),
  );
  fs.writeFileSync(file, `${JSON.stringify(demo, null, 2)}\n`, 'utf8');
}

test('services.json lists 20 canonical platform services', () => {
  const services = loadExpectedServices(defaultExpectedServicesPath());
  assert.equal(services.length, 20);
  assert.ok(services.includes('forge-control'));
  assert.ok(services.includes('forge-workflows'));
  assert.ok(services.includes('managed PostgreSQL'));
  assert.ok(services.includes('Declarative API (`forge apply`)'));
});

test('services.json matches service-coverage-matrix.md table', () => {
  const fromJson = loadExpectedServices();
  const fromMatrix = loadMatrixServices(matrixPath);
  assert.deepEqual(fromJson, fromMatrix);
});

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
  assert.equal(services.length, 20);
});

test('real demos 01–05 cover every services.json entry', () => {
  const products = discoverProducts(realDemosRoot)
    .filter((d) => {
      const n = Number(d.project.id.match(/^(\d+)/)?.[1] ?? NaN);
      return n >= 1 && n <= 5;
    })
    .map((d) => ({ id: d.project.id, services: d.project.services }));

  assert.equal(products.length, 5);
  const gate = verifyCoverage(products);
  assert.equal(gate.ok, true, gate.message);
  assert.equal(gate.uncoveredServices.length, 0);
  assert.match(gate.message, /^coverage: 20\/20 services$/);
});

test('removing a service from all demo.jsons fails the gate naming that service', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'forge-cov-gap-'));
  try {
    cloneRealDemoProducts(dir);
    const dirs = [
      '51-taskflow',
      '52-snapnote',
      '53-askdocs',
      '54-orderpipe',
      '55-pulseboard',
    ];
    for (const name of dirs) {
      rewriteDemoJson(dir, name, (demo) => {
        const services = (demo.services as string[]).filter(
          (s) =>
            s !== 'workflows' &&
            s !== 'forge-workflows' &&
            normalizeCoverageService(s) !== 'forge-workflows',
        );
        demo.services = services;
      });
    }

    const products = discoverProducts(dir).map((d) => ({
      id: d.project.id,
      services: d.project.services,
    }));
    const gate = verifyCoverage(products);
    assert.equal(gate.ok, false);
    assert.ok(gate.uncoveredServices.includes('forge-workflows'));
    assert.match(
      gate.message,
      /coverage gate failed: uncovered service\(s\): forge-workflows/,
    );
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('isFullSuiteRun true for default 01–05 selectors', () => {
  assert.equal(isFullSuiteRun(['01', '02', '03', '04', '05']), true);
  assert.equal(isFullSuiteRun(['01-taskflow', '02', '03', '04', '05']), true);
  assert.equal(isFullSuiteRun(['01', '02', '03']), false);
  assert.equal(isFullSuiteRun(['50']), false);
});

test('formatCoverageTable includes uncovered rows', () => {
  const rollup = coverageRollup(
    [{ id: '01-taskflow', services: ['control'] }],
    ['forge-control', 'forge-events'],
  );
  const table = formatCoverageTable(rollup);
  assert.match(table, /forge-control \| covered/);
  assert.match(table, /forge-events \| uncovered/);
  assert.match(table, /Coverage: 1\/2 covered, 1 uncovered/);
});

test('full suite orchestrator fails when a service is uncovered', async () => {
  const findings = tempFindingsPaths();
  const demosDir = fs.mkdtempSync(path.join(os.tmpdir(), 'forge-cov-orch-'));
  const servicesPath = path.join(demosDir, 'services.json');
  try {
    cloneRealDemoProducts(demosDir);
    for (const name of [
      '51-taskflow',
      '52-snapnote',
      '53-askdocs',
      '54-orderpipe',
      '55-pulseboard',
    ]) {
      rewriteDemoJson(demosDir, name, (demo) => {
        demo.services = (demo.services as string[]).filter(
          (s) => normalizeCoverageService(s) !== 'forge-network',
        );
      });
    }
    // Small expected list so we only need to prove the gap fails the suite.
    writeServicesFile(servicesPath, [
      'forge-control',
      'forge-cli',
      'forge-runtime',
      'forge-gateway',
      'forge-build',
      'forge-network',
    ]);

    const result = await runOrchestrator({
      repoRoot,
      demosRoot: demosDir,
      env: {
        headless: true,
        projects: [],
        keep: false,
        findingsOnly: true,
      },
      findingsPaths: {
        markdownPath: findings.markdownPath,
        jsonPath: findings.jsonPath,
      },
      platformPreflight: async () => undefined,
      hostPreflight: async () => undefined,
      skipResultWrite: true,
      servicesPath,
      timeoutMs: 10_000,
    });

    assert.equal(result.exitCode, 1);
    assert.equal(result.ok, false);
    assert.ok(result.coverage);
    assert.equal(result.coverage!.ok, false);
    assert.ok(result.coverage!.uncoveredServices.includes('forge-network'));
    assert.match(result.coverage!.message, /forge-network/);
  } finally {
    fs.rmSync(findings.dir, { recursive: true, force: true });
    fs.rmSync(demosDir, { recursive: true, force: true });
  }
});

test('full suite orchestrator passes when all expected services are covered', async () => {
  const findings = tempFindingsPaths();
  const demosDir = fs.mkdtempSync(path.join(os.tmpdir(), 'forge-cov-pass-'));
  const servicesPath = path.join(demosDir, 'services.json');
  try {
    cloneRealDemoProducts(demosDir);
    for (const name of [
      '51-taskflow',
      '52-snapnote',
      '53-askdocs',
      '54-orderpipe',
      '55-pulseboard',
    ]) {
      rewriteDemoJson(demosDir, name, () => undefined);
    }
    writeServicesFile(servicesPath, loadExpectedServices());

    const result = await runOrchestrator({
      repoRoot,
      demosRoot: demosDir,
      env: {
        headless: true,
        projects: [],
        keep: false,
        findingsOnly: true,
      },
      findingsPaths: {
        markdownPath: findings.markdownPath,
        jsonPath: findings.jsonPath,
      },
      platformPreflight: async () => undefined,
      hostPreflight: async () => undefined,
      skipResultWrite: true,
      servicesPath,
      timeoutMs: 10_000,
    });

    assert.equal(
      result.exitCode,
      0,
      result.coverage?.message ?? 'expected coverage pass',
    );
    assert.equal(result.ok, true);
    assert.ok(result.coverage);
    assert.equal(result.coverage!.ok, true);
    assert.match(result.coverage!.message, /^coverage: 20\/20 services$/);
  } finally {
    fs.rmSync(findings.dir, { recursive: true, force: true });
    fs.rmSync(demosDir, { recursive: true, force: true });
  }
});

test('subset PROJECTS skips coverage gate even with gaps', async () => {
  const findings = tempFindingsPaths();
  const fixturesDemos = path.join(e2eRoot, 'fixtures/demos');
  try {
    const result = await runOrchestrator({
      repoRoot,
      demosRoot: fixturesDemos,
      env: {
        headless: true,
        projects: ['01'],
        keep: false,
        findingsOnly: true,
      },
      findingsPaths: {
        markdownPath: findings.markdownPath,
        jsonPath: findings.jsonPath,
      },
      platformPreflight: async () => undefined,
      hostPreflight: async () => undefined,
      skipResultWrite: true,
      timeoutMs: 10_000,
    });

    // PROJECTS=01 is not a full suite → coverage gate not applied.
    assert.equal(result.coverage, undefined);
    assert.equal(result.exitCode, 0);
  } finally {
    fs.rmSync(findings.dir, { recursive: true, force: true });
  }
});

test('load fixture demo.json still validates services field', () => {
  const project = load(
    path.join(e2eRoot, 'fixtures/demos/01-alpha/demo.json'),
  );
  assert.ok(project.services.includes('gateway'));
});
