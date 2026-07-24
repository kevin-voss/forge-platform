import { expect, test } from '@playwright/test';

const BASE_URL =
  process.env.FORGE_E2E_BASE_URL ?? 'http://shop.orderpipe.localhost:4000';

/**
 * Scaffold smoke (54.01). Full saga / discovery / network E2E lands in 54.06.
 */
test.describe('04-orderpipe scaffold', () => {
  test('storefront renders OrderPipe brand and places a catalog order', async ({
    page,
  }) => {
    await page.goto(BASE_URL);
    await expect(page.getByText('OrderPipe', { exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: /^shop$/i })).toBeVisible();

    await expect(page.locator('#catalog-list .catalog-item').filter({ hasText: /Forge Mug/i })).toBeVisible({
      timeout: 15_000,
    });

    const email = `e2e-${Date.now()}@example.com`;
    await page.getByLabel('Email').fill(email);
    await page.getByLabel('SKU').selectOption('mug');
    await page.getByLabel('Qty').fill('1');
    await page.getByRole('button', { name: /place order/i }).click();

    await expect(page.getByText(/Order placed/i)).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('#order-summary')).toContainText(email, {
      timeout: 15_000,
    });
    await expect(page.locator('#order-summary')).toContainText('placed');
  });
});
