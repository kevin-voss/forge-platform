import { expect, test } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

import { platform } from '../../harness/findings';

const BASE_URL =
  process.env.FORGE_E2E_BASE_URL ?? 'http://app.taskflow.localhost:4000';
const DEMO_ID = '01-taskflow';
const repoRoot = path.resolve(__dirname, '../../../..');
const manifestPath = path.join(repoRoot, 'demos/51-taskflow/forge.yaml');
const dockerfilePath = path.join(repoRoot, 'demos/51-taskflow/api/Dockerfile');

/**
 * Scaffold smoke + platform secret-injection assertions (51.04).
 * Full signup→login→tasks E2E lands in 51.05.
 */
test.describe('01-taskflow scaffold', () => {
  test('landing page renders TaskFlow brand', async ({ page }) => {
    await page.goto(BASE_URL);
    await expect(page.getByText('TaskFlow', { exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: /team tasks/i })).toBeVisible();
    // Identity-gated board (51.03): first surface is sign-in, not the task form.
    await expect(page.getByRole('heading', { name: /sign in/i })).toBeVisible();
    await expect(page.getByLabel('Email')).toBeVisible();
    await expect(page.getByRole('button', { name: /log in/i })).toBeVisible();
  });

  test('platform: TaskFlow manifest has secret refs, no plaintext secrets', async () => {
    const result = await platform.expect(
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
        if (manifest.includes('taskflow-dev-jwt-key')) {
          throw new Error('forge.yaml still contains legacy plaintext JWT default');
        }
        if (/ENV\s+JWT_SIGNING_KEY=/.test(dockerfile)) {
          throw new Error('Dockerfile still bakes JWT_SIGNING_KEY as plaintext ENV');
        }
      },
      {
        severity: 'major',
        demo: DEMO_ID,
        title: 'TaskFlow secrets must be refs-only in manifests (no plaintext)',
        tested:
          'static forge.yaml + Dockerfile contain valueFrom secret refs and no JWT/DB plaintext',
        expected:
          'DATABASE_URL and JWT_SIGNING_KEY referenced via valueFrom.secret; Dockerfile has no JWT ENV',
        area: 'forge-secrets / portable Application env (51.04)',
        repro: [
          'make demo DEMO=51',
          'grep -n valueFrom demos/51-taskflow/forge.yaml',
          'grep -n JWT_SIGNING_KEY demos/51-taskflow/api/Dockerfile || true',
        ],
      },
    );
    expect(result.ok).toBe(true);
  });
});
