import { expect, test } from '@playwright/test';

const BASE_URL =
  process.env.FORGE_E2E_BASE_URL ?? 'http://board.pulseboard.localhost:4000';

/**
 * Scaffold smoke (55.01). Full load/scale E2E lands in 55.05.
 */
test.describe('05-pulseboard scaffold', () => {
  test('dashboard shows PulseBoard brand and replicas: 1', async ({ page }) => {
    await page.goto(BASE_URL);
    await expect(page.getByText('PulseBoard', { exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: /live metrics/i })).toBeVisible();
    await expect(page.locator('#replicas')).toHaveText('1', { timeout: 30_000 });
  });
});
