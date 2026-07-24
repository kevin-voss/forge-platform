import { expect, test } from '@playwright/test';

const BASE_URL =
  process.env.FORGE_E2E_BASE_URL ?? 'http://app.askdocs.localhost:4000';

/**
 * Scaffold smoke (53.01). Full upload→RAG→citation E2E lands in 53.05.
 */
test.describe('03-askdocs scaffold', () => {
  test('landing page renders AskDocs brand and persists an echo reply', async ({
    page,
  }) => {
    await page.goto(BASE_URL);
    await expect(page.getByText('AskDocs', { exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: /document q&a/i })).toBeVisible();

    const question = `e2e-${Date.now()}`;
    await page.getByLabel('Question').fill(question);
    await page.getByRole('button', { name: /^ask$/i }).click();

    await expect(page.getByText(question, { exact: true })).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.getByText(`Echo: ${question}`, { exact: true })).toBeVisible({
      timeout: 15_000,
    });

    await page.reload();
    await expect(page.getByText(question, { exact: true })).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.getByText(`Echo: ${question}`, { exact: true })).toBeVisible();
  });
});
