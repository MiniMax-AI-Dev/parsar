import { defineConfig } from '@playwright/test'
import { homedir } from 'node:os'
import { join } from 'node:path'

const outputDir = process.env.PARSAR_E2E_OUTPUT_DIR ?? join(homedir(), '.parsar', 'e2e', 'playwright-results')

export default defineConfig({
  testDir: '.',
  outputDir,
  timeout: 60_000,
  expect: {
    timeout: 10_000,
  },
  use: {
    baseURL: process.env.PARSAR_E2E_WEB_URL ?? 'http://127.0.0.1:5173',
    channel: process.env.PARSAR_E2E_BROWSER_CHANNEL ?? 'chrome',
    trace: 'retain-on-failure',
  },
  reporter: [['list']],
})
