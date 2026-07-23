import { expect, test } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

import { loadFindings, platform } from '../../harness/findings';

const BASE_URL = process.env.FORGE_E2E_BASE_URL ?? 'http://hello.localhost:4000';
const DEMO_ID = '50-e2e-harness';
const SAMPLE_TITLE =
  '[sample] harness self-test deliberate minor finding (50.07)';

const repoRoot = path.resolve(__dirname, '../../../..');
const markdownPath = path.join(
  repoRoot,
  'docs/demo-projects/PLATFORM_FINDINGS.md',
);
const jsonPath = path.join(repoRoot, 'tests/e2e/artifacts/findings.json');

/**
 * Restore findings docs after the deliberate sample write so the orchestrator
 * still exits 0 (minor findings would otherwise mark the product degraded).
 */
function restoreFindingsBackup(
  mdBackup: string,
  jsonBackup: string | undefined,
): void {
  fs.writeFileSync(markdownPath, mdBackup, 'utf8');
  if (jsonBackup === undefined) {
    if (fs.existsSync(jsonPath)) fs.unlinkSync(jsonPath);
  } else {
    fs.mkdirSync(path.dirname(jsonPath), { recursive: true });
    fs.writeFileSync(jsonPath, jsonBackup, 'utf8');
  }
}

test.describe('50-harness self-test', () => {
  test('click hello, record sample finding, clean up', async ({ page }) => {
    await page.goto(BASE_URL);
    await expect(page.getByRole('heading', { name: /harness self-test/i })).toBeVisible();

    await page.getByRole('button', { name: 'Say hello' }).click();
    await expect(page.getByText('Hello, Forge')).toBeVisible();

    const mdBackup = fs.readFileSync(markdownPath, 'utf8');
    const jsonBackup = fs.existsSync(jsonPath)
      ? fs.readFileSync(jsonPath, 'utf8')
      : undefined;

    try {
      const result = await platform.expect(
        'gateway',
        async () => {
          throw new Error(
            'deliberate sample assertion failure for harness self-test',
          );
        },
        {
          severity: 'minor',
          demo: DEMO_ID,
          title: SAMPLE_TITLE,
          tested: 'platform.expect deliberately throws once in 50-harness spec',
          expected: 'sample finding is recorded then cleaned by the same test',
          actual: 'thrown on purpose to exercise findings.ts',
          area: 'harness/findings (SAMPLE — not a real platform bug; cleaned by spec)',
          repro: [
            'make demo DEMO=50',
            '# sample finding is recorded then removed by the spec',
          ],
        },
      );

      expect(result.ok).toBe(false);
      expect(result.outcome).toBe('degraded');
      expect(result.finding?.appended).toBe(true);

      const after = loadFindings();
      const sample = after.findings.find((f) => f.title === SAMPLE_TITLE);
      expect(sample).toBeTruthy();
      expect(sample?.severity).toBe('minor');
      expect(sample?.demo).toBe(DEMO_ID);
      expect(sample?.service).toBe('forge-gateway');

      const md = fs.readFileSync(markdownPath, 'utf8');
      expect(md).toContain(SAMPLE_TITLE);
      expect(md).toMatch(/\|\s*Severity\s*\|\s*minor\s*\|/);
    } finally {
      restoreFindingsBackup(mdBackup, jsonBackup);
    }

    // Confirm cleanup left no sample residue for the orchestrator exit check.
    const cleaned = loadFindings();
    expect(cleaned.findings.some((f) => f.title === SAMPLE_TITLE)).toBe(false);
    const mdClean = fs.readFileSync(markdownPath, 'utf8');
    expect(mdClean).not.toContain(SAMPLE_TITLE);
  });
});
