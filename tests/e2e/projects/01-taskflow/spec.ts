import { expect, test } from '@playwright/test';

const BASE_URL =
  process.env.FORGE_E2E_BASE_URL ?? 'http://app.taskflow.localhost:4000';

/**
 * Scaffold smoke (51.01). Full signup→login→tasks E2E lands in 51.05.
 */
test.describe('01-taskflow scaffold', () => {
  test('landing page renders TaskFlow brand', async ({ page }) => {
    await page.goto(BASE_URL);
    await expect(page.getByText('TaskFlow', { exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: /team tasks/i })).toBeVisible();
    await expect(page.getByLabel('New task')).toBeVisible();
  });
});
