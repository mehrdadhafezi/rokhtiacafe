import { defineConfig, devices } from '@playwright/test'

/**
 * Browser acceptance (docs/C6-STAGING-REHEARSAL.md §3E–F) against an isolated backend started by
 * scripts/e2e.sh (throwaway database, release-style binary with the embedded frontend). The tests
 * share one database, so they run in order on one worker.
 */
const baseURL = process.env.E2E_BASE_URL
if (!baseURL) throw new Error('E2E_BASE_URL is not set: run scripts/e2e.sh')

const channel = process.env.PW_CHANNEL || undefined // e.g. msedge/chrome when Playwright's Chromium is unavailable

export default defineConfig({
  testDir: './e2e',
  globalSetup: './e2e/global-setup.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: !!process.env.CI,
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
  outputDir: 'test-results',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL,
    locale: 'fa-IR',
    timezoneId: 'Asia/Tehran',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    acceptDownloads: true,
  },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'], channel }, testIgnore: /mobile\.spec\.ts/ },
    { name: 'mobile', use: { ...devices['Pixel 7'], channel }, testMatch: /mobile\.spec\.ts/ },
  ],
})
