import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import {
  DemoLifecycle,
  DemoValidationError,
  load,
  validate,
} from './demo';

const e2eRoot = path.resolve(__dirname, '..');
const repoRoot = path.resolve(e2eRoot, '../..');

test('validate accepts a valid demo.json', () => {
  const project = validate(path.join(e2eRoot, 'fixtures/demo.json'));
  assert.equal(project.id, '00-fixture');
  assert.equal(project.baseURL, 'http://app.fixture.localhost:4000');
  assert.equal(project.hosts.length, 1);
});

test('load returns the same shape as validate', () => {
  const project = load(path.join(e2eRoot, 'fixtures/demo.json'));
  assert.equal(project.title, 'Fixture Demo');
  assert.ok(project.services.includes('gateway'));
});

test('validate rejects an invalid demo.json with a clear error', () => {
  assert.throws(
    () => validate(path.join(e2eRoot, 'fixtures/demo.invalid.json')),
    (err: unknown) => {
      assert.ok(err instanceof DemoValidationError);
      assert.match(err.message, /invalid demo\.json/);
      assert.match(err.message, /missing required property "deploy"/);
      assert.match(err.message, /missing required property "teardown"/);
      return true;
    },
  );
});

test('validate rejects unexpected properties', () => {
  assert.throws(
    () =>
      validate({
        id: 'x',
        title: 'X',
        epic: '50',
        deploy: 'd.sh',
        hosts: [{ host: 'h', path: '/', expect: 200 }],
        baseURL: 'http://h',
        spec: 's.ts',
        services: ['control'],
        teardown: 't.sh',
        extra: true,
      }),
    (err: unknown) => {
      assert.ok(err instanceof DemoValidationError);
      assert.match(err.message, /unexpected property "extra"/);
      return true;
    },
  );
});

test('DemoLifecycle runs deploy → seed → teardown in order', async () => {
  const orderLog = path.join(
    os.tmpdir(),
    `forge-lifecycle-order-${process.pid}-${Date.now()}.log`,
  );
  try {
    if (fs.existsSync(orderLog)) fs.unlinkSync(orderLog);

    const project = load(path.join(e2eRoot, 'fixtures/demo.json'));
    const lifecycle = new DemoLifecycle(project, {
      repoRoot,
      timeoutMs: 10_000,
      env: { LIFECYCLE_ORDER_LOG: orderLog },
    });

    assert.equal(lifecycle.baseURL, project.baseURL);

    let testSawBaseURL = '';
    const result = await lifecycle.run(async ({ baseURL }) => {
      testSawBaseURL = baseURL;
    });

    assert.equal(testSawBaseURL, project.baseURL);
    assert.equal(result.steps.length, 3);
    assert.deepEqual(
      result.steps.map((s) => s.command.split('/').pop()),
      ['deploy.sh', 'seed.sh', 'teardown.sh'],
    );

    const order = fs.readFileSync(orderLog, 'utf8').trim().split('\n');
    assert.deepEqual(order, ['deploy', 'seed', 'teardown']);
  } finally {
    if (fs.existsSync(orderLog)) fs.unlinkSync(orderLog);
  }
});

test('DemoLifecycle KEEP=1 skips teardown', async () => {
  const orderLog = path.join(
    os.tmpdir(),
    `forge-lifecycle-keep-${process.pid}-${Date.now()}.log`,
  );
  try {
    const project = load(path.join(e2eRoot, 'fixtures/demo.json'));
    const lifecycle = new DemoLifecycle(project, {
      repoRoot,
      keep: true,
      timeoutMs: 10_000,
      env: { LIFECYCLE_ORDER_LOG: orderLog },
    });

    await lifecycle.run();
    const order = fs.readFileSync(orderLog, 'utf8').trim().split('\n');
    assert.deepEqual(order, ['deploy', 'seed']);
  } finally {
    if (fs.existsSync(orderLog)) fs.unlinkSync(orderLog);
  }
});
