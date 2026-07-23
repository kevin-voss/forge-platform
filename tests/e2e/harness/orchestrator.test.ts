import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import {
  discoverProducts,
  matchesProjectSelector,
  numericPrefix,
  parseEnv,
  runOrchestrator,
  selectProducts,
} from './orchestrator';
import { load } from './demo';

const e2eRoot = path.resolve(__dirname, '..');
const repoRoot = path.resolve(e2eRoot, '../..');
const fixturesDemos = path.join(e2eRoot, 'fixtures/demos');

function tempFindingsPaths(): { markdownPath: string; jsonPath: string; dir: string } {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'forge-orch-findings-'));
  const markdownPath = path.join(dir, 'PLATFORM_FINDINGS.md');
  const jsonPath = path.join(dir, 'findings.json');
  fs.copyFileSync(
    path.join(repoRoot, 'docs/demo-projects/PLATFORM_FINDINGS.md'),
    markdownPath,
  );
  return { markdownPath, jsonPath, dir };
}

test('parseEnv defaults to headed, all projects, no keep', () => {
  const env = parseEnv({});
  assert.equal(env.headless, false);
  assert.deepEqual(env.projects, []);
  assert.equal(env.keep, false);
  assert.equal(env.findingsOnly, false);
});

test('parseEnv honors HEADLESS=1, PROJECTS subset, KEEP, FINDINGS_ONLY', () => {
  const env = parseEnv({
    HEADLESS: '1',
    PROJECTS: '01,50',
    KEEP: '1',
    FINDINGS_ONLY: '1',
  });
  assert.equal(env.headless, true);
  assert.deepEqual(env.projects, ['01', '50']);
  assert.equal(env.keep, true);
  assert.equal(env.findingsOnly, true);
});

test('parseEnv treats CI=1 as headless', () => {
  assert.equal(parseEnv({ CI: '1' }).headless, true);
  assert.equal(parseEnv({ CI: 'true' }).headless, true);
});

test('numericPrefix extracts leading digits', () => {
  assert.equal(numericPrefix('50-e2e-harness'), 50);
  assert.equal(numericPrefix('01'), 1);
  assert.equal(numericPrefix('02-beta'), 2);
});

test('discoverProducts finds fixture demos in numeric order', () => {
  const found = discoverProducts(fixturesDemos);
  assert.deepEqual(
    found.map((d) => d.project.id),
    ['01-alpha', '02-beta', '50-e2e-harness'],
  );
});

test('selectProducts PROJECTS=50 matches self-test fixture', () => {
  const found = discoverProducts(fixturesDemos);
  const selected = selectProducts(found, ['50']);
  assert.equal(selected.length, 1);
  assert.equal(selected[0].project.id, '50-e2e-harness');
});

test('selectProducts PROJECTS=01,02 subset and order', () => {
  const found = discoverProducts(fixturesDemos);
  const selected = selectProducts(found, ['02', '01']);
  assert.deepEqual(
    selected.map((d) => d.project.id),
    ['01-alpha', '02-beta'],
  );
});

test('matchesProjectSelector accepts id, dir name, and numeric prefix', () => {
  const project = load(path.join(fixturesDemos, '50-e2e-harness/demo.json'));
  assert.equal(matchesProjectSelector(project, '50', '50-e2e-harness'), true);
  assert.equal(matchesProjectSelector(project, '50-e2e-harness'), true);
  assert.equal(matchesProjectSelector(project, '01'), false);
});

test('orchestrator runs 50 self-test fixture end-to-end and exits 0', async () => {
  const findings = tempFindingsPaths();
  try {
    let playwrightCalls = 0;
    let sawHeadless = false;

    const result = await runOrchestrator({
      repoRoot,
      demosRoot: fixturesDemos,
      env: {
        headless: true,
        projects: ['50'],
        keep: false,
        findingsOnly: false,
      },
      findingsPaths: {
        markdownPath: findings.markdownPath,
        jsonPath: findings.jsonPath,
      },
      platformPreflight: async () => undefined,
      hostPreflight: async () => undefined,
      runPlaywright: async (_project, env) => {
        playwrightCalls += 1;
        sawHeadless = env.headless;
        return 0;
      },
      skipResultWrite: true,
      timeoutMs: 10_000,
    });

    assert.equal(result.exitCode, 0);
    assert.equal(result.ok, true);
    assert.equal(result.products.length, 1);
    assert.equal(result.products[0].project.id, '50-e2e-harness');
    assert.equal(result.products[0].outcome, 'passed');
    assert.equal(playwrightCalls, 1);
    assert.equal(sawHeadless, true);
    assert.equal(result.products[0].lifecycle?.steps.length, 3);
  } finally {
    fs.rmSync(findings.dir, { recursive: true, force: true });
  }
});

