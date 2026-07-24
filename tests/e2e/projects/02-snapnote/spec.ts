import { expect, test } from '@playwright/test';

const BASE_URL =
  process.env.FORGE_E2E_BASE_URL ?? 'http://app.snapnote.localhost:4000';

/**
 * Scaffold smoke (52.01). Full upload→thumbnail→autoscaling E2E lands in 52.05.
 */
test.describe('02-snapnote scaffold', () => {
  test('landing page renders SnapNote brand and creates a note', async ({ page }) => {
    await page.goto(BASE_URL);
    await expect(page.getByText('SnapNote', { exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: /notes with attachments/i })).toBeVisible();

    const title = `e2e-${Date.now()}`;
    await page.getByLabel('Title').fill(title);
    await page.getByLabel('Body').fill('Created from scaffold smoke');
    await page.getByRole('button', { name: /add note/i }).click();

    await expect(page.getByText(title, { exact: true })).toBeVisible({ timeout: 15_000 });
  });
});
