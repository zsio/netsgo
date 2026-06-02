import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  testMatch: '**/*.pw.ts',
  timeout: 180_000,
  workers: 1,
  expect: {
    timeout: 15_000,
  },
  retries: process.env.CI ? 1 : 0,
  reporter: [['list']],
  outputDir: '../../test-results/playwright',
  use: {
    baseURL: process.env.NETSGO_E2E_BASE_URL ?? 'http://127.0.0.1:19180',
    locale: 'en-US',
    trace: 'retain-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
