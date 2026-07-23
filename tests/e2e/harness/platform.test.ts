import assert from 'node:assert/strict';
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import test from 'node:test';

import {
  MAKE_WAIT_CHECKS,
  PreflightError,
  preflight,
  type HealthCheck,
} from './platform';

test('MAKE_WAIT_CHECKS mirrors Makefile wait endpoints', () => {
  const names = MAKE_WAIT_CHECKS.map((c) => c.name);
  assert.deepEqual(names, [
    'vault',
    'registry',
    'otel-collector',
    'prometheus',
    'tempo',
    'loki',
    'grafana',
    'postgres',
  ]);
  assert.equal(
    MAKE_WAIT_CHECKS.find((c) => c.name === 'grafana')?.url,
    'http://127.0.0.1:3000/api/health',
  );
});

test('preflight fails fast with a single blocker finding', async () => {
  await assert.rejects(
    () =>
      preflight({
        skipEnsure: true,
        checks: [{ name: 'dead', url: 'http://127.0.0.1:1/nope' }],
        timeoutSeconds: 1,
        overallTimeoutMs: 3_000,
        waitForUrl: async () => {
          throw new Error('Timed out waiting for http://127.0.0.1:1/nope');
        },
      }),
    (err: unknown) => {
      assert.ok(err instanceof PreflightError);
      assert.equal(err.finding.severity, 'blocker');
      assert.equal(err.finding.service, 'platform');
      assert.match(err.message, /platform preflight blocked/);
      assert.match(err.finding.actual, /Timed out/);
      return true;
    },
  );
});

test('preflight skips make dev when already healthy', async () => {
  let makeDevCalls = 0;
  const server = http.createServer((_req, res) => {
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end(JSON.stringify({ status: 'ok' }));
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = server.address() as AddressInfo;

  try {
    const checks: HealthCheck[] = [
      { name: 'mock', url: `http://127.0.0.1:${port}/health` },
    ];
    await preflight({
      checks,
      runMakeDev: async () => {
        makeDevCalls += 1;
      },
      waitForUrl: async (url) => {
        const res = await fetch(url);
        assert.equal(res.status, 200);
      },
    });
    assert.equal(makeDevCalls, 0);
  } finally {
    await new Promise<void>((resolve, reject) =>
      server.close((err) => (err ? reject(err) : resolve())),
    );
  }
});

test('preflight runs make dev when infra is down, then waits', async () => {
  let makeDevCalls = 0;
  let waitCalls = 0;
  let up = false;

  await preflight({
    checks: [{ name: 'mock', url: 'http://127.0.0.1:9/health' }],
    isUp: async () => up,
    runMakeDev: async () => {
      makeDevCalls += 1;
      up = true;
    },
    waitForUrl: async () => {
      waitCalls += 1;
      assert.equal(up, true);
    },
  });

  assert.equal(makeDevCalls, 1);
  assert.equal(waitCalls, 1);
});

test(
  'preflight passes against a healthy make wait stack',
  { timeout: 600_000 },
  async (t) => {
    // Integration: requires Docker Compose infra (make wait ports).
    // Skip when the operator has not brought infra up and SKIP_PLATFORM_E2E=1.
    if (process.env.SKIP_PLATFORM_E2E === '1') {
      t.skip('SKIP_PLATFORM_E2E=1');
      return;
    }

    await preflight({
      timeoutSeconds: 90,
      overallTimeoutMs: 600_000,
    });

    // Grafana is a known make-wait service — direct health must be 200.
    const grafana = await fetch('http://127.0.0.1:3000/api/health');
    assert.equal(grafana.status, 200);
  },
);