test('orchestrator honors HEADLESS=1 via parseEnv wiring', async () => {
  const findings = tempFindingsPaths();
  try {
    let headlessFlag: boolean | undefined;
    const result = await runOrchestrator({
      repoRoot,
      demosRoot: fixturesDemos,
      env: {
        HEADLESS: '1',
        PROJECTS: '50',
      },
      findingsPaths: {
        markdownPath: findings.markdownPath,
        jsonPath: findings.jsonPath,
      },
      platformPreflight: async () => undefined,
      hostPreflight: async () => undefined,
      runPlaywright: async (_p, env) => {
        headlessFlag = env.headless;
        return 0;
      },
      skipResultWrite: true,
      timeoutMs: 10_000,
    });

    assert.equal(result.exitCode, 0);
    assert.equal(headlessFlag, true);
    assert.equal(result.env.headless, true);
  } finally {
    fs.rmSync(findings.dir, { recursive: true, force: true });
  }
});

test('orchestrator FINDINGS_ONLY skips Playwright', async () => {
  const findings = tempFindingsPaths();
  try {
    let playwrightCalls = 0;
    const result = await runOrchestrator({
      repoRoot,
      demosRoot: fixturesDemos,
      env: {
        headless: true,
        projects: ['50'],
        keep: false,
        findingsOnly: true,
      },
      findingsPaths: {
        markdownPath: findings.markdownPath,
        jsonPath: findings.jsonPath,
      },
      platformPreflight: async () => undefined,
      hostPreflight: async () => undefined,
      runPlaywright: async () => {
        playwrightCalls += 1;
        return 0;
      },
      skipResultWrite: true,
      timeoutMs: 10_000,
    });

    assert.equal(result.exitCode, 0);
    assert.equal(playwrightCalls, 0);
  } finally {
    fs.rmSync(findings.dir, { recursive: true, force: true });
  }
});

test('orchestrator KEEP=1 skips teardown', async () => {
  const findings = tempFindingsPaths();
  try {
    const result = await runOrchestrator({
      repoRoot,
      demosRoot: fixturesDemos,
      env: {
        headless: true,
        projects: ['01'],
        keep: true,
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

    assert.equal(result.exitCode, 0);
    const commands = result.products[0].lifecycle?.steps.map((s) =>
      path.basename(s.command),
    );
    assert.deepEqual(commands, ['deploy.sh']);
  } finally {
    fs.rmSync(findings.dir, { recursive: true, force: true });
  }
});

test('orchestrator exits non-zero when Playwright fails', async () => {
  const findings = tempFindingsPaths();
  try {
    const result = await runOrchestrator({
      repoRoot,
      demosRoot: fixturesDemos,
      env: {
        headless: true,
        projects: ['50'],
        keep: false,
        findingsOnly: false,
      },
      findingsPaths: {
        markdownPath: findings.markdownPath,
        jsonPath: findings.jsonPath,
      },
      platformPreflight: async () => undefined,
      hostPreflight: async () => undefined,
      runPlaywright: async () => 1,
      skipResultWrite: true,
      timeoutMs: 10_000,
    });

    assert.equal(result.exitCode, 1);
    assert.equal(result.products[0].outcome, 'failed');
  } finally {
    fs.rmSync(findings.dir, { recursive: true, force: true });
  }
});

test('selectProducts throws when selector matches nothing', () => {
  const found = discoverProducts(fixturesDemos);
  assert.throws(() => selectProducts(found, ['99']), /matched no demo\.json/);
});
