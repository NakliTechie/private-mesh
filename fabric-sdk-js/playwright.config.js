// Playwright config for the M5 browser gate. The gate-runner script builds the
// dist bundle, starts a Hub on a free port, mints a wildcard Grant, and writes
// gate-config.json — then runs `npx playwright test browser-test`.
import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './browser-test',
  fullyParallel: false,
  workers: 1,
  reporter: [['list']],
  use: {
    baseURL: process.env.SDK_TEST_BASE_URL ?? 'http://127.0.0.1:5172',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
    { name: 'firefox', use: { ...devices['Desktop Firefox'] } },
    { name: 'webkit', use: { ...devices['Desktop Safari'] } },
  ],
});
