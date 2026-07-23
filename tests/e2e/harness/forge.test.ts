import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { Forge, ForgeError } from './forge';

const fakeForge = path.resolve(__dirname, '../fixtures/lifecycle/fake-forge.sh');

test('Forge build/apply/get/wait/logs shell out with clear args', async () => {
  const callLog = path.join(
    os.tmpdir(),
    `forge-call-${process.pid}-${Date.now()}.log`,
  );
  try {
    const forge = new Forge({
      bin: fakeForge,
      timeoutMs: 5_000,
      env: { FORGE_CALL_LOG: callLog },
    });

    await forge.build(['--source', '.']);
    await forge.apply(['-f', 'forge.yaml']);
    await forge.get(['applications', 'taskflow']);
    await forge.wait(['Ready', 'applications/taskflow']);
    await forge.logs(['--service', 'taskflow-api']);

    const lines = fs.readFileSync(callLog, 'utf8').trim().split('\n');
    assert.deepEqual(lines, [
      'build --source .',
      'apply -f forge.yaml',
      'get applications taskflow',
      'wait Ready applications/taskflow',
      'logs --service taskflow-api',
    ]);
  } finally {
    if (fs.existsSync(callLog)) fs.unlinkSync(callLog);
  }
});

test('Forge surfaces non-zero exits clearly', async () => {
  const callLog = path.join(
    os.tmpdir(),
    `forge-fail-${process.pid}-${Date.now()}.log`,
  );
  try {
    const forge = new Forge({
      bin: fakeForge,
      timeoutMs: 5_000,
      env: { FORGE_CALL_LOG: callLog },
    });

    await assert.rejects(
      () => forge.run(['fail', 'boom']),
      (err: unknown) => {
        assert.ok(err instanceof ForgeError);
        assert.match(err.message, /exited 3/);
        assert.match(err.message, /deliberate failure/);
        assert.equal(err.result.code, 3);
        return true;
      },
    );
  } finally {
    if (fs.existsSync(callLog)) fs.unlinkSync(callLog);
  }
});
