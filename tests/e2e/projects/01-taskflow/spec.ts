import { expect, test, type Page, type Response } from '@playwright/test';
import { execFile } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { promisify } from 'node:util';

import { platform } from '../../harness/findings';

const execFileAsync = promisify(execFile);

const BASE_URL =
  process.env.FORGE_E2E_BASE_URL ?? 'http://app.taskflow.localhost:4000';
const API_URL =
  process.env.FORGE_E2E_API_URL ?? 'http://api.taskflow.localhost:4000';
const IDENTITY_URL =
  process.env.FORGE_IDENTITY_HOST_URL ?? 'http://127.0.0.1:4002';
const TEMPO_URL = process.env.FORGE_TEMPO_URL ?? 'http://127.0.0.1:3002';
const OBSERVE_URL =
  process.env.FORGE_OBSERVE_HOST_URL ?? 'http://127.0.0.1:4106';
const DEMO_ID = '01-taskflow';
const ADMIN_EMAIL = process.env.TASKFLOW_ADMIN_EMAIL ?? 'admin@taskflow.local';
const ADMIN_PASSWORD =
  process.env.TASKFLOW_ADMIN_PASSWORD ?? 'AdminPass123!';
const MEMBER_EMAIL =
  process.env.TASKFLOW_MEMBER_EMAIL ?? 'member@taskflow.local';
const MEMBER_PASSWORD =
  process.env.TASKFLOW_MEMBER_PASSWORD ?? 'MemberPass123!';

const repoRoot = path.resolve(__dirname, '../../../..');
const manifestPath = path.join(repoRoot, 'demos/51-taskflow/forge.yaml');
const dockerfilePath = path.join(repoRoot, 'demos/51-taskflow/api/Dockerfile');
const statePath = path.join(repoRoot, 'demos/51-taskflow/.demo-state');

function uniqueEmail(prefix: string): string {
  const stamp = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
  return `${prefix}-${stamp}@taskflow.e2e`;
}

async function fillAuth(page: Page, email: string, password: string): Promise<void> {
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
}

async function signup(page: Page, email: string, password: string): Promise<void> {
  await fillAuth(page, email, password);
  await page.getByRole('button', { name: /sign up/i }).click();
  await expect(page.getByText(new RegExp(`Signed in as ${email}`, 'i'))).toBeVisible({
    timeout: 30_000,
  });
  await expect(page.getByRole('heading', { name: 'Tasks', exact: true })).toBeVisible();
}

async function login(page: Page, email: string, password: string): Promise<void> {
  await fillAuth(page, email, password);
  await page.getByRole('button', { name: /log in/i }).click();
  await expect(page.getByText(new RegExp(`Signed in as ${email}`, 'i'))).toBeVisible({
    timeout: 30_000,
  });
}

async function logout(page: Page): Promise<void> {
  await page.getByRole('button', { name: /log out/i }).click();
  await expect(page.getByRole('heading', { name: /sign in/i })).toBeVisible();
}

function readDemoState(): Record<string, string> {
  if (!fs.existsSync(statePath)) {
    throw new Error(`missing ${statePath}; deploy TaskFlow (demos/51-taskflow/run.sh) first`);
  }
  const out: Record<string, string> = {};
  for (const line of fs.readFileSync(statePath, 'utf8').split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;
    const eq = trimmed.indexOf('=');
    if (eq <= 0) continue;
    out[trimmed.slice(0, eq)] = trimmed.slice(eq + 1);
  }
  return out;
}

async function apiContainerId(deploymentId: string): Promise<string> {
  const { stdout: byLabel } = await execFileAsync(
    'docker',
    [
      'ps',
      '-q',
      '--filter',
      `label=forge.deployment_id=${deploymentId}`,
      '--filter',
      'label=forge.managed=true',
    ],
    { encoding: 'utf8' },
  );
  const labeled = byLabel.trim().split('\n').filter(Boolean)[0];
  if (labeled) return labeled;

  const short = deploymentId.replace(/-/g, '').slice(0, 8);
  const { stdout: byName } = await execFileAsync(
    'docker',
    [
      'ps',
      '-q',
      '--filter',
      'label=forge.managed=true',
      '--filter',
      `name=forge-api-${short}-`,
    ],
    { encoding: 'utf8' },
  );
  const named = byName.trim().split('\n').filter(Boolean)[0];
  if (!named) {
    throw new Error(`no running API container for deployment ${deploymentId}`);
  }
  return named;
}

