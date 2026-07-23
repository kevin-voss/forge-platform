import { test, expect } from '@playwright/test';

test('smoke opens about:blank', async ({ page }) => {
  await page.goto('about:blank');
  await expect(page).toHaveURL('about:blank');
});
