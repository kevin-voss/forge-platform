import { defineConfig, devices } from '@playwright/test';

const headless =
  process.env.HEADLESS === '1' ||
  process.env.CI === '1' ||
  process.env.CI === 'true';

export default defineConfig({
  testDir: '.',
  testMatch: '**/projects/**/spec.ts',
  outputDir: 'artifacts/',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  workers: 1,
  use: {
    ...devices['Desktop Chrome'],
    headless,
    launchOptions: headless ? undefined : { slowMo: 250 },
    trace: 'on',
    video: 'on',
    screenshot: 'only-on-failure',
  },
});