async function containerEnv(cid: string, key: string): Promise<string> {
  const { stdout } = await execFileAsync(
    'docker',
    ['inspect', '-f', '{{range .Config.Env}}{{println .}}{{end}}', cid],
    { encoding: 'utf8' },
  );
  for (const line of stdout.split('\n')) {
    const eq = line.indexOf('=');
    if (eq > 0 && line.slice(0, eq) === key) {
      return line.slice(eq + 1);
    }
  }
  return '';
}

async function waitApiReady(timeoutMs = 120_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let last = '';
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`${API_URL}/health/ready`);
      last = `HTTP ${res.status}`;
      if (res.ok) return;
    } catch (err) {
      last = err instanceof Error ? err.message : String(err);
    }
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error(`API not ready after restart (${last})`);
}

async function introspectToken(token: string): Promise<Record<string, unknown>> {
  const res = await fetch(`${IDENTITY_URL}/v1/auth/introspect`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ token }),
  });
  const body = (await res.json()) as Record<string, unknown>;
  if (!res.ok) {
    throw new Error(`introspect HTTP ${res.status}: ${JSON.stringify(body)}`);
  }
  return body;
}

async function apiJson<T>(
  pathName: string,
  init: RequestInit & { token?: string } = {},
): Promise<{ status: number; body: T; text: string }> {
  const headers = new Headers(init.headers);
  if (init.token) headers.set('Authorization', `Bearer ${init.token}`);
  if (init.body && !headers.has('content-type')) {
    headers.set('content-type', 'application/json');
  }
  const rest: RequestInit = { ...init };
  delete (rest as { token?: string }).token;
  const res = await fetch(`${API_URL}${pathName}`, { ...rest, headers });
  const text = await res.text();
  let body = undefined as unknown as T;
  if (text) {
    try {
      body = JSON.parse(text) as T;
    } catch {
      body = text as unknown as T;
    }
  }
  return { status: res.status, body, text };
}

async function listTasks(
  token: string,
): Promise<Array<{ id: string; title: string; done: boolean }>> {
  const { status, body, text } = await apiJson<
    Array<{ id: string; title: string; done: boolean }>
  >('/tasks', { token });
  if (status !== 200) {
    throw new Error(`GET /tasks HTTP ${status}: ${text}`);
  }
  return body;
}

async function findPostTasksTrace(): Promise<{ source: string; detail: string }> {
  const errors: string[] = [];

  // Tempo TraceQL: look for POST /tasks-ish spans from recent traffic.
  try {
    const q = encodeURIComponent('{ name=~".*tasks.*" || span.http.route=~".*tasks.*" }');
    const tempoRes = await fetch(`${TEMPO_URL}/api/search?q=${q}&limit=20`);
    if (tempoRes.ok) {
      const payload = (await tempoRes.json()) as {
        traces?: Array<{ rootTraceName?: string; rootServiceName?: string }>;
      };
      const traces = payload.traces ?? [];
      const hit = traces.find((t) => {
        const name = `${t.rootTraceName ?? ''} ${t.rootServiceName ?? ''}`.toLowerCase();
        return name.includes('task') || name.includes('post');
      });
      if (traces.length > 0) {
        return {
          source: 'tempo',
          detail: hit
            ? JSON.stringify(hit)
            : `tempo returned ${traces.length} trace(s); none clearly POST /tasks`,
        };
      }
      errors.push('tempo search returned zero traces');
    } else {
      errors.push(`tempo HTTP ${tempoRes.status}`);
    }
  } catch (err) {
    errors.push(`tempo: ${err instanceof Error ? err.message : String(err)}`);
  }

  // Observe logs: any POST /tasks structured line is weak evidence of telemetry path.
  try {
    const observeRes = await fetch(`${OBSERVE_URL}/v1/logs?limit=50`);
    if (observeRes.ok) {
      const text = await observeRes.text();
      if (/POST\s+\/tasks|\/tasks".*POST|method":"POST".*"\/tasks/i.test(text)) {
        return { source: 'observe-logs', detail: 'matched POST /tasks in observe logs' };
      }
      errors.push('observe logs had no POST /tasks match');
    } else {
      errors.push(`observe HTTP ${observeRes.status}`);
    }
  } catch (err) {
    errors.push(`observe: ${err instanceof Error ? err.message : String(err)}`);
  }

  throw new Error(
    `no OTEL trace evidence for POST /tasks (${errors.join('; ') || 'no backends reachable'})`,
  );
}

