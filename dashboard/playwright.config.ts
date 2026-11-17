import { defineConfig } from '@playwright/test'

// End-to-end tests drive a real `zerosetup dashboard` against a real Docker
// engine (see e2e/global-setup.ts). They are not mocked.
export default defineConfig({
  testDir: './e2e',
  timeout: 10 * 60_000,
  expect: { timeout: 30_000 },
  workers: 1,
  reporter: [['list']],
  globalSetup: './e2e/global-setup.ts',
  globalTeardown: './e2e/global-teardown.ts',
  use: {
    channel: process.env.PW_CHANNEL ?? 'chromium',
    trace: 'retain-on-failure',
  },
})