/**
 * TaskFlow browser E2E (51.05): signup → login → create/persist/complete → role gating,
 * plus platform.expect assertions (Identity, Secrets, managed DB restart, Observe).
 */
test.describe('01-taskflow', () => {
  test.describe.configure({ mode: 'serial' });

  test('signup → login → create/persist/complete → role gating', async ({ page }) => {
    test.setTimeout(240_000);

    const memberEmail = uniqueEmail('member');
    const memberPassword = 'MemberE2E-Pass123!';
    // Unique per run so leftover tasks from prior KEEP=1 deploys don't collide.
    const taskTitle = `Buy milk ${Date.now().toString(36)}`;

    await page.goto(BASE_URL);
    await expect(page.getByText('TaskFlow', { exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: /team tasks/i })).toBeVisible();
    await expect(page.getByRole('heading', { name: /sign in/i })).toBeVisible();

    // 1–2. Sign up fresh member, then log out / log in again.
    await signup(page, memberEmail, memberPassword);
    await logout(page);
    await login(page, memberEmail, memberPassword);

    // Seeded project/tasks should be visible to any authenticated user.
    await expect(page.getByText('Welcome to TaskFlow')).toBeVisible({ timeout: 15_000 });

    // 3. Create "Buy milk" and prove reload persistence (product / Postgres).
    await page.getByLabel('New task').fill(taskTitle);
    const createRespPromise = page.waitForResponse(
      (r: Response) =>
        r.url().includes('/tasks') &&
        r.request().method() === 'POST' &&
        !r.url().match(/\/tasks\/[^/]+$/),
    );
    await page.getByRole('button', { name: /^add$/i }).click();
    const createResp = await createRespPromise;
    expect(createResp.status()).toBe(201);
    const taskRow = page.locator('li', { hasText: taskTitle });
    await expect(taskRow).toBeVisible();

    await page.reload();
    await expect(page.getByText(new RegExp(`Signed in as ${memberEmail}`, 'i'))).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.locator('li', { hasText: taskTitle })).toBeVisible();

    // 4. Toggle done — PATCH /tasks/:id returns 200.
    const patchPromise = page.waitForResponse(
      (r: Response) => r.url().includes('/tasks/') && r.request().method() === 'PATCH',
    );
    await page
      .locator('li', { hasText: taskTitle })
      .getByRole('button', { name: /^complete$/i })
      .click();
    const patchResp = await patchPromise;
    expect(patchResp.status()).toBe(200);
    await expect(page.locator('li.done', { hasText: taskTitle })).toBeVisible();

    // 5. Role gating: member hides Delete project; admin shows it.
    await expect(page.getByRole('button', { name: /delete project/i })).toBeHidden();

    await logout(page);
    await login(page, ADMIN_EMAIL, ADMIN_PASSWORD);
    await expect(page.getByText(/\(admin\)/i)).toBeVisible();
    await expect(page.getByRole('button', { name: /delete project/i })).toBeVisible();

    // Also confirm seeded member account cannot see the control.
    await logout(page);
    await login(page, MEMBER_EMAIL, MEMBER_PASSWORD);
    await expect(page.getByText(/\(member\)/i)).toBeVisible();
    await expect(page.getByRole('button', { name: /delete project/i })).toBeHidden();

    const token = await page.evaluate(() => localStorage.getItem('taskflow.token') || '');
    expect(token.length).toBeGreaterThan(0);

    // --- Platform assertions (findings on failure; do not hard-fail the product path) ---

    const identityResult = await platform.expect(
      'identity',
      async () => {
        const info = await introspectToken(token);
        if (info.active !== true) {
          throw new Error(`introspect active!=true: ${JSON.stringify(info)}`);
        }
        const role = String(info.role ?? '');
        if (role !== 'developer') {
          throw new Error(`expected PAT role=developer, got ${JSON.stringify(info)}`);
        }
        const projectId = String(info.project_id ?? '');
        if (!projectId) {
          throw new Error(`introspect missing project_id: ${JSON.stringify(info)}`);
        }
        const principal = String(info.user_id ?? info.principal_id ?? '');
        if (!principal) {
          throw new Error(`introspect missing principal: ${JSON.stringify(info)}`);
        }
      },
      {
        severity: 'major',
        demo: DEMO_ID,
        title: 'Identity introspect must return active PAT with project role claims',
        tested: 'POST /v1/auth/introspect with TaskFlow-issued Bearer PAT after browser login',
        expected: 'active=true, role=developer, project_id and user/principal id present',
        area: 'forge-identity introspect claims (51.05)',
        repro: [
          'make demo DEMO=51 KEEP=1',
          'cd tests/e2e && npx playwright test projects/01-taskflow',
        ],
      },
    );
    // Soft: product path already passed; platform gap → finding only.
    expect(identityResult.outcome).not.toBe('failed');

    const secretsResult = await platform.expect(
      'secrets',
      async () => {
        const manifest = fs.readFileSync(manifestPath, 'utf8');
        const dockerfile = fs.readFileSync(dockerfilePath, 'utf8');
        if (!manifest.includes('valueFrom') || !manifest.includes('JWT_SIGNING_KEY')) {
          throw new Error('forge.yaml missing JWT_SIGNING_KEY valueFrom secret ref');
        }
        if (!manifest.includes('DATABASE_URL') || !manifest.includes('secret: DATABASE_URL')) {
          throw new Error('forge.yaml missing DATABASE_URL valueFrom secret ref');
        }
        if (/name:\s*JWT_SIGNING_KEY\s*\n\s*value:\s*\S+/.test(manifest)) {
          throw new Error('forge.yaml has plaintext JWT_SIGNING_KEY value');
        }
        if (/name:\s*DATABASE_URL\s*\n\s*value:\s*\S+/.test(manifest)) {
          throw new Error('forge.yaml has plaintext DATABASE_URL value');
        }
        if (/postgres(ql)?:\/\//i.test(manifest)) {
          throw new Error('forge.yaml contains a plaintext postgres URL');
        }
        if (/ENV\s+JWT_SIGNING_KEY=/.test(dockerfile)) {
          throw new Error('Dockerfile still bakes JWT_SIGNING_KEY as plaintext ENV');
        }

        const state = readDemoState();
        const deploymentId = state.API_DEPLOYMENT_ID;
        if (!deploymentId) throw new Error('API_DEPLOYMENT_ID missing from .demo-state');
        const cid = await apiContainerId(deploymentId);
        const dbUrl = await containerEnv(cid, 'DATABASE_URL');
        const jwt = await containerEnv(cid, 'JWT_SIGNING_KEY');
        if (!dbUrl) throw new Error('DATABASE_URL absent from API container env');
        if (!jwt) throw new Error('JWT_SIGNING_KEY absent from API container env');
        if (!/postgres(ql)?:\/\//i.test(dbUrl)) {
          throw new Error(`DATABASE_URL does not look like postgres: ${dbUrl.slice(0, 32)}…`);
        }

        // Best-effort: container logs must not echo the raw secret values.
        const { stdout, stderr } = await execFileAsync(
          'docker',
          ['logs', '--tail', '200', cid],
          { encoding: 'utf8' },
        );
        const logs = `${stdout}\n${stderr}`;
        if (logs.includes(jwt)) {
          throw new Error('JWT_SIGNING_KEY plaintext found in API container logs');
        }
        if (logs.includes(dbUrl)) {
          throw new Error('DATABASE_URL plaintext found in API container logs');
        }
      },
      {
        severity: 'major',
        demo: DEMO_ID,
        title: 'TaskFlow secrets must be injected and never leaked as plaintext',
        tested:
          'forge.yaml refs-only + API container env has DATABASE_URL/JWT_SIGNING_KEY; logs scrubbed',
        expected:
          'valueFrom secret refs in manifest; both secrets present in container env; absent from logs',
        area: 'forge-secrets injection / log masking (51.04/51.05)',
        repro: [
          'make demo DEMO=51 KEEP=1',
          'docker inspect <api-cid> | jq -r ".[0].Config.Env[]"',
        ],
      },
    );
    expect(secretsResult.outcome).not.toBe('failed');

    const persistResult = await platform.expect(
      'platform',
      async () => {
        const state = readDemoState();
        const deploymentId = state.API_DEPLOYMENT_ID;
        if (!deploymentId) throw new Error('API_DEPLOYMENT_ID missing from .demo-state');

        // Re-login as member who created Buy milk to obtain a fresh PAT if needed.
        const loginRes = await apiJson<{ token?: string; pat?: string }>('/auth/login', {
          method: 'POST',
          body: JSON.stringify({ email: memberEmail, password: memberPassword }),
        });
        if (loginRes.status !== 200) {
          throw new Error(`member login HTTP ${loginRes.status}: ${loginRes.text}`);
        }
        const memberToken = loginRes.body.token || loginRes.body.pat || '';
        if (!memberToken) throw new Error('member login missing token');

        const before = await listTasks(memberToken);
        const milk = before.find((t) => t.title === taskTitle);
        if (!milk) throw new Error(`Buy milk missing before restart: ${JSON.stringify(before)}`);

        const cid = await apiContainerId(deploymentId);
        await execFileAsync('docker', ['restart', cid]);
        await waitApiReady();

        const afterLogin = await apiJson<{ token?: string; pat?: string }>('/auth/login', {
          method: 'POST',
          body: JSON.stringify({ email: memberEmail, password: memberPassword }),
        });
        if (afterLogin.status !== 200) {
          throw new Error(
            `member login after restart HTTP ${afterLogin.status}: ${afterLogin.text}`,
          );
        }
        const afterToken = afterLogin.body.token || afterLogin.body.pat || '';
        const after = await listTasks(afterToken);
        const still = after.find((t) => t.id === milk.id);
        if (!still) {
          throw new Error(`Buy milk id=${milk.id} missing after API restart`);
        }
        if (still.title !== taskTitle) {
          throw new Error(`title mismatch after restart: ${JSON.stringify(still)}`);
        }
        if (still.done !== true) {
          throw new Error(`done flag lost after restart: ${JSON.stringify(still)}`);
        }
      },
      {
        severity: 'major',
        demo: DEMO_ID,
        title: 'Managed Postgres task data must survive API container restart',
        tested: 'create+complete Buy milk, docker restart API container, GET /tasks',
        expected: 'same task id/title/done=true still present via managed Database',
        area: 'managed PostgreSQL durability (51.02/51.05)',
        repro: [
          'make demo DEMO=51 KEEP=1',
          'docker restart $(docker ps -q --filter label=forge.managed=true | head -1)',
          'curl -H Host:api.taskflow.localhost -H "Authorization: Bearer $PAT" http://127.0.0.1:4000/tasks',
        ],
      },
    );
    expect(persistResult.outcome).not.toBe('failed');

    const observeResult = await platform.expect(
      'observe',
      async () => {
        // Trigger one more POST /tasks so a fresh span can exist if instrumentation is on.
        const loginRes = await apiJson<{ token?: string; pat?: string }>('/auth/login', {
          method: 'POST',
          body: JSON.stringify({ email: ADMIN_EMAIL, password: ADMIN_PASSWORD }),
        });
        if (loginRes.status !== 200) {
          throw new Error(`admin login for trace probe HTTP ${loginRes.status}`);
        }
        const adminToken = loginRes.body.token || loginRes.body.pat || '';
        const createRes = await apiJson('/tasks', {
          method: 'POST',
          token: adminToken,
          body: JSON.stringify({ title: `trace-probe-${Date.now()}` }),
        });
        if (createRes.status !== 201) {
          throw new Error(`trace-probe POST /tasks HTTP ${createRes.status}`);
        }

        // Give the collector a moment when OTEL is enabled.
        await new Promise((r) => setTimeout(r, 2000));
        await findPostTasksTrace();
      },
      {
        severity: 'major',
        demo: DEMO_ID,
        title: 'Observe should record at least one trace for POST /tasks',
        tested: 'POST /tasks then query Tempo /api/search and Observe /v1/logs',
        expected: '≥1 OTEL trace (or observe log evidence) for POST /tasks',
        area: 'forge-observe / product OTEL export (51.05)',
        repro: [
          'make demo DEMO=51 KEEP=1',
          'curl -s "http://127.0.0.1:3002/api/search?limit=20"',
          'curl -s "http://127.0.0.1:4106/v1/logs?limit=50"',
        ],
      },
    );
    // Trace gaps are recorded as findings; product E2E still passes.
    expect(['passed', 'degraded']).toContain(observeResult.outcome);
  });
});
